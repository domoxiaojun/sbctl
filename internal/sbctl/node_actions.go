package sbctl

import "strings"

func mutatingNodeJob(kind string) bool {
	return contains([]string{"configure", "protocol_change", "restart"}, kind)
}
func (c *Control) NodeJobs(id string) []M {
	result := []M{}
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		for _, r := range d.Rows("SELECT id,kind,node_id,state,result_json,error_code,message,created_at,updated_at FROM node_jobs WHERE node_id=? ORDER BY created_at DESC,id DESC LIMIT 20", id) {
			delete(r, "result_json")
			r["result"] = nil
			result = append(result, r)
		}
	})
	return result
}
func (w *Worker) executeNodeAction(job M, input NodeJobInput) M {
	c := w.Control
	id := input.Draft.ID
	hasAuthorizationWork := false
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		n := d.One("SELECT n.connection_json,COALESCE(m.version,0) AS version FROM nodes n LEFT JOIN node_management m ON m.node_id=n.id WHERE n.id=?", id)
		if n == nil {
			fail("NOT_FOUND", "节点不存在", 404)
		}
		if digest(parseConnection(object([]byte(str(n["connection_json"]))))) != digest(input.Draft.Connection) {
			fail("STALE_REVISION", "节点连接配置已变化，请重新打开操作", 409)
		}
		if mutatingNodeJob(input.Kind) && integer(n["version"]) != input.ExpectedVersion {
			// Recovery after the control record was committed requires no second remote write.
			binding := object([]byte(str(d.Value("SELECT binding_json FROM node_management WHERE node_id=?", id))))
			if binding["management_job_id"] != job["id"] {
				fail("STALE_REVISION", "节点管理配置已变化，请重新读取", 409)
			}
		}
		hasAuthorizationWork = mutatingNodeJob(input.Kind) && integer(d.Value("SELECT count(*) FROM outbox WHERE node_id=? AND state<>'done'", id)) > 0
		if hasAuthorizationWork && input.Kind == "restart" {
			fail("NODE_BUSY", "节点授权任务尚未完成，请完成后重启", 409)
		}
	})
	var recovered M
	if hasAuthorizationWork && input.Kind == "protocol_change" {
		status := w.Transport.Call(input.Draft.Connection, M{"action": "maintenance_receipt", "request_id": str(job["id"]), "input_hash": digest(input.Change)}, false)
		switch status["state"] {
		case "completed":
			recovered = mapping(status["result"])
		default:
			fail("NODE_BUSY", "节点授权任务尚未完成，请完成后重试维护任务", 409)
		}
	}
	body := M{"action": "protocols"}
	if input.Kind == "protocol_change" {
		body = M{"action": "protocol_change", "request_id": str(job["id"]), "change": input.Change}
	}
	if input.Kind == "restart" {
		body = M{"action": "restart", "request_id": str(job["id"])}
	}
	result := recovered
	if result == nil {
		result = w.Transport.Call(input.Draft.Connection, body, false)
	}
	if str(result["node_id"]) != id {
		fail("NODE_IDENTITY", "节点响应身份不匹配", 409)
	}
	catalog := result["catalog"]
	if input.Kind == "protocols" {
		catalog = result
	}
	var typed ProtocolCatalog
	decode(canonical(catalog), &typed)
	if typed.NodeID != id || !hashRE.MatchString(typed.Hash) {
		fail("NODE_RESPONSE", "协议清单回执无效", 409)
	}
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		if d.Value("SELECT lease_owner FROM node_jobs WHERE id=?", job["id"]) != w.Owner {
			fail("LEASE_LOST", "任务已交给另一执行器恢复", 409)
		}
		d.Exec("INSERT INTO node_catalog(node_id,catalog_json,checked_at) VALUES (?,?,?) ON CONFLICT(node_id) DO UPDATE SET catalog_json=excluded.catalog_json,checked_at=excluded.checked_at", id, string(canonical(typed)), now())
		if mutatingNodeJob(input.Kind) {
			var endpoints []Endpoint
			decode(canonical(result["endpoints"]), &endpoints)
			if len(endpoints) != 2 || !hashRE.MatchString(str(result["template_hash"])) || !boolean(result["ready"]) {
				fail("NODE_RESPONSE", "配置执行回执无效", 409)
			}
			for _, e := range endpoints {
				e.Validate()
			}
			row := d.One("SELECT version,binding_json FROM node_management WHERE node_id=?", id)
			binding := M{}
			if row != nil {
				binding = object([]byte(str(row["binding_json"])))
			}
			if integer(row["version"]) != input.ExpectedVersion && binding["management_job_id"] != job["id"] {
				fail("STALE_REVISION", "节点资料在执行期间变化", 409)
			}
			merge(binding, without(result, "catalog"))
			binding["management_job_id"] = job["id"]
			d.Exec("UPDATE nodes SET template_hash=?,endpoints_json=? WHERE id=?", result["template_hash"], string(canonical(endpoints)), id)
			// Authorization work may have been created with the previous template
			// while this maintenance receipt was missing. Retain accepted request
			// revisions so the node's monotonic safety version never goes backward.
			// A later worker claim seals the latest user intent against this template.
			if hasAuthorizationWork {
				for _, r := range d.Rows("SELECT id FROM node_requests WHERE node_id=? AND receipt_json IS NULL", id) {
					d.Exec("UPDATE outbox SET request_id=NULL WHERE node_id=? AND request_id=? AND state<>'done'", id, r["id"])
					d.Exec("UPDATE operation_targets SET request_id=NULL WHERE node_id=? AND request_id=? AND state NOT IN ('verified','superseded')", id, r["id"])
				}
			}
			d.Exec("INSERT INTO node_management(node_id,version,binding_json,checked_at) VALUES (?,?,?,?) ON CONFLICT(node_id) DO UPDATE SET version=excluded.version,binding_json=excluded.binding_json,checked_at=excluded.checked_at", id, input.ExpectedVersion+1, string(canonical(binding)), now())
			audit(d, "worker", "node_"+input.Kind, id, "节点维护完成，已保存回执与配置版本")
		}
	})
	return result
}
func encodeNodeInput(c *Control, input NodeJobInput) string {
	if input.Change != nil {
		change := *input.Change
		input.Change = &change
		if change.Password != "" {
			change.Password = "enc:" + c.Vault.Encrypt(change.Password)
		}
		if change.UUID != "" {
			change.UUID = "enc:" + c.Vault.Encrypt(change.UUID)
		}
	}
	return string(canonical(input))
}
func decodeNodeInput(c *Control, raw string) NodeJobInput {
	var input NodeJobInput
	decode([]byte(raw), &input)
	if input.Change != nil {
		for _, secret := range []*string{&input.Change.Password, &input.Change.UUID} {
			if strings.HasPrefix(*secret, "enc:") {
				*secret = str(c.Vault.Decrypt(strings.TrimPrefix(*secret, "enc:")))
			}
		}
	}
	return input
}
