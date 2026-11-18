package sbctl

import (
	"fmt"
	"strings"
	"testing"
)

func TestRegisterAllowsZeroAndMultipleSameProtocolEntrances(t *testing.T) {
	c := testControl(t)
	c.Register(M{
		"id": "node_zero", "name": "零入口", "region": "test", "location": "测试",
		"connection": M{"address": "100.70.0.20"}, "endpoints": []Endpoint{},
		"template_hash": strings.Repeat("a", 64), "image_digest": "sha256:" + strings.Repeat("b", 64),
		"group_id": "grp_zero", "group_name": "零入口",
	})
	c.Register(M{
		"id": "node_multi", "name": "多入口", "region": "test", "location": "测试",
		"connection": M{"address": "100.70.0.21"}, "endpoints": []Endpoint{
			{Protocol: "anytls", Tag: "any-a", Server: "a.test", Port: 443, SNI: "a.test"},
			{Protocol: "anytls", Tag: "any-b", Server: "b.test", Port: 8443, SNI: "b.test"},
		},
		"template_hash": strings.Repeat("a", 64), "image_digest": "sha256:" + strings.Repeat("b", 64),
		"group_id": "grp_multi", "group_name": "多入口",
	})
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		row := d.One("SELECT endpoints_json FROM nodes WHERE id='node_multi'")
		eps := decodeEndpoints(row["endpoints_json"])
		if len(eps) != 2 || eps[0].InboundID == eps[1].InboundID {
			t.Fatalf("same-protocol entrances must have distinct IDs: %#v", eps)
		}
	})
}

func TestRetireManagedInboundThroughController(t *testing.T) {
	c, w, bridge, user := syncFixture(t)
	var subscription string
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		subscription = str(c.Vault.Decrypt(str(d.Value("SELECT token_ciphertext FROM subscriptions WHERE user_id=?", user))))
	})
	original := agentRequest(bridge.a, 1, nil, nil)
	transaction(bridge.a.DBPath, false, func(d *DB) {
		original = parseRequest(object([]byte(str(d.Value("SELECT document FROM requests WHERE revision=1")))))
	})
	for i, tag := range []string{"anytls-in", "tuic-in"} {
		draft := draftFor(c)
		draft.ConfigDir = "/etc/sing-box"
		c.SubmitNodeJob("admin", fmt.Sprintf("read-protocol-catalog-%d", i), NodeJobInput{Kind: "protocols", Draft: draft})
		w.NodeStep()
		base := str(mapping(c.NodeConfig(draft.ID)["protocol_catalog"])["hash"])
		job := c.SubmitNodeJob("admin", fmt.Sprintf("retire-managed-inbound-%d", i), NodeJobInput{Kind: "protocol_change", Draft: draft,
			ExpectedVersion: int64(i), Change: &ProtocolChange{Action: "delete", Tag: tag, BaseHash: base}})
		if i == 0 {
			body := string(c.Public.Read(subscription, "surge.conf"))
			if strings.Contains(body, " = anytls,") || !strings.Contains(body, " = tuic-v5,") {
				t.Fatal("subscription must remove only the retiring entrance")
			}
		} else {
			expectFault(t, "NOT_FOUND", func() { c.Public.Read(subscription, "surge.conf") })
		}
		config, _ := currentConfig(bridge.a.Settings)
		if findInbound(config, tag) == nil {
			t.Fatal("subscription withdrawal must precede node mutation")
		}
		w.Step()
		w.NodeStep()
		status := c.NodeJob(str(job["job_id"]))
		evidence := mapping(mapping(status["result"])["retirement"])
		if status["state"] != "completed" || evidence["tag"] != tag || evidence["request_id"] != job["job_id"] ||
			len(bridge.a.Settings.Endpoints) != 1-i || len(bridge.a.Probes()) != 1-i {
			t.Fatalf("target must be retired with a versioned receipt: %v", status)
		}
	}
	config, _ := currentConfig(bridge.a.Settings)
	if findInbound(config, "anytls-in") != nil || findInbound(config, "tuic-in") != nil || findInbound(config, "legacy") == nil {
		t.Fatal("retirement removed the wrong live config")
	}
	original.TemplateHash = bridge.a.Settings.TemplateHash
	original.RequestID = "old_request_replayed"
	original.Seal()
	expectFault(t, "REQUEST_CONFLICT", func() { bridge.a.Accept(original) })
}

func TestCredentialsAreIsolatedByInboundID(t *testing.T) {
	c := testControl(t)
	c.Register(M{
		"id": "node_same", "name": "同协议", "region": "test", "location": "测试",
		"connection": M{"address": "100.70.0.22"}, "endpoints": []Endpoint{
			{Protocol: "anytls", Tag: "any-a", Server: "a.test", Port: 443, SNI: "a.test"},
			{Protocol: "anytls", Tag: "any-b", Server: "b.test", Port: 8443, SNI: "b.test"},
		},
		"template_hash": strings.Repeat("a", 64), "image_digest": "sha256:" + strings.Repeat("b", 64),
		"group_id": "grp_same", "group_name": "同协议",
	})
	request := CreateUser{DisplayName: "同协议用户", GroupIDs: []string{"grp_same"}}
	expectFault(t, "GRANT_EMPTY", func() { c.CreateUser("admin", "same-inbound-user-01", request, nil) })
	markTestEndpointsVerified(c)
	c.CreateUser("admin", "same-inbound-user-01", request, nil)
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		rows := d.Rows("SELECT inbound_id,protocol,generation FROM credentials WHERE node_id='node_same'")
		if len(rows) != 2 || str(rows[0]["inbound_id"]) == str(rows[1]["inbound_id"]) {
			t.Fatalf("credentials must remain isolated by inbound ID: %#v", rows)
		}
	})
}
