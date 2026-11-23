package sbctl

import (
	"fmt"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type NodeDraft struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Region     string         `json:"region"`
	Location   string         `json:"location"`
	GroupID    string         `json:"group_id"`
	GroupName  string         `json:"group_name"`
	Connection NodeConnection `json:"connection"`
	Container  string         `json:"container"`
	ConfigDir  string         `json:"config_dir"`
	PublicHost string         `json:"public_host"`
	ServerName string         `json:"server_name,omitempty"`
}
type NodeJobInput struct {
	Kind             string          `json:"kind"`
	Draft            NodeDraft       `json:"draft"`
	InspectionID     string          `json:"inspection_id"`
	ExpectedVersion  int64           `json:"expected_version"`
	DesiredContainer string          `json:"desired_container,omitempty"`
	DesiredImage     string          `json:"desired_image,omitempty"`
	Change           *ProtocolChange `json:"protocol_change,omitempty"`
	ConfigChange     *ConfigChange   `json:"config_change,omitempty"`
	Billing          *NodeBilling    `json:"billing,omitempty"`
}

func (d *NodeDraft) Validate() {
	d.Connection = parseConnection(d.Connection)
	if d.ConfigDir == "" {
		d.ConfigDir = "/etc/sing-box"
	}
	validateConfigDir(d.ConfigDir)
	if d.Container != "" && !containerName(d.Container) {
		fail("INVALID_REQUEST", "容器名格式无效", 422)
	}
	if d.PublicHost != "" && !match(`^[a-zA-Z0-9.:-]{1,253}$`, d.PublicHost) {
		fail("INVALID_REQUEST", "公网地址格式无效", 422)
	}
	if d.ServerName != "" && !match(`^[a-zA-Z0-9.-]{1,253}$`, d.ServerName) {
		fail("INVALID_REQUEST", "TLS 证书域名格式无效", 422)
	}
}
func validateNodeLabels(d NodeDraft) {
	validIdentity(d.ID)
	validIdentity(d.GroupID)
	for _, s := range []string{d.Name, d.Region, d.Location, d.GroupName} {
		if strings.TrimSpace(s) == "" || utf8.RuneCountInString(s) > 80 {
			fail("INVALID_REQUEST", "节点名称、地区与分组名称必填，最多 80 字", 422)
		}
		for _, r := range s {
			if r < 32 || r == 127 {
				fail("INVALID_REQUEST", "名称包含控制字符", 422)
			}
		}
	}
}
func (c *Control) NodeJob(id string) M {
	var r M
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		r = d.One("SELECT id,kind,node_id,state,result_json,error_code,message,created_at,updated_at FROM node_jobs WHERE id=?", id)
	})
	if r == nil {
		fail("NOT_FOUND", "节点任务不存在", 404)
	}
	var result any
	if r["result_json"] != nil {
		result = object([]byte(str(r["result_json"])))
	}
	delete(r, "result_json")
	r["result"] = result
	return r
}
func (c *Control) SubmitNodeJob(actor, key string, input NodeJobInput) M {
	input.Draft.Validate()
	if (input.DesiredContainer != "" && !containerName(input.DesiredContainer)) || (input.DesiredImage != "" && !imageReference(input.DesiredImage)) {
		fail("INVALID_REQUEST", "目标容器名或镜像名称格式无效", 422)
	}
	if !contains([]string{"inspect", "configure", "check", "verify_inbounds", "protocols", "protocol_change", "config", "config_change", "restart"}, input.Kind) {
		fail("INVALID_REQUEST", "未知节点操作", 422)
	}
	if input.Kind != "inspect" {
		validateNodeLabels(input.Draft)
	}
	if input.Kind == "protocol_change" {
		if input.Change == nil {
			fail("INVALID_REQUEST", "缺少协议变更", 422)
		}
		input.Change.Validate()
		if input.Change.RetireID != "" || input.Change.RevokedAtRevision != 0 || input.Change.TemplateHash != "" {
			fail("INVALID_REQUEST", "退役回执参数由主控生成", 422)
		}
	} else if input.Change != nil {
		fail("INVALID_REQUEST", "该操作不接受协议变更", 422)
	}
	if input.Kind == "config_change" {
		if input.ConfigChange == nil {
			fail("INVALID_REQUEST", "缺少配置变更", 422)
		}
		input.ConfigChange.Validate()
	} else if input.ConfigChange != nil {
		fail("INVALID_REQUEST", "该操作不接受配置变更", 422)
	}
	var out M
	affected := []string{}
	path := "/node-jobs"
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		if out = c.replay(d, actor, key, path, input); out != nil {
			return
		}
		if integer(d.Value("SELECT count(*) FROM node_jobs WHERE state IN ('queued','running')")) >= 20 {
			fail("QUEUE_BUSY", "节点检查队列繁忙，请稍后重试", 429)
		}
		if mutatingNodeJob(input.Kind) && d.One("SELECT 1 FROM inbound_retirements r JOIN node_jobs j ON j.id=r.job_id WHERE j.node_id=? AND j.state='failed' LIMIT 1", input.Draft.ID) != nil {
			fail("RETIREMENT_PENDING", "请先从节点管理任务重试未完成的入口退役", 409)
		}
		if input.Kind == "configure" {
			checkGroupChange(d, input.Draft.ID, input.Draft.GroupID)
			i := d.One("SELECT * FROM node_jobs WHERE id=? AND kind='inspect' AND state='completed'", input.InspectionID)
			if i == nil || time.Since(stamp(str(i["updated_at"]))) > 15*time.Minute {
				fail("INSPECTION_EXPIRED", "请重新检查节点后确认", 409)
			}
			old := decodeNodeInput(c, str(i["input_json"]))
			if digest(old.Draft.Connection) != digest(input.Draft.Connection) || old.Draft.Container != input.Draft.Container || old.Draft.ConfigDir != input.Draft.ConfigDir || old.Draft.PublicHost != input.Draft.PublicHost || old.Draft.ServerName != input.Draft.ServerName {
				fail("INSPECTION_CHANGED", "连接或容器参数已变化，请重新检查", 409)
			}
			n := d.One("SELECT n.id,COALESCE(m.version,0) AS version FROM nodes n LEFT JOIN node_management m ON n.id=m.node_id WHERE n.id=?", input.Draft.ID)
			if n != nil && integer(n["version"]) != input.ExpectedVersion {
				fail("STALE_REVISION", "节点配置已变化，请重新打开编辑", 409)
			}
			if n == nil && input.ExpectedVersion != 0 {
				fail("STALE_REVISION", "节点登记状态已变化", 409)
			}
			if other := d.One("SELECT id FROM nodes WHERE management_address=? AND id<>?", input.Draft.Connection.Address, input.Draft.ID); other != nil {
				fail("NODE_EXISTS", "该主机已经登记，请编辑现有节点", 409)
			}
			if integer(d.Value("SELECT count(*) FROM outbox WHERE node_id=? AND state<>'done'", input.Draft.ID)) > 0 {
				fail("NODE_BUSY", "节点仍有授权任务，请等待处理完成", 409)
			}
			if integer(d.Value("SELECT count(*) FROM node_jobs WHERE (node_id=? OR json_extract(input_json,'$.draft.connection.address')=?) AND kind IN ('configure','protocol_change','config_change','restart') AND state IN ('queued','running')", input.Draft.ID, input.Draft.Connection.Address)) > 0 {
				fail("NODE_BUSY", "节点配置任务正在执行", 409)
			}
		}
		if contains([]string{"protocol_change", "config_change", "restart"}, input.Kind) {
			node := d.One("SELECT COALESCE(m.version,0) AS version FROM nodes n LEFT JOIN node_management m ON n.id=m.node_id WHERE n.id=?", input.Draft.ID)
			if node == nil {
				fail("NOT_FOUND", "节点不存在", 404)
			}
			if integer(node["version"]) != input.ExpectedVersion {
				fail("STALE_REVISION", "节点管理版本已变化，请重新读取", 409)
			}
			if integer(d.Value("SELECT count(*) FROM node_jobs WHERE node_id=? AND kind IN ('configure','protocol_change','config_change','restart') AND state IN ('queued','running')", input.Draft.ID)) > 0 || integer(d.Value("SELECT count(*) FROM outbox WHERE node_id=? AND state<>'done'", input.Draft.ID)) > 0 {
				fail("NODE_BUSY", "节点仍有配置或授权任务，请完成后操作", 409)
			}
		}
		if contains([]string{"check", "verify_inbounds", "protocols", "protocol_change", "config", "config_change", "restart"}, input.Kind) {
			node := d.One("SELECT connection_json FROM nodes WHERE id=?", input.Draft.ID)
			if node == nil {
				fail("NOT_FOUND", "节点不存在", 404)
			}
			if digest(parseConnection(object([]byte(str(node["connection_json"]))))) != digest(input.Draft.Connection) {
				fail("STALE_REVISION", "节点连接信息已变化，请刷新", 409)
			}
		}
		id := identifier("njob")
		d.Exec("INSERT INTO node_jobs(id,kind,node_id,state,input_json,created_at,updated_at) VALUES (?,?,?,'queued',?,?,?)", id, input.Kind, input.Draft.ID, encodeNodeInput(c, input), now(), now())
		if input.Change != nil && contains([]string{"disable", "delete"}, input.Change.Action) {
			affected = beginInboundRetirement(d, id, input.Draft.ID, *input.Change)
		}
		out = M{"job_id": id}
		c.remember(d, actor, key, path, input, out, 202, false)
		audit(d, actor, "node_"+input.Kind, input.Draft.ID, "提交节点管理任务")
	})
	for _, user := range affected {
		c.RefreshSubscription(user)
	}
	return out
}
func (c *Control) NodeConfig(id string) M {
	var row M
	billingCfg := defaultNodeBilling()
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		row = d.One("SELECT n.*,m.version AS config_version,m.binding_json,m.checked_at,m.check_json FROM nodes n LEFT JOIN node_management m ON n.id=m.node_id WHERE n.id=?", id)
		if row == nil {
			fail("NOT_FOUND", "节点不存在", 404)
		}
		g := d.One("SELECT g.id,g.name FROM groups g JOIN group_nodes gn ON gn.group_id=g.id WHERE gn.node_id=? ORDER BY g.id LIMIT 1", id)
		row["group_id"] = g["id"]
		row["group_name"] = g["name"]
		if bm, ok := loadNodeBillingMap(d)[id]; ok {
			billingCfg = bm
		}
	})
	binding := M{}
	if row["binding_json"] != nil {
		binding = object([]byte(str(row["binding_json"])))
	}
	connection := parseConnection(object([]byte(str(row["connection_json"]))))
	draft := NodeDraft{ID: str(row["id"]), Name: str(row["name"]), Region: str(row["region"]), Location: str(row["location"]), GroupID: str(row["group_id"]), GroupName: str(row["group_name"]), Connection: connection, Container: str(binding["container"]), ConfigDir: str(binding["config_dir"]), PublicHost: str(row["public_host"])}
	if draft.ConfigDir == "" {
		draft.ConfigDir = "/etc/sing-box"
	}
	var last any
	if row["check_json"] != nil {
		last = object([]byte(str(row["check_json"])))
	}
	var catalog any
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		raw := d.Value("SELECT catalog_json FROM node_catalog WHERE node_id=?", id)
		if raw != nil {
			view := object([]byte(str(raw)))
			for _, value := range array(view["items"]) {
				item := mapping(value)
				if boolean(item["managed"]) {
					item["probe_state"] = "not_run"
					if probeVerified(d, row) {
						item["probe_state"] = "passed"
					}
				}
				if retiring := d.One("SELECT j.created_at,t.state,t.message FROM inbound_retirements r JOIN node_jobs j ON j.id=r.job_id JOIN operation_targets t ON t.operation_id=r.operation_id WHERE j.node_id=? AND r.inbound_id=? AND j.state<>'completed' ORDER BY j.created_at DESC LIMIT 1", id, item["inbound_id"]); retiring != nil {
					item["retirement"] = retiring
				}
			}
			catalog = view
		}
	})
	return M{"protocol_catalog": catalog, "draft": draft, "billing": billingCfg, "version": integer(row["config_version"]), "binding": binding, "checked_at": row["checked_at"], "last_check": last}
}

func renderSingleContainerCompose(controlURL, nodeID, code string, draft NodeDraft) string {
	container := draft.Container
	if container == "" {
		container = "sing-box"
	}
	return fmt.Sprintf(`services:
  sing-box:
    image: ${SING_BOX_IMAGE:?set an image with the embedded sbctl Agent}
    container_name: %s
    network_mode: host
    restart: unless-stopped
    environment:
      SBCTL_CONTROL_URL: %s
      SBCTL_NODE_ID: %s
      SBCTL_REGISTRATION_CODE: %s
    volumes:
      - type: bind
        source: %s
        target: /etc/sing-box
      - /var/lib/sing-box/sbctl:/var/lib/sing-box/sbctl
    command: ["-c", "/etc/sing-box/config.json", "run"]
`, strconv.Quote(container), strconv.Quote(controlURL), strconv.Quote(nodeID), strconv.Quote(code), strconv.Quote(strings.ReplaceAll(draft.ConfigDir, "$", "$$")))
}

func (c *Control) EnrollNode(actor, key string, draft NodeDraft, billing *NodeBilling, controlURL string) M {
	draft.Validate()
	validateNodeLabels(draft)
	if billing != nil {
		billing.NormalizeAndValidate()
	}
	code := token(32)
	nowUnix := timestamp()
	path := "/nodes/enroll"
	baseURL := strings.TrimRight(controlURL, "/")
	if baseURL == "" && len(c.Settings.AllowedOrigins) > 0 {
		baseURL = strings.TrimRight(c.Settings.AllowedOrigins[0], "/")
	}
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || !match(`^[A-Za-z0-9.:-]+$`, u.Hostname()) || u.Path != "" || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" {
		fail("INVALID_REQUEST", "请填写节点可访问的 HTTP 或 HTTPS 主控地址", 422)
	}
	reqBody := M{"draft": draft, "billing": billing, "control_url": baseURL}
	var out M
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		if out = c.replay(d, actor, key, path, reqBody); out != nil {
			return
		}
		if other := d.One("SELECT id FROM nodes WHERE management_address=? AND id<>?", draft.Connection.Address, draft.ID); other != nil {
			fail("NODE_EXISTS", "该 Tailscale 地址已绑定其他节点", 409)
		}
		existing := d.One("SELECT id FROM nodes WHERE id=?", draft.ID)
		if existing != nil {
			fail("NODE_EXISTS", "该节点 ID 已登记，请在节点详情生成新注册码", 409)
		}
		zeros := strings.Repeat("0", 64)
		d.Exec("INSERT INTO nodes(id,name,region,location,management_address,public_host,connection_json,endpoints_json,template_hash,image_digest,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
			draft.ID, draft.Name, draft.Region, draft.Location, draft.Connection.Address, draft.PublicHost, string(canonical(draft.Connection)), "[]", zeros, "sha256:"+zeros, now())
		d.Exec("INSERT OR IGNORE INTO groups VALUES (?,?)", draft.GroupID, draft.GroupName)
		d.Exec("INSERT INTO group_nodes VALUES (?,?)", draft.GroupID, draft.ID)
		d.Exec("INSERT INTO node_management(node_id,version,binding_json) VALUES (?,0,'{}')", draft.ID)
		if billing != nil {
			saveNodeBilling(d, draft.ID, billing)
		}
		d.Exec("DELETE FROM agent_registrations WHERE node_id=? OR expires_at<?", draft.ID, nowUnix)
		d.Exec("INSERT INTO agent_registrations(id,node_id,code_hash,expires_at,created_at) VALUES (?,?,?,?,?)",
			identifier("areg"), draft.ID, hashToken(code), nowUnix+agentRegistrationLifetime.Seconds(), now())
		out = M{
			"node_id":           draft.ID,
			"registration_code": code,
			"expires_at":        time.Now().UTC().Add(agentRegistrationLifetime).Format(time.RFC3339Nano),
			"compose_yaml":      renderSingleContainerCompose(baseURL, draft.ID, code, draft),
		}
		c.remember(d, actor, key, path, reqBody, out, 201, true)
		audit(d, actor, "enroll_node", draft.ID, "登记单容器节点并签发注册码")
	})
	NewWorker(c, nil).ExportInventory()
	return out
}

func (c *Control) UpdateNodeLabels(actor, key, id string, version int64, draft NodeDraft, billing ...*NodeBilling) M {
	draft.ID = id
	validateNodeLabels(draft)
	var bill *NodeBilling
	if len(billing) > 0 && billing[0] != nil {
		bCopy := *billing[0]
		bCopy.NormalizeAndValidate()
		bill = &bCopy
	}
	path := "/nodes/" + id + "/metadata"
	body := M{"draft": draft, "version": version}
	if bill != nil {
		body["billing"] = bill
	}
	var out M
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		if out = c.replay(d, actor, key, path, body); out != nil {
			return
		}
		n := d.One("SELECT n.id,COALESCE(m.version,0) AS version FROM nodes n LEFT JOIN node_management m ON n.id=m.node_id WHERE n.id=?", id)
		if n == nil {
			fail("NOT_FOUND", "节点不存在", 404)
		}
		if integer(n["version"]) != version {
			fail("STALE_REVISION", "节点信息已变化，请重新打开编辑", 409)
		}
		if integer(d.Value("SELECT count(*) FROM node_jobs WHERE node_id=? AND kind IN ('configure','protocol_change','config_change','restart') AND state IN ('queued','running')", id)) > 0 {
			fail("NODE_BUSY", "节点接管任务进行中，请稍后编辑", 409)
		}
		checkGroupChange(d, id, draft.GroupID)
		d.Exec("UPDATE nodes SET name=?,region=?,location=? WHERE id=?", draft.Name, draft.Region, draft.Location, id)
		d.Exec("INSERT OR IGNORE INTO groups VALUES (?,?)", draft.GroupID, draft.GroupName)
		d.Exec("DELETE FROM group_nodes WHERE node_id=?", id)
		d.Exec("INSERT INTO group_nodes VALUES (?,?)", draft.GroupID, id)
		if bill != nil {
			saveNodeBilling(d, id, bill)
		}
		d.Exec("INSERT INTO node_management(node_id,version,binding_json) VALUES (?,?, '{}') ON CONFLICT(node_id) DO UPDATE SET version=excluded.version", id, version+1)
		out = M{"saved": true, "version": version + 1}
		c.remember(d, actor, key, path, body, out, 200, false)
		audit(d, actor, "edit_node", id, "修改节点资料与计费规则")
	})
	return out
}
func (w *Worker) ClaimNodeJob() M {
	var job M
	ts := timestamp()
	transaction(w.Control.Settings.ControlDB, true, func(d *DB) {
		job = d.One("SELECT j.* FROM node_jobs j WHERE j.state IN ('queued','running') AND j.lease_until<? AND NOT EXISTS(SELECT 1 FROM node_jobs other WHERE other.id<>j.id AND other.node_id=j.node_id AND j.node_id<>'' AND other.state='running' AND other.lease_until>=?) AND (j.kind NOT IN ('configure','protocol_change','config_change','restart') OR NOT EXISTS(SELECT 1 FROM outbox o WHERE o.node_id=j.node_id AND o.state<>'done' AND o.lease_until>=?)) ORDER BY j.created_at,j.id LIMIT 1", ts, ts, ts)
		if job != nil {
			d.Exec("UPDATE node_jobs SET state='running',lease_owner=?,lease_until=?,updated_at=? WHERE id=?", w.Owner, ts+360, now(), job["id"])
		}
	})
	return job
}

// NodeStep claims and runs a single node job.
func (w *Worker) NodeStep() bool {
	job := w.ClaimNodeJob()
	if job == nil {
		return false
	}
	w.RunNodeJob(job)
	return true
}
func (w *Worker) RunNodeJob(job M) {
	c := w.Control
	var input NodeJobInput
	var result M
	err := Attempt(func() {
		input = decodeNodeInput(c, str(job["input_json"]))
		draft := input.Draft
		if mutatingNodeJob(input.Kind) {
			unlock := w.nodeLock(draft.ID)
			defer unlock()
		}
		switch input.Kind {
		case "protocols", "protocol_change", "config_change", "restart":
			result = w.executeNodeAction(job, input)
		case "config":
			result = callNewerAction(w.Transport, draft.Connection, M{"action": "config"})
			if str(result["node_id"]) != draft.ID || !hashRE.MatchString(str(result["hash"])) {
				fail("NODE_RESPONSE", "配置读取回执无效", 409)
			}
		case "verify_inbounds":
			unlock := w.nodeLock(draft.ID)
			defer unlock()
			result = callNewerAction(w.Transport, draft.Connection, M{"action": "verify_inbounds"})
			transaction(c.Settings.ControlDB, true, func(d *DB) {
				n := d.One("SELECT template_hash,endpoints_json FROM nodes WHERE id=?", draft.ID)
				if d.Value("SELECT lease_owner FROM node_jobs WHERE id=?", job["id"]) != w.Owner ||
					result["node_id"] != draft.ID || result["template_hash"] != n["template_hash"] ||
					digest(decodeEndpoints(result["endpoints"])) != digest(decodeEndpoints(n["endpoints_json"])) ||
					!saveProbeVerification(d, draft.ID, result) {
					fail("STALE_REVISION", "节点拨测结果与当前配置版本不符", 409)
				}
			})
		case "inspect":
			result = w.Transport.Call(draft.Connection, M{"action": "inspect", "container": draft.Container, "config_dir": draft.ConfigDir, "public_host": draft.PublicHost, "server_name": draft.ServerName}, false)
			stats := w.Transport.Call(draft.Connection, M{"action": "inspect"}, true)
			if !boolean(stats["ready"]) {
				fail("STATS_SETUP", "节点 Agent 统计接口未就绪，请核对配套镜像和配置", 409)
			}
		case "check":
			var node M
			transaction(c.Settings.ControlDB, false, func(d *DB) {
				node = d.One("SELECT endpoints_json,template_hash FROM nodes WHERE id=?", draft.ID)
			})
			connectError := Attempt(func() {
				result = w.Transport.Call(draft.Connection, M{"action": "diagnose"}, false)
				if result["node_id"] != draft.ID {
					fail("NODE_IDENTITY", "节点检查回执身份不匹配", 409)
				}
			})
			if connectError != nil {
				result = M{"node_id": draft.ID, "ready": false, "checked_at": now(), "checks": []M{
					{"name": "管理通道", "status": "failed", "ok": false, "message": PublicFault(connectError).Message},
					{"name": "运行绑定", "status": "not_run", "message": "等待管理通道恢复"},
				}}
			}
			statsError := Attempt(func() {
				snapshot := w.Transport.Call(draft.Connection, nil, true)
				if snapshot["node_id"] != draft.ID || snapshot["sampled_at"] == nil || time.Since(stamp(str(snapshot["sampled_at"]))) > 30*time.Second {
					fail("STATS_STALE", "统计快照未更新，请检查采集服务", 409)
				}
			})
			checks := array(result["checks"])
			if statsError != nil {
				result["ready"] = false
				checks = append(checks, M{"name": "统计通道", "status": "failed", "ok": false, "message": PublicFault(statsError).Message})
			} else {
				checks = append(checks, M{"name": "统计通道", "status": "passed", "ok": true, "message": "节点 Agent 已返回 30 秒内的采样"})
			}
			protocolStatus := "not_run"
			if len(decodeEndpoints(node["endpoints_json"])) == 0 {
				protocolStatus = "not_applicable"
			}
			checks = append(checks,
				M{"name": "节点协议拨测", "status": protocolStatus, "message": "节点检查仅读取管理和运行状态"},
				M{"name": "订阅下载", "status": "not_run", "message": "需针对具体用户订阅验证"},
				M{"name": "客户端互通", "status": "not_run", "message": "需在目标客户端和网络验证"},
			)
			result["template_hash"] = node["template_hash"]
			result["checks"] = checks
		case "configure":
			var observation M
			recovered := false
			transaction(c.Settings.ControlDB, false, func(d *DB) {
				n := d.One("SELECT version,binding_json FROM node_management WHERE node_id=?", draft.ID)
				if n != nil && object([]byte(str(n["binding_json"])))["management_job_id"] == job["id"] {
					result = object([]byte(str(n["binding_json"])))
					recovered = true
					return
				}
				if n != nil && integer(n["version"]) > input.ExpectedVersion {
					fail("STALE_REVISION", "节点配置已由其他操作更新，请重新检查", 409)
				}
				if integer(d.Value("SELECT count(*) FROM outbox WHERE node_id=? AND state<>'done'", draft.ID)) > 0 {
					fail("AUTHORIZATION_PENDING", "节点正在处理授权任务，维护任务排队等待", 409)
				}
				r := d.One("SELECT result_json FROM node_jobs WHERE id=?", input.InspectionID)
				observation = object([]byte(str(r["result_json"])))
			})
			if recovered {
				w.ExportInventory()
				break
			}
			if !boolean(observation["can_adopt"]) {
				fail("NODE_NOT_READY", "检查结果尚不满足接管条件", 409)
			}
			result = w.Transport.Call(draft.Connection, M{"action": "configure", "node_id": draft.ID, "inspection": observation, "request_id": str(job["id"]), "desired_container": input.DesiredContainer, "desired_image": input.DesiredImage}, false)
			if str(result["node_id"]) != draft.ID {
				fail("NODE_IDENTITY", "节点响应身份不匹配")
			}
			eps := decodeEndpoints(result["endpoints"])
			if !hashRE.MatchString(str(result["template_hash"])) || !hashRE.MatchString(str(result["engine_build_id"])) || !boolean(result["ready"]) {
				fail("NODE_RESPONSE", "节点接管回执无效")
			}
			result["management_job_id"] = job["id"]
			if version := strings.TrimPrefix(str(result["version"]), "sing-box version "); version != "" {
				draft.Connection.Version = version
			}
			transaction(c.Settings.ControlDB, true, func(d *DB) {
				if d.Value("SELECT lease_owner FROM node_jobs WHERE id=?", job["id"]) != w.Owner {
					fail("LEASE_LOST", "节点任务由另一个执行器恢复", 409)
				}
				n := d.One("SELECT id FROM nodes WHERE id=?", draft.ID)
				publicHost := str(draft.PublicHost)
				if publicHost == "" && len(eps) > 0 {
					publicHost = eps[0].Server
				}
				if n == nil {
					d.Exec("INSERT INTO nodes(id,name,region,location,management_address,public_host,connection_json,endpoints_json,template_hash,image_digest,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)", draft.ID, draft.Name, draft.Region, draft.Location, draft.Connection.Address, publicHost, string(canonical(draft.Connection)), string(canonical(eps)), result["template_hash"], "sha256:"+str(result["engine_build_id"]), now())
				} else {
					d.Exec("UPDATE nodes SET name=?,region=?,location=?,management_address=?,public_host=?,connection_json=?,endpoints_json=?,template_hash=?,image_digest=? WHERE id=?", draft.Name, draft.Region, draft.Location, draft.Connection.Address, publicHost, string(canonical(draft.Connection)), string(canonical(eps)), result["template_hash"], "sha256:"+str(result["engine_build_id"]), draft.ID)
				}
				d.Exec("INSERT OR IGNORE INTO groups VALUES (?,?)", draft.GroupID, draft.GroupName)
				d.Exec("DELETE FROM group_nodes WHERE node_id=?", draft.ID)
				d.Exec("INSERT INTO group_nodes VALUES (?,?)", draft.GroupID, draft.ID)
				d.Exec("INSERT INTO node_management(node_id,version,binding_json,checked_at,check_json) VALUES (?,?,?,?,?) ON CONFLICT(node_id) DO UPDATE SET version=excluded.version,binding_json=excluded.binding_json,checked_at=excluded.checked_at,check_json=excluded.check_json", draft.ID, input.ExpectedVersion+1, string(canonical(result)), now(), string(canonical(result)))
				if input.Billing != nil {
					saveNodeBilling(d, draft.ID, input.Billing)
				}
				saveProbeVerification(d, draft.ID, result)
				audit(d, "worker", "configure_node", draft.ID, "节点绑定已确认并保存")
			})
			w.ExportInventory()
		}
		if result["error"] != nil {
			e := mapping(result["error"])
			fail(str(e["code"]), str(e["message"]), 409)
		}
		if input.Kind == "check" {
			transaction(c.Settings.ControlDB, true, func(d *DB) {
				d.Exec("INSERT INTO node_management(node_id,version,binding_json,checked_at,check_json) VALUES (?,0,'{}',?,?) ON CONFLICT(node_id) DO UPDATE SET checked_at=excluded.checked_at,check_json=excluded.check_json", draft.ID, now(), string(canonical(result)))
			})
		}
	})
	state, message, code := "completed", "操作已完成", ""
	if input.Kind == "verify_inbounds" {
		message = "当前配置的节点侧入口检查通过"
		if result["protocol_status"] == "not_applicable" {
			message = "没有订阅入口，入口检查不适用"
		}
	}
	nextAttempt := float64(0)
	var output any
	if err != nil {
		state = "failed"
		f := PublicFault(err)
		code, message = f.Code, f.Message
		if code == "AUTHORIZATION_PENDING" || code == "LOCK_BUSY" || code == "CONFIG_SYNC_PENDING" || (input.Change != nil && contains([]string{"disable", "delete"}, input.Change.Action) && code == "NODE_UNREACHABLE") {
			state, code, nextAttempt = "queued", "", timestamp()+1
		}
	} else {
		output = string(canonical(result))
	}
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		d.Exec("UPDATE node_jobs SET state=?,result_json=?,error_code=?,message=?,updated_at=?,lease_owner=NULL,lease_until=? WHERE id=? AND lease_owner=?", state, output, code, message, now(), nextAttempt, job["id"], w.Owner)
	})
}
func DiscoveryExport(path string) {
	raw := object(runCommand([]string{"/usr/bin/tailscale", "status", "--json"}, nil, 10*time.Second))
	hosts := []M{}
	for _, value := range mapping(raw["Peer"]) {
		p := mapping(value)
		addresses := stringsOf(p["TailscaleIPs"])
		selected := ""
		for _, addr := range addresses {
			ip, e := netip.ParseAddr(addr)
			if e == nil && ip.Is4() {
				selected = addr
				break
			}
		}
		if selected == "" && len(addresses) > 0 {
			selected = addresses[0]
		}
		if selected == "" {
			continue
		}
		hosts = append(hosts, M{"name": str(p["HostName"]), "address": selected, "online": boolean(p["Online"]), "os": str(p["OS"])})
	}
	sort.Slice(hosts, func(i, j int) bool { return str(hosts[i]["name"]) < str(hosts[j]["name"]) })
	atomicWrite(path, canonical(M{"sampled_at": now(), "state": str(raw["BackendState"]), "hosts": hosts}), 0644)
}
func (c *Control) Discovery() M {
	var snapshot M
	err := Attempt(func() { snapshot = object(read(c.Settings.DiscoveryFile)) })
	if err != nil {
		return M{"available": false, "sampled_at": nil, "hosts": []M{}, "message": "发现服务尚未配置，可手动输入 Tailscale 地址"}
	}
	sample := str(snapshot["sampled_at"])
	fresh := false
	_ = Attempt(func() { fresh = time.Since(stamp(sample)) < 2*time.Minute })
	known := map[string]string{}
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		for _, n := range d.Rows("SELECT id,management_address FROM nodes") {
			known[str(n["management_address"])] = str(n["id"])
		}
	})
	hosts := []M{}
	for _, value := range array(snapshot["hosts"]) {
		h := mapping(value)
		h["node_id"] = known[str(h["address"])]
		hosts = append(hosts, h)
	}
	message := "已读取 Tailscale 主机"
	if !fresh {
		message = "发现快照已过期，请检查主控发现服务"
	}
	return M{"available": fresh && snapshot["state"] == "Running", "sampled_at": sample, "hosts": hosts, "message": message}
}
func (c *Control) Onboarding() M {
	return M{
		"keys": M{"publish": "", "stats": ""},
		"instructions": []string{
			"在控制台填写节点信息并生成一次性注册码与单容器 compose.yaml 配置",
			"在受管 VPS 准备 /etc/sing-box/config.json，设置 SING_BOX_IMAGE 为带内嵌 sbctl Agent 的镜像后启动单容器（无需安装 sbctl-node 二进制、SSH 账号或启动守卫）",
			"容器启动后自动经 Tailscale 向主控完成 Token 注册、进程内热重载接管与实时流量上报",
		},
		"release": Version,
	}
}

func checkGroupChange(d *DB, node, group string) {
	old := str(d.Value("SELECT group_id FROM group_nodes WHERE node_id=? LIMIT 1", node))
	if old == group {
		return
	}
	if integer(d.Value("SELECT count(*) FROM credentials WHERE node_id=? AND revoked=0", node)) > 0 || integer(d.Value("SELECT count(*) FROM grants g JOIN users u ON u.id=g.user_id WHERE g.group_id=? AND u.status<>'disabled'", group)) > 0 {
		fail("GROUP_IN_USE", "节点或目标分组仍有启用的用户；请先处理授权，再调整分组", 409)
	}
}
