package sbctl

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type NodeSettings struct {
	NodeID        string     `toml:"node_id" json:"node_id"`
	StateDir      string     `toml:"state_dir" json:"state_dir"`
	SafetyDir     string     `toml:"safety_dir" json:"safety_dir"`
	ConfigDir     string     `toml:"config_dir" json:"config_dir"`
	TemplateFile  string     `toml:"template_file" json:"template_file"`
	ProbeFile     string     `toml:"probe_file" json:"probe_file"`
	TemplateHash  string     `toml:"template_hash" json:"template_hash"`
	Image         string     `toml:"image" json:"image"`
	EngineBuildID string     `toml:"engine_build_id" json:"engine_build_id"`
	Container     string     `toml:"container" json:"container"`
	UID           int        `toml:"uid" json:"uid"`
	GID           int        `toml:"gid" json:"gid"`
	RevokeFailure string     `toml:"revoke_failure" json:"revoke_failure"`
	Endpoints     []Endpoint `toml:"endpoints" json:"endpoints"`
	ProbePort     int        `toml:"probe_port" json:"probe_port"`
	ProbeUDPPort  int        `toml:"probe_udp_port" json:"probe_udp_port"`
}

func containerName(s string) bool { return match(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`, s) }
func imageReference(s string) bool {
	return match(`^[a-zA-Z0-9][a-zA-Z0-9._:/@-]{0,254}$`, s) && !strings.Contains(s, "..")
}

func LoadNodeSettings() NodeSettings {
	s := NodeSettings{StateDir: "/opt/sbctl/data/node", SafetyDir: "/opt/sbctl/data/safety", ConfigDir: "/etc/sing-box", TemplateFile: "/opt/sbctl/config/template.json", ProbeFile: "/opt/sbctl/config/probe-credentials.json", ProbePort: 31840, ProbeUDPPort: 53190}
	check(toml.NewDecoder(bytes.NewReader(read(env("SBCTL_NODE_CONFIG", "/opt/sbctl/config/node.toml")))).DisallowUnknownFields().Decode(&s))
	s.Validate()
	return s
}

func (s *NodeSettings) Validate() {
	validIdentity(s.NodeID)
	if !hashRE.MatchString(s.TemplateHash) || !hashRE.MatchString(s.EngineBuildID) || !imageReference(s.Image) || !containerName(s.Container) || s.UID < 1 || s.GID < 1 || !contains([]string{"stop", "preserve"}, s.RevokeFailure) || s.ProbePort < 1024 || s.ProbePort > 65000 || s.ProbeUDPPort < 1024 || s.ProbeUDPPort > 65000 || len(s.Endpoints) > 4096 {
		fail("CONFIG_INVALID", "节点配置无效")
	}
	s.Endpoints = normalizeEndpoints(s.Endpoints)
}

func verifyConfigCredentials(settings NodeSettings, req NodeRequest, previous *NodeRequest) {
	active, _ := currentConfig(settings)
	endpoints := normalizeEndpoints(settings.Endpoints)
	current := map[string]Credential{}
	expectedAuth := map[string]string{}
	for _, c := range req.Users {
		current[c.CredentialID] = c
		ep, ok := credentialEndpoint(endpoints, c)
		if !ok {
			fail("CREDENTIAL_BINDING", "凭据未绑定到有效入口", 409)
		}
		inbound := findInbound(active, ep.Tag)
		if inbound == nil || str(inbound["type"]) != ep.Protocol {
			fail("VERIFY_FAILED", "活动配置缺少受管入口", 503)
		}
		matched := false
		for _, entry := range array(inbound["users"]) {
			user := mapping(entry)
			if str(user["name"]) != c.UserID || str(user["password"]) != c.Password {
				continue
			}
			if c.Protocol == "tuic" && (c.UUID == nil || !strings.EqualFold(str(user["uuid"]), *c.UUID)) {
				continue
			}
			matched = true
			break
		}
		if !matched {
			fail("VERIFY_FAILED", "活动配置缺少已授权的用户凭据", 503)
		}
		expectedAuth[ep.Tag+"\x00"+c.authenticationFingerprint()] = c.UserID
	}
	removedAuth := map[string]bool{}
	if previous != nil {
		for _, c := range previous.Users {
			if _, ok := current[c.CredentialID]; !ok {
				removedAuth[c.authenticationFingerprint()] = true
			}
		}
	}
	revokedPrints := map[string]bool{}
	for _, fp := range req.Revoked {
		revokedPrints[fp] = true
	}
	for _, raw := range array(active["inbounds"]) {
		inbound := mapping(raw)
		proto := str(inbound["type"])
		if proto != "anytls" && proto != "tuic" {
			continue
		}
		tag := str(inbound["tag"])
		for _, entry := range array(inbound["users"]) {
			user := mapping(entry)
			candidate := Credential{Protocol: proto, Password: str(user["password"]), UUID: nullable(str(user["uuid"]))}
			fp := candidate.authenticationFingerprint()
			if name := str(user["name"]); name != "" && expectedAuth[tag+"\x00"+fp] == name {
				continue
			}
			if removedAuth[fp] || revokedPrints[fp] {
				fail("REVOKE_FAILED", "活动配置仍包含已撤销的认证材料")
			}
		}
	}
}

type Engine interface {
	Check(string)
	Restart()
	Stop()
	Runtime() Runtime
	Verify(NodeRequest, *NodeRequest)
}

type Agent struct {
	Settings NodeSettings
	Engine   Engine
	DBPath   string
}

func NewAgent(s NodeSettings, e Engine) *Agent {
	s.Endpoints = normalizeEndpoints(s.Endpoints)
	return &Agent{s, e, filepath.Join(s.StateDir, "node.db")}
}
func (a *Agent) Initialize() {
	if !exists(a.DBPath) && exists(filepath.Join(a.Settings.SafetyDir, "manifest")) {
		fail("NODE_STATE_LOST", "安全状态库缺失但存在旧启动清单，拒绝重建")
	}
	check(os.MkdirAll(a.Settings.StateDir, 0700))
	a.migrateGuardManifest()
	initialize(a.DBPath, "node")
	transaction(a.DBPath, true, func(d *DB) {
		d.Exec("UPDATE requests SET state='superseded' WHERE revision<(SELECT max(revision) FROM requests) AND state NOT IN ('verified','superseded')")
	})
}

func (a *Agent) migrateGuardManifest() {
	if !exists(filepath.Join(a.Settings.SafetyDir, "manifest")) {
		return
	}
	unlock := a.Lock()
	defer unlock()
	path := filepath.Join(a.Settings.SafetyDir, "manifest")
	if !exists(path) {
		return
	}
	lines := strings.Split(string(read(path)), "\n")
	if len(lines) < 4 || (lines[1] != "2" && !hashRE.MatchString(lines[1])) {
		return
	}
	if lines[0] != "allowed" && lines[0] != "blocked" {
		return
	}
	if rev, err := strconv.ParseInt(lines[2], 10, 64); err != nil || rev < 0 {
		return
	}
	writeSafety(a.Settings, path, []byte(fmt.Sprintf("%s\n2\n%s\n", lines[0], lines[2])))
}
func (a *Agent) Lock() func() {
	return fileLock(filepath.Join(a.Settings.StateDir, "security.lock"), 60*time.Second)
}
func (a *Agent) Guard(allow bool, _ string, revision int64) {
	state := "blocked"
	if allow {
		state = "allowed"
	}
	path := filepath.Join(a.Settings.SafetyDir, "manifest")
	writeSafety(a.Settings, path, []byte(fmt.Sprintf("%s\n2\n%d\n", state, revision)))
}
func (a *Agent) Probes() []Credential {
	return []Credential{}
}

func (a *Agent) Render(r NodeRequest) []byte {
	data := read(a.Settings.TemplateFile)
	if hash(data) != a.Settings.TemplateHash {
		fail("TEMPLATE_MISMATCH", "本机模板摘要不匹配")
	}
	t := object(data)
	tags := map[string]Endpoint{}
	for _, e := range a.Settings.Endpoints {
		tags[e.Tag] = e
	}
	usersByTag := map[string][]Credential{}
	for _, c := range r.Users {
		ep, ok := credentialEndpoint(a.Settings.Endpoints, c)
		if !ok {
			fail("CREDENTIAL_BINDING", "凭据未绑定到有效入口", 409)
		}
		usersByTag[ep.Tag] = append(usersByTag[ep.Tag], c)
	}
	found := map[string]bool{}
	for _, value := range array(t["inbounds"]) {
		i := mapping(value)
		tag := str(i["tag"])
		ep, ok := tags[tag]
		if !ok {
			continue
		}
		if i["type"] != ep.Protocol || found[tag] {
			fail("TEMPLATE_INVALID", "受管入口类型或标签不匹配")
		}
		found[tag] = true
		users := []M{}
		for _, fixed := range array(i["users"]) {
			m := mapping(fixed)
			if str(m["name"]) == "sys_probe" {
				continue
			}
			users = append(users, m)
		}
		for _, c := range usersByTag[tag] {
			for _, fixed := range users {
				if str(fixed["password"]) == c.Password || (c.Protocol == "tuic" && str(fixed["uuid"]) == str(*c.UUID)) {
					fail("CREDENTIAL_CONFLICT", "凭据与节点保留用户冲突")
				}
			}
			u := M{"name": c.UserID, "password": c.Password}
			if c.Protocol == "tuic" {
				u["uuid"] = c.UUID
			}
			users = append(users, u)
		}
		i["users"] = users
	}
	if len(found) != len(tags) {
		fail("TEMPLATE_INVALID", "模板缺少已登记入口")
	}
	excludedUsers := []string{}
	for _, c := range r.Users {
		excludedUsers = append(excludedUsers, c.UserID)
	}
	configureStatistics(t, unique(excludedUsers))
	return append(canonical(t), '\n')
}
func receipt(r M) M {
	if r == nil {
		return nil
	}
	return pick(r, "id", "revision", "payload_hash", "state", "accepted_at", "applied_at", "verified_at", "config_hash", "error_code", "applied_epoch")
}
func (a *Agent) Status() M {
	var row M
	transaction(a.DBPath, false, func(d *DB) { row = d.One("SELECT * FROM requests ORDER BY revision DESC LIMIT 1") })
	var latest any
	if row != nil {
		latest = receipt(row)
	}
	return M{"node_id": a.Settings.NodeID, "latest": latest}
}
func current(d *DB, r NodeRequest) bool {
	return integer(d.Value("SELECT max(revision) FROM requests")) == r.NodeRevision
}
func (a *Agent) Accept(r NodeRequest) M {
	r.Verify()
	if r.NodeID != a.Settings.NodeID || r.TemplateHash != a.Settings.TemplateHash || r.EngineBuildID != a.Settings.EngineBuildID {
		fail("NODE_BINDING", "节点、模板或内核构建不匹配")
	}
	for _, c := range r.Users {
		if !strings.HasPrefix(c.UserID, "usr_") {
			fail("USER_IDENTITY", "受管用户必须使用中心永久身份")
		}
	}
	unlock := a.Lock()
	defer unlock()
	if observedConfigChanged(a.Settings) || exists(filepath.Join(a.Settings.StateDir, "config-adoption.json")) {
		fail("CONFIG_SYNC_PENDING", "节点配置已被手工修改，等待主控同步后再执行授权", 409)
	}
	var result M
	transaction(a.DBPath, true, func(d *DB) {
		old := d.One("SELECT * FROM requests WHERE id=? OR revision=?", r.RequestID, r.NodeRevision)
		if old != nil {
			if old["id"] != r.RequestID || old["payload_hash"] != r.PayloadHash {
				fail("REQUEST_CONFLICT", "请求标识或版本内容冲突", 409)
			}
			result = receipt(old)
			if !current(d, r) {
				result["state"] = "superseded"
			}
			return
		}
		if r.NodeRevision <= integer(d.Value("SELECT max(revision) FROM requests")) {
			fail("STALE_REVISION", "请求早于已接受安全版本", 409)
		}
		if r.Retirement != "" {
			found := false
			for _, endpoint := range a.Settings.Endpoints {
				found = found || endpoint.InboundID == r.Retirement
			}
			if !found {
				fail("CREDENTIAL_BINDING", "退役请求指向未登记入口", 409)
			}
			if job := d.Value("SELECT job_id FROM retiring_inbounds WHERE inbound_id=?", r.Retirement); job != nil && job != r.RetirementJob {
				fail("RETIREMENT_PENDING", "入口已由另一个退役请求锁定", 409)
			}
		}
		for _, c := range r.Users {
			endpoint, ok := credentialEndpoint(a.Settings.Endpoints, c)
			if !ok || endpoint.InboundID == r.Retirement {
				fail("CREDENTIAL_BINDING", "凭据未绑定到有效入口", 409)
			}
			if d.One("SELECT 1 FROM retiring_inbounds WHERE inbound_id=?", endpoint.InboundID) != nil {
				fail("RETIREMENT_PENDING", "入口正在退役，拒绝恢复用户凭据", 409)
			}
		}
		for _, row := range d.Rows("SELECT fingerprint FROM revoked") {
			f := str(row["fingerprint"])
			if !contains(r.Revoked, f) {
				fail("REVOKED_CREDENTIAL", "请求遗漏撤销记录", 409)
			}
			for _, c := range r.Users {
				if c.Fingerprint() == f {
					fail("REVOKED_CREDENTIAL", "请求包含已撤销凭据", 409)
				}
			}
		}
		a.Guard(false, "", 0)
		d.Exec("UPDATE requests SET state='superseded' WHERE revision<? AND state NOT IN ('verified','superseded')", r.NodeRevision)
		d.Exec("INSERT INTO requests(id,revision,payload_hash,document,state,accepted_at) VALUES (?,?,?,?,'accepted',?)", r.RequestID, r.NodeRevision, r.PayloadHash, string(canonical(r)), now())
		if r.Retirement != "" {
			d.Exec("INSERT OR IGNORE INTO retiring_inbounds(inbound_id,job_id) VALUES (?,?)", r.Retirement, r.RetirementJob)
		}
		for _, f := range r.Revoked {
			d.Exec("INSERT OR IGNORE INTO revoked VALUES (?)", f)
		}
		result = receipt(d.One("SELECT * FROM requests WHERE id=?", r.RequestID))
	})
	return result
}
func (a *Agent) ApplyOnce() bool {
	var row, prevRow M
	var appliedSinceVerification []M
	transaction(a.DBPath, false, func(d *DB) {
		row = d.One("SELECT * FROM requests ORDER BY revision DESC LIMIT 1")
		if row != nil {
			prevRow = d.One("SELECT document FROM requests WHERE verified_at IS NOT NULL AND revision<? ORDER BY revision DESC LIMIT 1", row["revision"])
			appliedSinceVerification = d.Rows("SELECT document FROM requests WHERE applied_at IS NOT NULL AND revision<? AND revision>COALESCE((SELECT max(revision) FROM requests WHERE verified_at IS NOT NULL AND revision<?),0) ORDER BY revision", row["revision"], row["revision"])
		}
	})
	if row == nil || contains([]string{"verified", "revoke_blocked", "failed", "superseded"}, str(row["state"])) {
		return false
	}
	if observedConfigChanged(a.Settings) || exists(filepath.Join(a.Settings.StateDir, "config-adoption.json")) {
		return false
	}
	r := parseRequest(object([]byte(str(row["document"]))))
	var prev *NodeRequest
	if prevRow != nil {
		p := parseRequest(object([]byte(str(prevRow["document"]))))
		prev = &p
	}
	for _, applied := range appliedSinceVerification {
		p := parseRequest(object([]byte(str(applied["document"]))))
		if prev == nil {
			prev = &NodeRequest{}
		}
		for _, credential := range p.Users {
			if contains(r.Revoked, credential.Fingerprint()) {
				prev.Users = append(prev.Users, credential)
			}
		}
	}
	pending := false
	for _, f := range r.Revoked {
		if prev == nil || !contains(prev.Revoked, f) {
			pending = true
		}
	}
	err := Attempt(func() {
		content := a.Render(r)
		ch := hash(content)
		candidate := filepath.Join(a.Settings.StateDir, "candidate.json")
		atomicWrite(candidate, content, 0600)
		if contains([]string{"accepted", "switching"}, str(row["state"])) {
			a.Engine.Check(candidate)
			superseded := false
			func() {
				unlock := a.Lock()
				defer unlock()
				oldEpoch := str(row["previous_epoch"])
				transaction(a.DBPath, true, func(d *DB) {
					if !current(d, r) {
						d.Exec("UPDATE requests SET state='superseded' WHERE id=?", r.RequestID)
						superseded = true
						return
					}
					if oldEpoch == "" {
						oldEpoch = a.Engine.Runtime().Epoch()
					}
					d.Exec("UPDATE requests SET state='switching',config_hash=?,previous_epoch=? WHERE id=?", ch, oldEpoch, r.RequestID)
				})
				if superseded {
					return
				}
				if observedConfigChanged(a.Settings) {
					fail("CONFIG_SYNC_PENDING", "配置已变化，等待同步后继续授权", 409)
				}
				a.Guard(false, "", 0)
				path := filepath.Join(a.Settings.ConfigDir, "config.json")
				atomicWrite(path, content, 0640)
				users := []string{}
				for _, c := range r.Users {
					users = append(users, c.UserID)
				}
				runtimePath := filepath.Join(a.Settings.SafetyDir, "runtime.json")
				atomicWrite(runtimePath, canonical(M{"node_revision": r.NodeRevision, "config_hash": ch, "users": unique(users)}), 0640)
				a.Guard(true, ch, r.NodeRevision)
				recordControlledConfig(a.Settings, content)
				rt := a.Engine.Runtime()
				if !(rt.Running && rt.Epoch() != oldEpoch && rt.ConfigHash == ch && rt.NodeRevision == r.NodeRevision) {
					a.Engine.Restart()
				}
				rt = a.Engine.Runtime()
				if !rt.Running || rt.Epoch() == oldEpoch || rt.ConfigHash != ch || rt.NodeRevision != r.NodeRevision {
					a.Guard(false, "", 0)
					a.Engine.Stop()
					fail("START_FAILED", "新运行实例未建立，保持服务停止")
				}
				transaction(a.DBPath, true, func(d *DB) {
					d.Exec("UPDATE requests SET state='applied',applied_at=?,applied_epoch=? WHERE id=?", now(), rt.Epoch(), r.RequestID)
				})
			}()
			if superseded {
				return
			}
		}
		before := a.Engine.Runtime()
		a.Engine.Verify(r, prev)
		after := a.Engine.Runtime()
		if !before.Stable(after) || after.NodeRevision != r.NodeRevision || after.ConfigHash != ch {
			fail("RUNTIME_CHANGED", "验证期间运行实例改变")
		}
		unlock := a.Lock()
		defer unlock()
		transaction(a.DBPath, true, func(d *DB) {
			if current(d, r) {
				d.Exec("UPDATE requests SET state='verified',verified_at=?,error_code=NULL WHERE id=?", now(), r.RequestID)
			} else {
				d.Exec("UPDATE requests SET state='superseded' WHERE id=?", r.RequestID)
			}
		})
	})
	if err != nil {
		f := PublicFault(err)
		unlock := a.Lock()
		defer unlock()
		transaction(a.DBPath, true, func(d *DB) {
			if !current(d, r) {
				return
			}
			stateNow := str(d.Value("SELECT state FROM requests WHERE id=?", r.RequestID))
			state := "failed"
			if stateNow == "applied" {
				state = "applied"
			}
			if stateNow != "applied" && pending {
				state = "revoke_blocked"
				a.Guard(false, "", 0)
				if a.Settings.RevokeFailure == "stop" {
					a.Engine.Stop()
				}
			} else if stateNow == "accepted" && prev != nil {
				a.Guard(true, hash(a.Render(*prev)), prev.NodeRevision)
			}
			d.Exec("UPDATE requests SET state=?,error_code=? WHERE id=?", state, f.Code, r.RequestID)
		})
	}
	return true
}
func (a *Agent) Retry() {
	unlock := a.Lock()
	defer unlock()
	transaction(a.DBPath, true, func(d *DB) {
		d.Exec("UPDATE requests SET state='accepted',error_code=NULL WHERE revision=(SELECT max(revision) FROM requests) AND state IN ('failed','revoke_blocked')")
	})
}
