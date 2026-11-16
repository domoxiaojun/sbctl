package sbctl

import (
	"bytes"
	"strings"
	"testing"
)

func TestUserManagementMetadataArchiveAndSubscription(t *testing.T) {
	c := testControl(t)
	c.CreateUser("admin", "management-create-1", CreateUser{DisplayName: "原名", GroupIDs: []string{"grp_hk"}}, nil)
	worker := NewWorker(c, &fakeTransport{})
	worker.Step()
	u := Views{c.Settings}.Users()[0]
	id := str(u["id"])
	revision := integer(u["revision"])
	version := int64(0)
	saved := c.EditUser("admin", "management-edit-01", id, UserMetadata{&version, "新名称", "用户备注"})
	if digest(saved) != digest(c.EditUser("admin", "management-edit-01", id, UserMetadata{&version, "新名称", "用户备注"})) {
		t.Fatal("metadata retry differs")
	}
	u = Views{c.Settings}.Users()[0]
	if u["display_name"] != "新名称" || integer(u["revision"]) != revision || integer(u["metadata_version"]) != 1 {
		t.Fatal(u)
	}
	expectFault(t, "STALE_REVISION", func() { c.EditUser("admin", "management-edit-02", id, UserMetadata{&version, "过时修改", ""}) })
	archive := ArchiveUsers{[]UserVersion{{id, 1, revision}}, true}
	expectFault(t, "ARCHIVE_NOT_READY", func() { c.ArchiveUsers("admin", "management-arch-01", archive) })
	link := c.SubscriptionLink("admin", "management-link-01", id)
	oldToken := strings.Split(str(link["url"]), "/")[4]
	generation := int64(1)
	reset := ResetSubscription{&revision, &generation, "链接泄露"}
	c.ResetSubscription("admin", "management-reset-1", id, reset)
	expectFault(t, "NOT_FOUND", func() { c.Public.Read(oldToken, "surge.conf") })
	worker.Reconcile()
	if digest(c.ResetSubscription("admin", "management-reset-1", id, reset)) != digest(M{"saved": true, "generation": int64(2)}) {
		t.Fatal("reset retry")
	}
	expectFault(t, "STALE_REVISION", func() { c.ResetSubscription("admin", "management-reset-2", id, reset) })
	u = Views{c.Settings}.Users()[0]
	if integer(u["revision"]) != revision || integer(u["subscription_generation"]) != 2 {
		t.Fatal("reset touched auth revision")
	}
	if integer(u["node_count"]) != 1 {
		t.Fatal("lost auth")
	}
	c.DisableUser("admin", "management-disable", id, DisableUser{&revision, "结束"})
	archive.Users[0].Revision++
	expectFault(t, "ARCHIVE_NOT_READY", func() { c.ArchiveUsers("admin", "management-arch-02", archive) })
	worker.Step()
	c.ArchiveUsers("admin", "management-arch-03", archive)
	all := Views{c.Settings}.Users()
	if !boolean(all[0]["archived"]) || integer(all[0]["node_count"]) != 0 {
		t.Fatal(all)
	}
	if integer(Paginate(all, defaultsFilters())["total"]) != 0 {
		t.Fatal("archived visible by default")
	}
	f := defaultsFilters()
	f.Archived = "true"
	if integer(Paginate(all, f)["total"]) != 1 {
		t.Fatal("archive filter")
	}
	revision++
	expectFault(t, "USER_ARCHIVED", func() { c.EnableUser("admin", "management-enable1", id, EnableUser{&revision, "重新使用"}) })
	archive.Archived = false
	archive.Users[0].Version = 2
	c.ArchiveUsers("admin", "management-restore", archive)
	if (Views{c.Settings}).Users()[0]["status"] != "disabled" {
		t.Fatal("restore granted access")
	}
	if bytes.Contains(read(c.Settings.ControlDB), []byte(oldToken)) {
		t.Fatal("plaintext URL token in database")
	}
}
func TestArchiveBatchIsAtomic(t *testing.T) {
	c := testControl(t)
	for _, name := range []string{"a", "b"} {
		c.CreateUser("admin", "batch-create-user-"+name, CreateUser{DisplayName: name, GroupIDs: []string{"grp_hk"}}, nil)
	}
	w := NewWorker(c, &fakeTransport{})
	for w.Step() {
	}
	users := Views{c.Settings}.Users()
	id := str(users[0]["id"])
	revision := integer(users[0]["revision"])
	c.DisableUser("admin", "batch-disable-user", id, DisableUser{&revision, "test"})
	for w.Step() {
	}
	request := ArchiveUsers{[]UserVersion{{id, 0, revision + 1}, {str(users[1]["id"]), 0, integer(users[1]["revision"])}}, true}
	expectFault(t, "ARCHIVE_NOT_READY", func() { c.ArchiveUsers("admin", "batch-archive-users", request) })
	for _, u := range (Views{c.Settings}).Users() {
		if boolean(u["archived"]) {
			t.Fatal("partial archive")
		}
	}
}
