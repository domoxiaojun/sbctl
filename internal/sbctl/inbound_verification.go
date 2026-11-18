package sbctl

import "github.com/google/uuid"

func newInboundProbe(endpoint Endpoint) Credential {
	var id *string
	if endpoint.Protocol == "tuic" {
		value := uuid.NewString()
		id = &value
	}
	return Credential{UserID: "sys_probe", Protocol: endpoint.Protocol, CredentialID: identifier("probe"),
		Generation: 1, Password: token(32), UUID: id, InboundID: endpoint.InboundID}
}

func verifyManagedEndpoints(s NodeSettings, engine Engine, expectedHash string) M {
	before := engine.Runtime()
	if !before.Running || before.ConfigHash != expectedHash {
		fail("RUNTIME_BINDING", "运行配置与待验证配置不符", 503)
	}
	engine.Verify(NodeRequest{Users: []Credential{}}, nil)
	if !before.Stable(engine.Runtime()) {
		fail("RUNTIME_CHANGED", "协议验证期间运行配置变化", 503)
	}
	return M{"template_hash": s.TemplateHash, "endpoints_hash": digest(normalizeEndpoints(s.Endpoints)), "runtime_epoch": before.Epoch(), "config_hash": before.ConfigHash, "checked_at": now()}
}

func saveProbeVerification(d *DB, node string, result M) bool {
	proof := mapping(result["probe_verification"])
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
	return proof["template_hash"] == node["template_hash"] && proof["endpoints_hash"] == digest(decodeEndpoints(node["endpoints_json"]))
}

func grantableEndpoints(d *DB, node M) []Endpoint {
	state := str(d.Value("SELECT state FROM node_config_snapshots WHERE node_id=?", node["id"]))
	if contains([]string{"pending_restart", "pending_sync", "conflict"}, state) || !probeVerified(d, node) {
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
