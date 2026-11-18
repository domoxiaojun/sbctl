package sbctl

import (
	"bytes"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func configFiles(m NodeMaintenance) (config, template M) {
	return object(read(filepath.Join(m.Settings.ConfigDir, "config.json"))), object(read(m.Settings.TemplateFile))
}
func sectionChange(m NodeMaintenance, section string, content M) ConfigChange {
	config, _ := configFiles(m)
	return ConfigChange{BaseHash: str(configView("", config)["hash"]), Section: section, Content: content}
}

func TestConfigSectionChangeRestartsAndKeepsCredentials(t *testing.T) {
	m, e := maintenanceFixture(t)
	before, _ := configFiles(m)
	users := canonical(findInbound(before, "anytls-in")["users"])
	view := configView(m.Settings.NodeID, before)
	if len(mapping(view["sections"])) != len(configSections) || len(stringsOf(view["inbound_tags"])) != 3 {
		t.Fatalf("unexpected view: %v", view)
	}
	route := M{"rules": []M{{"inbound": []string{"legacy"}, "action": "reject"}}, "final": "direct"}
	change := sectionChange(m, "route", route)
	restarts := e.restarts
	result := m.ApplyConfig("maintenance_route", change)
	if e.restarts != restarts+1 || !boolean(result["ready"]) || str(result["template_hash"]) == m.Settings.TemplateHash {
		t.Fatalf("change must restart once and move the template: restarts=%d result=%v", e.restarts-restarts, result)
	}
	if digest(m.ApplyConfig("maintenance_route", change)) != digest(result) || e.restarts != restarts+1 {
		t.Fatal("replaying the same task must not restart again")
	}
	m = updatedMaintenance(m)
	config, template := configFiles(m)
	if digest(config["route"]) != digest(route) || digest(template["route"]) != digest(route) {
		t.Fatal("section missing from the configuration or its template")
	}
	if hash(read(m.Settings.TemplateFile)) != m.Settings.TemplateHash || str(result["template_hash"]) != m.Settings.TemplateHash {
		t.Fatal("node settings must be bound to the new template")
	}
	if !bytes.Equal(users, canonical(findInbound(config, "anytls-in")["users"])) || len(array(template["inbounds"])) != 3 {
		t.Fatal("a section change must not touch inbounds or credentials")
	}
	if str(e.binding["config_hash"]) != hash(read(filepath.Join(m.Settings.ConfigDir, "config.json"))) {
		t.Fatal("the restarted proxy is not bound to the new configuration")
	}
	if exists(filepath.Join(m.Settings.StateDir, "maintenance-pending.json")) {
		t.Fatal("maintenance barrier left behind")
	}
	expectFault(t, "CONFIG_CHANGED", func() { m.ApplyConfig("maintenance_stale", change) })
	// An empty section is removed rather than written as an empty object.
	m.ApplyConfig("maintenance_clear", sectionChange(m, "route", M{}))
	m = updatedMaintenance(m)
	config, template = configFiles(m)
	if _, present := config["route"]; present || template["route"] != nil {
		t.Fatal("an emptied section must be removed")
	}
	// Unchanged content needs no restart.
	restarts = e.restarts
	if m.ApplyConfig("maintenance_same", sectionChange(m, "log", M{})); e.restarts != restarts {
		t.Fatal("identical content must not restart the proxy")
	}
}

func TestConfigSectionChangeIsValidated(t *testing.T) {
	m, e := maintenanceFixture(t)
	valid := sectionChange(m, "dns", M{"servers": []M{{"type": "local", "tag": "local"}}})
	for name, change := range map[string]ConfigChange{
		"inbounds are not editable":   {BaseHash: valid.BaseHash, Section: "inbounds", Content: M{}},
		"outbounds are not editable":  {BaseHash: valid.BaseHash, Section: "outbounds", Content: M{}},
		"experimental is not allowed": {BaseHash: valid.BaseHash, Section: "experimental", Content: M{}},
		"content is required":         {BaseHash: valid.BaseHash, Section: "dns"},
		"base hash is required":       {Section: "dns", Content: M{}},
		"size is limited":             {BaseHash: valid.BaseHash, Section: "dns", Content: M{"x": strings.Repeat("a", maxConfigSection)}},
	} {
		expectFault(t, "INVALID_REQUEST", func() { m.ApplyConfig("maintenance_invalid", change) })
		if exists(filepath.Join(m.Settings.StateDir, "maintenance-pending.json")) {
			t.Fatalf("%s: a rejected change left a barrier", name)
		}
	}
	original := read(filepath.Join(m.Settings.ConfigDir, "config.json"))
	e.preflight = true
	expectFault(t, "PREFLIGHT_FAILED", func() { m.ApplyConfig("maintenance_preflight", valid) })
	if !bytes.Equal(original, read(filepath.Join(m.Settings.ConfigDir, "config.json"))) || exists(filepath.Join(m.Settings.StateDir, "maintenance-pending.json")) {
		t.Fatal("a configuration that fails the preflight must not be written")
	}
}

func TestConfigSectionChangeRollsBackWhenTheProxyFailsToStart(t *testing.T) {
	m, e := maintenanceFixture(t)
	original := read(filepath.Join(m.Settings.ConfigDir, "config.json"))
	template := read(m.Settings.TemplateFile)
	failing := true
	m.Engine = func(s NodeSettings) Engine { e.s = s; return &maintenanceFailure{e, &failing} }
	change := sectionChange(m, "route", M{"final": "missing-outbound"})
	expectFault(t, "MAINTENANCE_ROLLED_BACK", func() { m.ApplyConfig("maintenance_rollback", change) })
	if !bytes.Equal(original, read(filepath.Join(m.Settings.ConfigDir, "config.json"))) || !bytes.Equal(template, read(m.Settings.TemplateFile)) {
		t.Fatal("the original configuration and template must be restored")
	}
	if updatedMaintenance(m).Settings.TemplateHash != m.Settings.TemplateHash || exists(filepath.Join(m.Settings.StateDir, "maintenance-pending.json")) {
		t.Fatal("rollback must restore the binding and release the barrier")
	}
	if str(e.binding["config_hash"]) != hash(original) || e.stopped {
		t.Fatal("the proxy must be running the original configuration again")
	}
}

// configTransport plays a node for controller-side configuration jobs.
type configTransport struct {
	calls []M
	view  M
}

func (c *configTransport) Call(_ NodeConnection, body M, _ bool) M {
	c.calls = append(c.calls, body)
	switch body["action"] {
	case "config":
		return c.view
	case "config_change":
		endpoints := normalizeEndpoints([]Endpoint{{"anytls", "anytls-in", "example.test", 443, "example.test", []string{"h3"}, ""}, {"tuic", "tuic-in", "example.test", 443, "example.test", []string{"h3"}, ""}})
		h := strings.Repeat("c", 64)
		return M{
			"node_id": "node_hk", "ready": true, "template_hash": h,
			"catalog":            ProtocolCatalog{NodeID: "node_hk", Hash: strings.Repeat("d", 64), CheckedAt: now(), Items: []ProtocolItem{}},
			"endpoints":          endpoints,
			"probe_verification": M{"template_hash": h, "endpoints_hash": digest(endpoints), "config_hash": h, "runtime_epoch": "fixture@2", "checked_at": now()},
		}
	case "submit":
		r := parseRequest(body["request"])
		return M{"node_id": r.NodeID, "latest": M{"id": r.RequestID, "revision": r.NodeRevision, "payload_hash": r.PayloadHash, "state": "verified"}}
	}
	return M{}
}

func TestConfigJobsReadAndChangeThroughTheController(t *testing.T) {
	c := testControl(t)
	view := configView("node_hk", M{"route": M{"final": "direct"}, "inbounds": []M{{"tag": "anytls-in"}}, "outbounds": []M{{"tag": "direct", "type": "direct", "password": "must-not-appear"}}})
	if bytes.Contains(canonical(view), []byte("must-not-appear")) {
		t.Fatal("outbound credentials must not leave the node")
	}
	tr := &configTransport{view: view}
	w := NewWorker(c, tr)
	read := c.SubmitNodeJob("admin", "config-read-key-0001", NodeJobInput{Kind: "config", Draft: draftFor(c)})
	w.NodeStep()
	job := c.NodeJob(str(read["job_id"]))
	if job["state"] != "completed" || mapping(job["result"])["hash"] != view["hash"] || digest(mapping(mapping(job["result"])["sections"])["route"]) != digest(M{"final": "direct"}) {
		t.Fatalf("configuration was not returned to the administrator: %v", job)
	}
	change := ConfigChange{BaseHash: str(view["hash"]), Section: "route", Content: M{"final": "block"}}
	expectFault(t, "INVALID_REQUEST", func() {
		c.SubmitNodeJob("admin", "config-change-key-bad", NodeJobInput{Kind: "config_change", Draft: draftFor(c)})
	})
	expectFault(t, "INVALID_REQUEST", func() {
		c.SubmitNodeJob("admin", "config-change-key-bad", NodeJobInput{Kind: "restart", Draft: draftFor(c), ConfigChange: &change})
	})
	expectFault(t, "STALE_REVISION", func() {
		c.SubmitNodeJob("admin", "config-change-key-old", NodeJobInput{Kind: "config_change", Draft: draftFor(c), ExpectedVersion: 7, ConfigChange: &change})
	})
	submitted := c.SubmitNodeJob("admin", "config-change-key-01", NodeJobInput{Kind: "config_change", Draft: draftFor(c), ConfigChange: &change})
	// While the change is queued the node accepts no new users or second change.
	expectFault(t, "NODE_BUSY", func() {
		c.CreateUser("admin", "config-user-key-00001", CreateUser{DisplayName: "变更期间", GroupIDs: []string{"grp_hk"}}, nil)
	})
	expectFault(t, "NODE_BUSY", func() {
		c.SubmitNodeJob("admin", "config-change-key-02", NodeJobInput{Kind: "config_change", Draft: draftFor(c), ConfigChange: &change})
	})
	w.NodeStep()
	if job := c.NodeJob(str(submitted["job_id"])); job["state"] != "completed" {
		t.Fatalf("change did not complete: %v", job)
	}
	sent := tr.calls[len(tr.calls)-1]
	if sent["action"] != "config_change" || sent["request_id"] != submitted["job_id"] || digest(sent["change"]) != digest(change) {
		t.Fatalf("unexpected request to the node: %v", sent)
	}
	var templateHash string
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		templateHash = str(d.Value("SELECT template_hash FROM nodes WHERE id='node_hk'"))
	})
	if templateHash != strings.Repeat("c", 64) || integer(c.NodeConfig("node_hk")["version"]) != 1 {
		t.Fatal("the controller must bind to the node's new template and advance the version")
	}
	// Later authorization is sealed against the new template.
	c.CreateUser("admin", "config-user-key-00002", CreateUser{DisplayName: "变更之后", GroupIDs: []string{"grp_hk"}}, nil)
	w.Step()
	if last := parseRequest(tr.calls[len(tr.calls)-1]["request"]); last.TemplateHash != strings.Repeat("c", 64) {
		t.Fatal("authorization after the change must use the new template")
	}
}

func TestNodeJobRequestsMayCarryALargeSection(t *testing.T) {
	body := strings.Repeat("a", 100*1024)
	read := func(limit ...int64) (out []byte, err error) {
		r := httptest.NewRequest("POST", "/api/v1/node-jobs", strings.NewReader(body))
		err = Attempt(func() { out = requestBody(httptest.NewRecorder(), r, limit...) })
		return
	}
	if _, err := read(); err == nil || PublicFault(err).Code != "INPUT_TOO_LARGE" {
		t.Fatalf("the default limit must still reject large bodies: %v", err)
	}
	if out, err := read(2 * maxConfigSection); err != nil || len(out) != len(body) {
		t.Fatalf("node job requests must accept a configuration section: %v", err)
	}
}

// outdatedNode rejects every action added after its release.
type outdatedNode struct{}

func (outdatedNode) Call(_ NodeConnection, body M, _ bool) M {
	fail("ACTION_REJECTED", "节点动作或参数无效", 409)
	return nil
}

func TestConfigJobsExplainThatTheNodeToolIsOutdated(t *testing.T) {
	c := testControl(t)
	w := NewWorker(c, outdatedNode{})
	change := ConfigChange{BaseHash: strings.Repeat("a", 64), Section: "log", Content: M{"level": "warn"}}
	for key, input := range map[string]NodeJobInput{
		"outdated-config-read-01": {Kind: "config", Draft: draftFor(c)},
		"outdated-config-edit-01": {Kind: "config_change", Draft: draftFor(c), ConfigChange: &change},
	} {
		submitted := c.SubmitNodeJob("admin", key, input)
		w.NodeStep()
		job := c.NodeJob(str(submitted["job_id"]))
		if job["state"] != "failed" || job["error_code"] != "NODE_UPGRADE_REQUIRED" || !strings.Contains(str(job["message"]), "升级") {
			t.Fatalf("%s: expected an upgrade instruction, got %v", input.Kind, job)
		}
	}
}
