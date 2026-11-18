package sbctl

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

type configBridge struct {
	a            *Agent
	e            *fakeEngine
	settingsPath string
	lostReply    bool
	beforeAdopt  func()
	adopts       int
	lastError    string
}

func (b *configBridge) Call(_ NodeConnection, body M, _ bool) M {
	switch body["action"] {
	case "protocols":
		config, _ := currentConfig(b.a.Settings)
		return object(canonical(catalogFor(b.a.Settings, config, protocolState(b.a.Settings))))
	case "protocol_change":
		var change ProtocolChange
		decode(canonical(body["change"]), &change)
		maintenance := NodeMaintenance{b.a.Settings, b.settingsPath, func(s NodeSettings) Engine { b.e.s = s; return b.e }}
		result := maintenance.Apply(str(body["request_id"]), &change)
		b.a = NewAgent(updatedMaintenance(maintenance).Settings, b.e)
		return result
	case "submit":
		r := parseRequest(body["request"])
		b.a.Accept(r)
		b.a.ApplyOnce()
		return b.a.Status()
	case "config_snapshot":
		return object(canonical(snapshotConfig(b.a.Settings, b.e)))
	case "adopt_config":
		if b.beforeAdopt != nil {
			f := b.beforeAdopt
			b.beforeAdopt = nil
			f()
		}
		var adoption ConfigAdoption
		decode(canonical(body["adoption"]), &adoption)
		var result M
		if err := Attempt(func() { result = adoptConfig(b.a.Settings, b.settingsPath, b.e, adoption) }); err != nil {
			b.lastError = PublicFault(err).Code + ": " + err.Error()
			panic(err)
		}
		var next NodeSettings
		check(toml.Unmarshal(read(b.settingsPath), &next))
		b.a.Settings = next
		b.e.s = next
		b.adopts++
		if b.lostReply {
			b.lostReply = false
			fail("NODE_UNREACHABLE", "simulated lost reply", 503)
		}
		return result
	}
	fail("ACTION_REJECTED", "unknown fake action")
	return nil
}
func syncFixture(t *testing.T) (*Control, *Worker, *configBridge, string) {
	t.Helper()
	c := testControl(t)
	a, e := testAgent(t)
	template := object(read(a.Settings.TemplateFile))
	for _, raw := range array(template["inbounds"]) {
		in := mapping(raw)
		if in["type"] == "anytls" || in["type"] == "tuic" {
			in["listen_port"] = 443
			in["tls"] = M{"server_name": "example.test", "alpn": []string{"h3"}}
		}
	}
	atomicWrite(a.Settings.TemplateFile, append(canonical(template), '\n'), 0600)
	a.Settings.TemplateHash = hash(read(a.Settings.TemplateFile))
	e.s = a.Settings
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		d.Exec("UPDATE nodes SET template_hash=?,image_digest=? WHERE id='node_hk'", a.Settings.TemplateHash, "sha256:"+a.Settings.EngineBuildID)
	})
	markTestEndpointsVerified(c)
	b := &configBridge{a: a, e: e, settingsPath: filepath.Join(t.TempDir(), "node.toml")}
	settings, err := toml.Marshal(a.Settings)
	check(err)
	atomicWrite(b.settingsPath, settings, 0600)
	w := NewWorker(c, b)
	c.CreateUser("admin", "config-sync-fixture-01", CreateUser{DisplayName: "同步用户", GroupIDs: []string{"grp_hk"}}, nil)
	w.Step()
	user := ""
	transaction(c.Settings.ControlDB, false, func(d *DB) { user = str(d.Value("SELECT id FROM users WHERE display_name='同步用户'")) })
	w.SyncConfigSnapshots()
	return c, w, b, user
}
func writeManual(b *configBridge, mutate func(M), restart bool) M {
	config, _ := currentConfig(b.a.Settings)
	mutate(config)
	raw := append(canonical(config), '\n')
	atomicWrite(filepath.Join(b.a.Settings.ConfigDir, "config.json"), raw, 0640)
	if restart {
		b.e.epoch++
		b.e.binding["config_hash"] = hash(raw)
	}
	return config
}
func syncState(c *Control) string {
	s := ""
	transaction(c.Settings.ControlDB, false, func(d *DB) { s = str(d.Value("SELECT state FROM node_config_snapshots WHERE node_id='node_hk'")) })
	return s
}
func credentialRows(c *Control, user string) []M {
	rows := []M{}
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		rows = d.Rows("SELECT * FROM credentials WHERE user_id=? AND revoked=0 ORDER BY protocol", user)
	})
	return rows
}

func TestConfigSyncWaitsForRestartAndIgnoresFormatting(t *testing.T) {
	c, w, b, _ := syncFixture(t)
	first := snapshotConfig(b.a.Settings, b.e)
	raw := read(filepath.Join(b.a.Settings.ConfigDir, "config.json"))
	atomicWrite(filepath.Join(b.a.Settings.ConfigDir, "config.json"), append([]byte(" \n"), raw...), 0640)
	formatted := snapshotConfig(b.a.Settings, b.e)
	if first.EventSeq != formatted.EventSeq || formatted.External {
		t.Fatal("formatting produced a semantic event")
	}
	changed := writeManual(b, func(config M) { config["log"] = M{"level": "warn"} }, false)
	w.SyncConfigSnapshots()
	if syncState(c) != "pending_restart" || b.adopts != 0 {
		t.Fatal("a live edit was adopted before restart")
	}
	b.e.epoch++
	b.e.binding["config_hash"] = hash(read(filepath.Join(b.a.Settings.ConfigDir, "config.json")))
	w.SyncConfigSnapshots()
	if syncState(c) != "synced" || b.adopts != 1 || digest(object(read(b.a.Settings.TemplateFile))["log"]) != digest(changed["log"]) {
		t.Fatalf("restarted edit was not adopted: %s (%s)", syncState(c), b.lastError)
	}
	w.SyncConfigSnapshots()
	if b.adopts != 1 {
		t.Fatal("duplicate event repeated adoption")
	}
}
func TestConfigSyncUpdatesCredentialsEndpointsSubscriptionsAndTemplate(t *testing.T) {
	c, w, b, user := syncFixture(t)
	old := credentialRows(c, user)
	writeManual(b, func(config M) {
		config["route"] = M{"final": "direct"}
		config["outbounds"] = []M{{"type": "direct", "tag": "direct"}, {"type": "socks", "tag": "secret-out", "server": "example.test", "password": "private-outbound-value"}}
		config["future_option"] = M{"enabled": true}
		any := findInbound(config, "anytls-in")
		any["listen_port"] = 5443
		any["tag"] = "new-any"
		mapping(any["tls"])["server_name"] = "new.example.test"
		for _, raw := range array(any["users"]) {
			if mapping(raw)["name"] == user {
				mapping(raw)["password"] = "manual-any-secret"
			}
		}
		tuic := findInbound(config, "tuic-in")
		for _, raw := range array(tuic["users"]) {
			if mapping(raw)["name"] == user {
				mapping(raw)["password"] = "manual-tuic-secret"
				mapping(raw)["uuid"] = "5fd05b3a-99b6-4ee1-b026-b499dd577d07"
			}
		}
	}, true)
	w.SyncConfigSnapshots()
	if syncState(c) != "synced" {
		t.Fatalf("external config did not synchronize: %s (%s)", syncState(c), b.lastError)
	}
	rows := credentialRows(c, user)
	if len(rows) != 2 {
		t.Fatal("lost active credentials")
	}
	for _, row := range rows {
		secret := mapping(c.Vault.Decrypt(str(row["secret_ciphertext"])))
		if integer(secret["external_sync_seq"]) < 1 || !strings.HasPrefix(str(secret["password"]), "manual-") {
			t.Fatal("external generation missing")
		}
	}
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		for _, r := range old {
			if d.One("SELECT 1 FROM revocations WHERE fingerprint=?", r["fingerprint"]) == nil {
				t.Fatal("revocation history lost")
			}
		}
	})
	pub := c.Public.Current()
	if len(pub) != 1 || !bytes.Contains(read(c.Settings.ControlDB), []byte("v2:")) {
		t.Fatal("subscription or encryption missing")
	}
	for key, rawEntry := range pub {
		body := read(filepath.Join(c.Public.Root, "data", key, str(mapping(rawEntry)["revision"]), "surge.conf"))
		for _, value := range []string{"manual-any-secret", "manual-tuic-secret", "5443", "new.example.test"} {
			if !bytes.Contains(body, []byte(value)) {
				t.Fatal("subscription did not publish the adopted endpoint and secrets")
			}
		}
	}
	for _, value := range []string{"manual-any-secret", "manual-tuic-secret", "private-outbound-value"} {
		if bytes.Contains(read(c.Settings.ControlDB), []byte(value)) || bytes.Contains(canonical((Views{c.Settings}).Nodes()), []byte(value)) {
			t.Fatal("snapshot secret leaked")
		}
	}
	c.CreateUser("admin", "config-sync-next-user-01", CreateUser{DisplayName: "同步之后", GroupIDs: []string{"grp_hk"}}, nil)
	w.Step()
	active, _ := currentConfig(b.a.Settings)
	if active["future_option"] == nil || findInbound(active, "new-any") == nil || str(mapping(active["route"])["final"]) != "direct" {
		t.Fatal("later authorization overwrote local changes")
	}
}
func TestConfigSyncRecoversLostAdoptionReply(t *testing.T) {
	c, w, b, user := syncFixture(t)
	writeManual(b, func(config M) {
		for _, raw := range array(findInbound(config, "anytls-in")["users"]) {
			if mapping(raw)["name"] == user {
				mapping(raw)["password"] = "new-secret-for-reply-loss"
			}
		}
	}, true)
	b.lostReply = true
	w.SyncConfigSnapshots()
	if syncState(c) != "pending_sync" {
		t.Fatal("lost reply was reported as synchronized")
	}
	w.SyncConfigSnapshots()
	if syncState(c) != "synced" {
		t.Fatal("lost adoption reply was not recovered")
	}
	rows := credentialRows(c, user)
	if integer(rows[0]["generation"]) != 2 {
		t.Fatal("recovery created extra generations")
	}
}
func TestConfigSyncUnknownIdentityIsAtomic(t *testing.T) {
	c, w, b, user := syncFixture(t)
	before := digest(credentialRows(c, user))
	writeManual(b, func(config M) {
		in := findInbound(config, "anytls-in")
		in["listen_port"] = 5443
		in["users"] = append(array(in["users"]), M{"name": "usr_unknown", "password": "unknown-user-password"})
	}, true)
	w.SyncConfigSnapshots()
	if syncState(c) != "conflict" || digest(credentialRows(c, user)) != before || b.adopts != 0 {
		t.Fatal("unknown identity partially changed center state")
	}
	if !bytes.Contains(read(c.Settings.ControlDB), []byte("v2:")) {
		t.Fatal("conflicting snapshot was not saved")
	}
}
func TestConfigSyncDeletionAndReappearancePreserveRevocations(t *testing.T) {
	c, w, b, user := syncFixture(t)
	config, _ := currentConfig(b.a.Settings)
	saved := object(canonical(config))
	original := credentialRows(c, user)
	writeManual(b, func(config M) {
		for _, tag := range []string{"anytls-in", "tuic-in"} {
			in := findInbound(config, tag)
			kept := []any{}
			for _, raw := range array(in["users"]) {
				if mapping(raw)["name"] != user {
					kept = append(kept, raw)
				}
			}
			in["users"] = kept
		}
	}, true)
	w.SyncConfigSnapshots()
	if len(credentialRows(c, user)) != 0 {
		t.Fatal("deleted user was restored")
	}
	writeManual(b, func(config M) {
		for _, tag := range []string{"anytls-in", "tuic-in"} {
			findInbound(config, tag)["users"] = findInbound(saved, tag)["users"]
		}
	}, true)
	w.SyncConfigSnapshots()
	if syncState(c) != "synced" || len(credentialRows(c, user)) != 2 {
		t.Fatal("reintroduced user did not acquire a new generation")
	}
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		for _, row := range original {
			if d.One("SELECT 1 FROM revocations WHERE fingerprint=?", row["fingerprint"]) == nil {
				t.Fatal("historical revocation was deleted")
			}
		}
	})
	c.CreateUser("admin", "config-sync-after-reintro", CreateUser{DisplayName: "恢复后", GroupIDs: []string{"grp_hk"}}, nil)
	w.Step()
	if mapping(b.a.Status()["latest"])["state"] != "verified" {
		t.Fatal("node rejected retained revocations and external generation")
	}
}
func TestConfigSyncMissingEntranceStopsAllocation(t *testing.T) {
	c, w, b, user := syncFixture(t)
	writeManual(b, func(config M) { removeInbound(config, "anytls-in") }, true)
	w.SyncConfigSnapshots()
	w.Reconcile()
	if syncState(c) != "conflict" || len(credentialRows(c, user)) != 0 {
		t.Fatal("missing entrance did not revoke node access")
	}
	expectFault(t, "GRANT_EMPTY", func() {
		c.CreateUser("admin", "config-sync-missing-entrance", CreateUser{DisplayName: "不能分配", GroupIDs: []string{"grp_hk"}}, nil)
	})
}
func TestConfigSyncKeepsNewCenterIntentDuringRPC(t *testing.T) {
	c, w, b, user := syncFixture(t)
	writeManual(b, func(config M) { config["log"] = M{"level": "error"} }, true)
	b.beforeAdopt = func() {
		var rev int64
		transaction(c.Settings.ControlDB, false, func(d *DB) { rev = integer(d.Value("SELECT revision FROM users WHERE id=?", user)) })
		c.DisableUser("admin", "config-sync-concurrent-disable", user, DisableUser{&rev, "并发禁用"})
	}
	w.SyncConfigSnapshots()
	if len(credentialRows(c, user)) != 0 {
		t.Fatal("external snapshot overrode a newer center disable")
	}
	w.Step()
	if mapping(b.a.Status()["latest"])["state"] != "verified" {
		t.Fatal("new center intent did not recover")
	}
}
func TestConfigSyncSerializesPublishAndAdoption(t *testing.T) {
	c, w, b, _ := syncFixture(t)
	unlock := w.nodeLock("node_hk")
	before := b.adopts
	w.SyncConfigSnapshots()
	if b.adopts != before {
		t.Fatal("snapshot ran through held node lease")
	}
	unlock()
	if !strings.Contains(str(c.Settings.ControlDB), "control.db") {
		t.Fatal("invalid fixture")
	}
}

func TestConfigSyncRejectsSequenceRollbackAndIdentityConflicts(t *testing.T) {
	c, w, b, _ := syncFixture(t)
	snap := object(canonical(snapshotConfig(b.a.Settings, b.e)))
	snap["semantic_hash"] = strings.Repeat("a", 64)
	expectFault(t, "NODE_RESPONSE", func() { parseNodeSnapshot(snap) })
	// The center keeps its own high-water mark, including across a restart of
	// the node helper. A rolled-back node cannot silently replace that snapshot.
	transaction(c.Settings.ControlDB, true, func(d *DB) { d.Exec("UPDATE node_config_snapshots SET event_seq=100 WHERE node_id='node_hk'") })
	expectFault(t, "CONFIG_SEQUENCE_OLD", func() { w.syncNodeConfig("node_hk", parseConnection(M{"address": "100.70.0.10"})) })
}
func TestConfigAdoptionChecksObservedVersionBeforeWriting(t *testing.T) {
	c, _, b, _ := syncFixture(t)
	writeManual(b, func(config M) { config["route"] = M{"final": "direct"} }, true)
	snap := snapshotConfig(b.a.Settings, b.e)
	var plan configSyncPlan
	transaction(c.Settings.ControlDB, false, func(d *DB) { plan = prepareConfigSync(d, c, snap, syncContext(d, "node_hk")) })
	templateBefore := string(read(b.a.Settings.TemplateFile))
	writeManual(b, func(config M) { config["route"] = M{"final": "changed-again"} }, true)
	expectFault(t, "CONFIG_CHANGED", func() { adoptConfig(b.a.Settings, b.settingsPath, b.e, plan.Adoption) })
	if string(read(b.a.Settings.TemplateFile)) != templateBefore {
		t.Fatal("stale adoption overwrote the template")
	}
}
func TestAuthorizationStopsBeforeOverwritingAnExternalEdit(t *testing.T) {
	_, _, b, _ := syncFixture(t)
	next := agentRequest(b.a, 2, nil, nil)
	b.a.Accept(next)
	writeManual(b, func(config M) { config["route"] = M{"final": "keep-local"} }, false)
	if b.a.ApplyOnce() {
		t.Fatal("accepted authorization applied over a local edit")
	}
	config, _ := currentConfig(b.a.Settings)
	if mapping(config["route"])["final"] != "keep-local" {
		t.Fatal("manual edit was overwritten")
	}
	expectFault(t, "CONFIG_SYNC_PENDING", func() { b.a.Accept(agentRequest(b.a, 3, nil, nil)) })
}

func TestConfigAdoptionResumesAfterPartialFileWrite(t *testing.T) {
	c, _, b, _ := syncFixture(t)
	writeManual(b, func(config M) { config["route"] = M{"final": "kept-after-interruption"} }, true)
	snap := snapshotConfig(b.a.Settings, b.e)
	var plan configSyncPlan
	transaction(c.Settings.ControlDB, false, func(d *DB) { plan = prepareConfigSync(d, c, snap, syncContext(d, "node_hk")) })
	restarts := b.e.restarts
	// Renaming a settings file onto an existing directory fails after the
	// template/probe files have been installed, leaving the durable journal.
	badDestination := t.TempDir()
	if err := Attempt(func() { adoptConfig(b.a.Settings, badDestination, b.e, plan.Adoption) }); err == nil {
		t.Fatal("expected interrupted settings write")
	}
	if !exists(filepath.Join(b.a.Settings.StateDir, "config-adoption.json")) {
		t.Fatal("interrupted adoption lost its recovery journal")
	}
	result := adoptConfig(b.a.Settings, b.settingsPath, b.e, plan.Adoption)
	if result["request_id"] != plan.Adoption.Request.RequestID || exists(filepath.Join(b.a.Settings.StateDir, "config-adoption.json")) || b.e.restarts != restarts {
		t.Fatal("adoption did not recover without another restart")
	}
}
