package sbctl

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

var clusterNodes = map[string]string{"100.70.0.10": "node_hk", "100.70.0.3": "node_sg", "100.70.0.4": "node_jp"}

// clusterControl registers three nodes in one group, so a single user
// operation fans out to every node.
func clusterControl(t *testing.T) *Control {
	t.Helper()
	c := testControl(t)
	for _, n := range [][4]string{{"node_sg", "新加坡", "100.70.0.3", "sg.test"}, {"node_jp", "日本", "100.70.0.4", "jp.test"}} {
		c.Register(M{"id": n[0], "name": n[1], "region": "ap", "location": n[1], "connection": M{"address": n[2]}, "endpoints": []Endpoint{{"anytls", "anytls-in", n[3], 443, n[3], nil, ""}, {"tuic", "tuic-in", n[3], 443, n[3], nil, ""}}, "template_hash": strings.Repeat("a", 64), "image_digest": "sha256:" + strings.Repeat("b", 64), "group_id": "grp_hk", "group_name": "香港"})
	}
	markTestEndpointsVerified(c)
	return c
}
func draftOf(c *Control, id string) NodeDraft {
	var d NodeDraft
	decode(canonical(c.NodeConfig(id)["draft"]), &d)
	d.Container = "sing-box"
	return d
}

// gatedTransport answers like a healthy node, can hold selected calls open and
// records how many calls were in flight at once.
type gatedTransport struct {
	mu                   sync.Mutex
	gates                map[string]chan struct{}
	delay                time.Duration
	inflight, calls      map[string]int
	total                int
	maxPerNode, maxTotal int
}

func newGatedTransport() *gatedTransport {
	return &gatedTransport{gates: map[string]chan struct{}{}, inflight: map[string]int{}, calls: map[string]int{}}
}

// block holds every matching call until the returned function runs. An empty
// node matches the action on all nodes.
func (g *gatedTransport) block(node, action string) func() {
	gate := make(chan struct{})
	g.mu.Lock()
	g.gates[node+":"+action] = gate
	g.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(gate) }) }
}
func (g *gatedTransport) started(action string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls[action]
}
func (g *gatedTransport) Call(n NodeConnection, body M, stats bool) M {
	node, action := clusterNodes[n.Address], str(body["action"])
	if stats {
		action = "stats"
	}
	g.mu.Lock()
	g.inflight[node]++
	g.total++
	g.calls[action]++
	g.maxPerNode, g.maxTotal = max(g.maxPerNode, g.inflight[node]), max(g.maxTotal, g.total)
	gate := g.gates[node+":"+action]
	if gate == nil {
		gate = g.gates[":"+action]
	}
	delay := g.delay
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		g.inflight[node]--
		g.total--
		g.mu.Unlock()
	}()
	if gate != nil {
		<-gate
	}
	time.Sleep(delay)
	switch action {
	case "submit":
		r := parseRequest(body["request"])
		return M{"node_id": r.NodeID, "latest": M{"id": r.RequestID, "revision": r.NodeRevision, "payload_hash": r.PayloadHash, "state": "verified"}}
	case "diagnose":
		return M{"node_id": node, "ready": true, "checks": []M{}}
	case "stats":
		return M{"node_id": node, "sampled_at": now()}
	}
	return M{}
}

// runWorker starts the dispatcher and returns a function that stops it.
func runWorker(t *testing.T, w *Worker, parallel int) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx, parallel)
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(15 * time.Second):
				t.Error("worker did not stop")
			}
		})
	}
}
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if condition() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}
func operationState(c *Control, id string) string {
	var state string
	transaction(c.Settings.ControlDB, false, func(d *DB) { state = str(d.Value("SELECT state FROM operations WHERE id=?", id)) })
	return state
}
func targetStates(c *Control, id string) map[string]string {
	states := map[string]string{}
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		for _, r := range d.Rows("SELECT node_id,state FROM operation_targets WHERE operation_id=?", id) {
			states[str(r["node_id"])] = str(r["state"])
		}
	})
	return states
}

func TestWorkerRunKeepsOtherNodesMovingWhileOneIsBlocked(t *testing.T) {
	c := clusterControl(t)
	tr := newGatedTransport()
	release := tr.block("node_sg", "submit")
	defer release()
	tr.delay = 500 * time.Millisecond
	stop := runWorker(t, NewWorker(c, tr), 4)
	defer stop()
	op := str(c.CreateUser("admin", "parallel-create-key-01", CreateUser{DisplayName: "并行下发", GroupIDs: []string{"grp_hk"}}, nil)["operation_id"])
	waitFor(t, "the reachable nodes to verify", func() bool {
		states := targetStates(c, op)
		return states["node_hk"] == "verified" && states["node_jp"] == "verified"
	})
	if state := targetStates(c, op)["node_sg"]; state == "verified" {
		t.Fatal("the blocked node cannot have verified yet")
	}
	release()
	waitFor(t, "the operation to complete", func() bool { return operationState(c, op) == "completed" })
}

func TestWorkerRunNeverOverlapsAuthorizationOnOneNode(t *testing.T) {
	c := clusterControl(t)
	tr := newGatedTransport()
	tr.delay = 500 * time.Millisecond
	ops := []string{}
	for i := 0; i < 3; i++ {
		ops = append(ops, str(c.CreateUser("admin", fmt.Sprintf("parallel-batch-key-%02d", i), CreateUser{DisplayName: fmt.Sprintf("批量 %d", i), GroupIDs: []string{"grp_hk"}}, nil)["operation_id"]))
	}
	stop := runWorker(t, NewWorker(c, tr), 4)
	defer stop()
	waitFor(t, "every operation to complete", func() bool {
		for _, op := range ops {
			if operationState(c, op) != "completed" {
				return false
			}
		}
		return true
	})
	stop()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.maxPerNode != 1 {
		t.Fatalf("one node received %d authorization calls at once", tr.maxPerNode)
	}
	if tr.maxTotal < 2 {
		t.Fatalf("deliveries to different nodes never overlapped (max in flight %d)", tr.maxTotal)
	}
}

func TestWorkerRunKeepsASlotForAuthorizationDuringNodeJobs(t *testing.T) {
	c := clusterControl(t)
	tr := newGatedTransport()
	stop := runWorker(t, NewWorker(c, tr), 2)
	defer stop()
	create := str(c.CreateUser("admin", "parallel-slot-create-01", CreateUser{DisplayName: "保留槽位", GroupIDs: []string{"grp_hk"}}, nil)["operation_id"])
	waitFor(t, "the user to be provisioned", func() bool { return operationState(c, create) == "completed" })
	release := tr.block("", "diagnose")
	defer release()
	jobs := []string{
		str(c.SubmitNodeJob("admin", "parallel-check-key-01", NodeJobInput{Kind: "check", Draft: draftOf(c, "node_hk")})["job_id"]),
		str(c.SubmitNodeJob("admin", "parallel-check-key-02", NodeJobInput{Kind: "check", Draft: draftOf(c, "node_sg")})["job_id"]),
	}
	waitFor(t, "a node job to start", func() bool { return tr.started("diagnose") == 1 })
	u := Views{c.Settings}.Users()[0]
	revision := integer(u["revision"])
	disable := str(c.DisableUser("admin", "parallel-slot-disable-1", str(u["id"]), DisableUser{Revision: &revision, Reason: "撤销"})["operation_id"])
	waitFor(t, "the revocation to finish while a node job is in flight", func() bool { return operationState(c, disable) == "completed" })
	states := []string{str(c.NodeJob(jobs[0])["state"]), str(c.NodeJob(jobs[1])["state"])}
	if tr.started("diagnose") != 1 || !contains(states, "running") || !contains(states, "queued") {
		t.Fatalf("node jobs took the slot kept for authorization: started=%d states=%v", tr.started("diagnose"), states)
	}
	release()
	waitFor(t, "both node jobs to finish", func() bool {
		return c.NodeJob(jobs[0])["state"] == "completed" && c.NodeJob(jobs[1])["state"] == "completed"
	})
}

// statsTransport serves the next ledger export of every node that is up.
type statsTransport struct {
	mu      sync.Mutex
	down    map[string]bool
	started string
	seq     map[string]int64
}

func (s *statsTransport) Call(n NodeConnection, body M, stats bool) M {
	id := clusterNodes[n.Address]
	if !stats || body != nil {
		fail("ACTION_REJECTED", "unexpected call", 409)
	}
	if s.down[id] {
		fail("NODE_UNREACHABLE", "节点不可达", 503)
	}
	s.mu.Lock()
	s.seq[id]++
	seq := s.seq[id]
	s.mu.Unlock()
	sampled := now()
	snapshot := Snapshot{SchemaVersion: 1, NodeID: id, LedgerID: "ledger_" + id, Seq: seq, StartedAt: s.started, SampledAt: &sampled, Quality: DataQuality{Integrity: "ok"}}
	snapshot.Seal()
	return object(canonical(snapshot))
}

func TestSyncNodesIsolatesAnUnreachableNode(t *testing.T) {
	c := clusterControl(t)
	nodes := array(object(read(c.Settings.InventoryFile))["nodes"])
	if len(nodes) != 3 {
		t.Fatalf("expected three nodes in the inventory, got %d", len(nodes))
	}
	traffic := Traffic{c.Settings.TrafficDB, c.Settings.TrafficSnapshot}
	traffic.Initialize()
	transport := &statsTransport{down: map[string]bool{"node_sg": true}, started: now(), seq: map[string]int64{}}
	for round, parallel := range []int{1, 4} {
		SyncNodes(context.Background(), traffic, transport, nodes, parallel)
		exported := mapping(object(read(c.Settings.TrafficSnapshot))["nodes"])
		for _, id := range []string{"node_hk", "node_jp"} {
			entry := mapping(exported[id])
			if integer(mapping(entry["snapshot"])["snapshot_seq"]) != int64(round+1) || entry["error_code"] != nil {
				t.Fatalf("parallel=%d: %s was not synchronized: %v", parallel, id, entry)
			}
		}
		if entry := mapping(exported["node_sg"]); entry["error_code"] != "NODE_UNREACHABLE" || entry["snapshot"] != nil {
			t.Fatalf("parallel=%d: the unreachable node was not recorded as failed: %v", parallel, entry)
		}
	}
}
