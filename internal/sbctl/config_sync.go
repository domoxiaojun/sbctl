package sbctl

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// This document travels only over the publish SSH connection. Only the safe
// synchronization status is exposed through HTTP; the full document is encrypted.
type NodeConfigSnapshot struct {
	NodeID       string `json:"node_id"`
	EventSeq     int64  `json:"event_seq"`
	SemanticHash string `json:"semantic_hash"`
	ConfigHash   string `json:"config_hash"`
	RuntimeEpoch string `json:"runtime_epoch"`
	NodeRevision int64  `json:"node_revision"`
	TemplateHash string `json:"template_hash"`
	Applied      bool   `json:"applied"`
	External     bool   `json:"external"`
	Config       M      `json:"config"`
	Projection   M      `json:"projection"`
}

type ConfigAdoption struct {
	EventSeq     int64       `json:"event_seq"`
	SemanticHash string      `json:"semantic_hash"`
	RuntimeEpoch string      `json:"runtime_epoch"`
	Request      NodeRequest `json:"request"`
}

type configSyncPlan struct {
	Adoption  ConfigAdoption `json:"adoption"`
	Endpoints []Endpoint     `json:"endpoints"`
	BaseHash  string         `json:"base_hash"`
	Removed   []string       `json:"removed"`
}

func configObservationPath(s NodeSettings) string {
	return filepath.Join(s.StateDir, "config-observation.json")
}
func currentConfig(s NodeSettings) (M, []byte) {
	raw := read(filepath.Join(s.ConfigDir, "config.json"))
	return object(raw), raw
}
func configObservation(s NodeSettings) M {
	if exists(configObservationPath(s)) {
		return object(read(configObservationPath(s)))
	}
	return M{}
}
func observedConfigChanged(s NodeSettings) bool {
	if !exists(filepath.Join(s.ConfigDir, "config.json")) {
		return false
	}
	config, _ := currentConfig(s)
	obs := configObservation(s)
	if str(obs["adopted_hash"]) != "" {
		return digest(config) != str(obs["adopted_hash"]) || boolean(obs["pending_sync"])
	}
	// Upgrade baseline: comparing parsed JSON ignores harmless reformatting.
	a := NewAgent(s, nil)
	var previous M
	if exists(a.DBPath) {
		transaction(a.DBPath, false, func(d *DB) {
			previous = d.One("SELECT document FROM requests WHERE applied_at IS NOT NULL ORDER BY revision DESC LIMIT 1")
		})
	}
	if previous != nil {
		var expected []byte
		if Attempt(func() { expected = a.Render(parseRequest(object([]byte(str(previous["document"]))))) }) == nil {
			return digest(config) != digest(object(expected))
		}
	}
	if exists(filepath.Join(s.SafetyDir, "runtime.json")) {
		binding := object(read(filepath.Join(s.SafetyDir, "runtime.json")))
		return hash(read(filepath.Join(s.ConfigDir, "config.json"))) != str(binding["config_hash"])
	}
	return false
}

// Called only for a write already owned by the authorization/maintenance locks.
func recordControlledConfig(s NodeSettings, data []byte) {
	obs := configObservation(s)
	h := digest(object(data))
	seq := integer(obs["event_seq"])
	if seq == 0 || h != str(obs["semantic_hash"]) {
		seq++
	}
	atomicWrite(configObservationPath(s), canonical(M{"event_seq": seq, "semantic_hash": h, "adopted_hash": h, "pending_sync": false}), 0600)
}

func configProjection(_ NodeSettings, config M) M {
	endpoints := []M{}
	for _, raw := range array(config["inbounds"]) {
		in := mapping(raw)
		if in["type"] != "anytls" && in["type"] != "tuic" {
			continue
		}
		users := []M{}
		for _, rawUser := range array(in["users"]) {
			u := mapping(rawUser)
			users = append(users, M{"name": str(u["name"]), "fingerprint": hash(canonical(pick(u, "password", "uuid")))})
		}
		endpoints = append(endpoints, M{"tag": str(in["tag"]), "protocol": str(in["type"]), "port": integer(in["listen_port"]), "users": users})
	}
	return M{"endpoints": endpoints}
}

func snapshotConfig(s NodeSettings, engine Engine) NodeConfigSnapshot {
	config, raw := currentConfig(s)
	before := engine.Runtime()
	obs := configObservation(s)
	h := digest(config)
	seq := integer(obs["event_seq"])
	if seq == 0 || h != str(obs["semantic_hash"]) {
		seq++
	}
	external := observedConfigChanged(s)
	// A startup marker reports the file actually loaded, rather than the file
	// presently on disk. Editing a live bind mount must not publish unused secrets.
	applied := before.Running && !before.Paused && !before.Restarting && before.ConfigHash == hash(raw)
	if !external && before.Running && !before.Paused && !before.Restarting && hashRE.MatchString(before.ConfigHash) {
		applied = true
	}
	if str(obs["loaded_epoch"]) == before.Epoch() && str(obs["loaded_semantic_hash"]) == h && before.Running {
		applied = true
	}
	after := engine.Runtime()
	if before.Epoch() != after.Epoch() || before.ConfigHash != after.ConfigHash || !after.Running {
		applied = false
	}
	if hash(read(filepath.Join(s.ConfigDir, "config.json"))) != hash(raw) {
		fail("CONFIG_CHANGED", "读取配置期间文件发生变化，稍后重试", 409)
	}
	obs["event_seq"], obs["semantic_hash"], obs["pending_sync"] = seq, h, external
	if applied {
		obs["loaded_epoch"], obs["loaded_semantic_hash"] = before.Epoch(), h
	}
	atomicWrite(configObservationPath(s), canonical(obs), 0600)
	rev := before.NodeRevision
	transaction(filepath.Join(s.StateDir, "node.db"), false, func(d *DB) { rev = max(rev, integer(d.Value("SELECT max(revision) FROM requests"))) })
	return NodeConfigSnapshot{s.NodeID, seq, h, hash(raw), before.Epoch(), rev, s.TemplateHash, applied, external, config, configProjection(s, config)}
}

func nodeConfigSnapshot(s NodeSettings) M {
	unlock := fileLock(filepath.Join(s.StateDir, "management.lock"), 20*time.Second)
	defer unlock()
	executor := fileLock(filepath.Join(s.StateDir, "executor.lock"), 0)
	defer executor()
	security := NewAgent(s, nil).Lock()
	defer security()
	if exists(filepath.Join(s.StateDir, "maintenance-pending.json")) {
		fail("NODE_BUSY", "节点维护中，稍后同步配置", 409)
	}
	snap := snapshotConfig(s, DockerEngine{s})
	return object(canonical(snap))
}

// Resolve a renamed primary tag only if there is exactly one candidate. An
// unchanged primary tag is preferred when other local entrances share its type.
func projectedConfig(s NodeSettings, config M) (NodeSettings, M, []Credential) {
	template := object(canonical(config))
	byTag := map[string]M{}
	for _, raw := range array(template["inbounds"]) {
		in := mapping(raw)
		tag := str(in["tag"])
		if tag == "" {
			continue
		}
		if byTag[tag] != nil {
			fail("BINDING_CONFLICT", "入口标签重复，暂停此节点授权", 409)
		}
		byTag[tag] = in
	}
	s.Endpoints = normalizeEndpoints(s.Endpoints)
	probes := []Credential{}
	claimed := map[string]bool{}
	for n := range s.Endpoints {
		e := &s.Endpoints[n]
		in := byTag[e.Tag]
		if in == nil || in["type"] != e.Protocol {
			candidates := []M{}
			for _, candidate := range byTag {
				if candidate["type"] == e.Protocol {
					candidates = append(candidates, candidate)
				}
			}
			if len(candidates) != 1 {
				fail("BINDING_CONFLICT", "受管入口缺失或无法唯一匹配，暂停此节点授权", 409)
			}
			in = candidates[0]
			e.Tag = str(in["tag"])
		}
		if claimed[e.Tag] {
			fail("BINDING_CONFLICT", "多个入口绑定到同一标签", 409)
		}
		claimed[e.Tag] = true
		e.Port = int(integer(in["listen_port"]))
		tls := mapping(in["tls"])
		if str(tls["server_name"]) != "" {
			e.SNI = str(tls["server_name"])
		}
		if tls["alpn"] != nil {
			e.ALPN = inspectionALPN(tls["alpn"])
		}
		e.Validate()
		kept := []any{}
		for _, rawUser := range array(in["users"]) {
			u := mapping(rawUser)
			name := str(u["name"])
			if name == "sys_probe" {
				p := Credential{UserID: name, Protocol: e.Protocol, InboundID: e.InboundID, CredentialID: "sys_" + e.InboundID, Generation: 1, Password: str(u["password"]), UUID: nullable(str(u["uuid"]))}
				p.Validate()
				probes = append(probes, p)
			} else if !strings.HasPrefix(name, "usr_") {
				kept = append(kept, rawUser)
			}
		}
		in["users"] = kept
	}
	// Missing system probes remain local state for future controlled writes. They
	// are not injected into the administrator's active file by adoption.
	if len(probes) != len(s.Endpoints) && exists(s.ProbeFile) {
		decode(read(s.ProbeFile), &probes)
	}
	return s, template, probes
}

func adoptConfig(s NodeSettings, settingsPath string, engine Engine, adoption ConfigAdoption) M {
	unlock := fileLock(filepath.Join(s.StateDir, "management.lock"), 20*time.Second)
	defer unlock()
	executor := fileLock(filepath.Join(s.StateDir, "executor.lock"), 0)
	defer executor()
	security := NewAgent(s, nil).Lock()
	defer security()
	if exists(filepath.Join(s.StateDir, "maintenance-pending.json")) {
		fail("NODE_BUSY", "请先恢复节点维护任务", 409)
	}
	adoption.Request.Verify()
	snap := snapshotConfig(s, engine)
	if !snap.Applied || snap.EventSeq != adoption.EventSeq || snap.SemanticHash != adoption.SemanticHash || snap.RuntimeEpoch != adoption.RuntimeEpoch {
		fail("CONFIG_CHANGED", "节点配置或运行周期已变化，重新同步", 409)
	}
	next, template, probes := projectedConfig(s, snap.Config)
	templateBytes := append(canonical(template), '\n')
	next.TemplateHash = hash(templateBytes)
	r := adoption.Request
	if r.NodeID != s.NodeID || r.TemplateHash != next.TemplateHash || r.EngineBuildID != s.EngineBuildID {
		fail("NODE_BINDING", "配置接管目标不一致", 409)
	}
	observed := map[string]string{}
	for _, e := range next.Endpoints {
		for _, raw := range array(findInbound(snap.Config, e.Tag)["users"]) {
			u := mapping(raw)
			if strings.HasPrefix(str(u["name"]), "usr_") {
				observed[str(u["name"])+":"+e.InboundID] = digest(pick(u, "password", "uuid"))
			}
		}
	}
	if len(observed) != len(r.Users) {
		fail("CONFIG_CHANGED", "接管授权与节点配置不一致", 409)
	}
	for _, u := range r.Users {
		value := M{"password": u.Password, "uuid": nil}
		if u.UUID != nil {
			value["uuid"] = *u.UUID
		}
		ep, ok := credentialEndpoint(next.Endpoints, u)
		if !ok || observed[u.UserID+":"+ep.InboundID] != digest(value) {
			fail("CONFIG_CHANGED", "接管凭据与节点配置不一致", 409)
		}
	}
	a := NewAgent(s, engine)
	var old M
	transaction(a.DBPath, false, func(d *DB) {
		old = d.One("SELECT * FROM requests WHERE id=?", r.RequestID)
		latest := integer(d.Value("SELECT max(revision) FROM requests"))
		if r.NodeRevision < latest || (old == nil && r.NodeRevision == latest) {
			fail("STALE_REVISION", "配置接管版本已过期", 409)
		}
		for _, item := range d.Rows("SELECT fingerprint FROM revoked") {
			if !contains(r.Revoked, str(item["fingerprint"])) {
				fail("REVOKED_CREDENTIAL", "配置接管请求遗漏撤销记录", 409)
			}
		}
		for _, credential := range r.Users {
			endpoint, _ := credentialEndpoint(next.Endpoints, credential)
			if d.One("SELECT 1 FROM retiring_inbounds WHERE inbound_id=?", endpoint.InboundID) != nil {
				fail("RETIREMENT_PENDING", "拒绝将手工凭据重新接管到退役入口", 409)
			}
		}
	})
	if old != nil && old["payload_hash"] != r.PayloadHash {
		fail("REQUEST_CONFLICT", "配置接管回执冲突", 409)
	}
	if old == nil {
		if _, docker := engine.(DockerEngine); docker {
			for _, u := range append(append([]Credential{}, r.Users...), probes...) {
				if !(DockerEngine{next}).Probe(u, true) {
					fail("PROBE_FAILED", "手工配置凭据的实际协议探测未通过", 503)
				}
			}
		} else {
			engine.Verify(r, nil)
		}
	}
	// Durable, replayable before-images are not needed: adoption never writes the
	// active config. Its journal repairs template/settings after an interruption.
	journal := filepath.Join(s.StateDir, "config-adoption.json")
	atomicWrite(journal, canonical(M{"adoption": adoption, "settings": next, "template": string(templateBytes), "probes": probes}), 0600)
	atomicWrite(next.TemplateFile, templateBytes, 0600)
	atomicWrite(next.ProbeFile, canonical(probes), 0600)
	settings, err := toml.Marshal(next)
	check(err)
	atomicWrite(settingsPath, settings, 0600)
	if hash(read(filepath.Join(s.ConfigDir, "config.json"))) != snap.ConfigHash || engine.Runtime().Epoch() != snap.RuntimeEpoch {
		fail("CONFIG_CHANGED", "接管期间节点配置发生变化，重新同步", 409)
	}
	transaction(a.DBPath, true, func(d *DB) {
		if old == nil {
			d.Exec("UPDATE requests SET state='superseded' WHERE state NOT IN ('verified','superseded')")
			d.Exec("INSERT INTO requests(id,revision,payload_hash,document,state,accepted_at,applied_at,verified_at,config_hash,applied_epoch) VALUES (?,?,?,?,'verified',?,?,?,?,?)", r.RequestID, r.NodeRevision, r.PayloadHash, string(canonical(r)), now(), now(), now(), snap.ConfigHash, snap.RuntimeEpoch)
		}
		for _, fp := range r.Revoked {
			d.Exec("INSERT OR IGNORE INTO revoked VALUES (?)", fp)
		}
	})
	runtimePath := filepath.Join(s.SafetyDir, "runtime.json")
	users := []string{}
	for _, c := range r.Users {
		users = append(users, c.UserID)
	}
	writeSafety(next, runtimePath, canonical(M{"node_revision": r.NodeRevision, "config_hash": snap.ConfigHash, "users": unique(users)}))
	NewAgent(next, engine).Guard(true, snap.ConfigHash, r.NodeRevision)
	obs := configObservation(s)
	obs["adopted_hash"], obs["pending_sync"] = snap.SemanticHash, false
	atomicWrite(configObservationPath(s), canonical(obs), 0600)
	checkRemove(journal)
	proof := M{"template_hash": next.TemplateHash, "endpoints_hash": digest(normalizeEndpoints(next.Endpoints)), "runtime_epoch": snap.RuntimeEpoch, "config_hash": snap.ConfigHash, "checked_at": now()}
	return M{"node_id": s.NodeID, "event_seq": snap.EventSeq, "semantic_hash": snap.SemanticHash, "template_hash": next.TemplateHash, "endpoints": next.Endpoints, "node_revision": r.NodeRevision, "request_id": r.RequestID, "probe_verification": proof}
}
func checkRemove(path string) {
	if exists(path) {
		check(os.Remove(path))
	}
}

func writeSafety(s NodeSettings, path string, data []byte) {
	atomicWrite(path, data, 0640)
	if os.Geteuid() == 0 {
		check(os.Chown(path, 0, s.GID))
		check(os.Chown(s.SafetyDir, 0, s.GID))
	}
	check(os.Chmod(s.SafetyDir, 0750))
	guard := filepath.Join(s.SafetyDir, "guard.sh")
	if exists(guard) {
		check(os.Chmod(guard, 0640))
		if os.Geteuid() == 0 {
			check(os.Chown(guard, 0, s.GID))
		}
	}
}

func parseNodeSnapshot(v M) NodeConfigSnapshot {
	var s NodeConfigSnapshot
	decode(canonical(v), &s)
	validIdentity(s.NodeID)
	if s.EventSeq < 1 || !hashRE.MatchString(s.SemanticHash) || !hashRE.MatchString(s.ConfigHash) || !hashRE.MatchString(s.TemplateHash) || s.Config == nil || s.SemanticHash != digest(s.Config) || s.RuntimeEpoch == "" {
		fail("NODE_RESPONSE", "配置快照无效", 409)
	}
	return s
}

func (w *Worker) nodeLock(id string) func() {
	return fileLock(w.Control.Settings.ControlDB+".node-"+hash([]byte(id))+".lock", 0)
}
func (w *Worker) SyncConfigSnapshots() {
	var nodes []M
	transaction(w.Control.Settings.ControlDB, false, func(d *DB) { nodes = d.Rows("SELECT id,connection_json FROM nodes ORDER BY id") })
	// One unreachable node must not stop authorization processing in Run. This
	// method is scheduled in a dispatcher slot, never on its scheduling loop.
	for _, n := range nodes {
		if err := Attempt(func() { w.syncNodeConfig(str(n["id"]), parseConnection(object([]byte(str(n["connection_json"]))))) }); err != nil {
			code := PublicFault(err).Code
			if code != "LOCK_BUSY" && code != "NODE_BUSY" {
				transaction(w.Control.Settings.ControlDB, true, func(d *DB) { d.Exec("UPDATE node_config_snapshots SET error_code=? WHERE node_id=?", code, n["id"]) })
			}
		}
	}
}
func syncContext(d *DB, id string) string {
	return digest(M{"users": d.Rows("SELECT c.id,c.generation,c.fingerprint,c.revoked,u.status,u.revision FROM credentials c JOIN users u ON u.id=c.user_id WHERE c.node_id=? ORDER BY c.id", id), "template": d.Value("SELECT template_hash FROM nodes WHERE id=?", id)})
}
func (w *Worker) syncNodeConfig(id string, link NodeConnection) {
	unlock := w.nodeLock(id)
	defer unlock()
	var base string
	transaction(w.Control.Settings.ControlDB, false, func(d *DB) { base = syncContext(d, id) })
	snap := parseNodeSnapshot(w.Transport.Call(link, M{"action": "config_snapshot"}, false))
	if snap.NodeID != id {
		fail("NODE_IDENTITY", "配置快照节点不一致", 409)
	}
	c := w.Control
	var plan configSyncPlan
	work := false
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		old := d.One("SELECT * FROM node_config_snapshots WHERE node_id=?", id)
		if old != nil && snap.EventSeq < integer(old["event_seq"]) {
			fail("CONFIG_SEQUENCE_OLD", "配置事件序号退回，需要恢复节点状态", 409)
		}
		if old != nil && snap.EventSeq == integer(old["event_seq"]) && snap.SemanticHash != str(old["semantic_hash"]) {
			fail("CONFIG_SEQUENCE_CONFLICT", "相同配置事件内容不同", 409)
		}
		if old != nil && snap.EventSeq == integer(old["event_seq"]) && snap.RuntimeEpoch == str(old["runtime_epoch"]) && old["state"] == "synced" {
			d.Exec("UPDATE node_config_snapshots SET checked_at=? WHERE node_id=?", now(), id)
			return
		}
		if old != nil && snap.EventSeq == integer(old["event_seq"]) && snap.RuntimeEpoch == str(old["runtime_epoch"]) && (old["state"] == "conflict" || (old["state"] == "pending_restart" && !snap.Applied)) {
			d.Exec("UPDATE node_config_snapshots SET checked_at=? WHERE node_id=?", now(), id)
			return
		}
		state, code := "pending_restart", ""
		if snap.Applied {
			if old != nil && old["state"] == "pending_sync" && snap.EventSeq == integer(old["event_seq"]) {
				saved := mapping(c.Vault.Decrypt(str(old["projection_ciphertext"])))
				decode(canonical(saved["plan"]), &plan)
				work = true
				state = "pending_sync"
			} else if !snap.External {
				state = "synced"
				d.Exec("UPDATE nodes SET template_hash=? WHERE id=?", snap.TemplateHash, id)
			} else if base != syncContext(d, id) {
				fail("CONFIG_CHANGED", "同步期间中心授权已变化，重新读取", 409)
			} else {
				prepareErr := Attempt(func() { plan = prepareConfigSync(d, c, snap, base) })
				if prepareErr != nil {
					code = PublicFault(prepareErr).Code
					state = "conflict"
					if code == "BINDING_CONFLICT" {
						revokeNodeCredentials(d, id)
						d.Exec("UPDATE nodes SET enabled=0 WHERE id=?", id)
					}
					alert(d, "config-sync:"+id, "节点配置同步冲突", "配置快照已加密保存；受管入口或身份无法对应，暂停自动写入", "high", id)
				} else {
					state = "pending_sync"
					work = true
				}
			}
		}
		projection := M{"projection": snap.Projection}
		if work {
			projection["plan"] = plan
		}
		d.Exec("INSERT INTO node_config_snapshots(node_id,event_seq,semantic_hash,runtime_epoch,config_ciphertext,projection_ciphertext,state,error_code,checked_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?) ON CONFLICT(node_id) DO UPDATE SET event_seq=excluded.event_seq,semantic_hash=excluded.semantic_hash,runtime_epoch=excluded.runtime_epoch,config_ciphertext=excluded.config_ciphertext,projection_ciphertext=excluded.projection_ciphertext,state=excluded.state,error_code=excluded.error_code,checked_at=excluded.checked_at,updated_at=excluded.updated_at", id, snap.EventSeq, snap.SemanticHash, snap.RuntimeEpoch, c.Vault.Encrypt(snap.Config), c.Vault.Encrypt(projection), state, code, now(), now())
		audit(d, "worker", "config_observed", id, "配置事件 "+str(snap.EventSeq)+"，状态 "+state)
	})
	if !work {
		return
	}
	result := w.Transport.Call(link, M{"action": "adopt_config", "adoption": plan.Adoption}, false)
	if str(result["request_id"]) != plan.Adoption.Request.RequestID || integer(result["event_seq"]) != snap.EventSeq || result["semantic_hash"] != snap.SemanticHash || result["template_hash"] != plan.Adoption.Request.TemplateHash {
		fail("NODE_RESPONSE", "配置接管回执不一致", 409)
	}
	affected := []string{}
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		row := d.One("SELECT event_seq,semantic_hash,state FROM node_config_snapshots WHERE node_id=?", id)
		if integer(row["event_seq"]) != snap.EventSeq || row["semantic_hash"] != snap.SemanticHash {
			fail("CONFIG_CHANGED", "配置同步已被新事件替代", 409)
		}
		if row["state"] == "synced" {
			return
		}
		if syncContext(d, id) == plan.BaseHash {
			affected = commitConfigSync(d, c, plan)
		}
		// If a newer center intent arrived during RPC, preserve it and re-seal its
		// queued request against this new template/revision instead of importing
		// an older credential snapshot over it.
		r := plan.Adoption.Request
		d.Exec("UPDATE nodes SET template_hash=?,endpoints_json=?,revision=max(revision,?),applied_revision=max(applied_revision,?),enabled=1 WHERE id=?", r.TemplateHash, string(canonical(plan.Endpoints)), r.NodeRevision, r.NodeRevision, id)
		saveProbeVerification(d, id, result)
		d.Exec("UPDATE outbox SET request_id=NULL WHERE node_id=? AND state<>'done'", id)
		d.Exec("UPDATE operation_targets SET request_id=NULL WHERE node_id=? AND state NOT IN ('verified','superseded')", id)
		d.Exec("INSERT INTO node_management(node_id,version,binding_json) VALUES (?,1,'{}') ON CONFLICT(node_id) DO UPDATE SET version=version+1", id)
		d.Exec("UPDATE node_config_snapshots SET state='synced',error_code=NULL,updated_at=? WHERE node_id=?", now(), id)
		d.Exec("UPDATE alerts SET state='resolved' WHERE dedupe_key=?", "config-sync:"+id)
		audit(d, "worker", "config_sync", id, "已吸收配置事件 "+str(snap.EventSeq)+"，入口、节点授权及订阅同步更新")
	})
	for _, user := range unique(affected) {
		c.RefreshSubscription(user)
	}
}

func prepareConfigSync(d *DB, c *Control, snap NodeConfigSnapshot, base string) configSyncPlan {
	eps := decodeEndpoints(d.Value("SELECT endpoints_json FROM nodes WHERE id=?", snap.NodeID))
	next, template, _ := projectedConfig(NodeSettings{Endpoints: eps}, snap.Config)
	type managedInbound struct {
		endpoint Endpoint
		inbound  M
	}
	managed := []managedInbound{}
	for _, e := range next.Endpoints {
		managed = append(managed, managedInbound{e, findInbound(snap.Config, e.Tag)})
	}
	users := []Credential{}
	seen := map[string]bool{}
	rawPrints := map[string]bool{}
	for _, item := range managed {
		protocol, in := item.endpoint.Protocol, item.inbound
		for _, raw := range array(in["users"]) {
			u := mapping(raw)
			name := str(u["name"])
			if !strings.HasPrefix(name, "usr_") {
				continue
			}
			if d.One("SELECT id FROM users WHERE id=?", name) == nil {
				fail("CONFIG_IDENTITY_CONFLICT", "节点存在未知受管身份", 409)
			}
			if retiringInbound(d, snap.NodeID, item.endpoint.InboundID) {
				fail("CONFIG_IDENTITY_CONFLICT", "退役中的入口不能重新导入用户凭据", 409)
			}
			key := name + ":" + item.endpoint.InboundID
			if seen[key] {
				fail("CONFIG_IDENTITY_CONFLICT", "受管用户有多组凭据，无法唯一匹配", 409)
			}
			seen[key] = true
			cred := Credential{UserID: name, Protocol: protocol, InboundID: item.endpoint.InboundID, CredentialID: identifier("cred"), Generation: integer(d.Value("SELECT COALESCE(MAX(generation),0)+1 FROM credentials WHERE user_id=? AND node_id=? AND inbound_id=?", name, snap.NodeID, item.endpoint.InboundID)), Password: str(u["password"]), UUID: nullable(str(u["uuid"])), ExternalSeq: snap.EventSeq}
			cred.Validate()
			rawFP := cred.authenticationFingerprint()
			if rawPrints[rawFP] {
				fail("CONFIG_IDENTITY_CONFLICT", "入口认证秘密重复", 409)
			}
			rawPrints[rawFP] = true
			current := d.One("SELECT * FROM credentials WHERE user_id=? AND node_id=? AND inbound_id=? AND revoked=0 ORDER BY generation DESC LIMIT 1", name, snap.NodeID, item.endpoint.InboundID)
			if current != nil {
				secret := mapping(c.Vault.Decrypt(str(current["secret_ciphertext"])))
				if str(secret["password"]) == cred.Password && str(secret["uuid"]) == str(u["uuid"]) {
					cred.CredentialID = str(current["id"])
					cred.Generation = integer(current["generation"])
					cred.ExternalSeq = integer(secret["external_sync_seq"])
				}
			}
			users = append(users, cred)
		}
	}
	removed := []string{}
	revoked := []string{}
	for _, row := range d.Rows("SELECT fingerprint FROM revocations WHERE node_id=?", snap.NodeID) {
		revoked = append(revoked, str(row["fingerprint"]))
	}
	activeIDs := map[string]bool{}
	for _, u := range users {
		activeIDs[u.CredentialID] = true
	}
	for _, row := range d.Rows("SELECT id,fingerprint FROM credentials WHERE node_id=? AND revoked=0", snap.NodeID) {
		if !activeIDs[str(row["id"])] {
			removed = append(removed, str(row["id"]))
			revoked = append(revoked, str(row["fingerprint"]))
		}
	}
	revision := max(integer(d.Value("SELECT revision FROM nodes WHERE id=?", snap.NodeID)), snap.NodeRevision) + 1
	r := NodeRequest{SchemaVersion: 1, NodeID: snap.NodeID, RequestID: identifier("sync"), NodeRevision: revision, TemplateHash: hash(append(canonical(template), '\n')), EngineBuildID: strings.TrimPrefix(str(d.Value("SELECT image_digest FROM nodes WHERE id=?", snap.NodeID)), "sha256:"), Users: users, Revoked: unique(revoked)}
	r.Seal()
	return configSyncPlan{ConfigAdoption{snap.EventSeq, snap.SemanticHash, snap.RuntimeEpoch, r}, next.Endpoints, base, removed}
}

func commitConfigSync(d *DB, c *Control, plan configSyncPlan) []string {
	r := plan.Adoption.Request
	affected := []string{}
	for _, id := range plan.Removed {
		row := d.One("SELECT user_id,fingerprint FROM credentials WHERE id=?", id)
		d.Exec("INSERT OR IGNORE INTO revocations VALUES (?,?,?,?)", r.NodeID, row["fingerprint"], id, now())
		d.Exec("UPDATE credentials SET revoked=1,verified_revision=NULL WHERE id=?", id)
		affected = append(affected, str(row["user_id"]))
	}
	for _, u := range r.Users {
		if d.One("SELECT id FROM credentials WHERE id=?", u.CredentialID) == nil {
			d.Exec("INSERT INTO credentials(id,user_id,node_id,inbound_id,protocol,generation,secret_ciphertext,fingerprint,verified_revision) VALUES (?,?,?,?,?,?,?,?,?)", u.CredentialID, u.UserID, r.NodeID, u.InboundID, u.Protocol, u.Generation, c.Vault.Encrypt(M{"password": u.Password, "uuid": u.UUID, "external_sync_seq": u.ExternalSeq}), u.Fingerprint(), r.NodeRevision)
		} else {
			d.Exec("UPDATE credentials SET verified_revision=? WHERE id=?", r.NodeRevision, u.CredentialID)
		}
		// Explicit local reactivation affects this node only. Other revoked node
		// credentials stay revoked; historical revocations are never deleted.
		d.Exec("UPDATE users SET status='active' WHERE id=? AND status='disabled'", u.UserID)
		affected = append(affected, u.UserID)
	}
	for _, user := range unique(affected) {
		d.Exec("UPDATE users SET revision=revision+1 WHERE id=?", user)
		d.Exec("UPDATE subscriptions SET state='pending' WHERE user_id=?", user)
	}
	return affected
}
func revokeNodeCredentials(d *DB, id string) {
	for _, row := range d.Rows("SELECT id,fingerprint,user_id FROM credentials WHERE node_id=? AND revoked=0", id) {
		d.Exec("INSERT OR IGNORE INTO revocations VALUES (?,?,?,?)", id, row["fingerprint"], row["id"], now())
		d.Exec("UPDATE credentials SET revoked=1,verified_revision=NULL WHERE id=?", row["id"])
		d.Exec("UPDATE subscriptions SET state='pending' WHERE user_id=?", row["user_id"])
	}
}
