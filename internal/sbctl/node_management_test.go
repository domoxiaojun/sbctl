package sbctl

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type managementTransport struct {
	Calls    []M
	FailCode string
	Receipt  M
}

func (m *managementTransport) Call(n NodeConnection, b M, stats bool) M {
	m.Calls = append(m.Calls, b)
	if m.FailCode != "" {
		fail(m.FailCode, "controlled failure", 409)
	}
	if stats {
		return M{"ready": true, "node_id": "node_hk", "sampled_at": now()}
	}
	switch b["action"] {
	case "maintenance_receipt":
		if m.Receipt != nil {
			return m.Receipt
		}
		return M{"state": "missing"}
	case "inspect":
		return M{"can_adopt": true, "container": "sing-box", "image": "registry.example/sing-box:custom", "engine_build_id": strings.Repeat("a", 64), "template_hash": strings.Repeat("b", 64), "observation_hash": strings.Repeat("c", 64), "endpoints": []Endpoint{{"anytls", "anytls-in", "example.test", 443, "example.test", []string{"h3"}}, {"tuic", "tuic-in", "example.test", 4443, "example.test", []string{"h3"}}}}
	case "configure":
		return M{"node_id": b["node_id"], "ready": true, "container": "sing-box", "config_dir": "/etc/sing-box", "image": "registry.example/sing-box:custom", "engine_build_id": strings.Repeat("a", 64), "template_hash": strings.Repeat("b", 64), "endpoints": []Endpoint{{"anytls", "anytls-in", "example.test", 443, "example.test", []string{"h3"}}, {"tuic", "tuic-in", "example.test", 4443, "example.test", []string{"h3"}}}}
	default:
		return M{"ready": false, "checks": []M{{"name": "binding", "ok": false, "message": "changed container"}}}
	}
}

func TestNodeActionReconcilesCompletedMaintenanceReceipt(t *testing.T) {
	c := testControl(t)
	draft := draftFor(c)
	catalog := ProtocolCatalog{NodeID: draft.ID, Hash: strings.Repeat("c", 64), CheckedAt: now(), Items: []ProtocolItem{}}
	tr := &managementTransport{Receipt: M{"state": "completed", "result": M{
		"node_id": draft.ID, "ready": true, "template_hash": strings.Repeat("a", 64),
		"endpoints": []Endpoint{{"anytls", "anytls-in", "example.test", 443, "example.test", []string{"h3"}}, {"tuic", "tuic-in", "example.test", 4443, "example.test", []string{"h3"}}},
		"catalog":   catalog,
	}}}
	w := NewWorker(c, tr)
	change := ProtocolChange{Action: "edit", Tag: "legacy", BaseHash: strings.Repeat("b", 64), Listen: "127.0.0.1", Port: 18090}
	job := c.SubmitNodeJob("admin", "maintenance-receipt-recovery", NodeJobInput{Kind: "protocol_change", Draft: draft, ExpectedVersion: 0, Change: &change})
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		d.Exec("INSERT INTO operations(id,kind,user_id,subject,state,priority,created_at) VALUES (?,?,?,?,?,?,?)", "op_receipt_recovery", "disable_user", nil, "maintenance", "running", 100, now())
		d.Exec("INSERT INTO outbox(id,operation_id,node_id,priority,state,next_attempt_at) VALUES (?,?,?,?,?,?)", "task_receipt_recovery", "op_receipt_recovery", draft.ID, 100, "queued", timestamp())
	})
	if !w.NodeStep() {
		t.Fatal("node job was not processed")
	}
	if state := c.NodeJob(str(job["job_id"]))["state"]; state != "completed" {
		t.Fatalf("receipt recovery failed: %+v", c.NodeJob(str(job["job_id"])))
	}
	if len(tr.Calls) != 1 || tr.Calls[0]["action"] != "maintenance_receipt" {
		t.Fatalf("expected receipt reconciliation before mutation, calls=%v", tr.Calls)
	}
}

type lostMaintenanceReplyTransport struct {
	changed bool
	receipt M
	request NodeRequest
}

func (tr *lostMaintenanceReplyTransport) Call(_ NodeConnection, body M, _ bool) M {
	switch body["action"] {
	case "protocol_change":
		tr.changed = true
		fail("NODE_UNREACHABLE", "节点已提交，但回执丢失", 503)
	case "maintenance_receipt":
		return tr.receipt
	case "submit":
		r := parseRequest(body["request"])
		tr.request = r
		if tr.changed && r.TemplateHash != strings.Repeat("c", 64) {
			fail("NODE_BINDING", "节点模板已变化", 409)
		}
		return M{"node_id": r.NodeID, "latest": M{"id": r.RequestID, "revision": r.NodeRevision, "payload_hash": r.PayloadHash, "state": "verified"}}
	}
	return M{}
}

func TestLostMaintenanceReplyThenDisableRecovers(t *testing.T) {
	c := testControl(t)
	tr := &lostMaintenanceReplyTransport{}
	w := NewWorker(c, tr)
	c.CreateUser("admin", "review-user-create-key01", CreateUser{DisplayName: "回执恢复", GroupIDs: []string{"grp_hk"}}, nil)
	w.Step()
	u := Views{c.Settings}.Users()[0]
	change := ProtocolChange{Action: "edit", Tag: "anytls-in", BaseHash: strings.Repeat("b", 64), Listen: "::", Port: 5443}
	job := c.SubmitNodeJob("admin", "review-maintenance-key01", NodeJobInput{Kind: "protocol_change", Draft: draftFor(c), Change: &change})
	w.NodeStep()
	if !tr.changed {
		t.Fatal("维护没有在节点提交")
	}
	revision := integer(u["revision"])
	op := c.DisableUser("admin", "review-disable-key01", str(u["id"]), DisableUser{Revision: &revision, Reason: "撤销"})
	w.Step()
	staleRevision, staleRequestID := tr.request.NodeRevision, tr.request.RequestID
	tr.receipt = M{"state": "completed", "result": M{
		"node_id": "node_hk", "ready": true, "template_hash": strings.Repeat("c", 64),
		"catalog":   ProtocolCatalog{NodeID: "node_hk", Hash: strings.Repeat("d", 64), CheckedAt: now(), Items: []ProtocolItem{}},
		"endpoints": []Endpoint{{"anytls", "anytls-in", "example.test", 443, "example.test", []string{"h3"}}, {"tuic", "tuic-in", "example.test", 443, "example.test", []string{"h3"}}},
	}}
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		d.Exec("UPDATE node_jobs SET state='queued',lease_until=0 WHERE id=?", job["job_id"])
	})
	w.NodeStep()
	if c.NodeJob(str(job["job_id"]))["state"] != "completed" {
		t.Fatal("旧维护回执没有恢复")
	}
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		d.Exec("UPDATE outbox SET next_attempt_at=0 WHERE operation_id=?", op["operation_id"])
	})
	w.Step()
	if tr.request.TemplateHash != strings.Repeat("c", 64) {
		t.Fatal("撤销请求没有使用恢复后的模板")
	}
	if tr.request.NodeRevision <= staleRevision || tr.request.RequestID == staleRequestID {
		t.Fatal("恢复后撤销请求没有推进节点安全版本")
	}
	w.Reconcile()
	if one(Views{c.Settings}.Operations(), str(op["operation_id"]))["state"] != "completed" {
		t.Fatal("禁用操作未完成")
	}
}
func draftFor(c *Control) NodeDraft {
	var d NodeDraft
	decode(canonical(c.NodeConfig("node_hk")["draft"]), &d)
	d.Container = "sing-box"
	return d
}
func TestNodeManagementInspectConfigureEdit(t *testing.T) {
	c := testControl(t)
	draft := draftFor(c)
	tr := &managementTransport{}
	w := NewWorker(c, tr)
	inspect := NodeJobInput{Kind: "inspect", Draft: draft}
	result := c.SubmitNodeJob("admin", "inspect-request-001", inspect)
	if digest(c.SubmitNodeJob("admin", "inspect-request-001", inspect)) != digest(result) {
		t.Fatal("inspect duplicated")
	}
	if !w.NodeStep() {
		t.Fatal("job not claimed")
	}
	jobID := str(result["job_id"])
	if c.NodeJob(jobID)["state"] != "completed" {
		t.Fatal("inspection failed")
	}
	config := NodeJobInput{Kind: "configure", Draft: draft, InspectionID: jobID}
	changed := config
	changed.Draft.Container = "other"
	expectFault(t, "INSPECTION_CHANGED", func() { c.SubmitNodeJob("admin", "configure-changed-1", changed) })
	submitted := c.SubmitNodeJob("admin", "configure-request-1", config)
	expectFault(t, "NODE_BUSY", func() { c.SubmitNodeJob("admin", "configure-request-2", config) })
	expectFault(t, "NODE_BUSY", func() {
		c.CreateUser("admin", "create-during-job-1", CreateUser{DisplayName: "a", GroupIDs: []string{"grp_hk"}}, nil)
	})
	w.NodeStep()
	if c.NodeJob(str(submitted["job_id"]))["state"] != "completed" {
		t.Fatal(c.NodeJob(str(submitted["job_id"])))
	}
	saved := c.NodeConfig(draft.ID)
	if integer(saved["version"]) != 1 || mapping(saved["binding"])["container"] != "sing-box" {
		t.Fatal("binding not committed")
	}
	// Simulate process loss after binding commit but before final job completion.
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		d.Exec("UPDATE node_jobs SET state='running',lease_until=0 WHERE id=?", submitted["job_id"])
	})
	before := len(tr.Calls)
	w.NodeStep()
	if len(tr.Calls) != before || c.NodeJob(str(submitted["job_id"]))["state"] != "completed" {
		t.Fatal("committed job was not recovered without repeating remote configuration")
	}
	draft.Name = "new name"
	c.UpdateNodeLabels("admin", "metadata-request-1", draft.ID, 1, draft)
	expectFault(t, "STALE_REVISION", func() { c.UpdateNodeLabels("admin", "metadata-request-2", draft.ID, 1, draft) })
	if one(Views{c.Settings}.Nodes(), draft.ID)["name"] != "new name" {
		t.Fatal("label not updated")
	}
	checkInput := NodeJobInput{Kind: "check", Draft: draft}
	checkJob := c.SubmitNodeJob("admin", "check-request-001", checkInput)
	w.NodeStep()
	if c.NodeJob(str(checkJob["job_id"]))["state"] != "completed" || mapping(c.NodeConfig(draft.ID)["last_check"])["ready"] != false {
		t.Fatal("diagnostic must preserve negative result")
	}
}
func TestNodeManagementFailureAndExpiry(t *testing.T) {
	c := testControl(t)
	draft := draftFor(c)
	tr := &managementTransport{FailCode: "HOST_KEY_MISMATCH"}
	w := NewWorker(c, tr)
	r := c.SubmitNodeJob("admin", "failed-inspect-001", NodeJobInput{Kind: "inspect", Draft: draft})
	w.NodeStep()
	j := c.NodeJob(str(r["job_id"]))
	if j["state"] != "failed" || j["error_code"] != "HOST_KEY_MISMATCH" {
		t.Fatal(j)
	}
	tr.FailCode = ""
	r = c.SubmitNodeJob("admin", "fresh-inspect-001", NodeJobInput{Kind: "inspect", Draft: draft})
	w.NodeStep()
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		d.Exec("UPDATE node_jobs SET updated_at=? WHERE id=?", time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), r["job_id"])
	})
	expectFault(t, "INSPECTION_EXPIRED", func() {
		c.SubmitNodeJob("admin", "expired-config-01", NodeJobInput{Kind: "configure", Draft: draft, InspectionID: str(r["job_id"])})
	})
}
func TestDiscoverySnapshotAndCustomNames(t *testing.T) {
	if digest(inspectionALPN("h3")) != digest(inspectionALPN([]string{"h3"})) {
		t.Fatal("sing-box listable ALPN string must be supported")
	}
	c := testControl(t)
	c.Settings.DiscoveryFile = filepath.Join(t.TempDir(), "hosts.json")
	if c.Discovery()["available"] != false {
		t.Fatal("missing snapshot")
	}
	atomicWrite(c.Settings.DiscoveryFile, canonical(M{"state": "Running", "sampled_at": now(), "hosts": []M{{"name": "hk", "address": "100.70.0.10", "online": true, "os": "linux"}}}), 0644)
	r := c.Discovery()
	if r["available"] != true || mapping(array(r["hosts"])[0])["node_id"] != "node_hk" {
		t.Fatal(r)
	}
	atomicWrite(c.Settings.DiscoveryFile, canonical(M{"state": "Running", "sampled_at": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), "hosts": []M{}}), 0644)
	if c.Discovery()["available"] != false {
		t.Fatal("stale discovery")
	}
	a, _ := testAgent(t)
	a.Settings.Container = "sing-box"
	a.Settings.Image = "registry.example:5000/custom/sing-box:stable"
	a.Settings.Validate()
	for _, name := range []string{"--privileged", "name;id", "/etc/passwd", "a\nb"} {
		if containerName(name) {
			t.Fatalf("unsafe container %q", name)
		}
	}
	expectFault(t, "CONFIG_PATH", func() { validateConfigDir("/opt/sbctl/managed/../../etc") })
	expectFault(t, "HOST_KEY_INVALID", func() { parseConnection(M{"address": "100.70.0.10", "host_key": "ssh-ed25519 not-a-key\nanything"}) })
}
func TestGroupChangeCannotImplicitlyMoveGrants(t *testing.T) {
	c := testControl(t)
	c.CreateUser("admin", "group-change-user1", CreateUser{DisplayName: "Alice", GroupIDs: []string{"grp_hk"}}, nil)
	d := draftFor(c)
	d.GroupID = "grp_new"
	expectFault(t, "GROUP_IN_USE", func() { c.UpdateNodeLabels("admin", "group-change-edit1", d.ID, 0, d) })
}
func TestRenderPreservesUnmanagedUsers(t *testing.T) {
	a, _ := testAgent(t)
	template := object(read(a.Settings.TemplateFile))
	in := mapping(array(template["inbounds"])[0])
	in["users"] = []M{{"name": "legacy-user", "password": "keep-this-password"}}
	atomicWrite(a.Settings.TemplateFile, canonical(template), 0600)
	a.Settings.TemplateHash = hash(read(a.Settings.TemplateFile))
	r := agentRequest(a, 1, []Credential{testCredential()}, nil)
	rendered := object(a.Render(r))
	users := array(mapping(array(rendered["inbounds"])[0])["users"])
	if mapping(users[0])["name"] != "legacy-user" || mapping(users[0])["password"] != "keep-this-password" {
		t.Fatal("unmanaged user overwritten")
	}
	r.Users[0].Password = "keep-this-password"
	expectFault(t, "CREDENTIAL_CONFLICT", func() { a.Render(r) })
}
