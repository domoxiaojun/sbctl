package sbctl

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func disabledUser(t *testing.T, c *Control, w *Worker) (string, M) {
	t.Helper()
	c.CreateUser("admin", "lifecycle-create-01", CreateUser{DisplayName: "保留身份", Note: "保留备注", GroupIDs: []string{"grp_hk"}}, nil)
	w.Step()
	u := Views{c.Settings}.Users()[0]
	rev := integer(u["revision"])
	op := c.DisableUser("admin", "lifecycle-disable-01", str(u["id"]), DisableUser{&rev, "停用"})
	return str(u["id"]), op
}

func TestEnablePreservesIdentityAndRotatesAllSecrets(t *testing.T) {
	c := testControl(t)
	f := &fakeTransport{}
	w := NewWorker(c, f)
	c.CreateUser("admin", "lifecycle-create-01", CreateUser{DisplayName: "保留身份", Note: "保留备注", GroupIDs: []string{"grp_hk"}}, nil)
	w.Step()
	v := Views{c.Settings}
	original := v.Users()[0]
	id := str(original["id"])
	snapshot := M{"started_at": now(), "sampled_at": now(), "data_quality": M{"integrity_state": "ok", "gap_count": 0, "has_historical_gap": false}}
	atomicWrite(c.Settings.TrafficSnapshot, canonical(M{"nodes": M{"node_hk": M{"snapshot": snapshot, "synced_at": now()}}}), 0644)
	history := read(c.Settings.TrafficSnapshot)
	oldCredentials := f.Requests[0].Users
	oldLink := c.SubscriptionLink("admin", "lifecycle-link-0001", id)
	oldToken := strings.Split(str(oldLink["url"]), "/")[4]
	rev := integer(original["revision"])
	disabled := c.DisableUser("admin", "lifecycle-disable-01", id, DisableUser{&rev, "停用"})
	w.Step()
	rev++
	input := EnableUser{&rev, "恢复访问"}
	enabled := c.EnableUser("admin", "lifecycle-enable-01", id, input)
	if digest(enabled) != digest(c.EnableUser("admin", "lifecycle-enable-01", id, input)) {
		t.Fatal("reenable retry generated another operation")
	}
	expectFault(t, "STALE_REVISION", func() { c.EnableUser("admin", "lifecycle-enable-02", id, input) })
	expectFault(t, "HASH_CONFLICT", func() { c.EnableUser("admin", "lifecycle-enable-01", id, EnableUser{&rev, "不同原因"}) })
	if u := v.Users()[0]; u["status"] != "provisioning" || u["subscription_state"] != "pending" {
		t.Fatal("unverified credentials became ready")
	}
	expectFault(t, "NOT_READY", func() { c.SubscriptionLink("admin", "lifecycle-link-0002", id) })
	w.Reconcile()
	expectFault(t, "NOT_FOUND", func() { c.Public.Read(oldToken, "surge.conf") })
	w.Step()
	u := v.Users()[0]
	if u["status"] != "active" || u["id"] != id || u["created_at"] != original["created_at"] || u["note"] != original["note"] || integer(u["revision"]) != 3 {
		t.Fatal("identity metadata changed or enable incomplete")
	}
	if !bytes.Equal(history, read(c.Settings.TrafficSnapshot)) {
		t.Fatal("historical telemetry changed")
	}
	last := f.Requests[len(f.Requests)-1]
	if len(last.Users) != 2 || len(last.Revoked) != 2 {
		t.Fatal("expected two new credentials and two retained revocations")
	}
	for _, fresh := range last.Users {
		if fresh.UserID != id || fresh.Generation != 2 {
			t.Fatal("invalid credential generation or identity")
		}
		for _, old := range oldCredentials {
			if fresh.Password == old.Password || fresh.CredentialID == old.CredentialID || fresh.Fingerprint() == old.Fingerprint() || (fresh.UUID != nil && old.UUID != nil && *fresh.UUID == *old.UUID) {
				t.Fatal("reused a revoked credential")
			}
		}
	}
	newLink := c.SubscriptionLink("admin", "lifecycle-link-0002", id)
	newToken := strings.Split(str(newLink["url"]), "/")[4]
	for _, name := range []string{"surge.conf", "mihomo.yaml"} {
		expectFault(t, "NOT_FOUND", func() { c.Public.Read(oldToken, name) })
		body := c.Public.Read(newToken, name)
		for _, old := range oldCredentials {
			if bytes.Contains(body, []byte(old.Password)) {
				t.Fatal("old credential leaked into new subscription")
			}
		}
	}
	expectFault(t, "LINK_EXPIRED", func() { c.SubscriptionLink("admin", "lifecycle-link-0001", id) })
	if one(v.Operations(), str(enabled["operation_id"]))["state"] != "completed" || one(v.Operations(), str(disabled["operation_id"]))["subscription_state"] != "revoked" {
		t.Fatal("operation history changed meaning")
	}
	// A second cycle proves the uniqueness constraint does not limit enable to once.
	rev = integer(u["revision"])
	c.DisableUser("admin", "lifecycle-disable-02", id, DisableUser{&rev, "再次停用"})
	w.Step()
	rev++
	c.EnableUser("admin", "lifecycle-enable-03", id, EnableUser{&rev, "再次启用"})
	w.Step()
	for _, fresh := range f.Requests[len(f.Requests)-1].Users {
		if fresh.Generation != 3 {
			t.Fatal("credential generation did not advance")
		}
	}
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		if integer(d.Value("SELECT generation FROM subscriptions WHERE user_id=?", id)) != 3 || integer(d.Value("SELECT count(*) FROM users")) != 1 {
			t.Fatal("subscription generation or permanent identity was lost")
		}
	})
}

func TestEnableConcurrentRetryAndPendingRevocation(t *testing.T) {
	c := testControl(t)
	f := &fakeTransport{}
	w := NewWorker(c, f)
	id, disabled := disabledUser(t, c, w)
	// Simulate an unfinished operation created by the previous release.
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		d.Exec("DELETE FROM meta WHERE key=?", "operation-revision:"+str(disabled["operation_id"]))
	})
	rev := int64(2)
	var wg sync.WaitGroup
	results, errs := make(chan M, 4), make(chan error, 4)
	for range 4 {
		wg.Go(func() {
			err := Attempt(func() { results <- c.EnableUser("admin", "concurrent-enable-01", id, EnableUser{&rev, "恢复"}) })
			if err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	operationID := ""
	for r := range results {
		if operationID != "" && operationID != str(r["operation_id"]) {
			t.Fatal("duplicate enable operation")
		}
		operationID = str(r["operation_id"])
	}
	// The older high-priority disable runs first, but it must apply current intent
	// with all old fingerprints revoked, and report its own goal as superseded.
	w.Step()
	if len(f.Requests[len(f.Requests)-1].Revoked) != 2 || len(f.Requests[len(f.Requests)-1].Users) != 2 {
		t.Fatal("pending revocation was discarded")
	}
	w.Step()
	v := Views{c.Settings}
	if one(v.Operations(), str(disabled["operation_id"]))["state"] != "superseded" || one(v.Operations(), operationID)["state"] != "completed" {
		t.Fatal("late operation was reported as current completion")
	}
	// Disable supersedes a newly queued enable before it can publish secrets.
	rev = 3
	c.DisableUser("admin", "pending-disable-02", id, DisableUser{&rev, "停用"})
	rev = 4
	enabled := c.EnableUser("admin", "pending-enable-02", id, EnableUser{&rev, "恢复"})
	rev = 5
	c.DisableUser("admin", "pending-disable-03", id, DisableUser{&rev, "紧急停用"})
	for w.Step() {
	}
	if v.Users()[0]["status"] != "disabled" || len(f.Requests[len(f.Requests)-1].Users) != 0 || one(v.Operations(), str(enabled["operation_id"]))["state"] != "superseded" {
		t.Fatal("late enable restored disabled access")
	}
}

func TestEnableRejectsUnsafeInputsWithoutMutation(t *testing.T) {
	for _, scenario := range []string{"stale", "missing-revision", "empty-reason", "control-character", "capacity", "empty-group", "busy", "publication"} {
		t.Run(scenario, func(t *testing.T) {
			c := testControl(t)
			id, _ := disabledUser(t, c, NewWorker(c, &fakeTransport{}))
			rev := int64(2)
			input, code := EnableUser{&rev, "恢复"}, "INVALID_REQUEST"
			switch scenario {
			case "stale":
				rev = 1
				code = "STALE_REVISION"
			case "missing-revision":
				input.Revision = nil
			case "empty-reason":
				input.Reason = "  "
			case "control-character":
				input.Reason = "a\nb"
			case "capacity":
				c.Settings.MaxUsers = 0
				code = "USER_LIMIT"
			case "empty-group":
				transaction(c.Settings.ControlDB, true, func(d *DB) { d.Exec("UPDATE nodes SET enabled=0") })
				code = "GRANT_EMPTY"
			case "busy":
				transaction(c.Settings.ControlDB, true, func(d *DB) {
					d.Exec("INSERT INTO node_jobs(id,kind,node_id,state,input_json,created_at,updated_at) VALUES ('job_busy','configure','node_hk','queued','{}',?,?)", now(), now())
				})
				code = "NODE_BUSY"
			case "publication":
				path := filepath.Join(c.Settings.PublicDir, "index.json")
				check(os.Remove(path))
				check(os.Mkdir(path, 0700))
				code = "SUBSCRIPTION_REVOKE_PENDING"
			}
			expectFault(t, code, func() { c.EnableUser("admin", "rejected-enable-01", id, input) })
			transaction(c.Settings.ControlDB, false, func(d *DB) {
				if integer(d.Value("SELECT count(*) FROM credentials")) != 2 || integer(d.Value("SELECT revision FROM users WHERE id=?", id)) != 2 || integer(d.Value("SELECT count(*) FROM operations WHERE kind='enable_user'")) != 0 {
					t.Fatal("rejected enable left partial mutations")
				}
			})
		})
	}
}

func TestExpiredWorkerCannotAllocateRequest(t *testing.T) {
	c := testControl(t)
	c.CreateUser("admin", "expired-worker-create", CreateUser{DisplayName: "测试", GroupIDs: []string{"grp_hk"}}, nil)
	old := NewWorker(c, &fakeTransport{})
	task := old.Claim()
	transaction(c.Settings.ControlDB, true, func(d *DB) { d.Exec("UPDATE outbox SET lease_until=0 WHERE id=?", task["id"]) })
	expectFault(t, "OUTBOX_LEASE_LOST", func() { old.Request(task) })
	fresh := NewWorker(c, &fakeTransport{})
	claimed := fresh.Claim()
	expectFault(t, "OUTBOX_LEASE_LOST", func() { old.Request(task) })
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		if integer(d.Value("SELECT revision FROM nodes WHERE id='node_hk'")) != 0 || integer(d.Value("SELECT count(*) FROM node_requests")) != 0 {
			t.Fatal("stale worker allocated a revision")
		}
	})
	fresh.Request(claimed)
}

func TestMixedSupersededTargetsAreTerminal(t *testing.T) {
	c := testControl(t)
	id, disabled := disabledUser(t, c, NewWorker(c, &fakeTransport{}))
	rev := int64(2)
	c.EnableUser("admin", "mixed-enable-request", id, EnableUser{&rev, "恢复"})
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		// Reuse an existing target with two terminal states from different nodes.
		d.Exec("INSERT INTO nodes SELECT 'node_other',name,region,location,management_address,public_host,connection_json,endpoints_json,template_hash,image_digest,revision,applied_revision,created_at,enabled FROM nodes WHERE id='node_hk'")
		d.Exec("UPDATE operation_targets SET state='verified' WHERE operation_id=?", disabled["operation_id"])
		d.Exec("INSERT INTO operation_targets VALUES (?, 'node_other','superseded','已由后续变更替代',NULL)", disabled["operation_id"])
	})
	NewWorker(c, &fakeTransport{}).Reconcile()
	if one(Views{c.Settings}.Operations(), str(disabled["operation_id"]))["state"] != "superseded" {
		t.Fatal("mixed terminal targets stayed pending")
	}
}

func TestEnableUsesCurrentGroupsAndRetainsHistoricalUsage(t *testing.T) {
	c := testControl(t)
	w := NewWorker(c, &fakeTransport{})
	id, _ := disabledUser(t, c, w)
	w.Step()
	atomicWrite(c.Settings.TrafficSnapshot, canonical(M{"nodes": M{"node_hk": M{"snapshot": M{"started_at": now(), "sampled_at": now()}, "synced_at": now()}}}), 0644)
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		d.Exec("INSERT INTO nodes SELECT 'node_new',name,region,location,management_address,public_host,connection_json,endpoints_json,template_hash,image_digest,0,0,created_at,enabled FROM nodes WHERE id='node_hk'")
		d.Exec("DELETE FROM group_nodes WHERE group_id='grp_hk'")
		d.Exec("INSERT INTO group_nodes VALUES ('grp_hk','node_new')")
	})
	rev := int64(2)
	enabled := c.EnableUser("admin", "current-group-enable", id, EnableUser{&rev, "恢复"})
	v := Views{c.Settings}
	u, sub := v.Users()[0], v.Subscription(id)
	op := one(v.Operations(), str(enabled["operation_id"]))
	ts := array(op["targets"])
	if len(ts) != 1 || mapping(ts[0])["node_id"] != "node_new" || integer(u["node_count"]) != 1 || integer(sub["total_nodes"]) != 1 {
		t.Fatal("authorization still counts removed historical nodes")
	}
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		if integer(d.Value("SELECT count(*) FROM credentials WHERE node_id='node_hk' AND revoked=0")) != 0 {
			t.Fatal("removed node received new access")
		}
	})
}

func TestEnableCLIUsesSharedIdempotentDomain(t *testing.T) {
	c := testControl(t)
	id, _ := disabledUser(t, c, NewWorker(c, &fakeTransport{}))
	config := filepath.Join(t.TempDir(), "server.toml")
	body, err := toml.Marshal(c.Settings)
	if err != nil {
		t.Fatal(err)
	}
	atomicWrite(config, body, 0600)
	t.Setenv("SBCTL_CONFIG", config)
	args := []string{"enable-user", id, "2", "恢复", "cli-enable-request"}
	if ControllerMain(args) != 0 || ControllerMain(args) != 0 {
		t.Fatal("CLI enable or retry failed")
	}
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		if integer(d.Value("SELECT count(*) FROM operations WHERE kind='enable_user'")) != 1 || integer(d.Value("SELECT count(*) FROM credentials WHERE revoked=0")) != 2 {
			t.Fatal("CLI created duplicate credentials")
		}
	})
}
