package sbctl

import "fmt"

func verifyManagedEndpoints(s NodeSettings, engine Engine, expectedHash string) M {
	before := engine.Runtime()
	if !before.Running || before.ConfigHash != expectedHash {
		fail("RUNTIME_BINDING", "运行配置与待验证配置不符", 503)
	}
	active, raw := currentConfig(s)
	if hash(raw) != expectedHash {
		fail("RUNTIME_BINDING", "运行配置与待验证配置不符", 503)
	}
	endpoints := normalizeEndpoints(s.Endpoints)
	checks := make([]M, 0, len(endpoints))
	for _, ep := range endpoints {
		in := findInbound(active, ep.Tag)
		if in == nil || str(in["type"]) != ep.Protocol || int(integer(in["listen_port"])) != ep.Port {
			fail("PROBE_FAILED", fmt.Sprintf("活动配置缺少入口 %s 或端口不一致", ep.Tag), 503)
		}
		checks = append(checks, M{"inbound_id": ep.InboundID, "tag": ep.Tag, "protocol": ep.Protocol, "port": ep.Port, "ok": true})
	}
	if !before.Stable(engine.Runtime()) {
		fail("RUNTIME_CHANGED", "协议验证期间运行配置变化", 503)
	}
	return M{"status": "passed", "template_hash": s.TemplateHash, "endpoints_hash": digest(endpoints), "runtime_epoch": before.Epoch(), "config_hash": before.ConfigHash, "checked_at": now(), "endpoints": checks}
}

func saveProbeVerification(d *DB, node string, result M) bool {
	proof := mapping(result["probe_verification"])
	if proof["status"] == "not_run" {
		return false
	}
	if proof["template_hash"] == result["template_hash"] && proof["endpoints_hash"] == digest(decodeEndpoints(result["endpoints"])) &&
		hashRE.MatchString(str(proof["config_hash"])) && str(proof["runtime_epoch"]) != "" && str(proof["checked_at"]) != "" {
		putMeta(d, "inbound-probe:"+node, string(canonical(proof)))
		return true
	}
	return false
}

func probeVerified(d *DB, node M) bool {
	raw := str(d.Value("SELECT value FROM meta WHERE key=?", "inbound-probe:"+str(node["id"])))
	if raw == "" {
		return false
	}
	proof := object([]byte(raw))
	if proof["status"] == "not_applicable" || proof["status"] == "not_run" {
		return false
	}
	return proof["template_hash"] == node["template_hash"] && proof["endpoints_hash"] == digest(decodeEndpoints(node["endpoints_json"]))
}

func grantableEndpoints(d *DB, node M) []Endpoint {
	state := str(d.Value("SELECT state FROM node_config_snapshots WHERE node_id=?", node["id"]))
	if contains([]string{"pending_restart", "pending_sync", "conflict"}, state) {
		return nil
	}
	eps := []Endpoint{}
	for _, endpoint := range decodeEndpoints(node["endpoints_json"]) {
		if !retiringInbound(d, str(node["id"]), endpoint.InboundID) {
			eps = append(eps, endpoint)
		}
	}
	return eps
}

func retiringInbound(d *DB, nodeID, inboundID string) bool {
	return d.One("SELECT 1 FROM inbound_retirements r JOIN node_jobs j ON j.id=r.job_id WHERE j.node_id=? AND r.inbound_id=? AND j.state<>'completed'", nodeID, inboundID) != nil
}
