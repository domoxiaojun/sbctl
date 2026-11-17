package sbctl

import (
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestAcceptanceRequiresActualInboundTraffic(t *testing.T) {
	baseline := M{"nodes": []M{{"id": "node_hk", "quality": M{"freshness": "current", "sampled_at": "2026-01-01T00:00:00Z"}, "inbounds": []M{{"tag": "anytls-in", "protocol": "anytls", "active": true, "upload_bytes": "100", "download_bytes": "200"}}}}}
	targets := []any{M{"node_id": "node_hk"}}
	if acceptanceTrafficReady(baseline, baseline, targets) {
		t.Fatal("old cumulative traffic cannot pass a new acceptance")
	}
	report := object(canonical(baseline))
	node := mapping(array(report["nodes"])[0])
	quality := mapping(node["quality"])
	quality["sampled_at"] = "2026-01-01T00:00:15Z"
	inbound := mapping(array(node["inbounds"])[0])
	inbound["upload_bytes"], inbound["download_bytes"] = "854", "3713"
	if !acceptanceTrafficReady(report, baseline, targets) {
		t.Fatal("fresh, bidirectional increments should pass")
	}
	if acceptanceTrafficReady(report, baseline, append(targets, M{"node_id": "node_missing"})) {
		t.Fatal("every authorized node must provide traffic evidence")
	}
	quality["freshness"] = "stale"
	if acceptanceTrafficReady(report, baseline, targets) {
		t.Fatal("stale history is not live traffic evidence")
	}
}

type acceptanceTransport struct{ control *Control }

func (f acceptanceTransport) Call(n NodeConnection, body M, stats bool) M {
	r := parseRequest(body["request"])
	if len(r.Users) > 0 {
		snap := M{"started_at": now(), "sampled_at": now(), "inbounds": []M{M{"tag": "anytls-in", "protocol": "anytls", "upload_bytes": "1024", "download_bytes": "4096", "started_at": now(), "active": true}}, "data_quality": M{"integrity_state": "ok", "gap_count": 0, "has_historical_gap": false}}
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
