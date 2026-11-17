package sbctl

import "testing"

func TestDeleteArchivedUsersAndHistory(t *testing.T) {
	c := testControl(t)
	transport := &fakeTransport{}
	w := NewWorker(c, transport)
	c.CreateUser("admin", "delete-create-user01", CreateUser{DisplayName: "删除验收", GroupIDs: []string{"grp_hk"}}, nil)
	for w.Step() {
	}
	u := Views{c.Settings}.Users()[0]
	id := str(u["id"])
	revision := integer(u["revision"])
	req := DeleteUsers{[]UserVersion{{id, 0, revision}}}
	expectFault(t, "DELETE_NOT_READY", func() { c.DeleteUsers("admin", "delete-active-user01", req) })
	c.DisableUser("admin", "delete-disable-user1", id, DisableUser{&revision, "结束"})
	for w.Step() {
	}
	revision++
	c.ArchiveUsers("admin", "delete-archive-user1", ArchiveUsers{[]UserVersion{{id, 0, revision}}, true})
	req.Users[0] = UserVersion{id, 1, revision}
	stale := DeleteUsers{[]UserVersion{{id, 0, revision}}}
	expectFault(t, "STALE_REVISION", func() { c.DeleteUsers("admin", "delete-stale-user01", stale) })
	for _, query := range []string{
		"UPDATE subscriptions SET state='pending' WHERE user_id=?",
		"UPDATE credentials SET revoked=0 WHERE user_id=?",
		"UPDATE outbox SET state='queued' WHERE operation_id IN (SELECT id FROM operations WHERE user_id=?)",
		"UPDATE operations SET state='failed' WHERE user_id=?",
	} {
		transaction(c.Settings.ControlDB, true, func(d *DB) { d.Exec(query, id) })
		expectFault(t, "DELETE_NOT_READY", func() { c.DeleteUsers("admin", "delete-notready-01", req) })
		transaction(c.Settings.ControlDB, true, func(d *DB) {
			d.Exec("UPDATE subscriptions SET state='revoked' WHERE user_id=?", id)
			d.Exec("UPDATE credentials SET revoked=1 WHERE user_id=?", id)
			d.Exec("UPDATE outbox SET state='done' WHERE operation_id IN (SELECT id FROM operations WHERE user_id=?)", id)
			d.Exec("UPDATE operations SET state='completed' WHERE user_id=?", id)
		})
	}
	tr := Traffic{c.Settings.TrafficDB, c.Settings.TrafficSnapshot}
	tr.Initialize()
	sample := historySnapshot(1, "2026-11-16T00:00:00Z", "123", "456")
	sample.Seal()
	tr.Ingest("node_hk", sample)
	before := read(c.Settings.TrafficSnapshot)
	requests := len(transport.Requests)
	var revocations, ops int64
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		revocations = integer(d.Value("SELECT count(*) FROM revocations"))
		ops = integer(d.Value("SELECT count(*) FROM operations"))
	})
	out := c.DeleteUsers("admin", "delete-final-user01", req)
	if digest(out) != digest(c.DeleteUsers("admin", "delete-final-user01", req)) {
		t.Fatal("retry changed result")
	}
	if len(transport.Requests) != requests || digest(before) != digest(read(c.Settings.TrafficSnapshot)) {
		t.Fatal("delete changed runtime or traffic")
	}
	if len((Views{c.Settings}).Users()) != 0 {
		t.Fatal("deleted user still listed")
	}
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		for _, table := range []string{"users", "grants", "credentials", "subscriptions", "user_management"} {
			if integer(d.Value("SELECT count(*) FROM "+table)) != 0 {
				t.Fatal("user data retained in", table)
			}
		}
		if integer(d.Value("SELECT count(*) FROM revocations")) != revocations || revocations == 0 {
			t.Fatal("revocations lost")
		}
		if integer(d.Value("SELECT count(*) FROM operations")) != ops || integer(d.Value("SELECT count(*) FROM operations WHERE user_id IS NOT NULL")) != 0 {
			t.Fatal("operation history lost or still linked")
		}
		if integer(d.Value("SELECT count(*) FROM audit WHERE action='delete_user' AND target_id=?", id)) != 1 {
			t.Fatal("delete audit missing or duplicated")
		}
		if len(d.Rows("PRAGMA foreign_key_check")) != 0 {
			t.Fatal("dangling foreign keys")
		}
	})
	expectFault(t, "NOT_FOUND", func() { c.EnableUser("admin", "delete-enable-user1", id, EnableUser{&revision, "重放"}) })
	expectFault(t, "NOT_FOUND", func() { c.ArchiveUsers("admin", "delete-restore-user", ArchiveUsers{req.Users, false}) })
	expectFault(t, "NOT_FOUND", func() { c.SubscriptionLink("admin", "delete-link-user001", id) })
}

func TestDeleteUsersBatchIsAtomic(t *testing.T) {
	c := testControl(t)
	w := NewWorker(c, &fakeTransport{})
	for _, name := range []string{"a", "b"} {
		c.CreateUser("admin", "delete-batch-create-"+name, CreateUser{DisplayName: name, GroupIDs: []string{"grp_hk"}}, nil)
	}
	for w.Step() {
	}
	users := (Views{c.Settings}).Users()
	items := []UserVersion{}
	for _, u := range users {
		id := str(u["id"])
		rev := integer(u["revision"])
		c.DisableUser("admin", "delete-batch-disable-"+id, id, DisableUser{&rev, "结束"})
		items = append(items, UserVersion{id, 0, rev + 1})
	}
	for w.Step() {
	}
	c.ArchiveUsers("admin", "delete-batch-archive", ArchiveUsers{items[:1], true})
	items[0].Version++
	expectFault(t, "DELETE_NOT_READY", func() { c.DeleteUsers("admin", "delete-batch-atomic1", DeleteUsers{items}) })
	if len((Views{c.Settings}).Users()) != 2 {
		t.Fatal("partial delete")
	}
	c.ArchiveUsers("admin", "delete-batch-archiv2", ArchiveUsers{items[1:], true})
	items[1].Version++
	duplicate := DeleteUsers{[]UserVersion{items[0], items[0]}}
	expectFault(t, "INVALID_REQUEST", func() { c.DeleteUsers("admin", "delete-batch-repeat", duplicate) })
	c.DeleteUsers("admin", "delete-batch-final01", DeleteUsers{items})
	if len((Views{c.Settings}).Users()) != 0 {
		t.Fatal("batch not deleted")
	}
}
