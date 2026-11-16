package sbctl

import (
	"github.com/google/uuid"
	"github.com/pelletier/go-toml/v2"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type ProtocolChange struct {
	Action     string `json:"action"`
	BaseHash   string `json:"base_hash"`
	Tag        string `json:"tag"`
	SourceTag  string `json:"source_tag,omitempty"`
	Listen     string `json:"listen,omitempty"`
	Port       int    `json:"port,omitempty"`
	TargetHost string `json:"target_host,omitempty"`
	TargetPort int    `json:"target_port,omitempty"`
	Username   string `json:"username,omitempty"`
	Password   string `json:"password,omitempty"`
	UUID       string `json:"uuid,omitempty"`
}

func (p ProtocolChange) Validate() {
	if !contains([]string{"create", "edit", "enable", "disable", "delete"}, p.Action) || !hashRE.MatchString(p.BaseHash) || !validStatName(p.Tag) || len(p.Tag) > 80 {
		fail("INVALID_REQUEST", "协议操作、配置版本或入口标签无效", 422)
	}
	if p.Action == "create" || p.Action == "edit" {
		if p.Port < 1 || p.Port > 65535 {
			fail("INVALID_REQUEST", "监听端口须为 1 至 65535", 422)
		}
		if _, err := netip.ParseAddr(p.Listen); err != nil {
			fail("INVALID_REQUEST", "监听地址必须是 IPv4 或 IPv6 地址", 422)
		}
		if p.TargetHost != "" && !match(`^[a-zA-Z0-9.:-]{1,253}$`, p.TargetHost) {
			fail("INVALID_REQUEST", "转发目标地址无效", 422)
		}
		if p.TargetPort < 0 || p.TargetPort > 65535 {
			fail("INVALID_REQUEST", "转发目标端口无效", 422)
		}
	}
	if p.Action == "create" && (!validStatName(p.SourceTag) || !validStatName(p.Username) || strings.HasPrefix(p.Username, "usr_") || strings.HasPrefix(p.Username, "sys_")) {
		fail("INVALID_REQUEST", "请选择配置模板并填写独立的本地用户名", 422)
	}
	if len(p.Password) > 256 || strings.ContainsAny(p.Password, "\x00\r\n") {
		fail("INVALID_REQUEST", "密码格式无效", 422)
	}
	if p.UUID != "" {
		if _, err := uuid.Parse(p.UUID); err != nil {
			fail("INVALID_REQUEST", "UUID 无效", 422)
		}
	}
}

type ProtocolItem struct {
	Tag        string `json:"tag"`
	Protocol   string `json:"protocol"`
	Listen     string `json:"listen"`
	Port       int    `json:"port"`
	Enabled    bool   `json:"enabled"`
	Managed    bool   `json:"managed"`
	UserCount  int    `json:"user_count"`
	TLS        bool   `json:"tls"`
	Method     string `json:"method"`
	TargetHost string `json:"target_host"`
	TargetPort int    `json:"target_port"`
}
type ProtocolCatalog struct {
	NodeID    string         `json:"node_id"`
	Hash      string         `json:"hash"`
	CheckedAt string         `json:"checked_at"`
	Items     []ProtocolItem `json:"items"`
}

func protocolState(s NodeSettings) M {
	p := filepath.Join(s.StateDir, "disabled-protocols.json")
	if exists(p) {
		return object(read(p))
	}
	return M{"inbounds": []any{}, "retired_tags": []string{}}
}
func catalogFor(s NodeSettings, config, disabled M) ProtocolCatalog {
	result := ProtocolCatalog{NodeID: s.NodeID, Hash: digest(M{"config": config, "disabled": disabled, "template_hash": s.TemplateHash, "endpoints": s.Endpoints}), CheckedAt: now(), Items: []ProtocolItem{}}
	managed := map[string]bool{}
	for _, e := range s.Endpoints {
		managed[e.Tag] = true
	}
	appendItems := func(values []any, enabled bool) {
		for _, raw := range values {
			i := mapping(raw)
			tag := str(i["tag"])
			if tag == "" {
				continue
			}
			result.Items = append(result.Items, ProtocolItem{tag, str(i["type"]), str(i["listen"]), int(integer(i["listen_port"])), enabled, managed[tag], len(array(i["users"])), boolean(mapping(i["tls"])["enabled"]), str(i["method"]), str(i["override_address"]), int(integer(i["override_port"]))})
		}
	}
	appendItems(array(config["inbounds"]), true)
	appendItems(array(disabled["inbounds"]), false)
	sort.Slice(result.Items, func(i, j int) bool { return result.Items[i].Tag < result.Items[j].Tag })
	return result
}
func readProtocolCatalog() ProtocolCatalog {
	s := LoadNodeSettings()
	unlock := fileLock(filepath.Join(s.StateDir, "management.lock"), 20*time.Second)
	defer unlock()
	return catalogFor(s, object(read(filepath.Join(s.ConfigDir, "config.json"))), protocolState(s))
}
func findInbound(config M, tag string) M {
	for _, i := range array(config["inbounds"]) {
		if mapping(i)["tag"] == tag {
			return mapping(i)
		}
	}
	return nil
}
func removeInbound(config M, tag string) {
	out := []any{}
	for _, i := range array(config["inbounds"]) {
		if mapping(i)["tag"] != tag {
			out = append(out, i)
		}
	}
	config["inbounds"] = out
}
func referencedInbound(value any, tag string) bool {
	switch v := value.(type) {
	case map[string]any:
		for k, x := range v {
			if k == "inbound" {
				switch selector := x.(type) {
				case string:
					if selector == tag {
						return true
					}
				case []any:
					for _, selected := range selector {
						if selected == tag {
							return true
						}
					}
				}
			}
			if referencedInbound(x, tag) {
				return true
			}
		}
	case []any:
		for _, x := range v {
			if referencedInbound(x, tag) {
				return true
			}
		}
	}
	return false
}
func planProtocol(s NodeSettings, config, template, disabled M, p ProtocolChange) (NodeSettings, M, M, M) {
	p.Validate()
	// Detached copies: callers retain the complete original bytes for rollback.
	config = object(canonical(config))
	template = object(canonical(template))
	disabled = object(canonical(disabled))
	if catalogFor(s, config, disabled).Hash != p.BaseHash {
		fail("PROTOCOL_CHANGED", "协议配置已变化，请重新读取后操作", 409)
	}
	managed := false
	for _, e := range s.Endpoints {
		managed = managed || e.Tag == p.Tag
	}
	if managed && contains([]string{"disable", "delete"}, p.Action) {
		fail("PROTOCOL_REQUIRED", "此入口承担 sbctl 授权与运行探针，不能停用或删除；可编辑监听配置", 409)
	}
	active := findInbound(config, p.Tag)
	inactive := findInbound(disabled, p.Tag)
	if p.Action == "create" {
		if active != nil || inactive != nil || contains(stringsOf(disabled["retired_tags"]), p.Tag) {
			fail("PROTOCOL_EXISTS", "入口标签已使用或曾被删除，请使用新标签以区分历史流量", 409)
		}
		source := findInbound(template, p.SourceTag)
		if source == nil {
			source = findInbound(disabled, p.SourceTag)
		}
		if source == nil || !contains([]string{"anytls", "tuic", "vmess", "vless", "shadowsocks", "direct"}, str(source["type"])) {
			fail("PROTOCOL_TEMPLATE", "请选择受支持的现有协议作为配置模板", 422)
		}
		entry := object(canonical(source))
		entry["tag"] = p.Tag
		protocol := str(entry["type"])
		if contains([]string{"anytls", "tuic", "shadowsocks"}, protocol) && len(p.Password) < 16 {
			fail("INVALID_REQUEST", "新入口密码至少 16 个字符", 422)
		}
		if contains([]string{"vmess", "vless", "tuic"}, protocol) && p.UUID == "" {
			fail("INVALID_REQUEST", "新入口需要独立 UUID", 422)
		}
		if protocol == "shadowsocks" {
			entry["password"] = p.Password
			delete(entry, "users")
		}
		if contains([]string{"anytls", "tuic", "vmess", "vless"}, protocol) {
			u := M{"name": p.Username}
			if protocol == "anytls" || protocol == "tuic" {
				u["password"] = p.Password
			}
			if protocol != "anytls" {
				u["uuid"] = p.UUID
			}
			entry["users"] = []M{u}
		}
		config["inbounds"] = append(array(config["inbounds"]), entry)
		template["inbounds"] = append(array(template["inbounds"]), object(canonical(entry)))
		active = entry
	} else if active == nil && inactive == nil {
		fail("NOT_FOUND", "协议入口不存在", 404)
	}
	switch p.Action {
	case "create", "edit":
		entry := active
		if entry == nil {
			entry = inactive
		}
		if !contains([]string{"anytls", "tuic", "vmess", "vless", "shadowsocks", "direct"}, str(entry["type"])) {
			fail("PROTOCOL_UNSUPPORTED", "当前表单不支持该入口类型", 422)
		}
		set := func(i M) {
			i["listen"] = p.Listen
			i["listen_port"] = p.Port
			if i["type"] == "direct" {
				if p.TargetHost == "" || p.TargetPort < 1 {
					fail("INVALID_REQUEST", "转发入口需要目标地址和端口", 422)
				}
				i["override_address"] = p.TargetHost
				i["override_port"] = p.TargetPort
			}
		}
		set(entry)
		if active != nil {
			set(findInbound(template, p.Tag))
		}
		if managed {
			s.Endpoints = append([]Endpoint{}, s.Endpoints...)
			for i := range s.Endpoints {
				if s.Endpoints[i].Tag == p.Tag {
					s.Endpoints[i].Port = p.Port
					s.Endpoints[i].Validate()
				}
			}
		}
	case "disable":
		if active == nil {
			fail("PROTOCOL_DISABLED", "入口已经停用", 409)
		}
		if referencedInbound(without(config, "inbounds"), p.Tag) {
			fail("PROTOCOL_REFERENCED", "路由或其他配置引用此入口，请先处理关联规则", 409)
		}
		disabled["inbounds"] = append(array(disabled["inbounds"]), object(canonical(active)))
		removeInbound(config, p.Tag)
		removeInbound(template, p.Tag)
	case "enable":
		if inactive == nil {
			fail("PROTOCOL_ENABLED", "入口已经启用", 409)
		}
		config["inbounds"] = append(array(config["inbounds"]), object(canonical(inactive)))
		template["inbounds"] = append(array(template["inbounds"]), object(canonical(inactive)))
		removeInbound(disabled, p.Tag)
	case "delete":
		if referencedInbound(without(config, "inbounds"), p.Tag) {
			fail("PROTOCOL_REFERENCED", "路由或其他配置引用此入口，请先处理关联规则", 409)
		}
		removeInbound(config, p.Tag)
		removeInbound(template, p.Tag)
		removeInbound(disabled, p.Tag)
		disabled["retired_tags"] = append(stringsOf(disabled["retired_tags"]), p.Tag)
	}
	// Be conservative about binding collisions, including UDP/TCP cross-protocol ports.
	if p.Action == "create" || p.Action == "edit" || p.Action == "enable" {
		changed := findInbound(config, p.Tag)
		if changed != nil {
			for _, raw := range array(config["inbounds"]) {
				i := mapping(raw)
				if i["tag"] != p.Tag && integer(i["listen_port"]) == integer(changed["listen_port"]) {
					fail("PORT_CONFLICT", "该端口已被其他入口使用，请选择独立端口", 409)
				}
			}
		}
	}
	configureStatistics(config)
	configureStatistics(template)
	return s, config, template, disabled
}

type NodeMaintenance struct {
	Settings     NodeSettings
	SettingsPath string
	Engine       func(NodeSettings) Engine
}

// ReceiptStatus reconciles a durable completed receipt. An unfinished plan is
// never resumed ahead of a queued authorization.
func (m NodeMaintenance) ReceiptStatus(id, inputHash string) M {
	validIdentity(id)
	if !hashRE.MatchString(inputHash) {
		fail("REQUEST_INVALID", "维护请求摘要无效", 422)
	}
	unlock := fileLock(filepath.Join(m.Settings.StateDir, "management.lock"), 3*time.Second)
	defer unlock()
	receiptPath := filepath.Join(m.Settings.StateDir, "maintenance-"+id+".json")
	pendingPath := filepath.Join(m.Settings.StateDir, "maintenance-pending.json")
	if exists(receiptPath) {
		r := object(read(receiptPath))
		if r["input_hash"] != inputHash {
			fail("REQUEST_CONFLICT", "任务内容与回执不同", 409)
		}
		// A crash can happen between committing the receipt and removing the
		// maintenance barrier. Clear only this completed request's barrier.
		if exists(pendingPath) {
			if object(read(pendingPath))["id"] != id {
				fail("REQUEST_CONFLICT", "其他节点维护任务仍在恢复", 409)
			}
			check(os.Remove(pendingPath))
		}
		return M{"state": "completed", "result": r["result"]}
	}
	planPath := filepath.Join(m.Settings.StateDir, "maintenance-plan-"+id+".json")
	if exists(planPath) && exists(pendingPath) {
		plan, pending := object(read(planPath)), object(read(pendingPath))
		if plan["input_hash"] != inputHash || pending["id"] != id {
			fail("REQUEST_CONFLICT", "节点维护计划与请求不符", 409)
		}
		return M{"state": "pending"}
	}
	return M{"state": "missing"}
}

func (m NodeMaintenance) Apply(id string, p *ProtocolChange) M { return m.apply(id, p, nil) }

// apply restarts the proxy when both changes are nil. At most one is set.
func (m NodeMaintenance) apply(id string, p *ProtocolChange, c *ConfigChange) M {
	validIdentity(id)
	s := m.Settings
	lock := fileLock(filepath.Join(s.StateDir, "management.lock"), 90*time.Second)
	defer lock()
	execute := fileLock(filepath.Join(s.StateDir, "executor.lock"), 3*time.Second)
	defer execute()
	security := fileLock(filepath.Join(s.StateDir, "security.lock"), 5*time.Second)
	defer security()
	pendingPath := filepath.Join(s.StateDir, "maintenance-pending.json")
	if exists(pendingPath) && object(read(pendingPath))["id"] != id {
		fail("NODE_BUSY", "先恢复原维护任务，再执行其他操作", 409)
	}
	inputHash := digest(p)
	if c != nil {
		inputHash = digest(c)
	}
	changing := p != nil || c != nil
	receiptPath := filepath.Join(s.StateDir, "maintenance-"+id+".json")
	if exists(receiptPath) {
		r := object(read(receiptPath))
		if r["input_hash"] != inputHash {
			fail("REQUEST_CONFLICT", "任务内容与回执不同", 409)
		}
		if exists(pendingPath) {
			check(os.Remove(pendingPath))
		}
		return mapping(r["result"])
	}
	transaction(filepath.Join(s.StateDir, "node.db"), false, func(d *DB) {
		if state := d.Value("SELECT state FROM requests ORDER BY revision DESC LIMIT 1"); state != nil && state != "verified" {
			fail("NODE_BUSY", "先完成或恢复节点授权任务，再维护协议", 409)
		}
	})
	engine := m.Engine(s)
	planPath := filepath.Join(s.StateDir, "maintenance-plan-"+id+".json")
	var plan M
	if exists(planPath) {
		plan = object(read(planPath))
		if plan["input_hash"] != inputHash {
			fail("REQUEST_CONFLICT", "任务内容与恢复计划不同", 409)
		}
	} else {
		rt := engine.Runtime()
		if !rt.Running && changing {
			fail("NODE_NOT_READY", "节点运行绑定异常，请先检查并更新绑定", 409)
		}
		paths := map[string]string{"config": filepath.Join(s.ConfigDir, "config.json"), "template": s.TemplateFile, "settings": m.SettingsPath, "runtime": filepath.Join(s.SafetyDir, "runtime.json"), "guard": filepath.Join(s.SafetyDir, "manifest"), "disabled": filepath.Join(s.StateDir, "disabled-protocols.json")}
		before := M{}
		for key, path := range paths {
			if exists(path) {
				before[key] = string(read(path))
			} else {
				before[key] = ""
			}
		}
		config, template, disabled := object([]byte(str(before["config"]))), object([]byte(str(before["template"]))), protocolState(s)
		next := s
		if p != nil {
			next, config, template, disabled = planProtocol(s, config, template, disabled, *p)
		}
		if c != nil {
			config, template = planConfig(config, template, *c)
		}
		after := object(canonical(before))
		if changing {
			after["config"] = string(append(canonical(config), '\n'))
			after["template"] = string(append(canonical(template), '\n'))
			after["disabled"] = string(canonical(disabled))
			next.TemplateHash = hash([]byte(str(after["template"])))
			next.Validate()
			settings, err := toml.Marshal(next)
			check(err)
			after["settings"] = string(settings)
			runtime := object([]byte(str(before["runtime"])))
			runtime["config_hash"] = hash([]byte(str(after["config"])))
			after["runtime"] = string(canonical(runtime))
			after["guard"] = "allowed\n" + str(runtime["config_hash"]) + "\n" + str(runtime["node_revision"]) + "\n"
		}
		candidate := filepath.Join(s.StateDir, "protocol-candidate.json")
		atomicWrite(candidate, []byte(str(after["config"])), 0600)
		m.Engine(next).Check(candidate)
		plan = M{"input_hash": inputHash, "paths": paths, "before": before, "after": after, "settings": next, "epoch_before": rt.Epoch(), "restart": !changing || hash([]byte(str(before["config"]))) != hash([]byte(str(after["config"])))}
		atomicWrite(planPath, canonical(plan), 0600)
		plan = object(canonical(plan))
	}
	var next NodeSettings
	decode(canonical(plan["settings"]), &next)
	paths, before, after := mapping(plan["paths"]), mapping(plan["before"]), mapping(plan["after"])
	for key, path := range paths {
		actual := ""
		if exists(str(path)) {
			actual = string(read(str(path)))
		}
		if actual != str(before[key]) && actual != str(after[key]) && !(key == "guard" && exists(pendingPath) && actual == "blocked\n\n0\n") {
			fail("PROTOCOL_CHANGED", "维护期间配置被其他操作修改，已停止覆盖，请检查节点", 409)
		}
	}
	var original NodeSettings
	check(toml.Unmarshal([]byte(str(before["settings"])), &original))
	atomicWrite(pendingPath, canonical(M{"id": id}), 0600)
	writeSet := func(files M) {
		for _, key := range []string{"template", "settings", "disabled", "config", "runtime", "guard"} {
			path := str(paths[key])
			data := []byte(str(files[key]))
			if len(data) == 0 {
				if exists(path) {
					check(os.Remove(path))
				}
				continue
			}
			mode := os.FileMode(0600)
			if key == "config" {
				mode = 0640
			}
			if key == "runtime" || key == "guard" {
				mode = 0644
			}
			atomicWrite(path, data, mode)
			if key == "config" && os.Geteuid() == 0 {
				check(os.Chown(path, 0, s.GID))
			}
		}
	}
	err := Attempt(func() {
		changed := str(before["config"]) != str(after["config"])
		if changed {
			NewAgent(s, engine).Guard(false, "", 0)
		}
		writeSet(after)
		updated := m.Engine(next)
		if boolean(plan["restart"]) {
			rt := updated.Runtime()
			if !rt.Running || rt.Epoch() == str(plan["epoch_before"]) {
				updated.Restart()
			}
			updated.Verify(NodeRequest{Users: []Credential{}}, nil)
			if !updated.Runtime().Running {
				fail("START_FAILED", "维护后的运行绑定检查失败", 503)
			}
		}
	})
	if err != nil {
		rollback := Attempt(func() {
			writeSet(before)
			m.Engine(original).Restart()
			if !m.Engine(original).Runtime().Running {
				fail("ROLLBACK_FAILED", "原配置未恢复运行", 503)
			}
		})
		if rollback != nil {
			NewAgent(s, engine).Guard(false, "", 0)
			fail("ROLLBACK_FAILED", "变更与自动回退均未完成，请检查节点后重试原任务", 503)
		}
		check(os.Remove(pendingPath))
		// Retry the same durable plan; it never rebuilds against a changed source.
		fail("MAINTENANCE_ROLLED_BACK", "变更未通过运行检查，原配置已恢复；可检查原因后重试原任务", 409)
	}
	catalog := catalogFor(next, object(read(filepath.Join(next.ConfigDir, "config.json"))), protocolState(next))
	result := M{"node_id": s.NodeID, "ready": true, "catalog": catalog, "template_hash": next.TemplateHash, "endpoints": next.Endpoints, "container": next.Container, "image": next.Image, "config_dir": next.ConfigDir, "engine_build_id": next.EngineBuildID, "backup_id": id, "checked_at": now()}
	result = object(canonical(result))
	atomicWrite(receiptPath, canonical(M{"input_hash": inputHash, "result": result}), 0600)
	check(os.Remove(pendingPath))
	return result
}
