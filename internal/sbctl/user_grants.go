package sbctl

import "strings"

type GrantSelection struct {
	Revision *int64   `json:"revision"`
	GroupIDs []string `json:"group_ids"`
}

func (g *GrantSelection) Validate() {
	if g.Revision == nil || *g.Revision < 0 || len(g.GroupIDs) < 1 || len(g.GroupIDs) > 100 {
		fail("INVALID_REQUEST", "请选择节点组并提供当前用户版本；停止全部访问请使用禁用用户", 422)
	}
	for _, id := range g.GroupIDs {
		validIdentity(id)
	}
	g.GroupIDs = unique(g.GroupIDs)
}

type UpdateGrants struct {
	GrantSelection
	Reason      string `json:"reason"`
	PreviewHash string `json:"preview_hash"`
}

// The preview is derived from actual credentials, not the previous group union:
// group membership may have changed since those credentials were issued.
func grantPreview(d *DB, user string, selection GrantSelection) M {
	u := d.One("SELECT * FROM users WHERE id=?", user)
	if u == nil {
		fail("NOT_FOUND", "用户不存在", 404)
	}
	if integer(u["revision"]) != *selection.Revision {
		fail("STALE_REVISION", "用户状态已变化，请刷新后重试", 409)
	}
	if u["status"] == "disabled" {
		fail("USER_DISABLED", "请先重新启用用户，再调整授权", 409)
	}
	nodes := targets(d, selection.GroupIDs)
	wanted, active, pending := map[string]bool{}, map[string]bool{}, map[string]bool{}
	credentials := d.Rows("SELECT id,node_id,protocol,generation FROM credentials WHERE user_id=? AND revoked=0 ORDER BY id", user)
	for _, r := range credentials {
		active[str(r["node_id"])] = true
	}
	for _, r := range d.Rows("SELECT DISTINCT b.node_id FROM outbox b JOIN operations o ON o.id=b.operation_id WHERE o.user_id=? AND b.state<>'done'", user) {
		pending[str(r["node_id"])] = true
	}
	added, retained, removed := []M{}, []M{}, []M{}
	bindings := []M{}
	for _, n := range nodes {
		id := str(n["id"])
		wanted[id] = true
		item := pick(n, "id", "name")
		if active[id] {
			retained = append(retained, item)
		} else {
			added = append(added, item)
		}
		bindings = append(bindings, pick(n, "id", "endpoints_json", "template_hash", "image_digest"))
	}
	for _, n := range d.Rows("SELECT id,name FROM nodes ORDER BY id") {
		id := str(n["id"])
		if !wanted[id] && (active[id] || pending[id]) {
			removed = append(removed, n)
		}
	}
	groups := []string{}
	for _, r := range d.Rows("SELECT group_id FROM grants WHERE user_id=? ORDER BY group_id", user) {
		groups = append(groups, str(r["group_id"]))
	}
	out := M{"revision": u["revision"], "group_ids": selection.GroupIDs, "added": added, "retained": retained, "removed": removed,
		"changed": digest(groups) != digest(selection.GroupIDs) || len(added) > 0 || len(removed) > 0}
	out["preview_hash"] = digest(M{"user_id": user, "preview": out, "credentials": credentials, "bindings": bindings})
	return out
}

func (c *Control) PreviewGrants(user string, req GrantSelection) M {
	req.Validate()
	var result M
	transaction(c.Settings.ControlDB, false, func(d *DB) { result = grantPreview(d, user, req) })
	return result
}

func (c *Control) UpdateGrants(actor, key, user string, req UpdateGrants) M {
	req.GrantSelection.Validate()
	reason := EnableUser{req.Revision, req.Reason}
	reason.Validate()
	req.Reason = reason.Reason
	if !hashRE.MatchString(req.PreviewHash) {
		fail("INVALID_REQUEST", "请先预览授权影响，再确认提交", 422)
	}
	path := "/users/" + user + "/grants"
	var result M
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		if result = c.replay(d, actor, key, path, req); result != nil {
			return
		}
		preview := grantPreview(d, user, req.GrantSelection)
		if preview["preview_hash"] != req.PreviewHash {
			fail("GRANT_PREVIEW_CHANGED", "节点组或授权状态已变化，请重新预览并确认", 409)
		}
		if preview["changed"] != true {
			fail("GRANT_UNCHANGED", "授权范围没有变化", 409)
		}
		nodeIDs := []string{}
		for _, kind := range []string{"added", "retained", "removed"} {
			for _, n := range preview[kind].([]M) {
				nodeIDs = append(nodeIDs, str(n["id"]))
				if integer(d.Value("SELECT count(*) FROM node_jobs WHERE node_id=? AND kind IN ('configure','protocol_change','config_change','restart') AND state IN ('queued','running')", n["id"])) > 0 {
					fail("NODE_BUSY", "目标节点正在更新管理配置，请稍后重新预览", 409)
				}
			}
		}
		for _, n := range preview["removed"].([]M) {
			for _, r := range d.Rows("SELECT id,fingerprint FROM credentials WHERE user_id=? AND node_id=? AND revoked=0", user, n["id"]) {
				d.Exec("INSERT OR IGNORE INTO revocations VALUES (?,?,?,?)", n["id"], r["fingerprint"], r["id"], now())
			}
			d.Exec("UPDATE credentials SET revoked=1,verified_revision=NULL WHERE user_id=? AND node_id=?", user, n["id"])
		}
		for _, n := range preview["added"].([]M) {
			var endpoints []Endpoint
			decode([]byte(str(d.Value("SELECT endpoints_json FROM nodes WHERE id=?", n["id"]))), &endpoints)
			for _, e := range endpoints {
				c.credential(d, user, n, e.Protocol, nil)
			}
		}
		d.Exec("DELETE FROM grants WHERE user_id=?", user)
		for _, g := range req.GroupIDs {
			d.Exec("INSERT INTO grants VALUES (?,?)", user, g)
		}
		d.Exec("UPDATE users SET revision=revision+1,status='provisioning' WHERE id=?", user)
		d.Exec("UPDATE subscriptions SET state='pending' WHERE user_id=?", user)
		id := operation(d, "update_grants", user, str(d.Value("SELECT display_name FROM users WHERE id=?", user)), nodeIDs, 10)
		// Removals are scheduled ahead of additions, including across operations.
		for _, n := range preview["removed"].([]M) {
			d.Exec("UPDATE outbox SET priority=100 WHERE operation_id=? AND node_id=?", id, n["id"])
			d.Exec("UPDATE operation_targets SET message='等待撤销访问权限' WHERE operation_id=? AND node_id=?", id, n["id"])
		}
		result = M{"operation_id": id}
		c.remember(d, actor, key, path, req, result, 202, false)
		audit(d, actor, "update_grants", user, req.Reason)
	})
	// Publication failure must not roll back or block already durable revocations.
	c.RefreshSubscription(user)
	return result
}

func (c *Control) publishUser(d *DB, u M) {
	eps := []M{}
	for _, r := range d.Rows("SELECT c.*,n.endpoints_json FROM credentials c JOIN nodes n ON n.id=c.node_id WHERE c.user_id=? AND c.revoked=0 AND c.verified_revision IS NOT NULL ORDER BY c.node_id,c.protocol", u["id"]) {
		var endpoints []M
		decode([]byte(str(r["endpoints_json"])), &endpoints)
		for _, e := range endpoints {
			if e["protocol"] == r["protocol"] {
				merge(e, M{"node_id": r["node_id"]})
				merge(e, mapping(c.Vault.Decrypt(str(r["secret_ciphertext"]))))
				eps = append(eps, e)
				break
			}
		}
	}
	if len(eps) == 0 {
		if c.Public.Current()[str(u["token_hash"])] != nil {
			c.Public.Revoke(str(u["token_hash"]))
		}
		return
	}
	files := renderSubscriptions(eps)
	source := digest(files)
	prev := d.Value("SELECT value FROM meta WHERE key=?", "publication:"+str(u["id"]))
	if str(prev) != source || u["state"] != "published" {
		rev := integer(u["publish_revision"]) + 1
		c.Public.Publish(str(u["token_hash"]), rev, files)
		d.Exec("UPDATE subscriptions SET state='published',publish_revision=? WHERE user_id=?", rev, u["id"])
		putMeta(d, "publication:"+str(u["id"]), source)
	}
	d.Exec("UPDATE users SET status='active' WHERE id=? AND status='provisioning'", u["id"])
	d.Exec("UPDATE alerts SET state='resolved' WHERE dedupe_key=?", "subscription:"+str(u["id"]))
}

func (c *Control) RefreshSubscription(user string) {
	err := Attempt(func() {
		unlock := c.Public.Lock()
		defer unlock()
		transaction(c.Settings.ControlDB, true, func(d *DB) {
			u := d.One("SELECT u.*,s.* FROM users u JOIN subscriptions s ON s.user_id=u.id WHERE u.id=? AND u.status<>'disabled'", user)
			if u != nil {
				c.publishUser(d, u)
			}
		})
	})
	if err != nil {
		transaction(c.Settings.ControlDB, true, func(d *DB) {
			alert(d, "subscription:"+user, "订阅更新待重试", "授权意图已保存，订阅更新失败；旧内容可能仍可读取，节点撤销独立执行", "high", nil)
		})
	}
}

func parseGrantGroups(value string) []string { return strings.Split(value, ",") }
