package sbctl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"sbctl/contracts"
)

func expectFault(t *testing.T, code string, fn func()) {
	t.Helper()
	err := Attempt(fn)
	if err == nil {
		t.Fatalf("expected %s", code)
	}
	f := PublicFault(err)
	if f.Code != code {
		t.Fatalf("expected %s, got %s (%T)", code, f.Code, err)
	}
}
func testControl(t *testing.T) *Control {
	t.Helper()
	dir := t.TempDir()
	s := DefaultSettings()
	s.ControlDB = filepath.Join(dir, "control.db")
	s.KeyFile = filepath.Join(dir, "key")
	s.PublicDir = filepath.Join(dir, "publication", "subscriptions")
	s.TrafficSnapshot = filepath.Join(dir, "traffic.json")
	s.TrafficDB = filepath.Join(dir, "traffic.db")
	s.InventoryFile = filepath.Join(dir, "inventory.json")
	s.PublicBaseURL = "https://sub.test"
	s.AllowedOrigins = []string{"https://admin.test"}
	CreateKey(s.KeyFile)
	c := NewControl(s)
	c.Initialize()
	c.Register(M{"id": "node_hk", "name": "香港", "region": "hk", "location": "香港", "connection": M{"address": "100.70.0.10"}, "endpoints": []Endpoint{{"anytls", "anytls-in", "example.test", 443, "example.test", nil}, {"tuic", "tuic-in", "example.test", 443, "example.test", nil}}, "template_hash": strings.Repeat("a", 64), "image_digest": "sha256:" + strings.Repeat("b", 64), "group_id": "grp_hk", "group_name": "香港"})
	return c
}

type fakeTransport struct {
	Requests []NodeRequest
	State    string
}

func (f *fakeTransport) Call(n NodeConnection, body M, stats bool) M {
	r := parseRequest(body["request"])
	f.Requests = append(f.Requests, r)
	state := f.State
	if state == "" {
		state = "verified"
	}
	return M{"node_id": r.NodeID, "latest": M{"id": r.RequestID, "revision": r.NodeRevision, "payload_hash": r.PayloadHash, "state": state}}
}
func TestAuthenticationAndVault(t *testing.T) {
	c := testControl(t)
	s := c.Settings
	password := "unit-test-admin-password"
	CreateAdmin(s, "admin", "管理员", password)
	expectFault(t, "LOGIN_FAILED", func() { Login(s, "admin", "wrong", "test") })
	tok, admin := Login(s, "admin", password, "test")
	if digest(Authenticate(s, tok)) != digest(admin) {
		t.Fatal("session mismatch")
	}
	if bytes.Contains(read(s.ControlDB), []byte(password)) || bytes.Contains(read(s.ControlDB), []byte(tok)) {
		t.Fatal("plaintext authentication material in database")
	}
	Logout(s, tok)
	expectFault(t, "UNAUTHENTICATED", func() { Authenticate(s, tok) })
	for range 10 {
		expectFault(t, "LOGIN_FAILED", func() { Login(s, "admin", "wrong", "limited") })
	}
	expectFault(t, "LOGIN_RATE_LIMIT", func() { Login(s, "admin", password, "limited") })
	encrypted := c.Vault.Encrypt(M{"password": "proxy-secret"})
	if strings.Contains(encrypted, "proxy-secret") || mapping(c.Vault.Decrypt(encrypted))["password"] != "proxy-secret" {
		t.Fatal("vault round trip")
	}
	expectFault(t, "KEY_MISMATCH", func() { c.Vault.Decrypt(encrypted[:len(encrypted)-6] + "wrong!") })
	if c.Vault.CSRF("one") == c.Vault.CSRF("two") {
		t.Fatal("unbound csrf")
	}
	expectFault(t, "KEY_EXISTS", func() { CreateKey(s.KeyFile) })
}
func TestControlPublishRevokeAndLateTask(t *testing.T) {
	c := testControl(t)
	req := CreateUser{DisplayName: "用户 & <> ", GroupIDs: []string{"grp_hk"}}
	created := c.CreateUser("admin", "request-create-0001", req, nil)
	if digest(created) != digest(c.CreateUser("admin", "request-create-0001", req, nil)) {
		t.Fatal("non-idempotent create")
	}
	expectFault(t, "HASH_CONFLICT", func() {
		c.CreateUser("admin", "request-create-0001", CreateUser{DisplayName: "other", GroupIDs: []string{"grp_hk"}}, nil)
	})
	f := &fakeTransport{}
	w := NewWorker(c, f)
	if !w.Step() {
		t.Fatal("not processed")
	}
	views := Views{c.Settings}
	u := views.Users()[0]
	if u["status"] != "active" || one(views.Operations(), str(created["operation_id"]))["state"] != "completed" {
		t.Fatal("publication incomplete")
	}
	links := c.SubscriptionLink("admin", "request-link-00001", str(u["id"]))
	tok := strings.Split(str(links["url"]), "/")[4]
	for _, name := range []string{"surge.conf", "mihomo.yaml"} {
		if len(c.Public.Read(tok, name)) == 0 {
			t.Fatal("empty subscription")
		}
	}
	rev := integer(u["revision"])
	disabled := c.DisableUser("admin", "request-disable-01", str(u["id"]), DisableUser{&rev, "test"})
	for _, name := range []string{"surge.conf", "mihomo.yaml"} {
		expectFault(t, "NOT_FOUND", func() { c.Public.Read(tok, name) })
	}
	w.Step()
	last := f.Requests[len(f.Requests)-1]
	if len(last.Users) != 0 || len(last.Revoked) != 2 {
		t.Fatal("revocations missing")
	}
	if one(views.Operations(), str(disabled["operation_id"]))["state"] != "completed" {
		t.Fatal("disable incomplete")
	}
	expectFault(t, "NOT_READY", func() { c.SubscriptionLink("admin", "request-link-00001", str(u["id"])) })
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		d.Exec("UPDATE outbox SET state='queued',next_attempt_at=? WHERE operation_id=?", timestamp()-1, created["operation_id"])
	})
	w.Step()
	if len(f.Requests[len(f.Requests)-1].Users) != 0 || views.Users()[0]["status"] != "disabled" {
		t.Fatal("late task restored revoked user")
	}
	if integer(views.Overview()["user_count"]) != 0 {
		t.Fatal("active count")
	}
}
func TestConcurrentIdempotentCreateAndClaims(t *testing.T) {
	c := testControl(t)
	var wg sync.WaitGroup
	out := make(chan M, 6)
	errs := make(chan error, 6)
	for range 6 {
		wg.Go(func() {
			var r M
			e := Attempt(func() {
				r = c.CreateUser("admin", "concurrent-request-01", CreateUser{DisplayName: "concurrent", GroupIDs: []string{"grp_hk"}}, nil)
			})
			if e != nil {
				errs <- e
			} else {
				out <- r
			}
		})
	}
	wg.Wait()
	close(errs)
	close(out)
	for e := range errs {
		t.Fatal(e)
	}
	id := ""
	for r := range out {
		if id != "" && id != str(r["operation_id"]) {
			t.Fatal("duplicate operation")
		}
		id = str(r["operation_id"])
	}
	if len(Views{c.Settings}.Users()) != 1 {
		t.Fatal("duplicate user")
	}
	claims := make(chan M, 2)
	for range 2 {
		wg.Go(func() { claims <- NewWorker(c, &fakeTransport{}).Claim() })
	}
	wg.Wait()
	close(claims)
	count := 0
	for v := range claims {
		if v != nil {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("concurrent claims %d", count)
	}
}
func TestJSONAndWireValidation(t *testing.T) {
	for _, raw := range []string{`{"a":1,"a":2}`, `{"a":{"b":1,"b":2}}`, `{} {}`, `{"a":NaN}`, "{\"a\":\"\xff\"}"} {
		expectFault(t, "INVALID_JSON", func() { object([]byte(raw)) })
	}
	expectFault(t, "INVALID_REQUEST", func() { var c CreateUser; decode([]byte(`{"display_name":"x","unexpected":true}`), &c) })
	r := NodeRequest{SchemaVersion: 1, NodeID: "node_hk", RequestID: "req_1", NodeRevision: 1, TemplateHash: strings.Repeat("a", 64), EngineBuildID: strings.Repeat("b", 64), Users: []Credential{{"usr_alice", "anytls", "cred_alice", 1, "password", nil}}, Revoked: []string{}}
	r.Seal()
	r.Verify()
	r.Users[0].Password = "changed"
	expectFault(t, "HASH_MISMATCH", r.Verify)
	r.Revoked = []string{r.Users[0].Fingerprint()}
	expectFault(t, "REQUEST_INVALID", r.Seal)
	for _, addr := range []string{"127.0.0.1", "8.8.8.8", "-oProxyCommand=x"} {
		expectFault(t, "CONNECTION_INVALID", func() { parseConnection(M{"address": addr}) })
	}
}
func testRuntime() Runtime {
	return Runtime{ContainerID: "container", StartedAt: "epoch-1", Running: true, ConfigHash: strings.Repeat("a", 64), NodeRevision: 1, Users: []string{"usr_alice"}, ImageDigest: "sha256:" + strings.Repeat("b", 64)}
}
func counters(up, down string) []M {
	return []M{{"name": "user>>>usr_alice>>>traffic>>>uplink", "value": up}, {"name": "user>>>usr_alice>>>traffic>>>downlink", "value": down}}
}
func TestLedgerAndTrafficMonotonicity(t *testing.T) {
	dir := t.TempDir()
	l := Ledger{filepath.Join(dir, "ledger.db"), filepath.Join(dir, "export.json"), "node_hk"}
	l.Create()
	rt := testRuntime()
	l.Ingest(rt, rt, counters("100", "200"), false)
	s := l.Snapshot()
	if s.Users[0].Upload != "0" {
		t.Fatal("first observation should baseline")
	}
	l.Ingest(rt, rt, counters("140", "260"), false)
	s = l.Snapshot()
	if s.Users[0].Upload != "40" || s.Users[0].Download != "60" {
		t.Fatal("delta")
	}
	l.Ingest(rt, rt, counters("140", "260"), false)
	if l.Snapshot().Users[0].Upload != "40" {
		t.Fatal("double count")
	}
	expectFault(t, "COUNTER_REGRESSION", func() { l.Ingest(rt, rt, counters("130", "260"), false) })
	s = l.Snapshot()
	if s.Users[0].Upload != "40" || s.Quality.Integrity != "counter_error" {
		t.Fatal("regression not frozen")
	}
	expectFault(t, "COUNTER_REGRESSION", func() { l.Ingest(rt, rt, []M{}, false) })
	l.Ingest(rt, rt, counters("150", "270"), false)
	rt.StartedAt = "epoch-2"
	l.Ingest(rt, rt, counters("10", "20"), false)
	s = l.Snapshot()
	if s.Users[0].Upload != "60" || s.Quality.Gaps != 1 {
		t.Fatal("restart totals")
	}
	other := rt
	other.NodeRevision++
	expectFault(t, "RUNTIME_CHANGED", func() { l.Ingest(rt, other, counters("20", "30"), false) })
	expectFault(t, "DUPLICATE_COUNTER", func() { l.Ingest(rt, rt, append(counters("10", "20"), counters("10", "20")[0]), false) })
	expectFault(t, "LEDGER_EXISTS", l.Create)
	traffic := Traffic{filepath.Join(dir, "traffic.db"), filepath.Join(dir, "traffic.json")}
	traffic.Initialize()
	if !traffic.Ingest("node_hk", s) || traffic.Ingest("node_hk", s) {
		t.Fatal("snapshot dedup")
	}
	bad := s
	bad.Seq--
	bad.Seal()
	expectFault(t, "SNAPSHOT_OLD", func() { traffic.Ingest("node_hk", bad) })
	bad = s
	bad.LedgerID = "ledger_other"
	bad.Seal()
	expectFault(t, "LEDGER_CHANGED", func() { traffic.Ingest("node_hk", bad) })
	bad = s
	bad.Seq++
	bad.Users = []LedgerUser{}
	bad.Seal()
	expectFault(t, "TOTAL_REGRESSION", func() { traffic.Ingest("node_hk", bad) })
	traffic.Failed("node_hk", "LEDGER_CHANGED")
	export := object(read(traffic.Export))
	if str(mapping(mapping(mapping(export["nodes"])["node_hk"])["snapshot"])["snapshot_seq"]) != fmt.Sprint(s.Seq) {
		t.Fatal("historical snapshot lost")
	}
}
func TestLegacyDataRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")
	initialize(path, "control")
	transaction(path, true, func(d *DB) { putMeta(d, "schema_version", "1") })
	expectFault(t, "SCHEMA_MISMATCH", func() { initialize(path, "control") })
	expectFault(t, "SCHEMA_MISMATCH", func() { transaction(path, false, func(d *DB) {}) })
}

func TestAPIContractFlow(t *testing.T) {
	c := testControl(t)
	CreateAdmin(c.Settings, "admin", "管理员", "only-a-test-password")
	handler := API(c)
	var cookie *http.Cookie
	csrf := ""
	request := func(method, path string, body any, origin, token, key string) *httptest.ResponseRecorder {
		t.Helper()
		var data []byte
		if body != nil {
			data = canonical(body)
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(data))
		r.RemoteAddr = "127.0.0.1:1234"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		r.Header.Set("X-CSRF-Token", token)
		r.Header.Set("Idempotency-Key", key)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
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
	if w := request("GET", "/api/v1/users", nil, "", "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	loginBody := M{"login_name": "admin", "password": "only-a-test-password"}
	if w := request("POST", "/api/v1/session", loginBody, "http://untrusted.test", "", ""); w.Code != 403 {
		t.Fatal(w.Code)
	}
	w := request("POST", "/api/v1/session", loginBody, "https://admin.test", "", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	validate("/api/v1/session", "post", 200, w.Body.Bytes())
	for _, v := range w.Result().Cookies() {
		if v.Name == CookieName {
			cookie = v
		}
	}
	if cookie == nil || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("cookie flags")
	}
	csrf = str(object(w.Body.Bytes())["csrf_token"])
	body := CreateUser{DisplayName: "验收", GroupIDs: []string{"grp_hk"}}
	if w = request("POST", "/api/v1/users", body, "https://admin.test", "wrong", "test-create-00001"); w.Code != 403 {
		t.Fatal("csrf", w.Code)
	}
	w = request("POST", "/api/v1/users", body, "https://admin.test", csrf, "test-create-00001")
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	validate("/api/v1/users", "post", 202, w.Body.Bytes())
	worker := NewWorker(c, &fakeTransport{})
	worker.Step()
	for _, path := range []string{"/session", "/overview", "/nodes", "/users", "/usage", "/operations", "/alerts", "/groups"} {
		w = request("GET", "/api/v1"+path, nil, "", "", "")
		if w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
		validate("/api/v1"+path, "get", 200, w.Body.Bytes())
		if strings.Contains(w.Body.String(), "secret_ciphertext") || strings.Contains(w.Body.String(), "password") {
			t.Fatal("secret in response")
		}
	}
	w = request("GET", "/api/v1/users?page_size=51", nil, "", "", "")
	if w.Code != 422 {
		t.Fatal("unbounded page")
	}
	for _, path := range []string{"/node-discovery", "/node-onboarding", "/nodes/node_hk/config", "/traffic"} {
		w = request("GET", "/api/v1"+path, nil, "", "", "")
		if w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
		contractPath := strings.ReplaceAll(path, "node_hk", "{id}")
		validate("/api/v1"+contractPath, "get", 200, w.Body.Bytes())
	}
	u := Views{c.Settings}.Users()[0]
	uid, revision := str(u["id"]), integer(u["revision"])
	traffic := Traffic{c.Settings.TrafficDB, c.Settings.TrafficSnapshot}
	traffic.Initialize()
	for i, sample := range []string{"2026-01-01T10:00:00Z", "2026-01-01T10:00:15Z"} {
		snapshot := historySnapshot(int64(i+1), sample, fmt.Sprint(100+i*100), fmt.Sprint(200+i*200))
		snapshot.Users[0].UserID = uid
		snapshot.Seal()
		traffic.Ingest("node_hk", snapshot)
	}
	w = request("GET", "/api/v1/traffic?from=2026-01-01&to=2026-01-01&node=node_hk&protocol=vmess", nil, "", "", "")
	if w.Code != 200 {
		t.Fatal("traffic date query", w.Code, w.Body.String())
	}
	validate("/api/v1/traffic", "get", 200, w.Body.Bytes())
	report := object(w.Body.Bytes())
	if mapping(array(report["nodes"])[0])["inbound_upload_bytes"] != "100" || mapping(array(report["users"])[0])["download_bytes"] != "200" {
		t.Fatal("traffic HTTP response must contain date deltas for inbounds and managed users")
	}
	if w = request("GET", "/api/v1/traffic?from=2026-02-30&to=2026-03-01", nil, "", "", ""); w.Code != 422 {
		t.Fatal("invalid traffic date accepted", w.Code)
	}
	c.DisableUser("admin", "api-disable-request", uid, DisableUser{&revision, "停用"})
	revision++
	enablePath := "/api/v1/users/" + uid + "/enable"
	enableBody := EnableUser{&revision, "重新启用"}
	w = request("POST", enablePath, enableBody, "https://admin.test", "wrong", "api-enable-request")
	if w.Code != 403 {
		t.Fatal("enable csrf", w.Code)
	}
	w = request("POST", enablePath, M{"reason": "missing revision"}, "https://admin.test", csrf, "api-enable-request")
	if w.Code != 422 {
		t.Fatal("enable validation", w.Code)
	}
	w = request("POST", enablePath, enableBody, "https://admin.test", csrf, "api-enable-request")
	if w.Code != 202 {
		t.Fatal("enable", w.Code, w.Body.String())
	}
	validate("/api/v1/users/{user_id}/enable", "post", 202, w.Body.Bytes())
	acceptedEnable := w.Body.String()
	w = request("POST", enablePath, enableBody, "https://admin.test", csrf, "api-enable-request")
	if w.Code != 202 || w.Body.String() != acceptedEnable {
		t.Fatal("enable replay")
	}
	w = request("GET", "/api/v1/requests/api-enable-request", nil, "", "", "")
	if w.Code != 200 || w.Body.String() != acceptedEnable {
		t.Fatal("enable recovery")
	}
	for worker.Step() {
	}
	w = request("GET", "/api/v1/operations", nil, "", "", "")
	validate("/api/v1/operations", "get", 200, w.Body.Bytes())
	revision++
	w = request("POST", enablePath, EnableUser{&revision, "已启用"}, "https://admin.test", csrf, "api-enable-active")
	if w.Code != 409 || object(w.Body.Bytes())["code"] != "NOT_DISABLED" {
		t.Fatal("active enable", w.Code)
	}
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		d.Exec("INSERT INTO groups VALUES ('grp_overlap','重叠组')")
		d.Exec("INSERT INTO group_nodes VALUES ('grp_overlap','node_hk')")
	})
	grantPath := "/api/v1/users/" + uid + "/grants"
	selection := GrantSelection{&revision, []string{"grp_hk", "grp_overlap"}}
	for _, path := range []string{grantPath, grantPath + "/preview"} {
		w = request("POST", path, selection, "https://admin.test", "wrong", "api-grants-request")
		if w.Code != 403 {
			t.Fatal("grant csrf", w.Code)
		}
	}
	beforePreview := read(c.Settings.ControlDB)
	w = request("POST", grantPath+"/preview", selection, "https://admin.test", csrf, "")
	if w.Code != 200 || !bytes.Equal(beforePreview, read(c.Settings.ControlDB)) {
		t.Fatal("preview must be read only", w.Code)
	}
	validate("/api/v1/users/{user_id}/grants/preview", "post", 200, w.Body.Bytes())
	grantBody := UpdateGrants{selection, "调整组授权", str(object(w.Body.Bytes())["preview_hash"])}
	w = request("POST", grantPath, grantBody, "https://admin.test", csrf, "api-grants-request")
	if w.Code != 202 {
		t.Fatal("grant update", w.Code, w.Body.String())
	}
	validate("/api/v1/users/{user_id}/grants", "post", 202, w.Body.Bytes())
	acceptedGrant := w.Body.String()
	w = request("POST", grantPath, grantBody, "https://admin.test", csrf, "api-grants-request")
	if w.Code != 202 || w.Body.String() != acceptedGrant {
		t.Fatal("grant replay")
	}
	w = request("GET", "/api/v1/requests/api-grants-request", nil, "", "", "")
	if w.Code != 200 || w.Body.String() != acceptedGrant {
		t.Fatal("grant recovery")
	}
	for worker.Step() {
	}
	w = request("GET", "/api/v1/operations", nil, "", "", "")
	validate("/api/v1/operations", "get", 200, w.Body.Bytes())
	nodeInput := NodeJobInput{Kind: "inspect", Draft: draftFor(c)}
	w = request("POST", "/api/v1/node-jobs", nodeInput, "https://admin.test", "wrong", "node-contract-001")
	if w.Code != 403 {
		t.Fatal("node csrf", w.Code)
	}
	w = request("POST", "/api/v1/node-jobs", nodeInput, "https://admin.test", csrf, "node-contract-001")
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	validate("/api/v1/node-jobs", "post", 202, w.Body.Bytes())
	jobID := str(object(w.Body.Bytes())["job_id"])
	NewWorker(c, &managementTransport{}).NodeStep()
	w = request("GET", "/api/v1/node-jobs/"+jobID, nil, "", "", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	validate("/api/v1/node-jobs/{id}", "get", 200, w.Body.Bytes())
	w = request("GET", "/api/v1/nodes/node_hk/jobs", nil, "", "", "")
	validate("/api/v1/nodes/{id}/jobs", "get", 200, w.Body.Bytes())
	u = Views{c.Settings}.Users()[0]
	metadataVersion := integer(u["metadata_version"])
	metadata := UserMetadata{&metadataVersion, "管理验收用户", "保留授权和历史"}
	metadataPath := "/api/v1/users/" + uid + "/metadata"
	w = request("POST", metadataPath, metadata, "https://admin.test", "wrong", "api-user-metadata")
	if w.Code != 403 {
		t.Fatal("metadata csrf", w.Code)
	}
	w = request("POST", metadataPath, metadata, "https://admin.test", csrf, "api-user-metadata")
	if w.Code != 200 {
		t.Fatal("metadata", w.Code, w.Body.String())
	}
	validate("/api/v1/users/{id}/metadata", "post", 200, w.Body.Bytes())
	revision = integer(u["revision"])
	generation := integer(u["subscription_generation"])
	resetPath := "/api/v1/users/" + uid + "/subscription-reset"
	reset := ResetSubscription{&revision, &generation, "重新交付链接"}
	w = request("POST", resetPath, reset, "https://admin.test", "wrong", "api-subscription-reset")
	if w.Code != 403 {
		t.Fatal("reset csrf", w.Code)
	}
	w = request("POST", resetPath, reset, "https://admin.test", csrf, "api-subscription-reset")
	if w.Code != 200 {
		t.Fatal("subscription reset", w.Code, w.Body.String())
	}
	validate("/api/v1/users/{id}/subscription-reset", "post", 200, w.Body.Bytes())
	worker.Reconcile()
	c.DisableUser("admin", "api-management-disable", uid, DisableUser{&revision, "归档验收"})
	for worker.Step() {
	}
	archive := ArchiveUsers{[]UserVersion{{uid, metadataVersion + 1, revision + 1}}, true}
	w = request("POST", "/api/v1/users/archive", archive, "https://admin.test", "wrong", "api-user-archive")
	if w.Code != 403 {
		t.Fatal("archive csrf", w.Code)
	}
	w = request("POST", "/api/v1/users/archive", archive, "https://admin.test", csrf, "api-user-archive")
	if w.Code != 200 {
		t.Fatal("archive", w.Code, w.Body.String())
	}
	validate("/api/v1/users/archive", "post", 200, w.Body.Bytes())
	w = request("GET", "/api/v1/users?archived=true", nil, "", "", "")
	validate("/api/v1/users", "get", 200, w.Body.Bytes())
	if integer(object(w.Body.Bytes())["total"]) != 1 {
		t.Fatal("archived user not listed")
	}
	w = request("GET", "/api/v1/users/"+uid+"/audit", nil, "", "", "")
	validate("/api/v1/users/{id}/audit", "get", 200, w.Body.Bytes())
	deletion := DeleteUsers{[]UserVersion{{uid, metadataVersion + 2, revision + 1}}}
	w = request("POST", "/api/v1/users/delete", deletion, "https://admin.test", "wrong", "api-user-delete01")
	if w.Code != 403 {
		t.Fatal("delete csrf", w.Code)
	}
	w = request("POST", "/api/v1/users/delete", deletion, "https://admin.test", csrf, "api-user-delete01")
	if w.Code != 200 {
		t.Fatal("delete", w.Code, w.Body.String())
	}
	validate("/api/v1/users/delete", "post", 200, w.Body.Bytes())
	w = request("POST", "/api/v1/users/delete", deletion, "https://admin.test", csrf, "api-user-delete01")
	if w.Code != 200 {
		t.Fatal("delete replay", w.Code)
	}
	w = request("GET", "/api/v1/users/"+uid, nil, "", "", "")
	if w.Code != 404 {
		t.Fatal("deleted detail still readable", w.Code)
	}
	w = request("GET", "/api/v1/operations", nil, "", "", "")
	validate("/api/v1/operations", "get", 200, w.Body.Bytes())
	w = request("DELETE", "/api/v1/session", nil, "https://admin.test", csrf, "")
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
	w = request("GET", "/api/v1/session", nil, "", "", "")
	if w.Code != 401 {
		t.Fatal("logout")
	}
	public := PublicAPI(c.Settings.PublicDir)
	w = httptest.NewRecorder()
	public.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/users", nil))
	if w.Code != 404 {
		t.Fatal("public isolation")
	}
}
func TestPublicationTamperAndTraversal(t *testing.T) {
	p := PublicStore{t.TempDir()}
	tok := token(48)
	key := hash([]byte(tok))
	p.Publish(key, 1, map[string]string{"surge.conf": "test", "mihomo.yaml": "test"})
	atomicWrite(filepath.Join(p.Root, "data", key, "1", "surge.conf"), []byte("tampered"), 0644)
	expectFault(t, "PUBLICATION_INVALID", func() { p.Read(tok, "surge.conf") })
	expectFault(t, "NOT_FOUND", func() { p.Read(tok, "../../etc/passwd") })
}
func TestFreshnessAndLargeTotals(t *testing.T) {
	c := testControl(t)
	snapshot := M{"started_at": now(), "sampled_at": now(), "users": []M{{"user_id": "usr_a", "upload_bytes": "9223372036854775807", "download_bytes": "9223372036854775807"}}, "data_quality": M{"integrity_state": "ok", "gap_count": 0, "has_historical_gap": false}}
	atomicWrite(c.Settings.TrafficSnapshot, canonical(M{"nodes": M{"node_hk": M{"snapshot": snapshot, "synced_at": now()}}}), 0644)
	v := Views{c.Settings}
	if v.Nodes()[0]["status"] != "online" {
		t.Fatal("fresh")
	}
	r := v.Overview()
	if str(r["upload_bytes"]) != "9223372036854775807" {
		t.Fatal("integer precision")
	}
	snapshot["sampled_at"] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	atomicWrite(c.Settings.TrafficSnapshot, canonical(M{"nodes": M{"node_hk": M{"snapshot": snapshot, "synced_at": now()}}}), 0644)
	if v.Nodes()[0]["status"] != "offline" {
		t.Fatal("stale")
	}
}
