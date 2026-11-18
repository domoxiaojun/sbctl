package sbctl

func beginInboundRetirement(d *DB, jobID, nodeID string, change ProtocolChange) []string {
	node := d.One("SELECT endpoints_json,template_hash FROM nodes WHERE id=?", nodeID)
	var endpoint Endpoint
	for _, candidate := range decodeEndpoints(node["endpoints_json"]) {
		if candidate.Tag == change.Tag {
			endpoint = candidate
			break
		}
	}
	if endpoint.InboundID == "" {
		return nil
	}
	rawCatalog := str(d.Value("SELECT catalog_json FROM node_catalog WHERE node_id=?", nodeID))
	if rawCatalog == "" {
		fail("PROTOCOL_CHANGED", "请先读取协议配置", 409)
	}
	catalog := object([]byte(rawCatalog))
	if catalog["hash"] != change.BaseHash {
		fail("PROTOCOL_CHANGED", "请重新读取协议配置后再退役入口", 409)
	}
	for _, item := range array(catalog["items"]) {
		if entry := mapping(item); entry["tag"] == change.Tag && boolean(entry["referenced"]) {
			fail("PROTOCOL_REFERENCED", "路由或其他配置引用此入口，请先处理关联规则", 409)
		}
	}
	opID := identifier("op")
	d.Exec("INSERT INTO operations(id,kind,user_id,subject,state,priority,created_at) VALUES (?,'retire_inbound',NULL,?,'queued',100,?)", opID, change.Tag, now())
	d.Exec("INSERT INTO operation_targets VALUES (?,?, 'queued', '等待撤销入口凭据',NULL)", opID, nodeID)
	d.Exec("INSERT INTO outbox(id,operation_id,node_id,priority,state,next_attempt_at) VALUES (?,?,?,100,'queued',?)", identifier("task"), opID, nodeID, timestamp())
	d.Exec("INSERT INTO inbound_retirements VALUES (?,?,?,?)", jobID, opID, endpoint.InboundID, node["template_hash"])
	affected := []string{}
	for _, row := range d.Rows("SELECT id,user_id,fingerprint FROM credentials WHERE node_id=? AND inbound_id=? AND revoked=0", nodeID, endpoint.InboundID) {
		d.Exec("INSERT OR IGNORE INTO revocations VALUES (?,?,?,?)", nodeID, row["fingerprint"], row["id"], now())
		affected = append(affected, str(row["user_id"]))
	}
	d.Exec("UPDATE credentials SET revoked=1,verified_revision=NULL WHERE node_id=? AND inbound_id=?", nodeID, endpoint.InboundID)
	return unique(affected)
}

func retirementUsers(d *DB, nodeID, inboundID string) []M {
	return d.Rows("SELECT DISTINCT user_id FROM credentials WHERE node_id=? AND inbound_id=?", nodeID, inboundID)
}
