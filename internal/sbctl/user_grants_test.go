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

func grantControl(t *testing.T) (*Control, *Worker, *fakeTransport, string) {
	t.Helper()
	c := testControl(t)
	c.Register(M{"id": "node_sg", "name": "新加坡", "region": "sg", "location": "新加坡", "connection": M{"address": "100.70.0.3"}, "endpoints": []Endpoint{{"anytls", "anytls-in", "sg.test", 443, "sg.test", nil}, {"tuic", "tuic-in", "sg.test", 443, "sg.test", nil}}, "template_hash": strings.Repeat("a", 64), "image_digest": "sha256:" + strings.Repeat("b", 64), "group_id": "grp_sg", "group_name": "新加坡"})
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		d.Exec("INSERT INTO groups VALUES ('grp_both','香港和新加坡')")
		d.Exec("INSERT INTO group_nodes VALUES ('grp_both','node_hk'),('grp_both','node_sg')")
	})
	f := &fakeTransport{}
	w := NewWorker(c, f)
	c.CreateUser("admin", "grants-create-0001", CreateUser{DisplayName: "授权调整用户", GroupIDs: []string{"grp_hk"}}, nil)
	w.Step()
	return c, w, f, str(Views{c.Settings}.Users()[0]["id"])
}

func grantChange(t *testing.T, c *Control, id string, groups ...string) (UpdateGrants, M) {
	t.Helper()
	rev := integer(one(Views{c.Settings}.Users(), id)["revision"])
	selection := GrantSelection{&rev, groups}
	preview := c.PreviewGrants(id, selection)
	return UpdateGrants{selection, "调整使用区域", str(preview["preview_hash"])}, preview
}

func TestGrantsUnionPreservesCredentialsAndSubscription(t *testing.T) {
	c, w, f, id := grantControl(t)
	old := f.Requests[0]
	link := c.SubscriptionLink("admin", "grants-link-000001", id)
	tok := strings.Split(str(link["url"]), "/")[4]
	history := canonical(M{"nodes": M{}, "sentinel": "historical usage"})
	atomicWrite(c.Settings.TrafficSnapshot, history, 0644)
	req, preview := grantChange(t, c, id, "grp_both", "grp_hk", "grp_both")
	if len(preview["added"].([]M)) != 1 || len(preview["retained"].([]M)) != 1 || len(preview["removed"].([]M)) != 0 {
		t.Fatal("incorrect union preview")
	}
	op := c.UpdateGrants("admin", "grants-update-001", id, req)
	if digest(op) != digest(c.UpdateGrants("admin", "grants-update-001", id, req)) {
		t.Fatal("retry created another operation")
	}
	for w.Step() {
	}
	for _, r := range f.Requests {
		if r.NodeID == "node_hk" && (r.RequestID != old.RequestID || digest(r.Users) != digest(old.Users)) {
			t.Fatal("retained node was assigned a new revision or credential")
		}
	}
	if len(c.Public.Read(tok, "surge.conf")) == 0 || one(Views{c.Settings}.Operations(), str(op["operation_id"]))["state"] != "completed" {
		t.Fatal("subscription or operation incomplete")
	}
	// Removing a redundant source changes the group assignment, not access.
	req, preview = grantChange(t, c, id, "grp_both")
	if len(preview["added"].([]M))+len(preview["removed"].([]M)) != 0 {
		t.Fatal("overlapping group removal revoked access")
	}
	c.UpdateGrants("admin", "grants-update-002", id, req)
	for w.Step() {
	}
	if !bytes.Equal(history, read(c.Settings.TrafficSnapshot)) {
		t.Fatal("grant edit changed historical traffic")
	}
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		if integer(d.Value("SELECT count(*) FROM credentials WHERE user_id=?", id)) != 4 || integer(d.Value("SELECT count(*) FROM revocations")) != 0 {
			t.Fatal("union edit duplicated or revoked credentials")
		}
	})
}

func TestGrantsRevokeBeforeAddAndReaddUsesNewSecrets(t *testing.T) {
	c, w, f, id := grantControl(t)
	old := f.Requests[0].Users
	tok := strings.Split(str(c.SubscriptionLink("admin", "grants-link-000001", id)["url"]), "/")[4]
	req, _ := grantChange(t, c, id, "grp_sg")
	op := c.UpdateGrants("admin", "grants-update-001", id, req)
	expectFault(t, "NOT_FOUND", func() { c.Public.Read(tok, "surge.conf") })
	f.State = "revoke_blocked"
	w.Step()
	r := f.Requests[len(f.Requests)-1]
	if r.NodeID != "node_hk" || len(r.Users) != 0 || len(r.Revoked) != 2 {
		t.Fatal("removal did not take priority")
	}
	f.State = ""
	w.Step()
	for _, name := range []string{"surge.conf", "mihomo.yaml"} {
		b := c.Public.Read(tok, name)
		if bytes.Contains(b, []byte("node_hk")) || !bytes.Contains(b, []byte("node_sg")) {
			t.Fatal("subscription includes removed node or omits ready node")
		}
	}
	if one(Views{c.Settings}.Operations(), str(op["operation_id"]))["state"] == "completed" {
		t.Fatal("blocked removal was marked complete")
	}
	// A new intent must still carry the unfinished removal, even though the
	// credentials have already been revoked in the controller database.
	req, preview := grantChange(t, c, id, "grp_both")
	if len(preview["added"].([]M)) != 1 {
		t.Fatal("re-added node not shown as new authorization")
	}
	latest := c.UpdateGrants("admin", "grants-update-002", id, req)
	transaction(c.Settings.ControlDB, true, func(d *DB) { d.Exec("UPDATE outbox SET next_attempt_at=0") })
	for w.Step() {
	}
	var last NodeRequest
	for _, r := range f.Requests {
		if r.NodeID == "node_hk" {
			last = r
		}
	}
	if len(last.Revoked) != 2 || len(last.Users) != 2 {
		t.Fatal("re-add lost old revocations or new credentials")
	}
	for _, fresh := range last.Users {
		if fresh.Generation != 2 {
			t.Fatal("generation not advanced")
		}
		for _, revoked := range old {
			if fresh.Fingerprint() == revoked.Fingerprint() || fresh.Password == revoked.Password {
				t.Fatal("revoked credential reused")
			}
		}
	}
	if one(Views{c.Settings}.Operations(), str(latest["operation_id"]))["state"] != "completed" || one(Views{c.Settings}.Operations(), str(op["operation_id"]))["state"] != "superseded" {
		t.Fatal("late operation completion wrong")
	}
}

func TestGrantsPreviewConflictAndValidationDoNotMutate(t *testing.T) {
	for _, scenario := range []string{"membership", "binding", "missing-hash", "stale", "unchanged", "disabled", "busy", "empty", "unknown", "reason"} {
		t.Run(scenario, func(t *testing.T) {
			c, _, _, id := grantControl(t)
			req, _ := grantChange(t, c, id, "grp_both")
			code := "GRANT_PREVIEW_CHANGED"
			switch scenario {
			case "membership":
				transaction(c.Settings.ControlDB, true, func(d *DB) { d.Exec("DELETE FROM group_nodes WHERE group_id='grp_both' AND node_id='node_sg'") })
			case "binding":
				transaction(c.Settings.ControlDB, true, func(d *DB) { d.Exec("UPDATE nodes SET template_hash=? WHERE id='node_sg'", strings.Repeat("c", 64)) })
			case "missing-hash":
				req.PreviewHash, code = "", "INVALID_REQUEST"
			case "stale":
				*req.Revision = 0
				code = "STALE_REVISION"
			case "unchanged":
				req, _ = grantChange(t, c, id, "grp_hk")
				code = "GRANT_UNCHANGED"
			case "disabled":
				transaction(c.Settings.ControlDB, true, func(d *DB) { d.Exec("UPDATE users SET status='disabled' WHERE id=?", id) })
				code = "USER_DISABLED"
			case "busy":
				transaction(c.Settings.ControlDB, true, func(d *DB) {
					d.Exec("INSERT INTO node_jobs(id,kind,node_id,state,input_json,created_at,updated_at) VALUES ('busy','configure','node_sg','queued','{}',?,?)", now(), now())
				})
				code = "NODE_BUSY"
			case "empty":
				req.GroupIDs, code = []string{}, "INVALID_REQUEST"
			case "unknown":
				req.GroupIDs, code = []string{"grp_missing"}, "GROUP_UNKNOWN"
			case "reason":
				req.Reason, code = "\n\x00", "INVALID_REQUEST"
			}
			before := read(c.Settings.ControlDB)
			expectFault(t, code, func() { c.UpdateGrants("admin", "grants-update-001", id, req) })
			if !bytes.Equal(before, read(c.Settings.ControlDB)) {
				t.Fatal("rejected request changed state")
			}
		})
	}
}

func TestGrantsPublicationFailureDoesNotBlockRevocation(t *testing.T) {
	c, w, f, id := grantControl(t)
	req, _ := grantChange(t, c, id, "grp_sg")
	index := filepath.Join(c.Settings.PublicDir, "index.json")
	check(os.Remove(index))
	check(os.Mkdir(index, 0700))
	op := c.UpdateGrants("admin", "grants-update-001", id, req)
	w.Step()
	if r := f.Requests[len(f.Requests)-1]; r.NodeID != "node_hk" || len(r.Users) != 0 || len(r.Revoked) != 2 {
		t.Fatal("subscription fault prevented node revocation")
	}
	w.Step()
	if one(Views{c.Settings}.Operations(), str(op["operation_id"]))["state"] == "completed" {
		t.Fatal("failed subscription was ignored")
	}
	check(os.Remove(index))
	w.Reconcile()
	if one(Views{c.Settings}.Operations(), str(op["operation_id"]))["state"] != "completed" {
		t.Fatal("publication did not recover")
	}
}

func TestGrantsConcurrentReplayAndDisableSupersedes(t *testing.T) {
	c, w, f, id := grantControl(t)
	req, _ := grantChange(t, c, id, "grp_sg")
	var wg sync.WaitGroup
	results := make(chan M, 3)
	for range 3 {
		wg.Go(func() { results <- c.UpdateGrants("admin", "grants-concurrent-01", id, req) })
	}
	wg.Wait()
	close(results)
	var operationID string
	for r := range results {
		if operationID != "" && operationID != str(r["operation_id"]) {
			t.Fatal("duplicate operation")
		}
		operationID = str(r["operation_id"])
	}
	rev := int64(2)
	c.DisableUser("admin", "grants-disable-01", id, DisableUser{&rev, "立即停用"})
	for w.Step() {
	}
	for _, r := range f.Requests[1:] {
		if len(r.Users) != 0 {
			t.Fatal("late grants restored disabled access")
		}
	}
	if one(Views{c.Settings}.Operations(), operationID)["state"] != "superseded" {
		t.Fatal("old grant operation was not superseded")
	}
}

func TestGrantsCLIUsesPreviewAndFixedRequestKey(t *testing.T) {
	c, _, _, id := grantControl(t)
	req, _ := grantChange(t, c, id, "grp_sg")
	config := filepath.Join(t.TempDir(), "server.toml")
	body, err := toml.Marshal(c.Settings)
	check(err)
	atomicWrite(config, body, 0600)
	t.Setenv("SBCTL_CONFIG", config)
	if ControllerMain([]string{"preview-grants", id, "1", "grp_sg"}) != 0 {
		t.Fatal("CLI preview failed")
	}
	args := []string{"update-grants", id, "1", "grp_sg", req.Reason, req.PreviewHash, "cli-grants-request"}
	if ControllerMain(args) != 0 || ControllerMain(args) != 0 {
		t.Fatal("CLI update or retry failed")
	}
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		if integer(d.Value("SELECT count(*) FROM operations WHERE kind='update_grants'")) != 1 || integer(d.Value("SELECT count(*) FROM credentials WHERE revoked=0")) != 2 {
			t.Fatal("CLI duplicated work")
		}
	})
}

func TestGrantsPendingRemovalSurvivesNextIntent(t *testing.T) {
	c, w, f, id := grantControl(t)
	req, _ := grantChange(t, c, id, "grp_sg")
	old := c.UpdateGrants("admin", "grants-update-001", id, req)
	f.State = "revoke_blocked"
	w.Step()
	f.State = ""
	w.Step()
	// HK credentials are already revoked in the database, but the node has
	// not acknowledged their removal. A subsequent edit must still track it.
	req, preview := grantChange(t, c, id, "grp_sg")
	removed := preview["removed"].([]M)
	if len(removed) != 1 || removed[0]["id"] != "node_hk" || len(preview["added"].([]M)) != 0 {
		t.Fatal("pending removal disappeared from the next preview")
	}
	latest := c.UpdateGrants("admin", "grants-update-002", id, req)
	transaction(c.Settings.ControlDB, true, func(d *DB) { d.Exec("UPDATE outbox SET next_attempt_at=0") })
	for w.Step() {
	}
	v := Views{c.Settings}
	if one(v.Operations(), str(latest["operation_id"]))["state"] != "completed" || one(v.Operations(), str(old["operation_id"]))["state"] != "superseded" {
		t.Fatal("pending removal did not converge under current intent")
	}
	for _, r := range f.Requests[1:] {
		if r.NodeID == "node_hk" && (len(r.Users) != 0 || len(r.Revoked) != 2) {
			t.Fatal("old node access was restored")
		}
	}
}
