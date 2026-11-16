package sbctl

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

type Worker struct {
	Control   *Control
	Transport Transport
	Owner     string
}

func NewWorker(c *Control, t Transport) *Worker {
	if t == nil {
		t = SSH{c.Settings.SSHDir}
	}
	return &Worker{c, t, identifier("worker")}
}
func (w *Worker) Claim() M {
	var out M
	ts := timestamp()
	transaction(w.Control.Settings.ControlDB, true, func(d *DB) {
		r := d.One("SELECT o.* FROM outbox o WHERE o.state<>'done' AND o.next_attempt_at<=? AND o.lease_until<? AND NOT EXISTS(SELECT 1 FROM outbox busy WHERE busy.node_id=o.node_id AND busy.lease_until>=?) AND NOT EXISTS(SELECT 1 FROM node_jobs j WHERE j.node_id=o.node_id AND j.kind IN ('configure','protocol_change','restart') AND j.state='running' AND j.lease_until>=?) ORDER BY o.priority DESC,o.next_attempt_at,o.id LIMIT 1", ts, ts, ts, ts)
		if r == nil {
			return
		}
		d.Exec("UPDATE outbox SET lease_owner=?,lease_until=?,claim_generation=claim_generation+1,attempts=attempts+1 WHERE id=?", w.Owner, ts+60, r["id"])
		out = d.One("SELECT * FROM outbox WHERE id=?", r["id"])
	})
	return out
}
func (w *Worker) Request(task M) (NodeRequest, NodeConnection) {
	var req NodeRequest
	var link NodeConnection
	transaction(w.Control.Settings.ControlDB, true, func(d *DB) {
		lease := d.One("SELECT lease_owner,claim_generation FROM outbox WHERE id=? AND lease_until>?", task["id"], timestamp())
		if lease == nil || str(lease["lease_owner"]) != w.Owner || integer(lease["claim_generation"]) != integer(task["claim_generation"]) {
			fail("OUTBOX_LEASE_LOST", "任务领取已失效，由当前执行者处理", 409)
		}
		n := d.One("SELECT * FROM nodes WHERE id=?", task["node_id"])
		if n == nil {
			fail("NODE_UNKNOWN", "节点不存在")
		}
		users := []Credential{}
		for _, r := range d.Rows("SELECT c.* FROM credentials c JOIN users u ON u.id=c.user_id WHERE c.node_id=? AND c.revoked=0 AND u.status<>'disabled' ORDER BY c.id", n["id"]) {
			secret := mapping(w.Control.Vault.Decrypt(str(r["secret_ciphertext"])))
			users = append(users, Credential{str(r["user_id"]), str(r["protocol"]), str(r["id"]), integer(r["generation"]), str(secret["password"]), nullable(str(secret["uuid"]))})
		}
		revoked := []string{}
		for _, r := range d.Rows("SELECT fingerprint FROM revocations WHERE node_id=? ORDER BY fingerprint", n["id"]) {
			revoked = append(revoked, str(r["fingerprint"]))
		}
		last := d.One("SELECT * FROM node_requests WHERE node_id=? ORDER BY revision DESC LIMIT 1", n["id"])
		req = NodeRequest{SchemaVersion: 1, NodeID: str(n["id"]), RequestID: identifier("req"), NodeRevision: integer(n["revision"]) + 1, TemplateHash: str(n["template_hash"]), EngineBuildID: strings.TrimPrefix(str(n["image_digest"]), "sha256:"), Users: users, Revoked: revoked}
		req.Seal()
		if last != nil {
			old := parseRequest(w.Control.Vault.Decrypt(str(last["payload_ciphertext"])))
			a := without(object(canonical(req)), "request_id", "node_revision", "payload_hash")
			b := without(object(canonical(old)), "request_id", "node_revision", "payload_hash")
			if digest(a) == digest(b) {
				req = old
			}
		}
		if last == nil || req.RequestID != str(last["id"]) {
			d.Exec("INSERT INTO node_requests(id,node_id,revision,payload_hash,payload_ciphertext,created_at) VALUES (?,?,?,?,?,?)", req.RequestID, n["id"], req.NodeRevision, req.PayloadHash, w.Control.Vault.Encrypt(req), now())
			d.Exec("UPDATE nodes SET revision=? WHERE id=?", req.NodeRevision, n["id"])
		}
		d.Exec("UPDATE outbox SET request_id=? WHERE id=? AND lease_owner=? AND claim_generation=?", req.RequestID, task["id"], w.Owner, task["claim_generation"])
		d.Exec("UPDATE operation_targets SET request_id=? WHERE operation_id=? AND node_id=?", req.RequestID, task["operation_id"], n["id"])
		link = parseConnection(object([]byte(str(n["connection_json"]))))
	})
	return req, link
}

// Step claims and delivers a single outbox task. Run does the same for several
// nodes at once.
func (w *Worker) Step() bool {
	task := w.Claim()
	if task == nil {
		return false
	}
	w.Deliver(task)
	w.Reconcile()
	return true
}
func (w *Worker) Deliver(task M) {
	state, message, done, delay := "queued", "节点已持久接收，等待执行", false, 0.5
	err := Attempt(func() {
		req, link := w.Request(task)
		value := w.Transport.Call(link, M{"action": "submit", "request": req}, false)
		if str(value["node_id"]) != req.NodeID {
			fail("NODE_IDENTITY", "节点响应身份不匹配")
		}
		receipt := mapping(value["latest"])
		if str(receipt["id"]) != req.RequestID || str(receipt["payload_hash"]) != req.PayloadHash || integer(receipt["revision"]) != req.NodeRevision {
			fail("RECEIPT_MISMATCH", "节点回执与持久请求不匹配")
		}
		transaction(w.Control.Settings.ControlDB, true, func(d *DB) {
			lease := d.One("SELECT lease_owner,claim_generation FROM outbox WHERE id=? AND lease_until>?", task["id"], timestamp())
			if lease == nil || str(lease["lease_owner"]) != w.Owner || integer(lease["claim_generation"]) != integer(task["claim_generation"]) {
				return
			}
			d.Exec("UPDATE node_requests SET receipt_json=? WHERE id=?", string(canonical(receipt)), req.RequestID)
			newest := integer(d.Value("SELECT revision FROM nodes WHERE id=?", req.NodeID))
			if receipt["state"] == "verified" && newest == req.NodeRevision {
				d.Exec("UPDATE nodes SET applied_revision=? WHERE id=?", req.NodeRevision, req.NodeID)
				for _, c := range req.Users {
					d.Exec("UPDATE credentials SET verified_revision=? WHERE id=? AND revoked=0 AND user_id IN (SELECT id FROM users WHERE status<>'disabled')", req.NodeRevision, c.CredentialID)
				}
				op := d.One("SELECT id,kind,user_id FROM operations WHERE id=?", task["operation_id"])
				disabled := str(d.Value("SELECT status FROM users WHERE id=?", op["user_id"])) == "disabled"
				present := false
				for _, c := range req.Users {
					present = present || c.UserID == str(op["user_id"])
				}
				goal := !disabled && present
				if op["kind"] == "disable_user" {
					goal = disabled && !present
				}
				if op["kind"] == "update_grants" {
					wanted := integer(d.Value("SELECT count(*) FROM credentials WHERE user_id=? AND node_id=? AND revoked=0", op["user_id"], req.NodeID)) > 0
					goal = !disabled && present == wanted
				}
				goal = goal && operationCurrent(d, op)
				state, message, done = "superseded", "用户后续变更已替代本次授权目标", true
				if goal {
					state, message = "verified", "实际协议请求及当前配置绑定验证通过"
					if op["kind"] == "update_grants" && !present {
						message = "该用户的节点访问权限已撤销并验证"
					}
				}
				d.Exec("UPDATE alerts SET state='resolved' WHERE dedupe_key=?", "node-operation:"+req.NodeID)
			} else if contains([]string{"failed", "revoke_blocked"}, str(receipt["state"])) {
				state, delay = str(receipt["state"]), 10
				message = "节点预检或启动失败，需维护处理"
				if state == "revoke_blocked" {
					message = "节点撤销受阻，需维护处理"
				}
				alert(d, "node-operation:"+req.NodeID, "节点操作未完成", message, "high", req.NodeID)
			} else if receipt["state"] == "applied" {
				state, message, delay = "applied", "配置已应用，等待实际协议验证", 2
			}
		})
	})
	if err != nil {
		state, message, delay = "unreachable", "管理通道或回执暂不可用，保留任务并重试", float64(int64(1)<<min(integer(task["attempts"]), 4))
		f := PublicFault(err)
		if f.Code != "NODE_UNREACHABLE" {
			message = f.Message
		}
	}
	transaction(w.Control.Settings.ControlDB, true, func(d *DB) {
		outState := "queued"
		if done {
			outState = "done"
		}
		changed := d.Exec("UPDATE outbox SET state=?,next_attempt_at=?,lease_owner=NULL,lease_until=0 WHERE id=? AND lease_owner=? AND claim_generation=?", outState, timestamp()+delay, task["id"], w.Owner, task["claim_generation"])
		if changed > 0 {
			d.Exec("UPDATE operation_targets SET state=?,message=? WHERE operation_id=? AND node_id=?", state, message, task["operation_id"], task["node_id"])
		}
	})
}

// Run drives the outbox and the node job queue until ctx ends, keeping up to
// parallel tasks in flight. The claim queries never hand out two authorization
// tasks or two node jobs for one node, nor an authorization task next to a job
// that changes that node, so a slow or unreachable node no longer delays
// authorization and revocation elsewhere.
func (w *Worker) Run(ctx context.Context, parallel int) {
	if parallel < 1 {
		parallel = 1
	}
	// Node jobs can run for minutes. Keep one slot they cannot take, so a
	// revocation never waits behind a full set of maintenance tasks.
	jobLimit := max(1, parallel-1)
	finished := make(chan bool, parallel)
	running, jobs, settled := 0, 0, false
	settle := func(job bool) {
		running--
		if job {
			jobs--
		}
		settled = true
	}
	start := func(job bool, fn func()) {
		running++
		if job {
			jobs++
		}
		go func() {
			defer func() { finished <- job }()
			if err := Attempt(fn); err != nil {
				fmt.Fprintln(os.Stderr, "control_worker:", PublicFault(err).Code)
			}
		}()
	}
	last := time.Time{}
	for {
		claimed := false
		err := Attempt(func() {
			for running < parallel {
				task := w.Claim()
				if task == nil {
					break
				}
				claimed = true
				start(false, func() { w.Deliver(task) })
			}
			for running < parallel && jobs < jobLimit {
				job := w.ClaimNodeJob()
				if job == nil {
					break
				}
				claimed = true
				start(true, func() { w.RunNodeJob(job) })
			}
			if settled || time.Since(last) > 5*time.Second {
				w.Reconcile()
				settled, last = false, time.Now()
			}
		})
		delay := 250 * time.Millisecond
		if claimed {
			delay = 50 * time.Millisecond
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "control_worker:", PublicFault(err).Code)
			delay = time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			// Give short calls a moment to finish. A lease that is still held when
			// the process exits simply expires and the task is claimed again.
			grace := time.After(8 * time.Second)
			for running > 0 {
				select {
				case job := <-finished:
					settle(job)
				case <-grace:
					return
				}
			}
			return
		case job := <-finished:
			timer.Stop()
			settle(job)
		case <-timer.C:
		}
		for more := true; more; {
			select {
			case job := <-finished:
				settle(job)
			default:
				more = false
			}
		}
	}
}
func (w *Worker) Reconcile() {
	c := w.Control
	var pending []M
	transaction(c.Settings.ControlDB, false, func(d *DB) { pending = d.Rows("SELECT user_id FROM subscriptions WHERE state='revoke_pending'") })
	for _, r := range pending {
		c.RevokeSubscription(str(r["user_id"]))
	}
	nodeViews := Views{c.Settings}.Nodes()
	unlock := c.Public.Lock()
	defer unlock()
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		for _, u := range d.Rows("SELECT u.*,s.* FROM users u JOIN subscriptions s ON s.user_id=u.id WHERE u.status<>'disabled'") {
			if err := Attempt(func() { c.publishUser(d, u) }); err != nil {
				alert(d, "subscription:"+str(u["id"]), "订阅更新待重试", "授权意图已保存，订阅更新失败；旧内容可能仍可读取，节点撤销独立执行", "high", nil)
			}
		}
		for _, op := range d.Rows("SELECT o.*,s.state AS subscription_state FROM operations o JOIN subscriptions s ON s.user_id=o.user_id WHERE o.state NOT IN ('completed','superseded')") {
			states := []string{}
			for _, r := range d.Rows("SELECT state FROM operation_targets WHERE operation_id=?", op["id"]) {
				states = append(states, str(r["state"]))
			}
			all := func(allowed ...string) bool {
				if len(states) == 0 {
					return false
				}
				for _, v := range states {
					if !contains(allowed, v) {
						return false
					}
				}
				return true
			}
			expected := "published"
			if op["kind"] == "disable_user" {
				expected = "revoked"
			}
			state := "running"
			switch {
			case all("verified", "superseded") && (contains(states, "superseded") || !operationCurrent(d, op)):
				state = "superseded"
			case all("verified") && op["subscription_state"] == expected:
				state = "completed"
			case contains(states, "failed") || contains(states, "revoke_blocked"):
				state = "failed"
			case contains(states, "verified"):
				state = "partial"
			}
			d.Exec("UPDATE operations SET state=? WHERE id=?", state, op["id"])
		}
		for _, n := range nodeViews {
			q := mapping(n["quality"])
			if q["freshness"] != "current" || q["integrity_state"] != "ok" {
				alert(d, "telemetry:"+str(n["id"]), "节点统计需关注", "统计尚未就绪、过期或累计异常；已记录历史保留", "medium", n["id"])
			} else {
				d.Exec("UPDATE alerts SET state='resolved' WHERE dedupe_key=?", "telemetry:"+str(n["id"]))
			}
		}
	})
}
func (w *Worker) ExportInventory() {
	// Different node jobs and CLI processes publish the same file. Serialize
	// the database read as well as the rename so an older snapshot cannot win.
	unlock := fileLock(w.Control.Settings.InventoryFile+".lock", 5*time.Second)
	defer unlock()
	nodes := []M{}
	transaction(w.Control.Settings.ControlDB, false, func(d *DB) {
		for _, r := range d.Rows("SELECT id,connection_json FROM nodes WHERE enabled=1") {
			nodes = append(nodes, M{"id": r["id"], "connection": object([]byte(str(r["connection_json"])))})
		}
	})
	atomicWrite(w.Control.Settings.InventoryFile, canonical(M{"nodes": nodes}), 0644)
}
