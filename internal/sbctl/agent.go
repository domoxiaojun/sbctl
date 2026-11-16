package sbctl

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Engine interface {
	Check(string)
	Restart()
	Stop()
	Runtime() Runtime
	Verify(NodeRequest, *NodeRequest)
}
type Agent struct {
	Settings NodeSettings
	Engine   Engine
	DBPath   string
}

func NewAgent(s NodeSettings, e Engine) *Agent {
	return &Agent{s, e, filepath.Join(s.StateDir, "node.db")}
}
func (a *Agent) Initialize() {
	if !exists(a.DBPath) && exists(filepath.Join(a.Settings.SafetyDir, "manifest")) {
		fail("NODE_STATE_LOST", "安全状态库缺失但存在旧启动清单，拒绝重建")
	}
	check(os.MkdirAll(a.Settings.StateDir, 0700))
	initialize(a.DBPath, "node")
}
func (a *Agent) Lock() func() {
	return fileLock(filepath.Join(a.Settings.StateDir, "security.lock"), 60*time.Second)
}
func (a *Agent) Guard(allow bool, configHash string, revision int64) {
	state := "blocked"
	if allow {
		state = "allowed"
	}
	atomicWrite(filepath.Join(a.Settings.SafetyDir, "manifest"), []byte(fmt.Sprintf("%s\n%s\n%d\n", state, configHash, revision)), 0644)
}
func (a *Agent) Probes() []Credential {
	var probes []Credential
	decode(read(a.Settings.ProbeFile), &probes)
	protocols := []string{}
	for i := range probes {
		probes[i].Validate()
		if !strings.HasPrefix(probes[i].UserID, "sys_") {
			fail("PROBE_IDENTITY", "运维探针身份无效")
		}
		protocols = append(protocols, probes[i].Protocol)
	}
	if len(unique(protocols)) != 2 {
		fail("PROBE_IDENTITY", "运维探针身份不完整")
	}
	return probes
}
func (a *Agent) Render(r NodeRequest) []byte {
	data := read(a.Settings.TemplateFile)
	if hash(data) != a.Settings.TemplateHash {
		fail("TEMPLATE_MISMATCH", "本机模板摘要不匹配")
	}
	t := object(data)
	tags := map[string]string{}
	for _, e := range a.Settings.Endpoints {
		tags[e.Tag] = e.Protocol
	}
	probes := a.Probes()
	all := append(append([]Credential{}, r.Users...), probes...)
	found := map[string]bool{}
	for _, value := range array(t["inbounds"]) {
		i := mapping(value)
		tag := str(i["tag"])
		protocol, ok := tags[tag]
		if !ok {
			continue
		}
		if i["type"] != protocol || found[tag] {
			fail("TEMPLATE_INVALID", "受管入口类型或标签不匹配")
		}
		found[tag] = true
		users := []M{}
		for _, fixed := range array(i["users"]) {
			users = append(users, mapping(fixed))
		}
		for _, c := range all {
			if c.Protocol == protocol {
				for _, fixed := range users {
					if str(fixed["password"]) == c.Password || (c.Protocol == "tuic" && str(fixed["uuid"]) == str(*c.UUID)) {
						fail("CREDENTIAL_CONFLICT", "凭据与节点保留用户冲突")
					}
				}
				u := M{"name": c.UserID, "password": c.Password}
				if protocol == "tuic" {
					u["uuid"] = c.UUID
				}
				users = append(users, u)
			}
		}
		i["users"] = users
	}
	if len(found) != len(tags) {
		fail("TEMPLATE_INVALID", "模板缺少受管入口")
	}
	configureStatistics(t)
	return append(canonical(t), '\n')
}
func receipt(r M) M {
	if r == nil {
		return nil
	}
	return pick(r, "id", "revision", "payload_hash", "state", "accepted_at", "applied_at", "verified_at", "config_hash", "error_code", "applied_epoch")
}
func (a *Agent) Status() M {
	var row M
	transaction(a.DBPath, false, func(d *DB) { row = d.One("SELECT * FROM requests ORDER BY revision DESC LIMIT 1") })
	var latest any
	if row != nil {
		latest = receipt(row)
	}
	return M{"node_id": a.Settings.NodeID, "latest": latest}
}
func current(d *DB, r NodeRequest) bool {
	return integer(d.Value("SELECT max(revision) FROM requests")) == r.NodeRevision
}
func (a *Agent) Accept(r NodeRequest) M {
	r.Verify()
	if r.NodeID != a.Settings.NodeID || r.TemplateHash != a.Settings.TemplateHash || r.EngineBuildID != a.Settings.EngineBuildID {
		fail("NODE_BINDING", "节点、模板或内核构建不匹配")
	}
	for _, c := range r.Users {
		if !strings.HasPrefix(c.UserID, "usr_") {
			fail("USER_IDENTITY", "受管用户必须使用中心永久身份")
		}
	}
	unlock := a.Lock()
	defer unlock()
	var result M
	transaction(a.DBPath, true, func(d *DB) {
		old := d.One("SELECT * FROM requests WHERE id=? OR revision=?", r.RequestID, r.NodeRevision)
		if old != nil {
			if old["id"] != r.RequestID || old["payload_hash"] != r.PayloadHash {
				fail("REQUEST_CONFLICT", "请求标识或版本内容冲突", 409)
			}
			result = receipt(old)
			if !current(d, r) {
				result["state"] = "superseded"
			}
			return
		}
		if r.NodeRevision <= integer(d.Value("SELECT max(revision) FROM requests")) {
			fail("STALE_REVISION", "请求早于已接受安全版本", 409)
		}
		for _, row := range d.Rows("SELECT fingerprint FROM revoked") {
			f := str(row["fingerprint"])
			if !contains(r.Revoked, f) {
				fail("REVOKED_CREDENTIAL", "请求遗漏撤销记录", 409)
			}
			for _, c := range r.Users {
				if c.Fingerprint() == f {
					fail("REVOKED_CREDENTIAL", "请求包含已撤销凭据", 409)
				}
			}
		}
		a.Guard(false, "", 0)
		d.Exec("INSERT INTO requests(id,revision,payload_hash,document,state,accepted_at) VALUES (?,?,?,?,'accepted',?)", r.RequestID, r.NodeRevision, r.PayloadHash, string(canonical(r)), now())
		for _, f := range r.Revoked {
			d.Exec("INSERT OR IGNORE INTO revoked VALUES (?)", f)
		}
		result = receipt(d.One("SELECT * FROM requests WHERE id=?", r.RequestID))
	})
	return result
}
func (a *Agent) ApplyOnce() bool {
	var row, prevRow M
	transaction(a.DBPath, false, func(d *DB) {
		row = d.One("SELECT * FROM requests ORDER BY revision DESC LIMIT 1")
		if row != nil {
			prevRow = d.One("SELECT document FROM requests WHERE applied_at IS NOT NULL AND revision<? ORDER BY revision DESC LIMIT 1", row["revision"])
		}
	})
	if row == nil || contains([]string{"verified", "revoke_blocked", "failed", "superseded"}, str(row["state"])) {
		return false
	}
	r := parseRequest(object([]byte(str(row["document"]))))
	var prev *NodeRequest
	if prevRow != nil {
		p := parseRequest(object([]byte(str(prevRow["document"]))))
		prev = &p
	}
	pending := false
	for _, f := range r.Revoked {
		if prev == nil || !contains(prev.Revoked, f) {
			pending = true
		}
	}
	err := Attempt(func() {
		content := a.Render(r)
		ch := hash(content)
		candidate := filepath.Join(a.Settings.StateDir, "candidate.json")
		atomicWrite(candidate, content, 0600)
		if contains([]string{"accepted", "switching"}, str(row["state"])) {
			a.Engine.Check(candidate)
			superseded := false
			func() {
				unlock := a.Lock()
				defer unlock()
				oldEpoch := str(row["previous_epoch"])
				transaction(a.DBPath, true, func(d *DB) {
					if !current(d, r) {
						d.Exec("UPDATE requests SET state='superseded' WHERE id=?", r.RequestID)
						superseded = true
						return
					}
					if oldEpoch == "" {
						oldEpoch = a.Engine.Runtime().Epoch()
					}
					d.Exec("UPDATE requests SET state='switching',config_hash=?,previous_epoch=? WHERE id=?", ch, oldEpoch, r.RequestID)
				})
				if superseded {
					return
				}
				a.Guard(false, "", 0)
				path := filepath.Join(a.Settings.ConfigDir, "config.json")
				atomicWrite(path, content, 0640)
				if os.Geteuid() == 0 {
					check(os.Chown(path, 0, a.Settings.GID))
				}
				users := []string{}
				for _, c := range r.Users {
					users = append(users, c.UserID)
				}
				atomicWrite(filepath.Join(a.Settings.SafetyDir, "runtime.json"), canonical(M{"node_revision": r.NodeRevision, "config_hash": ch, "users": unique(users)}), 0644)
				a.Guard(true, ch, r.NodeRevision)
				rt := a.Engine.Runtime()
				if !(rt.Running && rt.Epoch() != oldEpoch && rt.ConfigHash == ch && rt.NodeRevision == r.NodeRevision) {
					a.Engine.Restart()
				}
				rt = a.Engine.Runtime()
				if !rt.Running || rt.Epoch() == oldEpoch || rt.ConfigHash != ch || rt.NodeRevision != r.NodeRevision {
					a.Guard(false, "", 0)
					a.Engine.Stop()
					fail("START_FAILED", "新运行实例未建立，保持服务停止")
				}
				transaction(a.DBPath, true, func(d *DB) {
					d.Exec("UPDATE requests SET state='applied',applied_at=?,applied_epoch=? WHERE id=?", now(), rt.Epoch(), r.RequestID)
				})
			}()
			if superseded {
				return
			}
		}
		before := a.Engine.Runtime()
		a.Engine.Verify(r, prev)
		after := a.Engine.Runtime()
		if !before.Stable(after) || after.NodeRevision != r.NodeRevision || after.ConfigHash != ch {
			fail("RUNTIME_CHANGED", "验证期间运行实例改变")
		}
		unlock := a.Lock()
		defer unlock()
		transaction(a.DBPath, true, func(d *DB) {
			if current(d, r) {
				d.Exec("UPDATE requests SET state='verified',verified_at=?,error_code=NULL WHERE id=?", now(), r.RequestID)
			} else {
				d.Exec("UPDATE requests SET state='superseded' WHERE id=?", r.RequestID)
			}
		})
	})
	if err != nil {
		f := PublicFault(err)
		unlock := a.Lock()
		defer unlock()
		transaction(a.DBPath, true, func(d *DB) {
			if !current(d, r) {
				return
			}
			stateNow := str(d.Value("SELECT state FROM requests WHERE id=?", r.RequestID))
			state := "failed"
			if stateNow == "applied" {
				state = "applied"
			}
			if stateNow != "applied" && pending {
				state = "revoke_blocked"
				a.Guard(false, "", 0)
				if a.Settings.RevokeFailure == "stop" {
					a.Engine.Stop()
				}
			} else if stateNow == "accepted" && prev != nil {
				a.Guard(true, hash(a.Render(*prev)), prev.NodeRevision)
			}
			d.Exec("UPDATE requests SET state=?,error_code=? WHERE id=?", state, f.Code, r.RequestID)
		})
	}
	return true
}
func (a *Agent) Retry() {
	unlock := a.Lock()
	defer unlock()
	transaction(a.DBPath, true, func(d *DB) {
		d.Exec("UPDATE requests SET state='accepted',error_code=NULL WHERE revision=(SELECT max(revision) FROM requests) AND state IN ('failed','revoke_blocked')")
	})
}
