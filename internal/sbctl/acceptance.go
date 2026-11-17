package sbctl

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Acceptance exercises a live deployment using a dedicated, finally-disabled
// identity. Tokens and credentials are never printed or included in the report.
func Acceptance(args []string) {
	f := argsFor("acceptance", args)
	base := f.String("base-url", "http://127.0.0.1:18100", "")
	public := f.String("public-url", "http://127.0.0.1:18101", "")
	originURL := f.String("origin", "https://sbctl.domob.org", "")
	passwordFile := f.String("password-file", "/opt/sbctl/config/admin.initial", "")
	group := f.String("group", "grp_hk", "")
	lifecycle := f.Bool("lifecycle", false, "verify re-enablement and grant preview using the same test identity")
	report := f.String("report", "/tmp/sbctl-go-acceptance.json", "")
	parsed(f, args, 0)
	client := &http.Client{Timeout: 15 * time.Second}
	session, csrf := "", ""
	call := func(method, path string, body any, key string) (int, M) {
		var input io.Reader
		if body != nil {
			input = bytes.NewReader(canonical(body))
		}
		req, e := http.NewRequest(method, strings.TrimRight(*base, "/")+"/api/v1"+path, input)
		check(e)
		req.Header.Set("Origin", *originURL)
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		if session != "" {
			req.AddCookie(&http.Cookie{Name: CookieName, Value: session})
			req.Header.Set("X-CSRF-Token", csrf)
		}
		resp, e := client.Do(req)
		check(e)
		defer resp.Body.Close()
		for _, c := range resp.Cookies() {
			if c.Name == CookieName {
				session = c.Value
			}
		}
		data, e := io.ReadAll(io.LimitReader(resp.Body, MaxDocument))
		check(e)
		if resp.StatusCode == 204 {
			return 204, M{}
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			fail("ACCEPTANCE_API", fmt.Sprintf("验收接口 %s 返回 %d", path, resp.StatusCode), 503)
		}
		return resp.StatusCode, object(data)
	}
	_, login := call("POST", "/session", M{"login_name": "admin", "password": strings.TrimSpace(string(read(*passwordFile)))}, "")
	csrf = str(login["csrf_token"])
	defer func() { _ = Attempt(func() { call("DELETE", "/session", nil, "") }) }()
	call("GET", "/session", nil, "")
	_, baseline := call("GET", "/traffic?scope=inbounds", nil, "")
	_, created := call("POST", "/users", CreateUser{DisplayName: "Go 功能验收", Note: "验证节点流量后禁用", GroupIDs: []string{*group}}, token(24))
	wait := func(id string) M {
		deadline := time.Now().Add(150 * time.Second)
		for {
			_, op := call("GET", "/operations/"+id, nil, "")
			if op["state"] == "completed" {
				return op
			}
			if contains([]string{"failed", "superseded"}, str(op["state"])) || time.Now().After(deadline) {
				fail("ACCEPTANCE_OPERATION", "节点操作未完成验收", 503)
			}
			time.Sleep(time.Second)
		}
	}
	userID := ""
	disabled := false
	defer func() {
		if userID != "" && !disabled {
			_ = Attempt(func() {
				_, u := call("GET", "/users/"+userID, nil, "")
				rev := integer(u["revision"])
				call("POST", "/users/"+userID+"/disable", DisableUser{&rev, "验收清理"}, token(24))
			})
		}
	}()
	_, queued := call("GET", "/operations/"+str(created["operation_id"]), nil, "")
	userID = str(queued["user_id"])
	op := wait(str(created["operation_id"]))
	_, u := call("GET", "/users/"+userID, nil, "")
	_, links := call("POST", "/users/"+userID+"/subscription-link", nil, token(24))
	fetch := func(raw string) int {
		parsed, e := url.Parse(raw)
		check(e)
		resp, e := client.Get(strings.TrimRight(*public, "/") + parsed.EscapedPath())
		check(e)
		defer resp.Body.Close()
		_, e = io.Copy(io.Discard, io.LimitReader(resp.Body, MaxDocument))
		check(e)
		return resp.StatusCode
	}
	for _, key := range []string{"url", "mihomo_url"} {
		if fetch(str(links[key])) != 200 {
			fail("ACCEPTANCE_SUBSCRIPTION", "订阅下载失败")
		}
	}
	telemetryDeadline := time.Now().Add(60 * time.Second)
	var verifiedTraffic M
	for {
		_, observed := call("GET", "/traffic?scope=inbounds", nil, "")
		if acceptanceTrafficReady(observed, baseline, array(op["targets"])) {
			verifiedTraffic = observed
			break
		}
		if time.Now().After(telemetryDeadline) {
			fail("ACCEPTANCE_TELEMETRY", "授权节点的协议入口尚未形成新的双向流量增量", 503)
		}
		time.Sleep(time.Second)
	}
	rev := integer(u["revision"])
	_, dis := call("POST", "/users/"+userID+"/disable", DisableUser{&rev, "验收结束"}, token(24))
	revoke := wait(str(dis["operation_id"]))
	disabled = true
	for _, key := range []string{"url", "mihomo_url"} {
		if fetch(str(links[key])) != 404 {
			fail("ACCEPTANCE_REVOCATION", "禁用后订阅仍可下载")
		}
	}
	lifecycleResult := M{"tested": false}
	if *lifecycle {
		_, beforeEnable := call("GET", "/users/"+userID, nil, "")
		enableRevision := integer(beforeEnable["revision"])
		key := token(24)
		input := EnableUser{&enableRevision, "验收重新启用"}
		_, accepted := call("POST", "/users/"+userID+"/enable", input, key)
		disabled = false
		_, replay := call("POST", "/users/"+userID+"/enable", input, key)
		_, recovered := call("GET", "/requests/"+key, nil, "")
		if digest(accepted) != digest(replay) || digest(accepted) != digest(recovered) {
			fail("ACCEPTANCE_IDEMPOTENCY", "重新启用的重试或请求恢复返回不同操作")
		}
		enabled := wait(str(accepted["operation_id"]))
		_, active := call("GET", "/users/"+userID, nil, "")
		if active["id"] != userID || active["created_at"] != beforeEnable["created_at"] || active["status"] != "active" {
			fail("ACCEPTANCE_IDENTITY", "重新启用未保留身份")
		}
		_, newLinks := call("POST", "/users/"+userID+"/subscription-link", nil, token(24))
		for _, name := range []string{"url", "mihomo_url"} {
			if newLinks[name] == links[name] || fetch(str(links[name])) != 404 || fetch(str(newLinks[name])) != 200 {
				fail("ACCEPTANCE_REENABLE", "重新启用后的新旧订阅状态不正确")
			}
		}
		grantRevision := integer(active["revision"])
		_, preview := call("POST", "/users/"+userID+"/grants/preview", GrantSelection{&grantRevision, []string{*group}}, "")
		if boolean(preview["changed"]) || len(array(preview["added"])) != 0 || len(array(preview["removed"])) != 0 || len(array(preview["retained"])) != len(array(enabled["targets"])) {
			fail("ACCEPTANCE_GRANTS", "当前节点组预览与已验证授权不一致")
		}
		_, finalDisable := call("POST", "/users/"+userID+"/disable", DisableUser{&grantRevision, "验收清理"}, token(24))
		final := wait(str(finalDisable["operation_id"]))
		disabled = true
		for _, name := range []string{"url", "mihomo_url"} {
			if fetch(str(newLinks[name])) != 404 {
				fail("ACCEPTANCE_REVOCATION", "验收清理后新订阅仍可下载")
			}
		}
		lifecycleResult = M{"tested": true, "enable_operation": enabled["id"], "enable_state": enabled["state"], "final_disable_operation": final["id"], "final_disable_state": final["state"], "identity_and_history_preserved": true, "idempotent_recovery": true, "old_subscriptions": "404", "new_subscriptions": "200_then_404", "grant_preview": preview, "cross_node_grant_changes_tested": false}
	}
	_, nodes := call("GET", "/nodes", nil, "")
	result := M{"checked_at": now(), "language": "go", "version": Version, "login": "passed", "create_operation": op["state"], "disable_operation": revoke["state"], "subscriptions": "downloaded_then_404", "node_targets": revoke["targets"], "nodes": nodes["items"], "test_user": userID, "verified_traffic": verifiedTraffic, "lifecycle": lifecycleResult}
	check(os.MkdirAll(filepath.Dir(*report), 0700))
	atomicWrite(*report, canonical(result), 0600)
	emit(result)
}

func acceptanceTrafficReady(report, baseline M, targets []any) bool {
	if len(targets) == 0 {
		return false
	}
	findNode := func(doc M, id string) M {
		for _, raw := range array(doc["nodes"]) {
			if node := mapping(raw); str(node["id"]) == id {
				return node
			}
		}
		return M{}
	}
	for _, raw := range targets {
		id := str(mapping(raw)["node_id"])
		node, old := findNode(report, id), findNode(baseline, id)
		quality := mapping(node["quality"])
		if quality["freshness"] != "current" || quality["sampled_at"] == nil || str(quality["sampled_at"]) <= str(mapping(old["quality"])["sampled_at"]) {
			return false
		}
		ready := false
		for _, value := range array(node["inbounds"]) {
			inbound := mapping(value)
			if !boolean(inbound["active"]) || !contains([]string{"anytls", "tuic"}, str(inbound["protocol"])) {
				continue
			}
			prior := M{}
			for _, previous := range array(old["inbounds"]) {
				if mapping(previous)["tag"] == inbound["tag"] {
					prior = mapping(previous)
				}
			}
			ready = ready || bytesInt(inbound["upload_bytes"]).Cmp(bytesInt(prior["upload_bytes"])) > 0 && bytesInt(inbound["download_bytes"]).Cmp(bytesInt(prior["download_bytes"])) > 0
		}
		if !ready {
			return false
		}
	}
	return true
}
