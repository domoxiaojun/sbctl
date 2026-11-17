package sbctl

import (
	"github.com/google/uuid"
	"sort"
	"strings"
)

type Control struct {
	Settings Settings
	Vault    *Vault
	Public   PublicStore
}

func NewControl(s Settings) *Control {
	return &Control{s, NewVault(s.KeyFile), PublicStore{s.PublicDir}}
}
func (c *Control) Initialize() { initialize(c.Settings.ControlDB, "control") }
func (c *Control) replay(d *DB, actor, key, path string, body any) M {
	if !match(`^[A-Za-z0-9_-]{16,128}$`, key) {
		fail("KEY_REQUIRED", "请求标识无效")
	}
	r := d.One("SELECT * FROM idempotency WHERE admin_id=? AND key=?", actor, key)
	if r == nil {
		return nil
	}
	if str(r["method"]) != "POST" || str(r["path"]) != path || str(r["payload_hash"]) != digest(body) {
		fail("HASH_CONFLICT", "同一请求标识不能用于不同操作", 409)
	}
	v := str(r["response_json"])
	if strings.HasPrefix(v, "enc:") {
		return mapping(c.Vault.Decrypt(v[4:]))
	}
	return object([]byte(v))
}
func (c *Control) remember(d *DB, actor, key, path string, body, result any, status int, secret bool) {
	v := string(canonical(result))
	if secret {
		v = "enc:" + c.Vault.Encrypt(result)
	}
	d.Exec("INSERT INTO idempotency VALUES (?,?,?,?,?,?,?,?)", actor, key, "POST", path, digest(body), v, status, now())
}
func unique(in []string) []string {
	m := map[string]bool{}
	for _, s := range in {
		m[s] = true
	}
	out := []string{}
	for s := range m {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
func targets(d *DB, groups []string) []M {
	groups = unique(groups)
	if len(groups) == 0 {
		fail("GRANT_EMPTY", "至少选择一个节点组")
	}
	args := []any{}
	p := []string{}
	for _, s := range groups {
		args = append(args, s)
		p = append(p, "?")
	}
	q := strings.Join(p, ",")
	if len(d.Rows("SELECT id FROM groups WHERE id IN ("+q+")", args...)) != len(groups) {
		fail("GROUP_UNKNOWN", "节点组不存在")
	}
	nodes := d.Rows("SELECT DISTINCT n.* FROM nodes n JOIN group_nodes g ON g.node_id=n.id WHERE g.group_id IN ("+q+") AND n.enabled=1 AND NOT EXISTS(SELECT 1 FROM node_config_snapshots s WHERE s.node_id=n.id AND s.state IN ('conflict','pending_sync','pending_restart')) ORDER BY n.id", args...)
	if len(nodes) == 0 {
		fail("GRANT_EMPTY", "所选节点组没有可管理节点")
	}
	return nodes
}
func (c *Control) credential(d *DB, user string, node M, protocol string, existing M) {
	password := token(32)
	var u *string
	if protocol == "tuic" {
		s := uuid.NewString()
		u = &s
	}
	if existing != nil {
		password = str(existing["password"])
		u = nullable(str(existing["uuid"]))
	}
	generation := integer(d.Value("SELECT COALESCE(MAX(generation),0)+1 FROM credentials WHERE user_id=? AND node_id=? AND protocol=?", user, node["id"], protocol))
	cred := Credential{user, protocol, identifier("cred"), generation, password, u, 0}
	cred.Validate()
	d.Exec("INSERT INTO credentials(id,user_id,node_id,protocol,generation,secret_ciphertext,fingerprint) VALUES (?,?,?,?,?,?,?)", cred.CredentialID, user, node["id"], protocol, generation, c.Vault.Encrypt(M{"password": cred.Password, "uuid": cred.UUID}), cred.Fingerprint())
}
func operation(d *DB, kind, user, subject string, nodes []string, priority int) string {
	id := identifier("op")
	d.Exec("INSERT INTO operations VALUES (?,?,?,?,?,?,?)", id, kind, user, subject, "queued", priority, now())
	revision := integer(d.Value("SELECT revision FROM users WHERE id=?", user))
	putMeta(d, "operation-revision:"+id, str(revision))
	// Pre-snapshot unfinished operations also predate this mutation. They must
	// not be mistaken for the new intent when an upgraded worker resumes them.
	d.Exec("INSERT OR IGNORE INTO meta(key,value) SELECT 'operation-revision:'||id,? FROM operations WHERE user_id=? AND id<>? AND state NOT IN ('completed','superseded')", str(revision-1), user, id)
	for _, n := range unique(nodes) {
		d.Exec("INSERT INTO operation_targets VALUES (?,?,?,?,NULL)", id, n, "queued", "等待执行")
		d.Exec("INSERT INTO outbox(id,operation_id,node_id,priority,state,next_attempt_at) VALUES (?,?,?,?,?,?)", identifier("task"), id, n, priority, "queued", timestamp())
	}
	return id
}
func operationCurrent(d *DB, op M) bool {
	revision := d.Value("SELECT value FROM meta WHERE key=?", "operation-revision:"+str(op["id"]))
	// Older operations predate revision snapshots; retain their existing goal checks.
	return revision == nil || integer(revision) == integer(d.Value("SELECT revision FROM users WHERE id=?", op["user_id"]))
}
func audit(d *DB, actor, action, target, summary string) {
	d.Exec("INSERT INTO audit(actor,action,target_id,summary,created_at) VALUES (?,?,?,?,?)", actor, action, target, summary, now())
}
func (c *Control) CreateUser(actor, key string, req CreateUser, imported M) M {
	req.Validate()
	var result M
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		if result = c.replay(d, actor, key, "/users", req); result != nil {
			return
		}
		if integer(d.Value("SELECT count(*) FROM users WHERE status<>'disabled'")) >= int64(c.Settings.MaxUsers) {
			fail("USER_LIMIT", "已达到当前用户容量上限", 409)
		}
		nodes := targets(d, req.GroupIDs)
		for _, n := range nodes {
			if integer(d.Value("SELECT count(*) FROM node_jobs WHERE node_id=? AND kind IN ('configure','protocol_change','config_change','restart') AND state IN ('queued','running')", n["id"])) > 0 {
				fail("NODE_BUSY", "所选节点正在更新管理配置，请稍后添加用户", 409)
			}
		}
		uid := identifier("usr")
		d.Exec("INSERT INTO users VALUES (?,?,?,?,?,?)", uid, req.DisplayName, req.Note, "provisioning", 1, now())
		for _, g := range unique(req.GroupIDs) {
			d.Exec("INSERT INTO grants VALUES (?,?)", uid, g)
		}
		nodeIDs := []string{}
		for _, n := range nodes {
			nodeIDs = append(nodeIDs, str(n["id"]))
			var endpoints []M
			decode([]byte(str(n["endpoints_json"])), &endpoints)
			protocols := []string{}
			for _, e := range endpoints {
				protocols = append(protocols, str(e["protocol"]))
			}
			for _, p := range unique(protocols) {
				var old M
				if imported != nil {
					v := mapping(imported[str(n["id"])])[p]
					if v == nil {
						fail("IMPORT_INCOMPLETE", "导入缺少节点协议凭据")
					}
					old = mapping(v)
				}
				c.credential(d, uid, n, p, old)
			}
		}
		t := token(48)
		d.Exec("INSERT INTO subscriptions VALUES (?,?,?,?,?,0)", uid, hash([]byte(t)), c.Vault.Encrypt(t), 1, "pending")
		id := operation(d, "create_user", uid, req.DisplayName, nodeIDs, 10)
		result = M{"operation_id": id}
		c.remember(d, actor, key, "/users", req, result, 202, false)
		audit(d, actor, "create_user", uid, "创建独立身份并提交节点授权")
	})
	return result
}
func (c *Control) DisableUser(actor, key, user string, req DisableUser) M {
	req.Validate()
	path := "/users/" + user + "/disable"
	var result M
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		if result = c.replay(d, actor, key, path, req); result != nil {
			return
		}
		u := d.One("SELECT * FROM users WHERE id=?", user)
		if u == nil {
			fail("NOT_FOUND", "用户不存在", 404)
		}
		if integer(u["revision"]) != *req.Revision {
			fail("STALE_REVISION", "用户状态已变化，请刷新后重试", 409)
		}
		if str(u["status"]) == "disabled" {
			fail("ALREADY_DISABLED", "用户已经禁用", 409)
		}
		nodes := []string{}
		for _, r := range d.Rows("SELECT * FROM credentials WHERE user_id=?", user) {
			nodes = append(nodes, str(r["node_id"]))
			d.Exec("INSERT OR IGNORE INTO revocations VALUES (?,?,?,?)", r["node_id"], r["fingerprint"], r["id"], now())
		}
		d.Exec("UPDATE credentials SET revoked=1,verified_revision=NULL WHERE user_id=?", user)
		d.Exec("UPDATE users SET status='disabled',revision=revision+1 WHERE id=?", user)
		d.Exec("UPDATE subscriptions SET state='revoke_pending' WHERE user_id=?", user)
		id := operation(d, "disable_user", user, str(u["display_name"]), nodes, 100)
		result = M{"operation_id": id}
		c.remember(d, actor, key, path, req, result, 202, false)
		audit(d, actor, "disable_user", user, req.Reason)
	})
	c.RevokeSubscription(user)
	return result
}
func (c *Control) RevokeSubscription(user string) {
	err := Attempt(func() {
		unlock := c.Public.Lock()
		defer unlock()
		transaction(c.Settings.ControlDB, true, func(d *DB) {
			r := d.One("SELECT s.token_hash,u.status FROM subscriptions s JOIN users u ON s.user_id=u.id WHERE user_id=?", user)
			if r != nil && str(r["status"]) == "disabled" {
				c.Public.Revoke(str(r["token_hash"]))
				d.Exec("UPDATE subscriptions SET state='revoked' WHERE user_id=?", user)
				d.Exec("UPDATE alerts SET state='resolved' WHERE dedupe_key=?", "subscription:"+user)
			}
		})
	})
	if err != nil {
		transaction(c.Settings.ControlDB, true, func(d *DB) {
			alert(d, "subscription:"+user, "订阅撤销待重试", "用户已禁用，公开订阅索引更新失败，等待重试", "high", nil)
		})
	}
}
func (c *Control) EnableUser(actor, key, user string, req EnableUser) M {
	req.Validate()
	path := "/users/" + user + "/enable"
	var result M
	// Use the same lock order as publication. Remove the old public entry before
	// committing a new token, so a crash can never make the old link live again.
	unlock := c.Public.Lock()
	defer unlock()
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		if result = c.replay(d, actor, key, path, req); result != nil {
			return
		}
		u := d.One("SELECT * FROM users WHERE id=?", user)
		if u == nil {
			fail("NOT_FOUND", "用户不存在", 404)
		}
		if integer(u["revision"]) != *req.Revision {
			fail("STALE_REVISION", "用户状态已变化，请刷新后重试", 409)
		}
		if userManagement(d, user)["archived_at"] != nil {
			fail("USER_ARCHIVED", "请先恢复归档用户，再重新启用", 409)
		}
		if u["status"] != "disabled" {
			fail("NOT_DISABLED", "只能重新启用已禁用的用户", 409)
		}
		if integer(d.Value("SELECT count(*) FROM users WHERE status<>'disabled'")) >= int64(c.Settings.MaxUsers) {
			fail("USER_LIMIT", "已达到当前用户容量上限", 409)
		}
		groups := []string{}
		for _, g := range d.Rows("SELECT group_id FROM grants WHERE user_id=?", user) {
			groups = append(groups, str(g["group_id"]))
		}
		nodes := targets(d, groups)
		for _, n := range nodes {
			if integer(d.Value("SELECT count(*) FROM node_jobs WHERE node_id=? AND kind IN ('configure','protocol_change','config_change','restart') AND state IN ('queued','running')", n["id"])) > 0 {
				fail("NODE_BUSY", "所选节点正在更新管理配置，请稍后重新启用", 409)
			}
		}
		// A disabled identity must never regain any previous credential, including
		// when a node has not yet acknowledged the preceding revocation.
		for _, r := range d.Rows("SELECT * FROM credentials WHERE user_id=?", user) {
			d.Exec("INSERT OR IGNORE INTO revocations VALUES (?,?,?,?)", r["node_id"], r["fingerprint"], r["id"], now())
		}
		d.Exec("UPDATE credentials SET revoked=1,verified_revision=NULL WHERE user_id=?", user)
		nodeIDs := []string{}
		for _, n := range nodes {
			nodeIDs = append(nodeIDs, str(n["id"]))
			var endpoints []Endpoint
			decode([]byte(str(n["endpoints_json"])), &endpoints)
			protocols := []string{}
			for _, e := range endpoints {
				protocols = append(protocols, e.Protocol)
			}
			for _, protocol := range unique(protocols) {
				c.credential(d, user, n, protocol, nil)
			}
		}
		oldToken := str(d.Value("SELECT token_hash FROM subscriptions WHERE user_id=?", user))
		if oldToken == "" {
			fail("SUBSCRIPTION_MISSING", "用户订阅记录缺失，需维护处理", 409)
		}
		if err := Attempt(func() { c.Public.Revoke(oldToken) }); err != nil {
			fail("SUBSCRIPTION_REVOKE_PENDING", "旧订阅链接暂时无法吊销，重新启用未提交，请稍后重试", 409)
		}
		t := token(48)
		d.Exec("UPDATE subscriptions SET token_hash=?,token_ciphertext=?,generation=generation+1,state='pending' WHERE user_id=?", hash([]byte(t)), c.Vault.Encrypt(t), user)
		d.Exec("UPDATE users SET status='provisioning',revision=revision+1 WHERE id=?", user)
		d.Exec("UPDATE alerts SET state='resolved' WHERE dedupe_key=?", "subscription:"+user)
		id := operation(d, "enable_user", user, str(u["display_name"]), nodeIDs, 10)
		result = M{"operation_id": id}
		c.remember(d, actor, key, path, req, result, 202, false)
		audit(d, actor, "enable_user", user, req.Reason)
	})
	return result
}
func alert(d *DB, key, title, description, severity string, node any) {
	d.Exec("INSERT INTO alerts(id,dedupe_key,title,description,severity,state,node_id,created_at) VALUES (?,?,?,?,?,'open',?,?) ON CONFLICT(dedupe_key) DO UPDATE SET description=excluded.description,state=CASE WHEN alerts.state='resolved' THEN 'open' ELSE alerts.state END", identifier("alert"), key, title, description, severity, node, now())
}
func (c *Control) SubscriptionLink(actor, key, user string) M {
	var result M
	path := "/users/" + user + "/subscription-link"
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		r := d.One("SELECT s.*,u.status FROM subscriptions s JOIN users u ON u.id=s.user_id WHERE user_id=?", user)
		if r == nil {
			fail("NOT_FOUND", "用户不存在", 404)
		}
		if str(r["state"]) != "published" || str(r["status"]) != "active" {
			fail("NOT_READY", "订阅尚未发布或已撤销", 409)
		}
		t := str(c.Vault.Decrypt(str(r["token_ciphertext"])))
		base := c.Settings.PublicBaseURL + "/s/" + t + "/"
		if result = c.replay(d, actor, key, path, M{}); result != nil {
			if str(result["url"]) != base+"surge.conf" {
				fail("LINK_EXPIRED", "该请求对应的订阅链接已吊销，请重新获取", 409)
			}
			return
		}
		result = M{"url": base + "surge.conf", "mihomo_url": base + "mihomo.yaml"}
		c.remember(d, actor, key, path, M{}, result, 200, true)
		audit(d, actor, "subscription_link", user, "管理员显式获取订阅链接")
	})
	return result
}
func (c *Control) FindRequest(actor, key string) M {
	var r M
	transaction(c.Settings.ControlDB, false, func(d *DB) { r = d.One("SELECT response_json FROM idempotency WHERE admin_id=? AND key=?", actor, key) })
	if r == nil || strings.HasPrefix(str(r["response_json"]), "enc:") {
		fail("NOT_FOUND", "没有找到对应操作", 404)
	}
	result := object([]byte(str(r["response_json"])))
	if result["operation_id"] == nil {
		fail("NOT_FOUND", "请求没有异步操作", 404)
	}
	return result
}
func (c *Control) Acknowledge(actor, key, id string) M {
	var result M
	path := "/alerts/" + id + "/ack"
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		if result = c.replay(d, actor, key, path, M{}); result != nil {
			return
		}
		result = d.One("SELECT id,title,description,severity,state,node_id,created_at FROM alerts WHERE id=?", id)
		if result == nil {
			fail("NOT_FOUND", "告警不存在", 404)
		}
		if str(result["state"]) == "open" {
			d.Exec("UPDATE alerts SET state='acknowledged',ack_by=? WHERE id=?", actor, id)
			result["state"] = "acknowledged"
		}
		c.remember(d, actor, key, path, M{}, result, 200, false)
		audit(d, actor, "acknowledge", id, "确认告警；未更改故障状态")
	})
	return result
}
func (c *Control) Register(v M) {
	id := str(v["id"])
	validIdentity(id)
	link := parseConnection(v["connection"])
	var eps []Endpoint
	decode(canonical(v["endpoints"]), &eps)
	if len(eps) != 2 {
		fail("ENDPOINTS", "节点需要 AnyTLS 与 TUIC 入口")
	}
	for i := range eps {
		eps[i].Validate()
	}
	if eps[0].Protocol == eps[1].Protocol {
		fail("ENDPOINTS", "入口协议重复")
	}
	if !hashRE.MatchString(str(v["template_hash"])) || !match(`^sha256:[a-f0-9]{64}$`, str(v["image_digest"])) {
		fail("NODE_BUILD", "模板或镜像摘要无效")
	}
	g := str(v["group_id"])
	if g == "" {
		g = "grp_pilot"
	}
	validIdentity(g)
	gn := str(v["group_name"])
	if gn == "" {
		gn = "试点"
	}
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		if d.One("SELECT 1 FROM nodes WHERE id=?", id) != nil {
			fail("NODE_EXISTS", "节点已登记，拒绝覆盖")
		}
		d.Exec("INSERT INTO nodes(id,name,region,location,management_address,public_host,connection_json,endpoints_json,template_hash,image_digest,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)", id, str(v["name"]), str(v["region"]), str(v["location"]), link.Address, eps[0].Server, string(canonical(link)), string(canonical(eps)), v["template_hash"], v["image_digest"], now())
		d.Exec("INSERT OR IGNORE INTO groups VALUES (?,?)", g, gn)
		d.Exec("INSERT INTO group_nodes VALUES (?,?)", g, id)
	})
	NewWorker(c, nil).ExportInventory()
}
func (c *Control) Doctor() M {
	r := M{"language": "go", "version": Version}
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		counts := M{}
		for _, t := range []string{"nodes", "users", "operations"} {
			counts[t] = d.Value("SELECT count(*) FROM " + t)
		}
		r["counts"] = counts
		r["sqlite"] = d.Value("SELECT sqlite_version()")
		r["journal"] = d.Value("PRAGMA journal_mode")
	})
	return r
}
