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

// PrepareNode is a maintenance command, unavailable through the SSH RPC surface.
// Existing managed users are exported only when capture_existing is explicit.
func PrepareNode(input M) {
	if os.Geteuid() != 0 {
		fail("ROOT_REQUIRED", "节点准备需要 root")
	}
	destination := str(input["destination"])
	if destination == "" {
		destination = "/opt/sbctl/config/node.toml"
	}
	if exists(destination) {
		fail("CONFIG_EXISTS", "节点配置已存在，拒绝覆盖")
	}
	s := NodeSettings{StateDir: "/opt/sbctl/data/node", SafetyDir: "/opt/sbctl/data/safety", ConfigDir: "/etc/sing-box", TemplateFile: "/opt/sbctl/config/template.json", ProbeFile: "/opt/sbctl/config/probe-credentials.json", ProbePort: 31840, ProbeUDPPort: 53190}
	decode(canonical(input["settings"]), &s)
	s.TemplateHash = strings.Repeat("0", 64)
	s.Validate()
	if exists(s.TemplateFile) || exists(s.ProbeFile) || exists(filepath.Join(s.StateDir, "node.db")) {
		fail("NODE_EXISTS", "节点目标文件已存在，拒绝覆盖")
	}
	config := object(read(filepath.Join(s.ConfigDir, "config.json")))
	existing := map[string]M{}
	found := map[string]bool{}
	for _, value := range array(config["inbounds"]) {
		i := mapping(value)
		for _, ep := range s.Endpoints {
			if str(i["tag"]) != ep.Tag {
				continue
			}
			if found[ep.Tag] || i["type"] != ep.Protocol || integer(i["listen_port"]) != int64(ep.Port) {
				fail("NODE_BINDING", "实际入口与登记元数据不匹配")
			}
			found[ep.Tag] = true
			for _, user := range array(i["users"]) {
				if !boolean(input["capture_existing"]) {
					continue
				}
				u := mapping(user)
				name := str(u["name"])
				if strings.HasPrefix(name, "sys_") {
					continue
				}
				if name == "" {
					fail("IMPORT_IDENTITY", "现有入口包含未命名用户")
				}
				if existing[name] == nil {
					existing[name] = M{}
				}
				if existing[name][ep.Protocol] != nil {
					fail("IMPORT_CONFLICT", "现有用户有重复协议凭据，需人工确认")
				}
				existing[name][ep.Protocol] = M{"password": u["password"], "uuid": u["uuid"]}
			}
			i["users"] = []M{}
		}
	}
	if len(found) != 2 {
		fail("NODE_BINDING", "未找到两个受管入口")
	}
	template := append(canonical(config), '\n')
	s.TemplateHash = hash(template)
	probes := []Credential{}
	for _, p := range []string{"anytls", "tuic"} {
		var id *string
		if p == "tuic" {
			u := uuid.NewString()
			id = &u
		}
		probes = append(probes, Credential{"sys_probe", p, "sys_" + p, 1, token(32), id})
	}
	atomicWrite(s.TemplateFile, template, 0600)
	atomicWrite(s.ProbeFile, canonical(probes), 0600)
	settingsData, e := toml.Marshal(s)
	check(e)
	atomicWrite(destination, settingsData, 0600)
	a := NewAgent(s, DockerEngine{s})
	a.Initialize()
	imports := []M{}
	names := []string{}
	for name := range existing {
		names = append(names, name)
	}
	for _, name := range unique(names) {
		imports = append(imports, M{"display_name": fmt.Sprintf("已有用户 %d", len(imports)+1), "credentials": M{s.NodeID: existing[name]}})
	}
	if boolean(input["capture_existing"]) {
		atomicWrite(filepath.Join(s.StateDir, "import-existing.json"), canonical(imports), 0600)
	}
	metadata := M{"id": s.NodeID, "name": input["name"], "region": input["region"], "location": input["location"], "connection": input["connection"], "endpoints": s.Endpoints, "template_hash": s.TemplateHash, "image_digest": "sha256:" + s.EngineBuildID, "group_id": input["group_id"], "group_name": input["group_name"]}
	atomicWrite(filepath.Join(s.StateDir, "registration.json"), canonical(metadata), 0644)
	emit(M{"prepared": true, "existing_users": len(imports), "node_id": s.NodeID})
}
func InstallGuard(s NodeSettings) {
	installGuardQuiet(s)
	fmt.Println("启动守卫配置已安装，等待发布请求应用")
}
func installGuardQuiet(s NodeSettings) {
	if os.Geteuid() != 0 {
		fail("ROOT_REQUIRED", "安装启动守卫需要 root")
	}
	path := filepath.Join(s.ConfigDir, "compose.yaml")
	original := read(path)
	var compose M
	check(yaml.Unmarshal(original, &compose))
	service := mapping(mapping(compose["services"])["sing-box"])
	if service["image"] != s.Image || service["network_mode"] != "host" || service["container_name"] != s.Container {
		fail("NODE_BINDING", "Compose 与节点登记不一致")
	}
	service["entrypoint"] = []string{"/bin/sh", "/run/sbctl/guard.sh"}
	service["command"] = []string{"-D", "/var/lib/sing-box", "-c", "/etc/sing-box/config.json", "run"}
	vols := []any{}
	for _, v := range array(service["volumes"]) {
		if text, ok := v.(string); ok && strings.Contains(text, ":/run/sbctl:") {
			continue
		}
		if mapping(v)["target"] == "/run/sbctl" {
			continue
		}
		vols = append(vols, v)
	}
	service["volumes"] = append(vols, s.SafetyDir+":/run/sbctl:ro")
	out, e := yaml.Marshal(compose)
	check(e)
	candidate := filepath.Join(s.StateDir, "compose.pending.yaml")
	atomicWrite(candidate, out, 0644)
	runCommand([]string{"/usr/bin/docker", "compose", "--project-directory", s.ConfigDir, "-f", candidate, "config", "--quiet"}, nil, 15*time.Second)
	backup := filepath.Join("/opt/sbctl/backups", s.NodeID, time.Now().UTC().Format("20060102T150405Z"))
	atomicWrite(filepath.Join(backup, "compose.before-go.yaml"), original, 0600)
	atomicWrite(filepath.Join(backup, "config.before-go.json"), read(filepath.Join(s.ConfigDir, "config.json")), 0600)
	atomicWrite(path, out, 0644)
}
