package sbctl

import (
	"bytes"
	"github.com/pelletier/go-toml/v2"
	"path/filepath"
	"testing"
)

func maintenanceFixture(t *testing.T) (NodeMaintenance, *fakeEngine) {
	a, e := testAgent(t)
	a.Accept(agentRequest(a, 1, []Credential{testCredential()}, nil))
	a.ApplyOnce()
	// Maintenance preserves exact settings bytes and proxy identities.
	settingsPath := filepath.Join(t.TempDir(), "node.toml")
	b, err := toml.Marshal(a.Settings)
	check(err)
	atomicWrite(settingsPath, b, 0600)
	return NodeMaintenance{a.Settings, settingsPath, func(s NodeSettings) Engine { e.s = s; return e }}, e
}
func protocolInput(m NodeMaintenance, action, tag string) ProtocolChange {
	return ProtocolChange{Action: action, Tag: tag, BaseHash: catalogFor(m.Settings, object(read(filepath.Join(m.Settings.ConfigDir, "config.json"))), protocolState(m.Settings)).Hash}
}
func updatedMaintenance(m NodeMaintenance) NodeMaintenance {
	check(toml.Unmarshal(read(m.SettingsPath), &m.Settings))
	return m
}
func TestProtocolCreateEditDisableEnableDelete(t *testing.T) {
	m, e := maintenanceFixture(t)
	before := object(read(filepath.Join(m.Settings.ConfigDir, "config.json")))
	secretBefore := canonical(findInbound(before, "anytls-in")["users"])
	p := protocolInput(m, "create", "new-direct")
	p.SourceTag = "legacy"
	p.Listen = "127.0.0.1"
	p.Port = 18090
	p.TargetHost = "example.test"
	p.TargetPort = 443
	p.Username = "local_test"
	r := m.Apply("maintenance_create", &p)
	count := e.restarts
	if !boolean(r["ready"]) || str(r["template_hash"]) == m.Settings.TemplateHash {
		t.Fatal("template not updated")
	}
	if digest(m.Apply("maintenance_create", &p)) != digest(r) || e.restarts != count {
		t.Fatal("replay restarted proxy")
	}
	m = updatedMaintenance(m)
	config := object(read(filepath.Join(m.Settings.ConfigDir, "config.json")))
	firstID := inboundIdentity(m.Settings, protocolState(m.Settings), findInbound(config, p.Tag))
	if !bytes.Equal(secretBefore, canonical(findInbound(config, "anytls-in")["users"])) {
		t.Fatal("credentials changed")
	}
	p = protocolInput(m, "edit", "new-direct")
	p.Listen = "127.0.0.1"
	p.Port = 18091
	p.TargetHost = "example.test"
	p.TargetPort = 8443
	m.Apply("maintenance_edit", &p)
	m = updatedMaintenance(m)
	for _, action := range []string{"disable", "enable", "delete"} {
		p = protocolInput(m, action, "new-direct")
		m.Apply("maintenance_"+action, &p)
		m = updatedMaintenance(m)
		present := findInbound(object(read(filepath.Join(m.Settings.ConfigDir, "config.json"))), "new-direct") != nil
		if present != (action == "enable") {
			t.Fatalf("wrong %s visibility", action)
		}
	}
	p = protocolInput(m, "create", "new-direct")
	p.SourceTag = "legacy"
	p.Listen = "127.0.0.1"
	p.Port = 18090
	p.TargetHost = "example.test"
	p.TargetPort = 443
	p.Username = "local_test"
	m.Apply("maintenance_reuse", &p)
	m = updatedMaintenance(m)
	active := object(read(filepath.Join(m.Settings.ConfigDir, "config.json")))
	if inboundIdentity(m.Settings, protocolState(m.Settings), findInbound(active, p.Tag)) == firstID {
		t.Fatal("recreated entrance inherited the deleted entrance ID")
	}
}
func TestProtocolValidationAndProtection(t *testing.T) {
	m, e := maintenanceFixture(t)
	p := protocolInput(m, "disable", "anytls-in")
	expectFault(t, "RETIREMENT_REQUIRED", func() { m.Apply("maintenance_disable_managed", &p) })
	original := read(filepath.Join(m.Settings.ConfigDir, "config.json"))
	restarts := e.restarts
	p = protocolInput(m, "edit", "legacy")
	p.Listen = "0.0.0.0"
	p.Port = 8081
	p.TargetHost = "example.test"
	p.TargetPort = 443
	p.BaseHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	expectFault(t, "PROTOCOL_CHANGED", func() { m.Apply("maintenance_stale", &p) })
	p.BaseHash = protocolInput(m, "edit", "legacy").BaseHash
	e.preflight = true
	expectFault(t, "PREFLIGHT_FAILED", func() { m.Apply("maintenance_invalid", &p) })
	if e.restarts != restarts || !bytes.Equal(original, read(filepath.Join(m.Settings.ConfigDir, "config.json"))) {
		t.Fatal("preflight touched live state")
	}
}

func TestProtocolMixedRouteReferences(t *testing.T) {
	m, _ := maintenanceFixture(t)
	config := object(read(filepath.Join(m.Settings.ConfigDir, "config.json")))
	template := object(read(m.Settings.TemplateFile))
	disabled := protocolState(m.Settings)
	config["route"] = M{"rules": []M{{"inbound": "anytls-in", "outbound": "direct"}, {"inbound": []string{"tuic-in", "other"}, "outbound": "direct"}}}
	p := ProtocolChange{Action: "disable", Tag: "legacy", BaseHash: catalogFor(m.Settings, config, disabled).Hash}
	_, updated, _, _ := planProtocol(m.Settings, config, template, disabled, p)
	if findInbound(updated, "legacy") != nil || digest(updated["route"]) != digest(config["route"]) {
		t.Fatal("unrelated scalar route reference blocked removal or changed routing")
	}
	for _, selection := range []any{"legacy", []string{"other", "legacy"}} {
		config["route"] = M{"rules": []M{{"type": "logical", "rules": []M{{"inbound": selection}}}}}
		p.BaseHash = catalogFor(m.Settings, config, disabled).Hash
		for _, action := range []string{"disable", "delete"} {
			p.Action = action
			expectFault(t, "PROTOCOL_REFERENCED", func() { planProtocol(m.Settings, config, template, disabled, p) })
		}
	}
}
func TestProtocolRollbackAndRecovery(t *testing.T) {
	m, e := maintenanceFixture(t)
	before := read(filepath.Join(m.Settings.ConfigDir, "config.json"))
	template := read(m.Settings.TemplateFile)
	settings := read(m.SettingsPath)
	p := protocolInput(m, "edit", "legacy")
	p.Listen = "127.0.0.1"
	p.Port = 18088
	p.TargetHost = "example.test"
	p.TargetPort = 443
	failNext := true
	m.Engine = func(s NodeSettings) Engine { e.s = s; return &maintenanceFailure{e, &failNext} }
	expectFault(t, "MAINTENANCE_ROLLED_BACK", func() { m.Apply("maintenance_rollback", &p) })
	if !bytes.Equal(before, read(filepath.Join(m.Settings.ConfigDir, "config.json"))) || !bytes.Equal(template, read(m.Settings.TemplateFile)) || !bytes.Equal(settings, read(m.SettingsPath)) {
		t.Fatal("rollback did not restore bytes")
	}
	m.Apply("maintenance_rollback", &p)
	if integer(findInbound(object(read(filepath.Join(m.Settings.ConfigDir, "config.json"))), "legacy")["listen_port"]) != 18088 {
		t.Fatal("retry did not apply")
	}
}
func TestProtocolSecretStorageAndMaskedCatalog(t *testing.T) {
	c := testControl(t)
	p := ProtocolChange{Action: "create", Tag: "extra_tls", SourceTag: "anytls-in", BaseHash: stringsRepeatHash(), Listen: "0.0.0.0", Port: 5444, Username: "local_alice", Password: "unique-new-password-that-must-be-encrypted"}
	input := NodeJobInput{Kind: "protocol_change", Draft: draftFor(c), Change: &p}
	r := c.SubmitNodeJob("admin", "protocol-secret-01", input)
	if bytes.Contains(read(c.Settings.ControlDB), []byte(p.Password)) {
		t.Fatal("plaintext protocol password")
	}
	var raw string
	transaction(c.Settings.ControlDB, false, func(d *DB) { raw = str(d.Value("SELECT input_json FROM node_jobs WHERE id=?", r["job_id"])) })
	if decodeNodeInput(c, raw).Change.Password != p.Password {
		t.Fatal("secret decode")
	}
	m, _ := maintenanceFixture(t)
	p = protocolInput(m, "create", "extra_tls")
	p.SourceTag = "anytls-in"
	p.Listen = "127.0.0.1"
	p.Port = 5444
	p.Username = "local_alice"
	p.Password = "unique-new-password-that-must-be-encrypted"
	result := m.Apply("maintenance_secret", &p)
	if bytes.Contains(canonical(result), []byte(p.Password)) {
		t.Fatal("receipt leaked credentials")
	}
}
func stringsRepeatHash() string {
	return "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
}

type maintenanceFailure struct {
	*fakeEngine
	next *bool
}

func (e *maintenanceFailure) Restart() {
	e.fakeEngine.Restart()
	if *e.next {
		*e.next = false
		fail("START_FAILED", "controlled restart failure", 503)
	}
}
func TestProtocolCrashRecoveryDoesNotRepeatRestart(t *testing.T) {
	m, e := maintenanceFixture(t)
	p := protocolInput(m, "edit", "legacy")
	p.Listen = "127.0.0.1"
	p.Port = 18089
	p.TargetHost = "example.test"
	p.TargetPort = 443
	e.crash = true
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected process interruption")
			}
		}()
		m.Apply("maintenance_interrupted", &p)
	}()
	count := e.restarts
	m = updatedMaintenance(m)
	r := m.Apply("maintenance_interrupted", &p)
	if e.restarts != count || !boolean(r["ready"]) || exists(filepath.Join(m.Settings.StateDir, "maintenance-pending.json")) {
		t.Fatal("recovery repeated restart or left barrier")
	}
}

func TestMaintenanceReceiptClearsOnlyCompletedBarrier(t *testing.T) {
	m, _ := maintenanceFixture(t)
	id := "maintenance_receipt_barrier"
	inputHash := digest(M{"action": "edit"})
	pendingPath := filepath.Join(m.Settings.StateDir, "maintenance-pending.json")
	atomicWrite(pendingPath, canonical(M{"id": id}), 0600)
	if state := m.ReceiptStatus(id, inputHash)["state"]; state != "missing" || !exists(pendingPath) {
		t.Fatal("missing receipt must leave unfinished barrier in place")
	}
	atomicWrite(filepath.Join(m.Settings.StateDir, "maintenance-"+id+".json"), canonical(M{"input_hash": inputHash, "result": M{"node_id": m.Settings.NodeID}}), 0600)
	if state := m.ReceiptStatus(id, inputHash)["state"]; state != "completed" || exists(pendingPath) {
		t.Fatal("completed receipt must release its matching barrier")
	}
	atomicWrite(pendingPath, canonical(M{"id": "other_maintenance"}), 0600)
	expectFault(t, "REQUEST_CONFLICT", func() { m.ReceiptStatus(id, inputHash) })
	if !exists(pendingPath) {
		t.Fatal("receipt query removed another request's barrier")
	}
}
