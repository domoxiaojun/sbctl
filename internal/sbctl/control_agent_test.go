package sbctl

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"sbctl/contracts"
)

func TestAgentRegistrationPollAndReceipt(t *testing.T) {
	c := testControl(t)
	registration := c.CreateAgentRegistration("admin", "agent-reg-key-0001", "node_hk")
	replay := c.CreateAgentRegistration("admin", "agent-reg-key-0001", "node_hk")
	if digest(registration) != digest(replay) {
		t.Fatal("agent registration creation was not idempotent")
	}
	if bytes.Contains(read(c.Settings.ControlDB), []byte(str(registration["registration_code"]))) {
		t.Fatal("plaintext registration code stored in database")
	}
	expectFault(t, "INVALID_REQUEST", func() {
		c.AgentRegister(M{"node_id": "node_hk", "registration_code": registration["registration_code"], "public_key": "too-short"}, "100.70.0.10")
	})
	pubKey := "sbctl-ed25519-AAAAC3NzaC1lZDI1NTE5AAAAIAgentTestKeyMaterial0123456789"
	registered := c.AgentRegister(M{"node_id": "node_hk", "registration_code": registration["registration_code"], "public_key": pubKey, "version": Version, "capabilities": M{"stats": true, "hot_reload": true}}, "100.70.0.10")
	tok := str(registered["agent_token"])
	if tok == "" {
		t.Fatal("agent token missing")
	}
	expectFault(t, "AGENT_REGISTRATION_INVALID", func() {
		c.AgentRegister(M{"node_id": "node_hk", "registration_code": registration["registration_code"], "public_key": pubKey}, "100.70.0.10")
	})
	expectFault(t, "AGENT_UNAUTHORIZED", func() { c.AgentPoll("invalid-agent-token") })

	// Expired code is rejected.
	expired := c.CreateAgentRegistration("admin", "agent-reg-key-0002", "node_hk")
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		d.Exec("UPDATE agent_registrations SET expires_at=0 WHERE node_id='node_hk'")
	})
	expectFault(t, "AGENT_REGISTRATION_INVALID", func() {
		c.AgentRegister(M{"node_id": "node_hk", "registration_code": expired["registration_code"], "public_key": pubKey}, "100.70.0.10")
	})

	// Enqueue an encrypted sensitive command with a long-action lease.
	secretReq := "super-secret-password-value"
	secretRes := "super-secret-receipt-value"
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		cipher := c.Vault.Encrypt(M{"body": M{"action": "configure", "secret": secretReq}, "stats": false})
		d.Exec("INSERT INTO agent_commands(id,node_id,action,body_ciphertext,created_at,updated_at) VALUES (?,?,?,?,?,?)", "acmd_test", "node_hk", "configure", cipher, now(), now())
	})
	if bytes.Contains(read(c.Settings.ControlDB), []byte(secretReq)) {
		t.Fatal("plaintext command body stored in database")
	}
	poll := c.AgentPoll(tok)
	command := mapping(poll["command"])
	if command["id"] != "acmd_test" || command["action"] != "configure" {
		t.Fatalf("unexpected agent command: %v", poll)
	}
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		lease := str(d.Value("SELECT lease_until FROM agent_commands WHERE id='acmd_test'"))
		var val float64
		if _, err := fmt.Sscan(lease, &val); err != nil || val < timestamp()+250 {
			t.Fatalf("long action did not receive extended lease: %v", lease)
		}
	})
	c.AgentReceipt(tok, M{"command_id": "acmd_test", "result": M{"node_id": "node_hk", "ready": true, "secret": secretRes}})
	if bytes.Contains(read(c.Settings.ControlDB), []byte(secretRes)) {
		t.Fatal("plaintext command receipt stored in database")
	}
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		row := d.One("SELECT state,response_ciphertext FROM agent_commands WHERE id=?", "acmd_test")
		if row["state"] != "done" || str(row["response_ciphertext"]) == "" {
			t.Fatalf("receipt not committed: %v", row)
		}
	})
}

func TestAgentTransportZeroPollingWakeupAndOfflineReject(t *testing.T) {
	c := testControl(t)
	reg := c.CreateAgentRegistration("admin", "agent-tr-reg-0001", "node_hk")
	tok := str(c.AgentRegister(M{"node_id": "node_hk", "registration_code": reg["registration_code"], "public_key": strings.Repeat("k", 40), "version": Version}, "100.70.0.10")["agent_token"])
	tr := AgentTransport{Control: c, Timeout: 2 * time.Second}
	link := parseConnection(M{"address": "100.70.0.10"})

	done := make(chan struct{})
	go func() {
		defer close(done)
		p1 := c.AgentPollWait(context.Background(), tok, 2*time.Second)
		cmd1 := mapping(p1["command"])
		if cmd1["id"] != "" {
			c.AgentReceipt(tok, M{"command_id": cmd1["id"], "result": M{"via": "embedded-sing-box"}})
		}
		p2 := c.AgentPollWait(context.Background(), tok, 2*time.Second)
		cmd2 := mapping(p2["command"])
		if cmd2["id"] != "" {
			c.AgentReceipt(tok, M{"command_id": cmd2["id"], "error": M{"code": "ACTION_REJECTED", "message": "unsupported"}})
		}
	}()
	started := time.Now()
	res := tr.Call(link, M{"action": "diagnose"}, false)
	if res["via"] != "embedded-sing-box" {
		t.Fatalf("expected embedded agent transport, got %v", res)
	}
	if elapsed := time.Since(started); elapsed > 180*time.Millisecond {
		t.Fatalf("expected instant channel wakeup, took %v", elapsed)
	}
	expectFault(t, "ACTION_REJECTED", func() { tr.Call(link, M{"action": "config"}, false) })
	<-done

	// Timeout cancels the queued command so it cannot replay later.
	short := AgentTransport{Control: c, Timeout: 120 * time.Millisecond}
	expectFault(t, "NODE_UNREACHABLE", func() { short.Call(link, M{"action": "diagnose"}, false) })
	if p := c.AgentPoll(tok); p["command"] != nil {
		t.Fatalf("timed-out command remained claimable: %v", p)
	}

	// Offline agent returns NODE_UNREACHABLE directly (zero SSH fallback).
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		d.Exec("UPDATE agents SET last_seen=? WHERE node_id='node_hk'", nowMinus(120))
	})
	expectFault(t, "NODE_UNREACHABLE", func() { tr.Call(link, M{"action": "diagnose"}, false) })
}

func TestAgentHTTPBoundariesEnrollTelemetryAndLiveSSE(t *testing.T) {
	c := testControl(t)
	CreateAdmin(c.Settings, "admin", "管理员", "only-a-test-password")
	handler := API(c)
	schemaDoc := object(contracts.OpenAPI)
	compiler := jsonschema.NewCompiler()
	check(compiler.AddResource("https://schema.sbctl.test/openapi.json", schemaDoc))
	validate := func(path, method string, status int, b []byte) {
		t.Helper()
		schemaPath := "https://schema.sbctl.test/openapi.json#/paths/" + strings.ReplaceAll(path, "/", "~1") + "/" + method + "/responses/" + fmt.Sprint(status) + "/content/application~1json/schema"
		schema, err := compiler.Compile(schemaPath)
		if err != nil {
			t.Fatalf("compile %s: %v", path, err)
		}
		var data any
		check(json.Unmarshal(b, &data))
		if err = schema.Validate(data); err != nil {
			t.Fatalf("contract %s: %v", path, err)
		}
	}

	req := func(method, path string, body any, peer, bearerTok, origin, csrf, key string, cookie *http.Cookie) *httptest.ResponseRecorder {
		var r *http.Request
		if body != nil {
			r = httptest.NewRequest(method, path, bytes.NewReader(canonical(body)))
			r.Header.Set("Content-Type", "application/json")
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		r.RemoteAddr = peer
		if bearerTok != "" {
			r.Header.Set("Authorization", "Bearer "+bearerTok)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	loginW := req("POST", "/api/v1/session", M{"login_name": "admin", "password": "only-a-test-password"}, "127.0.0.1:1000", "", "https://admin.test", "", "", nil)
	if loginW.Code != 200 {
		t.Fatal(loginW.Code, loginW.Body.String())
	}
	var sessionCookie *http.Cookie
	for _, ck := range loginW.Result().Cookies() {
		if ck.Name == CookieName {
			sessionCookie = ck
		}
	}
	csrf := str(object(loginW.Body.Bytes())["csrf_token"])

	// Enroll a new single-container node with monthly billing quota.
	enrollBody := M{
		"draft": M{
			"id":          "node_jp",
			"name":        "日本东京一区",
			"region":      "日本",
			"location":    "东京",
			"group_id":    "grp_jp",
			"group_name":  "日本组",
			"connection":  M{"address": "100.70.0.22"},
			"container":   "sing-box",
			"config_dir":  "/etc/sing-box",
			"public_host": "jp.example.test",
		},
		"billing": M{
			"quota_bytes":     "1073741824000",
			"reset_day":       15,
			"accounting_mode": "both",
			"warn_percent":    80,
		},
		"control_url": "http://100.64.0.1:8080",
	}
	enrollW := req("POST", "/api/v1/nodes/enroll", enrollBody, "127.0.0.1:1000", "", "https://admin.test", csrf, "enroll-node-jp-key-0001", sessionCookie)
	if enrollW.Code != 201 {
		t.Fatalf("enroll failed: %d %s", enrollW.Code, enrollW.Body.String())
	}
	validate("/api/v1/nodes/enroll", "post", 201, enrollW.Body.Bytes())
	enrollRes := object(enrollW.Body.Bytes())
	if !strings.Contains(str(enrollRes["compose_yaml"]), fmt.Sprintf("SBCTL_REGISTRATION_CODE: %q", enrollRes["registration_code"])) {
		t.Fatalf("compose_yaml missing registration code: %s", enrollRes["compose_yaml"])
	}

	regCodeW := req("POST", "/api/v1/nodes/node_hk/agent-registration", nil, "127.0.0.1:1000", "", "https://admin.test", csrf, "agent-http-reg-key-01", sessionCookie)
	if regCodeW.Code != 201 {
		t.Fatal(regCodeW.Code, regCodeW.Body.String())
	}
	validate("/api/v1/nodes/{id}/agent-registration", "post", 201, regCodeW.Body.Bytes())
	code := str(object(regCodeW.Body.Bytes())["registration_code"])

	// Public peer is rejected on all Agent routes including telemetry.
	pubPayload := M{"node_id": "node_hk", "registration_code": code, "public_key": strings.Repeat("p", 40)}
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/v1/agent/register", pubPayload},
		{"GET", "/api/v1/agent/poll?wait=0", nil},
		{"POST", "/api/v1/agent/receipt", M{"command_id": "c1"}},
		{"POST", "/api/v1/agent/telemetry", M{"live": M{"upload_bps": 100}}},
	} {
		if w := req(tc.method, tc.path, tc.body, "203.0.113.10:12345", "tok", "", "", "", nil); w.Code != 403 {
			t.Fatalf("expected 403 for public peer on %s, got %d", tc.path, w.Code)
		}
	}

	// Rate limiting triggers after 20 failures.
	for i := range 20 {
		bad := M{"node_id": "node_hk", "registration_code": strings.Repeat("x", 32), "public_key": strings.Repeat("p", 40)}
		w := req("POST", "/api/v1/agent/register", bad, "100.70.0.10:4444", "", "", "", "", nil)
		if w.Code != 409 {
			t.Fatalf("attempt %d expected 409, got %d", i, w.Code)
		}
	}
	if w := req("POST", "/api/v1/agent/register", pubPayload, "100.70.0.10:4444", "", "", "", "", nil); w.Code != 429 {
		t.Fatalf("expected 429 rate limit, got %d", w.Code)
	}
	transaction(c.Settings.ControlDB, true, func(d *DB) { d.Exec("DELETE FROM login_failures") })

	regW := req("POST", "/api/v1/agent/register", M{"node_id": "node_hk", "registration_code": code, "public_key": strings.Repeat("p", 40), "version": "1.14.4-sbctl.2"}, "100.70.0.10:4444", "", "", "", "", nil)
	if regW.Code != 201 {
		t.Fatal(regW.Code, regW.Body.String())
	}
	validate("/api/v1/agent/register", "post", 201, regW.Body.Bytes())
	tok := str(object(regW.Body.Bytes())["agent_token"])

	// Push live telemetry + outbound counters + closed connections.
	sampleAt := now()
	epoch := "box-epoch-1"
	cfgHash := strings.Repeat("b", 64)
	snap := Snapshot{
		SchemaVersion: 1,
		NodeID:        "node_hk",
		LedgerID:      "led_hk_01",
		Seq:           1,
		StartedAt:     sampleAt,
		SampledAt:     &sampleAt,
		SourceEpoch:   &epoch,
		ConfigHash:    &cfgHash,
		Inbounds:      []LedgerInbound{{Tag: "anytls-in", Protocol: "anytls", Port: 443, Upload: "1000", Download: "5000", StartedAt: sampleAt, Active: true}},
		Outbounds: []LedgerOutbound{
			{Tag: "direct", Upload: "400", Download: "1000", StartedAt: sampleAt, Active: true},
			{Tag: "warp-out", Upload: "600", Download: "4000", StartedAt: sampleAt, Active: true},
		},
		TotalInbounds: 1,
		ConnectionSeq: 2,
		Quality:       DataQuality{Integrity: "ok"},
	}
	snap.Seal()

	telemetryPayload := M{
		"version":          "1.14.4-sbctl.2",
		"snapshot":         snap,
		"connection_after": 0,
		"connection_next":  2,
		"connections": []M{
			{"sequence": 1, "id": "c_big_bytes", "inbound": "anytls-in", "inbound_type": "anytls", "network": "tcp", "domain": "video.example.com", "destination": "93.184.216.34:443", "source": "198.51.100.8:51000", "outbound": "warp-out", "outbound_type": "wireguard", "chain": []string{"warp-out"}, "rule": "geosite:streaming", "upload_bytes": "500", "download_bytes": "4000", "started_at": sampleAt, "closed_at": sampleAt},
			{"sequence": 2, "id": "c_many_small", "inbound": "anytls-in", "inbound_type": "anytls", "network": "udp", "domain": "dns.example.com", "destination": "1.1.1.1:53", "source": "198.51.100.8:51001", "outbound": "direct", "outbound_type": "direct", "chain": []string{"direct"}, "rule": "protocol=dns", "upload_bytes": "50", "download_bytes": "50", "started_at": sampleAt, "closed_at": sampleAt},
		},
		"live": M{
			"sampled_at":         sampleAt,
			"upload_bps":         125000,
			"download_bps":       980000,
			"active_connections": 1,
			"inbounds":           []M{{"tag": "anytls-in", "upload_bps": 125000, "download_bps": 980000}},
			"outbounds":          []M{{"tag": "warp-out", "upload_bps": 125000, "download_bps": 980000}},
			"active_items": []M{
				{"id": "c_live_1", "inbound": "anytls-in", "inbound_type": "anytls", "network": "tcp", "domain": "live.example.com", "destination": "104.16.0.1:443", "source": "198.51.100.8:52000", "outbound": "warp-out", "outbound_type": "wireguard", "chain": []string{"warp-out"}, "rule": "final", "upload_bytes": "2048", "download_bytes": "65536", "started_at": sampleAt},
			},
		},
	}
	telW := req("POST", "/api/v1/agent/telemetry", telemetryPayload, "100.70.0.10:4444", tok, "", "", "", nil)
	if telW.Code != 200 {
		t.Fatalf("telemetry failed: %d %s", telW.Code, telW.Body.String())
	}
	validate("/api/v1/agent/telemetry", "post", 200, telW.Body.Bytes())
	if integer(object(telW.Body.Bytes())["acked_connection_seq"]) != 2 || integer(object(telW.Body.Bytes())["next_interval_ms"]) != 5000 {
		t.Fatalf("unexpected telemetry ack: %s", telW.Body.String())
	}

	// Verify GET /api/v1/traffic/live, /api/v1/overview, /api/v1/nodes, /api/v1/traffic?scope=outbounds, and byte-ranked connections.
	liveW := req("GET", "/api/v1/traffic/live", nil, "127.0.0.1:1000", "", "", "", "", sessionCookie)
	if liveW.Code != 200 {
		t.Fatalf("live traffic failed: %d %s", liveW.Code, liveW.Body.String())
	}
	validate("/api/v1/traffic/live", "get", 200, liveW.Body.Bytes())
	liveObj := object(liveW.Body.Bytes())
	if integer(liveObj["download_bps"]) != 980000 || len(array(liveObj["active_items"])) != 1 {
		t.Fatalf("unexpected live snapshot: %v", liveObj)
	}

	overviewW := req("GET", "/api/v1/overview", nil, "127.0.0.1:1000", "", "", "", "", sessionCookie)
	if overviewW.Code != 200 {
		t.Fatal(overviewW.Code, overviewW.Body.String())
	}
	validate("/api/v1/overview", "get", 200, overviewW.Body.Bytes())
	if integer(object(overviewW.Body.Bytes())["download_bps"]) != 980000 {
		t.Fatalf("overview missing live throughput: %s", overviewW.Body.String())
	}

	nodesW := req("GET", "/api/v1/nodes?q=node_hk", nil, "127.0.0.1:1000", "", "", "", "", sessionCookie)
	if nodesW.Code != 200 {
		t.Fatal(nodesW.Code, nodesW.Body.String())
	}
	validate("/api/v1/nodes", "get", 200, nodesW.Body.Bytes())
	nodes := array(object(nodesW.Body.Bytes())["items"])
	if len(nodes) != 1 {
		t.Fatalf("expected one matching node, got %d", len(nodes))
	}
	node := mapping(nodes[0])
	if node["id"] != "node_hk" || node["status"] != "online" || node["agent_state"] != "online" || node["live_fresh"] != true {
		t.Fatalf("expected a healthy node with fresh live telemetry, got %v", node)
	}

	outTrafficW := req("GET", "/api/v1/traffic?scope=outbounds&outbound=warp-out", nil, "127.0.0.1:1000", "", "", "", "", sessionCookie)
	if outTrafficW.Code != 200 {
		t.Fatalf("outbound traffic failed: %d %s", outTrafficW.Code, outTrafficW.Body.String())
	}
	validate("/api/v1/traffic", "get", 200, outTrafficW.Body.Bytes())
	outNodes := array(object(outTrafficW.Body.Bytes())["nodes"])
	if len(outNodes) == 0 || str(mapping(outNodes[0])["outbound_download_bytes"]) != "4000" {
		t.Fatalf("expected filtered warp-out download_bytes=4000, got %v", outNodes)
	}

	connW := req("GET", "/api/v1/traffic/connections", nil, "127.0.0.1:1000", "", "", "", "", sessionCookie)
	if connW.Code != 200 {
		t.Fatal(connW.Code, connW.Body.String())
	}
	validate("/api/v1/traffic/connections", "get", 200, connW.Body.Bytes())
	topDomains := array(mapping(object(connW.Body.Bytes())["summary"])["domains"])
	if len(topDomains) < 2 || str(mapping(topDomains[0])["name"]) != "video.example.com" {
		t.Fatalf("expected byte-ranked top domain video.example.com first, got %v", topDomains)
	}

	// Verify SSE stream delivers immediate frame and switches agent telemetry cadence to 1500ms.
	srv := httptest.NewServer(handler)
	defer srv.Close()
	sseCtx, cancelSSE := context.WithCancel(context.Background())
	defer cancelSSE()
	sseReq, err := http.NewRequestWithContext(sseCtx, "GET", srv.URL+"/api/v1/traffic/live/stream", nil)
	check(err)
	sseReq.AddCookie(sessionCookie)
	sseResp, err := http.DefaultClient.Do(sseReq)
	check(err)
	defer sseResp.Body.Close()
	reader := bufio.NewReader(sseResp.Body)
	line, err := reader.ReadString('\n')
	check(err)
	if !strings.HasPrefix(line, "data: ") {
		t.Fatalf("unexpected SSE first line: %q", line)
	}

	telW2 := req("POST", "/api/v1/agent/telemetry", M{"live": M{"upload_bps": 200000, "download_bps": 500000}}, "100.70.0.10:4444", tok, "", "", "", nil)
	if integer(object(telW2.Body.Bytes())["next_interval_ms"]) != 1500 {
		t.Fatalf("expected 1500ms fast cadence while SSE subscriber connected, got %s", telW2.Body.String())
	}
	cancelSSE()

	// Submit >64 KiB receipt to verify MaxDocument body limit works.
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		cipher := c.Vault.Encrypt(M{"body": M{"action": "config_snapshot"}, "stats": false})
		d.Exec("INSERT INTO agent_commands(id,node_id,action,body_ciphertext,created_at,updated_at) VALUES (?,?,?,?,?,?)", "acmd_large", "node_hk", "config_snapshot", cipher, now(), now())
	})
	pollW := req("GET", "/api/v1/agent/poll?wait=0", nil, "100.70.0.10:4444", tok, "", "", "", nil)
	if pollW.Code != 200 {
		t.Fatal(pollW.Code, pollW.Body.String())
	}
	validate("/api/v1/agent/poll", "get", 200, pollW.Body.Bytes())
	largeReceipt := M{"command_id": "acmd_large", "result": M{"config_blob": strings.Repeat("a", 80*1024)}}
	receiptW := req("POST", "/api/v1/agent/receipt", largeReceipt, "100.70.0.10:4444", tok, "", "", "", nil)
	if receiptW.Code != 200 {
		t.Fatalf("large receipt failed with %d: %s", receiptW.Code, receiptW.Body.String())
	}
	validate("/api/v1/agent/receipt", "post", 200, receiptW.Body.Bytes())
}

func TestNodeBillingCycleClampingModesAndThresholdAlerts(t *testing.T) {
	// Reset day 31 clamps cleanly across February (non-leap & leap) and 30-day months.
	febNonLeap := time.Date(2026, 2, 10, 12, 0, 0, 0, trafficZone)
	start, end := billingCycleWindow(febNonLeap, 31)
	if start.Format(time.DateOnly) != "2026-01-31" || end.Format(time.DateOnly) != "2026-02-28" {
		t.Fatalf("unexpected non-leap Feb window: %s -> %s", start.Format(time.DateOnly), end.Format(time.DateOnly))
	}
	febLeap := time.Date(2028, 2, 29, 0, 0, 0, 0, trafficZone)
	start2, end2 := billingCycleWindow(febLeap, 31)
	if start2.Format(time.DateOnly) != "2028-02-29" || end2.Format(time.DateOnly) != "2028-03-31" {
		t.Fatalf("unexpected leap Feb reset day window: %s -> %s", start2.Format(time.DateOnly), end2.Format(time.DateOnly))
	}

	c := testControl(t)
	tr := Traffic{c.Settings.TrafficDB, c.Settings.TrafficSnapshot}
	tr.Initialize()
	t0 := time.Now().UTC().Add(-30 * time.Second).Format(time.RFC3339Nano)
	t1 := time.Now().UTC().Format(time.RFC3339Nano)
	epoch := "e1"
	cfgHash := strings.Repeat("c", 64)
	s1 := Snapshot{SchemaVersion: 1, NodeID: "node_hk", LedgerID: "led_bill", Seq: 1, StartedAt: t0, SampledAt: &t0, SourceEpoch: &epoch, ConfigHash: &cfgHash, Inbounds: []LedgerInbound{{Tag: "anytls-in", Protocol: "anytls", Port: 443, Upload: "0", Download: "0", StartedAt: t0, Active: true}}, Quality: DataQuality{Integrity: "ok"}}
	s1.Seal()
	tr.Ingest("node_hk", s1)
	s2 := Snapshot{SchemaVersion: 1, NodeID: "node_hk", LedgerID: "led_bill", Seq: 2, StartedAt: t0, SampledAt: &t1, SourceEpoch: &epoch, ConfigHash: &cfgHash, Inbounds: []LedgerInbound{{Tag: "anytls-in", Protocol: "anytls", Port: 443, Upload: "300", Download: "600", StartedAt: t0, Active: true}}, Quality: DataQuality{Integrity: "ok"}}
	s2.Seal()
	tr.Ingest("node_hk", s2)

	for _, tc := range []struct {
		mode     string
		expected string
		state    string
	}{
		{"both", "900", "warning"},
		{"download_only", "600", "normal"},
		{"upload_only", "300", "normal"},
		{"max_direction", "600", "normal"},
	} {
		view := computeNodeBillingView(c.Settings, "node_hk", NodeBilling{QuotaBytes: "1000", ResetDay: 1, AccountingMode: tc.mode, WarnPercent: 80}, time.Now())
		if str(view["used_bytes"]) != tc.expected || str(view["state"]) != tc.state {
			t.Fatalf("mode %s expected used=%s state=%s, got %v", tc.mode, tc.expected, tc.state, view)
		}
	}

	// Reconcile emits warning alert at 90% and critical alert when exceeded.
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		b := &NodeBilling{QuotaBytes: "1000", ResetDay: 1, AccountingMode: "both", WarnPercent: 80}
		saveNodeBilling(d, "node_hk", b)
	})
	w := NewWorker(c, &fakeTransport{})
	w.Reconcile()
	var sev string
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		sev = str(d.Value("SELECT severity FROM alerts WHERE dedupe_key='billing:node_hk' AND state='open'"))
	})
	if sev != "medium" {
		t.Fatalf("expected medium billing alert, got %q", sev)
	}

	transaction(c.Settings.ControlDB, true, func(d *DB) {
		b := &NodeBilling{QuotaBytes: "800", ResetDay: 1, AccountingMode: "both", WarnPercent: 80}
		saveNodeBilling(d, "node_hk", b)
	})
	w.Reconcile()
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		sev = str(d.Value("SELECT severity FROM alerts WHERE dedupe_key='billing:node_hk' AND state='open'"))
	})
	if sev != "high" {
		t.Fatalf("expected high billing alert when quota exceeded, got %q", sev)
	}
}

func TestStandaloneAgentCommandUsesSharedDatabase(t *testing.T) {
	api := testControl(t)
	reg := api.CreateAgentRegistration("admin", "standalone-test-key", "node_hk")
	tok := str(api.AgentRegister(M{"node_id": "node_hk", "registration_code": reg["registration_code"], "public_key": strings.Repeat("k", 40)}, "100.70.0.10")["agent_token"])
	cli := *api
	cli.Hub = NewAgentHub()
	tr := AgentTransport{Control: &cli, Timeout: 10 * time.Second}
	link := parseConnection(M{"address": "100.70.0.10"})
	if got := tr.resolveNodeID(link, M{"node_id": "node_other"}); got != "" {
		t.Fatalf("offline target resolved to %q", got)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		cmd := mapping(api.AgentPollWait(context.Background(), tok, 8*time.Second)["command"])
		if cmd["id"] != nil {
			api.AgentReceipt(tok, M{"command_id": cmd["id"], "result": M{"node_id": "node_hk"}})
		}
	}()
	if got := tr.Call(link, nil, true); got["node_id"] != "node_hk" {
		t.Fatalf("standalone command receipt: %v", got)
	}
	<-done
}

func TestLegacyNodeJobConnection(t *testing.T) {
	job := decodeNodeInput(nil, `{"kind":"inspect","draft":{"connection":{"address":"100.70.0.10","port":22,"publish_user":"sbctl-publish","stats_user":"sbctl-stats","version":"1.12.9"}}}`)
	if job.Draft.Connection.Address != "100.70.0.10" || job.Draft.Connection.Version != "1.12.9" {
		t.Fatalf("lost stored connection: %+v", job.Draft.Connection)
	}
}
