package sbctl

import (
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestAcceptanceRequiresActualUserTraffic(t *testing.T) {
	user := M{"quality": M{"freshness": "current"}, "upload_bytes": "0", "download_bytes": "0"}
	if acceptanceUsageReady(user) {
		t.Fatal("fresh node snapshot alone cannot verify a new user's traffic")
	}
	user["upload_bytes"], user["download_bytes"] = "854", "3713"
	if !acceptanceUsageReady(user) {
		t.Fatal("fresh, nonzero traffic should pass")
	}
	user["quality"] = M{"freshness": "stale"}
	if acceptanceUsageReady(user) {
		t.Fatal("stale history is not live traffic evidence")
	}
}

type acceptanceTransport struct{ control *Control }

func (f acceptanceTransport) Call(n NodeConnection, body M, stats bool) M {
	r := parseRequest(body["request"])
	if len(r.Users) > 0 {
		users := []M{}
		seen := map[string]bool{}
		for _, c := range r.Users {
			if !seen[c.UserID] {
				seen[c.UserID] = true
				users = append(users, M{"user_id": c.UserID, "upload_bytes": "1024", "download_bytes": "4096"})
			}
		}
		snap := M{"started_at": now(), "sampled_at": now(), "users": users, "data_quality": M{"integrity_state": "ok", "gap_count": 0, "has_historical_gap": false}}
		atomicWrite(f.control.Settings.TrafficSnapshot, canonical(M{"nodes": M{r.NodeID: M{"snapshot": snap, "synced_at": now()}}}), 0644)
	}
	return (&fakeTransport{}).Call(n, body, stats)
}

func TestAcceptanceLifecycleAgainstActualAPI(t *testing.T) {
	c := testControl(t)
	CreateAdmin(c.Settings, "admin", "管理员", "only-a-test-password")
	server := httptest.NewServer(API(c))
	defer server.Close()
	public := httptest.NewServer(PublicAPI(c.Settings.PublicDir))
	defer public.Close()
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		w := NewWorker(c, acceptanceTransport{c})
		for {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
				_ = Attempt(func() { w.Step() })
			}
		}
	}()
	defer func() { close(stop); <-done }()
	dir := t.TempDir()
	password, report := filepath.Join(dir, "password"), filepath.Join(dir, "report.json")
	atomicWrite(password, []byte("only-a-test-password"), 0600)
	if err := Attempt(func() {
		Acceptance([]string{"--base-url", server.URL, "--public-url", public.URL, "--origin", "https://admin.test", "--password-file", password, "--group", "grp_hk", "--report", report, "--lifecycle"})
	}); err != nil {
		t.Fatal(PublicFault(err))
	}
	r := object(read(report))
	if mapping(r["lifecycle"])["final_disable_state"] != "completed" || (Views{c.Settings}).Users()[0]["status"] != "disabled" {
		t.Fatal("lifecycle acceptance did not finish with access revoked")
	}
}
