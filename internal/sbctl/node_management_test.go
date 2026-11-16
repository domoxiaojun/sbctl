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
	case "inspect":
		return M{"can_adopt": true, "container": "sing-box", "image": "registry.example/sing-box:custom", "engine_build_id": strings.Repeat("a", 64), "template_hash": strings.Repeat("b", 64), "observation_hash": strings.Repeat("c", 64), "endpoints": []Endpoint{{"anytls", "anytls-in", "example.test", 443, "example.test", []string{"h3"}}, {"tuic", "tuic-in", "example.test", 4443, "example.test", []string{"h3"}}}}
	case "configure":
		return M{"node_id": b["node_id"], "ready": true, "container": "sing-box", "config_dir": "/etc/sing-box", "image": "registry.example/sing-box:custom", "engine_build_id": strings.Repeat("a", 64), "template_hash": strings.Repeat("b", 64), "endpoints": []Endpoint{{"anytls", "anytls-in", "example.test", 443, "example.test", []string{"h3"}}, {"tuic", "tuic-in", "example.test", 4443, "example.test", []string{"h3"}}}}
	default:
		return M{"ready": false, "checks": []M{{"name": "binding", "ok": false, "message": "changed container"}}}
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
