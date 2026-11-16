package sbctl

import (
	"fmt"
	"github.com/google/uuid"
	"github.com/pelletier/go-toml/v2"
	"go.yaml.in/yaml/v3"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func containerName(s string) bool { return match(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`, s) }
func imageReference(s string) bool {
	return match(`^[a-zA-Z0-9][a-zA-Z0-9._:/@-]{0,254}$`, s) && !strings.Contains(s, "..")
}
func validateConfigDir(path string) {
	if path != "/etc/sing-box" && path != "/opt/sing-box" && !strings.HasPrefix(path, "/opt/sbctl/managed/") {
		fail("CONFIG_PATH", "配置目录须为 /etc/sing-box、/opt/sing-box 或 /opt/sbctl/managed 下的目录", 422)
	}
	if filepath.Clean(path) != path || strings.ContainsAny(path, "\r\n\x00") {
		fail("CONFIG_PATH", "配置路径无效", 422)
	}
}

func checkCollectorTools() {
	tool, err := os.Stat("/opt/sbctl/node-tools/grpcurl")
	if err != nil || tool.Mode()&0111 == 0 || !exists("/opt/sbctl/node-tools/stats.proto") {
		fail("STATS_SETUP", "请安装 /opt/sbctl/node-tools/grpcurl 和 stats.proto", 409)
	}
}
func inspectionALPN(v any) []string {
	if value, ok := v.(string); ok {
		return []string{value}
	}
	return stringsOf(v)
}
func inspectNode(container, dir, host, serverName string) M {
	checkCollectorTools()
	validateConfigDir(dir)
	resolved, e := filepath.EvalSymlinks(dir)
	check(e)
	if resolved != dir {
		fail("CONFIG_PATH", "配置目录不能通过软链接指向其他位置")
	}
	composeBytes := read(filepath.Join(dir, "compose.yaml"))
	var compose M
	check(yaml.Unmarshal(composeBytes, &compose))
	service := mapping(mapping(compose["services"])["sing-box"])
	if len(service) == 0 {
		fail("COMPOSE_SERVICE", "Compose 必须包含 sing-box 服务")
	}
	actualName := str(service["container_name"])
	if container == "" {
		container = actualName
	}
	if !containerName(container) || container != actualName {
		fail("CONTAINER_BINDING", "容器名与所选 Compose 服务不一致")
	}
	if !imageReference(str(service["image"])) || service["network_mode"] != "host" {
		fail("COMPOSE_MODE", "当前接管支持 host 网络的 sing-box Compose 服务")
	}
	var objects []M
	decode(runCommand([]string{"/usr/bin/docker", "container", "inspect", container}, nil, 10*time.Second), &objects)
	if len(objects) != 1 {
		fail("CONTAINER_MISSING", "容器不存在")
	}
	o := objects[0]
	cfg := mapping(o["Config"])
	state := mapping(o["State"])
	if str(cfg["Image"]) != str(service["image"]) {
		fail("IMAGE_DRIFT", "Compose 镜像与运行容器不同，请先同步部署")
	}
	imageID := str(o["Image"])
	if !match(`^sha256:[a-f0-9]{64}$`, imageID) {
		fail("IMAGE_INVALID", "无法读取镜像摘要")
	}
	mounted := false
	guardMounted := false
	for _, v := range array(o["Mounts"]) {
		m := mapping(v)
		if m["Source"] == dir && m["Destination"] == "/etc/sing-box" && !boolean(m["RW"]) {
			mounted = true
		}
		if m["Source"] == "/opt/sbctl/data/safety" && m["Destination"] == "/run/sbctl" {
			guardMounted = true
		}
	}
	if !mounted {
		fail("CONFIG_MOUNT", "容器必须将所选配置目录只读挂载为 /etc/sing-box")
	}
	data := read(filepath.Join(dir, "config.json"))
	config := object(data)
	eps := []Endpoint{}
	managed, unmanaged := 0, 0
	primary := map[string]bool{}
	if exists("/opt/sbctl/config/node.toml") {
		for _, e := range LoadNodeSettings().Endpoints {
			primary[e.Tag] = true
		}
	}
	for _, v := range array(config["inbounds"]) {
		i := mapping(v)
		p := str(i["type"])
		if len(primary) > 0 && !primary[str(i["tag"])] {
			continue
		}
		if p != "anytls" && p != "tuic" {
			continue
		}
		tls := mapping(i["tls"])
		alpn := inspectionALPN(tls["alpn"])
		if len(alpn) == 0 {
			alpn = []string{"h3"}
		}
		if host == "" && exists("/opt/sbctl/config/node.toml") {
			old := LoadNodeSettings()
			if len(old.Endpoints) > 0 {
				host = old.Endpoints[0].Server
			}
		}
		ep := Endpoint{p, str(i["tag"]), host, int(integer(i["listen_port"])), str(tls["server_name"]), alpn}
		if ep.SNI == "" && exists("/opt/sbctl/config/node.toml") {
			for _, old := range LoadNodeSettings().Endpoints {
				if old.Protocol == p {
					ep.SNI = old.SNI
				}
			}
		}
		if serverName != "" {
			ep.SNI = serverName
		}
		if ep.SNI == "" {
			ep.SNI = host
		}
		ep.Validate()
		eps = append(eps, ep)
		for _, value := range array(i["users"]) {
			u := mapping(value)
			if strings.HasPrefix(str(u["name"]), "sys_") {
				continue
			}
			if strings.HasPrefix(str(u["name"]), "usr_") {
				managed++
			} else {
				unmanaged++
			}
		}
	}
	if len(eps) != 2 || eps[0].Protocol == eps[1].Protocol {
		fail("ENDPOINTS", "当前接管需要各一个有标签的 AnyTLS 与 TUIC 入口")
	}
	uid, gid := 0, 0
	if _, e := fmt.Sscanf(str(service["user"]), "%d:%d", &uid, &gid); e != nil || uid < 1 || gid < 1 {
		fail("CONTAINER_USER", "Compose 需要明确非 root 的数字 UID:GID")
	}
	version := strings.TrimSpace(string(runCommand([]string{"/usr/bin/docker", "exec", container, "/usr/local/bin/sing-box", "version"}, nil, 10*time.Second)))
	if !strings.Contains(version, "with_v2ray_api") {
		fail("STATS_UNSUPPORTED", "镜像缺少 with_v2ray_api，请先更换具备统计功能的镜像")
	}
	version = strings.Split(version, "\n")[0]
	ready := boolean(state["Running"])
	nodeID := ""
	if exists("/opt/sbctl/config/node.toml") {
		nodeID = LoadNodeSettings().NodeID
	}
	out := M{"container": container, "config_dir": dir, "image": str(service["image"]), "engine_build_id": strings.TrimPrefix(imageID, "sha256:"), "config_hash": hash(data), "compose_hash": hash(composeBytes), "container_id": o["Id"], "running": ready, "endpoints": eps, "uid": uid, "gid": gid, "existing_node_id": nodeID, "managed_credentials": managed, "preserved_users": unmanaged, "guard_current": guardMounted && strings.Join(stringsOf(cfg["Entrypoint"]), " ") == "/bin/sh /run/sbctl/guard.sh", "can_adopt": ready, "version": version, "checked_at": now(), "impact": "保存节点绑定并安装启动守卫；需要时重新创建当前容器，连接会短暂重连。镜像名称与容器名称保持检查结果。"}
	out["server_name"] = serverName
	out["impact"] = "启用全部 sing-box 入口与命名用户统计，缺少标签或用户名称时补充统计标识；保留凭据、端口与路由。必要时重建容器，连接短暂重连；默认保留当前镜像和容器名。"
	out["observation_hash"] = digest(without(out, "checked_at"))
	return out
}

// configureNode commits only an observed deployment. A durable plan allows a
// retry after process/SSH loss without accepting a newly changed external file.
func configureNode(id, requestID string, inspection M, desiredContainer, desiredImage string) M {
	inputHash := digest(M{"inspection": inspection, "container": desiredContainer, "image": desiredImage})
	validIdentity(id)
	validIdentity(requestID)
	root := "/opt/sbctl/data/node"
	if exists(filepath.Join(root, "maintenance-pending.json")) {
		fail("NODE_BUSY", "请先恢复未完成的节点维护任务", 409)
	}
	check(os.MkdirAll(root, 0700))
	unlock := fileLock(filepath.Join(root, "management.lock"), 90*time.Second)
	defer unlock()
	if exists(filepath.Join(root, "maintenance-pending.json")) {
		fail("NODE_BUSY", "请先恢复未完成的节点维护任务", 409)
	}
	receiptPath := filepath.Join(root, "management-"+requestID+".json")
	if exists(receiptPath) {
		r := object(read(receiptPath))
		if r["input_hash"] != inputHash || mapping(r["result"])["node_id"] != id {
			fail("REQUEST_CONFLICT", "接管请求内容冲突", 409)
		}
		return mapping(r["result"])
	}
	executor := fileLock(filepath.Join(root, "executor.lock"), 3*time.Second)
	defer executor()
	security := fileLock(filepath.Join(root, "security.lock"), 5*time.Second)
	defer security()
	planPath := filepath.Join(root, "management-plan-"+requestID+".json")
	var plan M
	if exists(planPath) {
		plan = object(read(planPath))
		if plan["input_hash"] != inputHash || plan["node_id"] != id {
			fail("REQUEST_CONFLICT", "接管计划与请求不符", 409)
		}
	} else {
		plan = prepareManagementPlan(id, requestID, inspection, desiredContainer, desiredImage, inputHash)
		atomicWrite(planPath, canonical(plan), 0600)
	}
	var s NodeSettings
	decode(canonical(plan["settings"]), &s)
	s.Validate()
	var selected []M
	decode(runCommand([]string{"/usr/bin/docker", "image", "inspect", s.Image}, nil, 10*time.Second), &selected)
	if len(selected) != 1 || selected[0]["Id"] != "sha256:"+s.EngineBuildID {
		fail("IMAGE_DRIFT", "镜像标签指向已变化，请重新检查", 409)
	}
	configFile := filepath.Join(s.ConfigDir, "config.json")
	composeFile := filepath.Join(s.ConfigDir, "compose.yaml")
	if !contains([]string{str(plan["config_before_hash"]), hash([]byte(str(plan["config_after"])))}, hash(read(configFile))) || !contains([]string{str(plan["compose_before_hash"]), hash([]byte(str(plan["compose_after"])))}, hash(read(composeFile))) {
		fail("INSPECTION_CHANGED", "配置在接管期间被外部修改，已停止，请重新检查", 409)
	}
	settings, e := toml.Marshal(s)
	check(e)
	path := "/opt/sbctl/config/node.toml"
	if exists(path) && !contains([]string{str(plan["settings_before_hash"]), hash(settings)}, hash(read(path))) {
		fail("INSPECTION_CHANGED", "节点登记在接管期间变化", 409)
	}
	if exists(s.TemplateFile) && !contains([]string{str(plan["template_before_hash"]), hash([]byte(str(plan["template"])))}, hash(read(s.TemplateFile))) {
		fail("INSPECTION_CHANGED", "节点模板在接管期间变化", 409)
	}
	if exists(s.ProbeFile) && hash(read(s.ProbeFile)) != hash([]byte(str(plan["probes"]))) {
		fail("INSPECTION_CHANGED", "探针凭据在接管期间变化", 409)
	}
	candidate := filepath.Join(root, "management-candidate.json")
	atomicWrite(candidate, []byte(str(plan["config_after"])), 0600)
	DockerEngine{s}.Check(candidate)
	composeCandidate := filepath.Join(root, "management-compose.yaml")
	atomicWrite(composeCandidate, []byte(str(plan["compose_after"])), 0600)
	runCommand([]string{"/usr/bin/docker", "compose", "--project-directory", s.ConfigDir, "-f", composeCandidate, "config", "--quiet"}, nil, 15*time.Second)
	a := NewAgent(s, DockerEngine{s})
	if !exists(a.DBPath) {
		a.Initialize()
	}
	atomicWrite(s.ProbeFile, []byte(str(plan["probes"])), 0600)
	atomicWrite(s.TemplateFile, []byte(str(plan["template"])), 0600)
	atomicWrite(path, settings, 0600)
	// Block restart until the new files and binding are durably present.
	a.Guard(false, "", 0)
	atomicWrite(configFile, []byte(str(plan["config_after"])), 0640)
	check(os.Chown(configFile, 0, s.GID))
	atomicWrite(composeFile, []byte(str(plan["compose_after"])), 0644)
	revision := integer(plan["revision"])
	configHash := hash([]byte(str(plan["config_after"])))
	users := stringsOf(plan["users"])
	atomicWrite(filepath.Join(s.SafetyDir, "runtime.json"), canonical(M{"node_revision": revision, "config_hash": configHash, "users": users}), 0644)
	a.Guard(true, configHash, revision)
	engine := DockerEngine{s}
	running := false
	_ = Attempt(func() { running = engine.Runtime().Running })
	if !running {
		engine.Restart()
	}
	rt := engine.Runtime()
	if !rt.Running {
		fail("RUNTIME_BINDING", "运行状态绑定未通过，请重试原任务", 503)
	}
	for _, probe := range a.Probes() {
		if !engine.Probe(probe, true) {
			fail("PROBE_FAILED", "接管后的真实协议探测未通过，请检查公网地址和 TLS 证书域名后重试", 503)
		}
	}
	// Check the non-resetting statistics endpoint before declaring adoption ready.
	runCommand([]string{"/opt/sbctl/node-tools/grpcurl", "-plaintext", "-connect-timeout", "3", "-max-time", "5", "-import-path", "/opt/sbctl/node-tools", "-proto", "stats.proto", "-d", `{"reset":false}`, "127.0.0.1:10085", "v2ray.core.app.stats.command.StatsService/QueryStats"}, nil, 10*time.Second)
	// The collector's ID is local state, never a remote command argument.
	atomicWrite("/opt/sbctl/config/collector.env", []byte("SBCTL_NODE_ID="+id+"\n"), 0644)
	if !exists("/opt/sbctl/data/ledger/ledger.db") {
		runCommand([]string{"/usr/sbin/runuser", "-u", "sbctl-stats", "--", "/opt/sbctl/bin/sbctl-node", "collect", "--initialize-ledger", id}, nil, 10*time.Second)
	}
	runCommand([]string{"/usr/bin/systemctl", "start", "sbctl-node"}, nil, 10*time.Second)
	runCommand([]string{"/usr/bin/systemctl", "restart", "sbctl-collector"}, nil, 10*time.Second)
	runCommand([]string{"/usr/bin/systemctl", "enable", "sbctl-node", "sbctl-collector"}, nil, 10*time.Second)
	result := M{"node_id": id, "container": s.Container, "image": s.Image, "engine_build_id": s.EngineBuildID, "config_dir": s.ConfigDir, "template_hash": s.TemplateHash, "endpoints": s.Endpoints, "runtime": rt, "checked_at": now(), "ready": true}
	result["version"] = strings.Split(strings.TrimSpace(string(runCommand([]string{"/usr/bin/docker", "exec", s.Container, "/usr/local/bin/sing-box", "version"}, nil, 10*time.Second))), "\n")[0]
	atomicWrite(receiptPath, canonical(M{"input_hash": inputHash, "result": result}), 0600)
	return result
}
func prepareManagementPlan(id, requestID string, inspection M, desiredContainer, desiredImage, inputHash string) M {
	var eps []Endpoint
	decode(canonical(inspection["endpoints"]), &eps)
	if len(eps) != 2 {
		fail("INSPECTION_INVALID", "入口检查数据无效")
	}
	actual := inspectNode(str(inspection["container"]), str(inspection["config_dir"]), eps[0].Server, str(inspection["server_name"]))
	if actual["observation_hash"] != inspection["observation_hash"] {
		fail("INSPECTION_CHANGED", "节点运行状态或配置已变化，请重新检查", 409)
	}
	if !boolean(actual["can_adopt"]) {
		fail("NODE_NOT_READY", "节点尚未运行")
	}
	path := "/opt/sbctl/config/node.toml"
	existing := exists(path)
	root := "/opt/sbctl/data/node"
	s := NodeSettings{NodeID: id, StateDir: root, SafetyDir: "/opt/sbctl/data/safety", TemplateFile: "/opt/sbctl/config/template.json", ProbeFile: "/opt/sbctl/config/probe-credentials.json", ProbePort: 31840, ProbeUDPPort: 53190, RevokeFailure: "preserve"}
	settingsHash, templateHash := "", ""
	if existing {
		s = LoadNodeSettings()
		if s.NodeID != id {
			fail("NODE_IDENTITY", "该代理已属于其他节点 ID，不能覆盖", 409)
		}
		settingsHash = hash(read(path))
		templateHash = hash(read(s.TemplateFile))
	} else if integer(actual["managed_credentials"]) > 0 {
		fail("EXISTING_MANAGED_USERS", "发现其他主控格式的用户，请先完成迁移处理", 409)
	}
	s.Container = str(actual["container"])
	s.Image = str(actual["image"])
	s.ConfigDir = str(actual["config_dir"])
	s.EngineBuildID = str(actual["engine_build_id"])
	s.UID = int(integer(actual["uid"]))
	s.GID = int(integer(actual["gid"]))
	s.Endpoints = eps
	if desiredContainer != "" && desiredContainer != s.Container {
		if !containerName(desiredContainer) {
			fail("CONTAINER_INVALID", "目标容器名称无效")
		}
		listed := strings.TrimSpace(string(runCommand([]string{"/usr/bin/docker", "ps", "-a", "--format", "{{.Names}}"}, nil, 10*time.Second)))
		if contains(strings.Split(listed, "\n"), desiredContainer) {
			fail("CONTAINER_EXISTS", "目标容器名称已被占用", 409)
		}
		s.Container = desiredContainer
	}
	if desiredImage != "" && desiredImage != s.Image {
		if !imageReference(desiredImage) {
			fail("IMAGE_INVALID", "目标镜像名称无效")
		}
		var images []M
		if err := Attempt(func() {
			decode(runCommand([]string{"/usr/bin/docker", "image", "inspect", desiredImage}, nil, 10*time.Second), &images)
		}); err != nil {
			runCommand([]string{"/usr/bin/docker", "pull", desiredImage}, nil, 90*time.Second)
			decode(runCommand([]string{"/usr/bin/docker", "image", "inspect", desiredImage}, nil, 10*time.Second), &images)
		}
		if len(images) != 1 || !match(`^sha256:[a-f0-9]{64}$`, str(images[0]["Id"])) {
			fail("IMAGE_INVALID", "无法识别目标镜像")
		}
		version := string(runCommand([]string{"/usr/bin/docker", "run", "--rm", "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--user", fmt.Sprintf("%d:%d", s.UID, s.GID), "--entrypoint", "/usr/local/bin/sing-box", str(images[0]["Id"]), "version"}, nil, 15*time.Second))
		if !strings.Contains(version, "with_v2ray_api") {
			fail("STATS_UNSUPPORTED", "目标镜像缺少 with_v2ray_api")
		}
		s.Image, s.EngineBuildID = desiredImage, strings.TrimPrefix(str(images[0]["Id"]), "sha256:")
	}
	known := map[string]bool{}
	revision := int64(0)
	if existing {
		transaction(filepath.Join(root, "node.db"), false, func(d *DB) {
			row := d.One("SELECT document,revision,state FROM requests ORDER BY revision DESC LIMIT 1")
			if row != nil {
				if row["state"] != "verified" {
					fail("NODE_BUSY", "请先恢复未验证的授权任务，再更新节点绑定", 409)
				}
				revision = integer(row["revision"])
				r := parseRequest(object([]byte(str(row["document"]))))
				for _, c := range r.Users {
					known[c.UserID] = true
				}
			}
		})
	}
	oldConfig := read(filepath.Join(s.ConfigDir, "config.json"))
	config := object(oldConfig)
	probes := []Credential{}
	var probeBytes []byte
	if exists(s.ProbeFile) {
		probeBytes = read(s.ProbeFile)
		decode(probeBytes, &probes)
	} else {
		for _, p := range []string{"anytls", "tuic"} {
			var uid *string
			if p == "tuic" {
				u := uuid.NewString()
				uid = &u
			}
			probes = append(probes, Credential{"sys_probe", p, "sys_" + p, 1, token(32), uid})
		}
		probeBytes = canonical(probes)
	}
	if !existing {
		for _, v := range array(config["inbounds"]) {
			in := mapping(v)
			if in["type"] == "anytls" || in["type"] == "tuic" {
				for _, u := range array(in["users"]) {
					if mapping(u)["name"] == "sys_probe" {
						fail("PROBE_IDENTITY", "现有 sys_probe 名称被保留，请先处理冲突再接管", 409)
					}
				}
			}
		}
	}
	for _, v := range array(config["inbounds"]) {
		in := mapping(v)
		bound := false
		for _, e := range s.Endpoints {
			bound = bound || e.Tag == str(in["tag"])
		}
		if !bound {
			continue
		}
		if in["type"] != "anytls" && in["type"] != "tuic" {
			continue
		}
		preserved := []any{}
		for _, u := range array(in["users"]) {
			name := str(mapping(u)["name"])
			if name != "sys_probe" {
				preserved = append(preserved, u)
			}
		}
		for _, c := range probes {
			if c.Protocol == str(in["type"]) {
				u := M{"name": c.UserID, "password": c.Password}
				if c.UUID != nil {
					u["uuid"] = *c.UUID
				}
				preserved = append(preserved, u)
			}
		}
		in["users"] = preserved
	}
	users := []string{}
	for u := range known {
		users = append(users, u)
	}
	configureStatistics(config)
	afterConfig := append(canonical(config), '\n')
	template := object(afterConfig)
	for _, v := range array(template["inbounds"]) {
		in := mapping(v)
		bound := false
		for _, e := range s.Endpoints {
			bound = bound || e.Tag == str(in["tag"])
		}
		if !bound {
			continue
		}
		if in["type"] != "anytls" && in["type"] != "tuic" {
			continue
		}
		fixed := []any{}
		for _, u := range array(in["users"]) {
			name := str(mapping(u)["name"])
			if !known[name] && name != "sys_probe" {
				fixed = append(fixed, u)
			}
		}
		in["users"] = fixed
	}
	templateBytes := append(canonical(template), '\n')
	s.TemplateHash = hash(templateBytes)
	s.Validate()
	original := read(filepath.Join(s.ConfigDir, "compose.yaml"))
	var compose M
	check(yaml.Unmarshal(original, &compose))
	service := mapping(mapping(compose["services"])["sing-box"])
	service["container_name"] = s.Container
	service["image"] = s.Image
	service["entrypoint"] = []string{"/bin/sh", "/run/sbctl/guard.sh"}
	volumes := []any{}
	for _, v := range array(service["volumes"]) {
		if text, ok := v.(string); ok && strings.Contains(text, ":/run/sbctl:") {
			continue
		}
		if mapping(v)["target"] == "/run/sbctl" {
			continue
		}
		volumes = append(volumes, v)
	}
	service["volumes"] = append(volumes, s.SafetyDir+":/run/sbctl:ro")
	composeAfter, e := yaml.Marshal(compose)
	check(e)
	backup := filepath.Join("/opt/sbctl/backups/management", requestID)
	atomicWrite(filepath.Join(backup, "config.before.json"), oldConfig, 0600)
	atomicWrite(filepath.Join(backup, "compose.before.yaml"), original, 0600)
	if existing {
		atomicWrite(filepath.Join(backup, "node.before.toml"), read(path), 0600)
		atomicWrite(filepath.Join(backup, "template.before.json"), read(s.TemplateFile), 0600)
	}
	return M{"node_id": id, "input_hash": inputHash, "settings": s, "settings_before_hash": settingsHash, "template_before_hash": templateHash, "config_before_hash": hash(oldConfig), "config_after": string(afterConfig), "compose_before_hash": hash(original), "compose_after": string(composeAfter), "template": string(templateBytes), "probes": string(probeBytes), "revision": revision, "users": unique(users)}
}
func diagnoseNode() M {
	s := LoadNodeSettings()
	result := M{"node_id": s.NodeID, "container": s.Container, "image": s.Image, "engine_build_id": s.EngineBuildID, "config_dir": s.ConfigDir, "checked_at": now(), "ready": false, "checks": []M{}}
	checks := []M{}
	err := Attempt(func() {
		actual := inspectNode(s.Container, s.ConfigDir, s.Endpoints[0].Server, "")
		result["observed"] = actual
		checks = append(checks, M{"name": "SSH 与容器", "ok": true, "message": "固定命令可达，容器运行信息已读取"})
		rt := DockerEngine{s}.Runtime()
		result["runtime"] = rt
		checks = append(checks, M{"name": "运行绑定", "ok": rt.Running, "message": map[bool]string{true: "容器、配置和启动守卫一致", false: "容器、镜像或启动守卫与登记不一致，请重新检查并确认绑定"}[rt.Running]})
		result["ready"] = rt.Running
	})
	if err != nil {
		f := PublicFault(err)
		checks = append(checks, M{"name": "节点检查", "ok": false, "message": f.Message, "code": f.Code})
	}
	result["checks"] = checks
	return result
}
func NodeRPC(v M) M {
	switch str(v["action"]) {
	case "maintenance_receipt":
		if len(v) != 3 {
			fail("ACTION_REJECTED", "维护回执查询参数无效")
		}
		m := NodeMaintenance{LoadNodeSettings(), "/opt/sbctl/config/node.toml", nil}
		return m.ReceiptStatus(str(v["request_id"]), str(v["input_hash"]))
	case "protocols":
		if len(v) != 1 {
			fail("ACTION_REJECTED", "协议读取参数无效")
		}
		return object(canonical(readProtocolCatalog()))
	case "config":
		if len(v) != 1 {
			fail("ACTION_REJECTED", "配置读取参数无效")
		}
		s := LoadNodeSettings()
		unlock := fileLock(filepath.Join(s.StateDir, "management.lock"), 20*time.Second)
		defer unlock()
		return configView(s.NodeID, object(read(filepath.Join(s.ConfigDir, "config.json"))))
	case "config_change":
		if len(v) != 3 {
			fail("ACTION_REJECTED", "配置变更参数无效")
		}
		var change ConfigChange
		decode(canonical(v["change"]), &change)
		return (NodeMaintenance{LoadNodeSettings(), "/opt/sbctl/config/node.toml", func(s NodeSettings) Engine { return DockerEngine{s} }}).ApplyConfig(str(v["request_id"]), change)
	case "protocol_change", "restart":
		kind := str(v["action"])
		var change *ProtocolChange
		if kind == "protocol_change" {
			if len(v) != 3 {
				fail("ACTION_REJECTED", "协议变更参数无效")
			}
			change = &ProtocolChange{}
			decode(canonical(v["change"]), change)
			change.Validate()
		} else if len(v) != 2 {
			fail("ACTION_REJECTED", "重启参数无效")
		}
		return (NodeMaintenance{LoadNodeSettings(), "/opt/sbctl/config/node.toml", func(s NodeSettings) Engine { return DockerEngine{s} }}).Apply(str(v["request_id"]), change)
	case "inspect":
		if len(v) != 5 {
			fail("ACTION_REJECTED", "检查参数无效")
		}
		return inspectNode(str(v["container"]), str(v["config_dir"]), str(v["public_host"]), str(v["server_name"]))
	case "configure":
		if len(v) != 6 {
			fail("ACTION_REJECTED", "配置参数无效")
		}
		return configureNode(str(v["node_id"]), str(v["request_id"]), mapping(v["inspection"]), str(v["desired_container"]), str(v["desired_image"]))
	case "diagnose":
		if len(v) != 1 {
			fail("ACTION_REJECTED", "诊断参数无效")
		}
		return diagnoseNode()
	case "status", "submit":
		unlock := fileLock("/opt/sbctl/data/node/management.lock", 120*time.Second)
		defer unlock()
		s := LoadNodeSettings()
		if exists(filepath.Join(s.StateDir, "maintenance-pending.json")) {
			fail("NODE_BUSY", "节点维护恢复中，授权任务稍后重试", 409)
		}
		a := NewAgent(s, DockerEngine{s})
		if len(v) == 1 && v["action"] == "status" {
			return a.Status()
		}
		if len(v) == 2 && v["action"] == "submit" {
			return M{"node_id": s.NodeID, "latest": a.Accept(parseRequest(v["request"]))}
		}
	}
	fail("ACTION_REJECTED", "节点动作或参数无效")
	return nil
}
