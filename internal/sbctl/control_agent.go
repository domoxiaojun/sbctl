package sbctl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	agentRegistrationLifetime = 15 * time.Minute
	maxLiveHistoryPoints      = 60
)

func hashToken(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func bearer(r *http.Request) string {
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(value, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(value, "Bearer "))
	}
	return ""
}

type LiveRateItem struct {
	Tag         string `json:"tag"`
	UploadBps   int64  `json:"upload_bps"`
	DownloadBps int64  `json:"download_bps"`
}

type LivePoint struct {
	Timestamp         string `json:"timestamp"`
	UploadBps         int64  `json:"upload_bps"`
	DownloadBps       int64  `json:"download_bps"`
	ActiveConnections int    `json:"active_connections"`
}

type NodeLiveState struct {
	NodeID            string              `json:"node_id"`
	SampledAt         string              `json:"sampled_at"`
	UpdatedAt         time.Time           `json:"-"`
	UploadBps         int64               `json:"upload_bps"`
	DownloadBps       int64               `json:"download_bps"`
	ActiveConnections int                 `json:"active_connections"`
	Inbounds          []LiveRateItem      `json:"inbounds"`
	Outbounds         []LiveRateItem      `json:"outbounds"`
	ActiveItems       []TrafficConnection `json:"active_items"`
	History           []LivePoint         `json:"history"`
	ConfigEventSeq    int64               `json:"-"`
	SemanticHash      string              `json:"-"`
	ConfigExternal    bool                `json:"-"`
}

// AgentHub coordinates zero-polling in-memory command wakeup, receipt delivery,
// and real-time live traffic streaming across goroutines.
type AgentHub struct {
	mu          sync.RWMutex
	cmdSignals  map[string]chan struct{}
	receiptWait map[string]chan struct{}
	liveNodes   map[string]*NodeLiveState
	liveSubs    map[chan struct{}]struct{}
	subCount    atomic.Int32
}

func NewAgentHub() *AgentHub {
	return &AgentHub{
		cmdSignals:  map[string]chan struct{}{},
		receiptWait: map[string]chan struct{}{},
		liveNodes:   map[string]*NodeLiveState{},
		liveSubs:    map[chan struct{}]struct{}{},
	}
}

func (h *AgentHub) commandChan(nodeID string) chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch, ok := h.cmdSignals[nodeID]
	if !ok {
		ch = make(chan struct{}, 1)
		h.cmdSignals[nodeID] = ch
	}
	return ch
}

func (h *AgentHub) notifyCommand(nodeID string) {
	ch := h.commandChan(nodeID)
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (h *AgentHub) registerReceiptWaiter(cmdID string) (chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	h.receiptWait[cmdID] = ch
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.receiptWait, cmdID)
		h.mu.Unlock()
	}
}

func (h *AgentHub) notifyReceipt(cmdID string) {
	h.mu.RLock()
	ch, ok := h.receiptWait[cmdID]
	h.mu.RUnlock()
	if ok {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (h *AgentHub) SubscribeLive() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	h.liveSubs[ch] = struct{}{}
	h.subCount.Add(1)
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if _, ok := h.liveSubs[ch]; ok {
			delete(h.liveSubs, ch)
			h.subCount.Add(-1)
		}
		h.mu.Unlock()
	}
}

func (h *AgentHub) Subscribers() int {
	return int(h.subCount.Load())
}

func (h *AgentHub) updateLive(nodeID string, live M, eventSeq int64, semHash string, ext bool) {
	sampled := str(live["sampled_at"])
	if sampled == "" {
		sampled = now()
	}
	stamp(sampled)
	upBps := integer(live["upload_bps"])
	downBps := integer(live["download_bps"])
	activeCount := int(integer(live["active_connections"]))

	inbounds := []LiveRateItem{}
	for _, r := range array(live["inbounds"]) {
		m := mapping(r)
		inbounds = append(inbounds, LiveRateItem{Tag: str(m["tag"]), UploadBps: integer(m["upload_bps"]), DownloadBps: integer(m["download_bps"])})
	}
	outbounds := []LiveRateItem{}
	for _, r := range array(live["outbounds"]) {
		m := mapping(r)
		outbounds = append(outbounds, LiveRateItem{Tag: str(m["tag"]), UploadBps: integer(m["upload_bps"]), DownloadBps: integer(m["download_bps"])})
	}
	activeItems := []TrafficConnection{}
	if rawItems := live["active_items"]; rawItems != nil {
		decode(canonical(rawItems), &activeItems)
		for _, item := range activeItems {
			counter(item.Upload)
			counter(item.Download)
		}
	}

	h.mu.Lock()
	st, ok := h.liveNodes[nodeID]
	if !ok {
		st = &NodeLiveState{NodeID: nodeID}
		h.liveNodes[nodeID] = st
	}
	st.SampledAt = sampled
	st.UpdatedAt = time.Now()
	st.UploadBps = upBps
	st.DownloadBps = downBps
	st.ActiveConnections = activeCount
	st.Inbounds = inbounds
	st.Outbounds = outbounds
	st.ActiveItems = activeItems
	if eventSeq > 0 {
		st.ConfigEventSeq = eventSeq
		st.SemanticHash = semHash
		st.ConfigExternal = ext
	}
	st.History = append(st.History, LivePoint{
		Timestamp:         sampled,
		UploadBps:         upBps,
		DownloadBps:       downBps,
		ActiveConnections: activeCount,
	})
	if len(st.History) > maxLiveHistoryPoints {
		st.History = append([]LivePoint(nil), st.History[len(st.History)-maxLiveHistoryPoints:]...)
	}
	subs := make([]chan struct{}, 0, len(h.liveSubs))
	for ch := range h.liveSubs {
		subs = append(subs, ch)
	}
	h.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (h *AgentHub) ObservedConfigEvent(nodeID string) (int64, string, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if st, ok := h.liveNodes[nodeID]; ok {
		return st.ConfigEventSeq, st.SemanticHash, st.ConfigExternal
	}
	return 0, "", false
}

func (h *AgentHub) LiveSnapshot(filterNode string) M {
	h.mu.RLock()
	defer h.mu.RUnlock()

	cutoff := time.Now().Add(-30 * time.Second)
	var totalUp, totalDown int64
	totalActive := 0
	nodes := []M{}
	activeItems := []M{}
	var mergedHistory []LivePoint

	keys := make([]string, 0, len(h.liveNodes))
	for k := range h.liveNodes {
		if filterNode == "" || k == filterNode {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	for _, id := range keys {
		st := h.liveNodes[id]
		fresh := !st.UpdatedAt.Before(cutoff)
		up, down, act := st.UploadBps, st.DownloadBps, st.ActiveConnections
		if !fresh {
			up, down, act = 0, 0, 0
		}
		totalUp += up
		totalDown += down
		totalActive += act
		nodes = append(nodes, M{
			"node_id":            st.NodeID,
			"sampled_at":         st.SampledAt,
			"fresh":              fresh,
			"upload_bps":         up,
			"download_bps":       down,
			"active_connections": act,
			"inbounds":           st.Inbounds,
			"outbounds":          st.Outbounds,
			"history":            st.History,
		})
		if fresh {
			for _, item := range st.ActiveItems {
				chain := item.Chain
				if chain == nil {
					chain = []string{}
				}
				activeItems = append(activeItems, M{
					"node_id":        id,
					"connection_id":  item.ID,
					"inbound_id":     item.InboundID,
					"inbound":        item.Inbound,
					"inbound_type":   item.InboundType,
					"network":        item.Network,
					"domain":         item.Domain,
					"destination":    item.Destination,
					"source":         item.Source,
					"outbound":       item.Outbound,
					"outbound_type":  item.OutboundType,
					"chain":          chain,
					"rule":           item.Rule,
					"upload_bytes":   item.Upload,
					"download_bytes": item.Download,
					"started_at":     item.StartedAt,
					"closed_at":      "",
				})
			}
		}
		if len(keys) == 1 {
			mergedHistory = append([]LivePoint(nil), st.History...)
		}
	}

	if len(keys) > 1 {
		// Nodes report independently. Use the latest sample per node per five-second bucket.
		totals := map[int64]LivePoint{}
		for _, id := range keys {
			latest := map[int64]LivePoint{}
			for _, sample := range h.liveNodes[id].History {
				bucket := stamp(sample.Timestamp).Truncate(5 * time.Second).Unix()
				latest[bucket] = sample
			}
			for bucket, sample := range latest {
				pt := totals[bucket]
				pt.Timestamp = time.Unix(bucket, 0).UTC().Format(time.RFC3339)
				pt.UploadBps += sample.UploadBps
				pt.DownloadBps += sample.DownloadBps
				pt.ActiveConnections += sample.ActiveConnections
				totals[bucket] = pt
			}
		}
		for _, pt := range totals {
			mergedHistory = append(mergedHistory, pt)
		}
		sort.Slice(mergedHistory, func(i, j int) bool { return mergedHistory[i].Timestamp < mergedHistory[j].Timestamp })
		if len(mergedHistory) > maxLiveHistoryPoints {
			mergedHistory = mergedHistory[len(mergedHistory)-maxLiveHistoryPoints:]
		}
	}
	sort.SliceStable(activeItems, func(i, j int) bool {
		total := func(item M) uint64 {
			return uint64(counter(item["upload_bytes"])) + uint64(counter(item["download_bytes"]))
		}
		return total(activeItems[i]) > total(activeItems[j])
	})
	activeItems = activeItems[:min(len(activeItems), 100)]
	if mergedHistory == nil {
		mergedHistory = []LivePoint{}
	}
	return M{
		"as_of":              now(),
		"upload_bps":         totalUp,
		"download_bps":       totalDown,
		"active_connections": totalActive,
		"nodes":              nodes,
		"history":            mergedHistory,
		"active_items":       activeItems,
	}
}

var hubRegistry = struct {
	sync.RWMutex
	byDB map[string]*AgentHub
}{byDB: map[string]*AgentHub{}}

func registerHub(controlDB string, hub *AgentHub) {
	if controlDB == "" || hub == nil {
		return
	}
	hubRegistry.Lock()
	hubRegistry.byDB[controlDB] = hub
	hubRegistry.Unlock()
}

func hubFor(controlDB string) *AgentHub {
	hubRegistry.RLock()
	defer hubRegistry.RUnlock()
	return hubRegistry.byDB[controlDB]
}

func (c *Control) hub() *AgentHub {
	if c.Hub == nil {
		c.Hub = NewAgentHub()
		registerHub(c.Settings.ControlDB, c.Hub)
	}
	return c.Hub
}

func (c *Control) CreateAgentRegistration(actor, key, nodeID string) M {
	validIdentity(nodeID)
	code := token(32)
	nowUnix := timestamp()
	path := "/nodes/" + nodeID + "/agent-registration"
	reqBody := M{"node_id": nodeID}
	var out M
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		if out = c.replay(d, actor, key, path, reqBody); out != nil {
			return
		}
		if d.One("SELECT id FROM nodes WHERE id=?", nodeID) == nil {
			fail("NOT_FOUND", "节点不存在", 404)
		}
		d.Exec("DELETE FROM agent_registrations WHERE node_id=? OR expires_at<?", nodeID, nowUnix)
		d.Exec("INSERT INTO agent_registrations(id,node_id,code_hash,expires_at,created_at) VALUES (?,?,?,?,?)", identifier("areg"), nodeID, hashToken(code), nowUnix+agentRegistrationLifetime.Seconds(), now())
		out = M{"node_id": nodeID, "registration_code": code, "expires_at": time.Now().UTC().Add(agentRegistrationLifetime).Format(time.RFC3339Nano)}
		c.remember(d, actor, key, path, reqBody, out, 201, true)
		audit(d, actor, "create_agent_registration", nodeID, "生成节点单容器注册码")
	})
	return out
}

type agentRegisterInput struct {
	NodeID           string `json:"node_id"`
	RegistrationCode string `json:"registration_code"`
	PublicKey        string `json:"public_key"`
	Version          string `json:"version,omitempty"`
	Capabilities     M      `json:"capabilities,omitempty"`
}

func (c *Control) AgentRegister(input M, source string) M {
	var req agentRegisterInput
	decode(canonical(input), &req)
	validIdentity(req.NodeID)
	if len(req.RegistrationCode) < 32 || len(req.RegistrationCode) > 128 || len(req.PublicKey) < 32 || len(req.PublicKey) > 512 || strings.ContainsAny(req.PublicKey, "\r\n\x00") {
		fail("INVALID_REQUEST", "Agent 注册材料无效", 422)
	}
	if req.Version == "" {
		req.Version = "unknown"
	}
	if len(req.Version) > 80 || strings.ContainsAny(req.Version, "\r\n\x00") {
		fail("INVALID_REQUEST", "Agent 版本信息无效", 422)
	}
	if req.Capabilities == nil {
		req.Capabilities = M{}
	}
	subjects := reserveAgentRegisterAttempt(c.Settings, req.NodeID, source)
	agentToken := token(48)
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		row := d.One("SELECT id,expires_at,used_at FROM agent_registrations WHERE node_id=? AND code_hash=?", req.NodeID, hashToken(req.RegistrationCode))
		expires := 0.0
		var err error
		if row != nil {
			expires, err = strconv.ParseFloat(str(row["expires_at"]), 64)
		}
		if row == nil || err != nil || expires < timestamp() || row["used_at"] != nil {
			fail("AGENT_REGISTRATION_INVALID", "Agent 注册码无效或已过期", 409)
		}
		if d.One("SELECT id FROM nodes WHERE id=?", req.NodeID) == nil {
			fail("NOT_FOUND", "节点不存在", 404)
		}
		d.Exec("DELETE FROM login_failures WHERE subject IN (?,?)", subjects...)
		d.Exec("UPDATE agent_registrations SET used_at=? WHERE id=?", now(), row["id"])
		d.Exec("INSERT INTO agents(node_id,token_hash,public_key,version,capabilities_json,last_seen,state) VALUES (?,?,?,?,?,?,?) ON CONFLICT(node_id) DO UPDATE SET token_hash=excluded.token_hash,public_key=excluded.public_key,version=excluded.version,capabilities_json=excluded.capabilities_json,last_seen=excluded.last_seen,state='online'", req.NodeID, hashToken(agentToken), req.PublicKey, req.Version, string(canonical(req.Capabilities)), now(), "online")
		audit(d, "agent", "agent_register", req.NodeID, "节点内置 sing-box 控制通道注册成功")
	})
	return M{"node_id": req.NodeID, "agent_token": agentToken, "control_path": "/api/v1/agent/poll"}
}

func (c *Control) authenticateAgent(tok string) (string, string) {
	if tok == "" || len(tok) > 256 {
		fail("AGENT_UNAUTHORIZED", "Agent 凭据缺失", 401)
	}
	var row M
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		row = d.One("SELECT node_id,last_seen FROM agents WHERE token_hash=?", hashToken(tok))
	})
	if row == nil || str(row["node_id"]) == "" {
		fail("AGENT_UNAUTHORIZED", "Agent 凭据无效", 401)
	}
	return str(row["node_id"]), str(row["last_seen"])
}

func (c *Control) AgentPoll(tok string) M {
	return c.AgentPollWait(context.Background(), tok, 0)
}

func (c *Control) AgentPollWait(ctx context.Context, tok string, wait time.Duration) M {
	nodeID, lastSeen := c.authenticateAgent(tok)
	if wait < 0 {
		wait = 0
	}
	if wait > 25*time.Second {
		wait = 25 * time.Second
	}
	var lastHeartbeat time.Time
	if lastSeen != "" {
		_ = Attempt(func() {
			lastHeartbeat = stamp(lastSeen)
		})
	}
	claim := func() M {
		needsHeartbeat := lastHeartbeat.IsZero() || time.Since(lastHeartbeat) >= 15*time.Second
		var ready bool
		transaction(c.Settings.ControlDB, false, func(d *DB) {
			ready = d.One("SELECT id FROM agent_commands WHERE node_id=? AND ((state='queued') OR (state='running' AND lease_until<?)) ORDER BY created_at,id LIMIT 1", nodeID, timestamp()) != nil
		})
		if !ready && !needsHeartbeat {
			return nil
		}
		var cmd M
		transaction(c.Settings.ControlDB, true, func(d *DB) {
			if d.Value("SELECT node_id FROM agents WHERE node_id=? AND token_hash=?", nodeID, hashToken(tok)) == nil {
				fail("AGENT_UNAUTHORIZED", "Agent 凭据已变更", 401)
			}
			if needsHeartbeat || ready {
				d.Exec("UPDATE agents SET last_seen=?,state='online' WHERE node_id=?", now(), nodeID)
				lastHeartbeat = time.Now()
			}
			row := d.One("SELECT id,action,body_ciphertext FROM agent_commands WHERE node_id=? AND ((state='queued') OR (state='running' AND lease_until<?)) ORDER BY created_at,id LIMIT 1", nodeID, timestamp())
			if row == nil {
				return
			}
			payload := mapping(c.Vault.Decrypt(str(row["body_ciphertext"])))
			timeout := actionTimeout(mapping(payload["body"]))
			lease := timestamp() + timeout.Seconds() + 30
			d.Exec("UPDATE agent_commands SET state='running',lease_until=?,updated_at=? WHERE id=?", lease, now(), row["id"])
			d.Exec("UPDATE agents SET last_seen=?,state='online' WHERE node_id=?", now(), nodeID)
			lastHeartbeat = time.Now()
			cmd = M{"id": row["id"], "action": row["action"], "request": payload}
		})
		return cmd
	}

	if cmd := claim(); cmd != nil {
		return M{"node_id": nodeID, "poll_after_seconds": 1, "command": cmd}
	}
	if wait <= 0 {
		return M{"node_id": nodeID, "poll_after_seconds": 2, "command": nil}
	}

	notifyCh := c.hub().commandChan(nodeID)
	timer := time.NewTimer(wait)
	defer timer.Stop()
	// Standalone workers and CLI commands share SQLite, but not this hub.
	fallback := time.NewTicker(4 * time.Second)
	defer fallback.Stop()
	for {
		select {
		case <-notifyCh:
		case <-fallback.C:
		case <-ctx.Done():
			return M{"node_id": nodeID, "poll_after_seconds": 2, "command": nil}
		case <-timer.C:
			return M{"node_id": nodeID, "poll_after_seconds": 2, "command": nil}
		}
		if cmd := claim(); cmd != nil {
			return M{"node_id": nodeID, "poll_after_seconds": 1, "command": cmd}
		}
	}
}

type agentReceiptInput struct {
	CommandID string `json:"command_id"`
	Result    M      `json:"result,omitempty"`
	Error     M      `json:"error,omitempty"`
}

func (c *Control) AgentReceipt(tok string, input M) M {
	nodeID, _ := c.authenticateAgent(tok)
	var req agentReceiptInput
	decode(canonical(input), &req)
	if req.CommandID == "" || len(req.CommandID) > 128 {
		fail("INVALID_REQUEST", "缺少 Agent 命令 ID", 422)
	}
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		row := d.One("SELECT state FROM agent_commands WHERE id=? AND node_id=?", req.CommandID, nodeID)
		if row == nil {
			fail("NOT_FOUND", "Agent 命令不存在", 404)
		}
		if row["state"] != "running" {
			return
		}
		if req.Error != nil {
			d.Exec("UPDATE agent_commands SET state='failed',error_code=?,response_ciphertext=?,updated_at=?,lease_until=0 WHERE id=?", str(req.Error["code"]), c.Vault.Encrypt(req.Error), now(), req.CommandID)
		} else {
			res := req.Result
			if res == nil {
				res = M{}
			}
			d.Exec("UPDATE agent_commands SET state='done',response_ciphertext=?,updated_at=?,lease_until=0 WHERE id=?", c.Vault.Encrypt(res), now(), req.CommandID)
		}
		d.Exec("UPDATE agents SET last_seen=?,state='online' WHERE node_id=?", now(), nodeID)
	})
	c.hub().notifyReceipt(req.CommandID)
	return M{"accepted": true, "command_id": req.CommandID}
}

// AgentTelemetry ingests pushed durable ledger snapshots, closed connection
// batches, config event sequence metadata and real-time throughput metrics.
func (c *Control) AgentTelemetry(tok string, input M) M {
	nodeID, lastSeen := c.authenticateAgent(tok)
	if str(input["node_id"]) != "" && str(input["node_id"]) != nodeID {
		fail("NODE_IDENTITY", "遥测节点身份不匹配", 409)
	}
	ver := strings.TrimSpace(str(input["version"]))
	needHeartbeat := lastSeen == ""
	if !needHeartbeat {
		_ = Attempt(func() { needHeartbeat = time.Since(stamp(lastSeen)) >= 15*time.Second })
	}
	if needHeartbeat {
		transaction(c.Settings.ControlDB, true, func(d *DB) {
			if ver != "" && len(ver) <= 80 && !strings.ContainsAny(ver, "\r\n\x00") {
				d.Exec("UPDATE agents SET last_seen=?,version=?,state='online' WHERE node_id=?", now(), ver, nodeID)
			} else {
				d.Exec("UPDATE agents SET last_seen=?,state='online' WHERE node_id=?", now(), nodeID)
			}
		})
	}

	t := Traffic{c.Settings.TrafficDB, c.Settings.TrafficSnapshot}
	if !exists(t.Path) {
		t.Initialize()
	}
	var ackedSeq int64
	if input["snapshot"] != nil {
		var snap Snapshot
		decode(canonical(input["snapshot"]), &snap)
		t.Ingest(nodeID, snap)
		if rawConns := input["connections"]; rawConns != nil {
			var items []TrafficConnection
			decode(canonical(rawConns), &items)
			nextSeq := integer(input["connection_next"])
			afterSeq := integer(input["connection_after"])
			connGap := boolean(input["connection_gap"])
			if len(items) > 0 || nextSeq > afterSeq || connGap {
				t.IngestConnectionBatch(nodeID, snap.LedgerID, afterSeq, nextSeq, connGap, items)
				ackedSeq = nextSeq
			} else {
				ackedSeq = afterSeq
			}
		}
	}

	c.hub().updateLive(
		nodeID,
		mapping(input["live"]),
		integer(input["config_event_seq"]),
		str(input["semantic_hash"]),
		boolean(input["config_external"]),
	)

	subs := c.hub().Subscribers()
	nextMs := 5000
	if subs > 0 {
		nextMs = 1500
	}
	return M{
		"accepted":             true,
		"acked_connection_seq": ackedSeq,
		"live_subscribers":     subs,
		"next_interval_ms":     nextMs,
	}
}

type AgentTransport struct {
	Control *Control
	Timeout time.Duration
}

func (t AgentTransport) resolveNodeID(link NodeConnection, body M) string {
	targetID := str(body["node_id"])
	if targetID == "" && body["request"] != nil {
		targetID = str(mapping(object(canonical(body["request"])))["node_id"])
	}
	var nodeID string
	transaction(t.Control.Settings.ControlDB, false, func(d *DB) {
		if targetID != "" {
			nodeID = str(d.Value("SELECT node_id FROM agents WHERE node_id=? AND state='online' AND last_seen>?", targetID, nowMinus(60)))
		}
		if targetID == "" && link.Address != "" {
			nodeID = str(d.Value("SELECT a.node_id FROM agents a JOIN nodes n ON n.id=a.node_id WHERE n.management_address=? AND a.state='online' AND a.last_seen>?", link.Address, nowMinus(60)))
		}
	})
	return nodeID
}

func (t AgentTransport) Call(link NodeConnection, body M, stats bool) M {
	nodeID := t.resolveNodeID(link, body)
	if nodeID == "" {
		fail("NODE_UNREACHABLE", "节点控制连接未在线，请确认自研 sing-box 容器已启动并连接主控", 503)
	}
	commandID := identifier("acmd")
	action := str(body["action"])
	if action == "" && stats {
		action = "stats"
	}
	payload := M{"body": body, "stats": stats}
	cipher := t.Control.Vault.Encrypt(payload)
	waitCh, cancelWait := t.Control.hub().registerReceiptWaiter(commandID)
	defer cancelWait()

	transaction(t.Control.Settings.ControlDB, true, func(d *DB) {
		d.Exec("DELETE FROM agent_commands WHERE state IN ('done','failed') AND updated_at<?", nowMinus(86400))
		d.Exec("INSERT INTO agent_commands(id,node_id,action,body_ciphertext,created_at,updated_at) VALUES (?,?,?,?,?,?)", commandID, nodeID, action, cipher, now(), now())
	})
	t.Control.hub().notifyCommand(nodeID)

	timeout := t.Timeout
	if timeout <= 0 {
		timeout = actionTimeout(body)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	checkDone := func() (M, bool) {
		var row M
		transaction(t.Control.Settings.ControlDB, false, func(d *DB) {
			row = d.One("SELECT state,response_ciphertext,error_code FROM agent_commands WHERE id=?", commandID)
		})
		if row == nil || (row["state"] != "done" && row["state"] != "failed") {
			return nil, false
		}
		var res M
		if raw := str(row["response_ciphertext"]); raw != "" {
			res = mapping(t.Control.Vault.Decrypt(raw))
		}
		if row["state"] == "failed" {
			code := str(res["code"])
			if code == "" {
				code = str(row["error_code"])
			}
			if code == "" {
				code = "AGENT_EXECUTION_FAILED"
			}
			if code == "NODE_UNREACHABLE" && res["message"] == nil {
				fail(code, "节点控制通道响应超时", 503)
			}
			msg := str(res["message"])
			if msg == "" {
				msg = "节点执行操作失败"
			}
			fail(code, msg, 409)
		}
		return res, true
	}

	if res, ok := checkDone(); ok {
		return res
	}
	fallback := time.NewTicker(time.Second)
	defer fallback.Stop()
	for {
		select {
		case <-waitCh:
		case <-fallback.C:
		case <-timer.C:
			transaction(t.Control.Settings.ControlDB, true, func(d *DB) {
				d.Exec("UPDATE agent_commands SET state='failed',error_code='NODE_UNREACHABLE',updated_at=?,lease_until=0 WHERE id=? AND state IN ('queued','running')", now(), commandID)
			})
			if res, ok := checkDone(); ok {
				return res
			}
			fail("NODE_UNREACHABLE", "节点控制通道响应超时", 503)
		}
		if res, ok := checkDone(); ok {
			return res
		}
	}
}

func nowMinus(seconds int) string {
	return time.Now().UTC().Add(-time.Duration(seconds) * time.Second).Format("2006-01-02T15:04:05.000000Z")
}
