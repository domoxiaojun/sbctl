package sbctl

import (
	"strings"
	"sync"
	"testing"
)

// faultyCluster stands in for three nodes. Each node keeps the requests it
// accepted, can be taken off the network and can be told to report a state
// other than verified.
type faultyCluster struct {
	mu       sync.Mutex
	down     map[string]bool
	state    map[string]string
	accepted map[string][]NodeRequest
}

func newFaultyCluster() *faultyCluster {
	return &faultyCluster{down: map[string]bool{}, state: map[string]string{}, accepted: map[string][]NodeRequest{}}
}
func (f *faultyCluster) Call(n NodeConnection, body M, stats bool) M {
	node := clusterNodes[n.Address]
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down[node] {
		fail("NODE_UNREACHABLE", "节点不可达", 503)
	}
	r := parseRequest(body["request"])
	if history := f.accepted[node]; len(history) > 0 && r.NodeRevision < history[len(history)-1].NodeRevision {
		fail("STALE_REVISION", "请求早于已接受安全版本", 409)
	}
	f.accepted[node] = append(f.accepted[node], r)
	state := f.state[node]
	if state == "" {
		state = "verified"
	}
	return M{"node_id": r.NodeID, "latest": M{"id": r.RequestID, "revision": r.NodeRevision, "payload_hash": r.PayloadHash, "state": state}}
}

// latest is the request a node would currently be running.
func (f *faultyCluster) latest(node string) NodeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	history := f.accepted[node]
	if len(history) == 0 {
		return NodeRequest{}
	}
	return history[len(history)-1]
}
func holdsUser(r NodeRequest, user string) bool {
	for _, c := range r.Users {
		if c.UserID == user {
			return true
		}
	}
	return false
}

// drain delivers until nothing is due; tasks waiting for a retry stay queued.
func drain(w *Worker) {
	for i := 0; i < 100 && w.Step(); i++ {
	}
}
func retryNow(c *Control) {
	transaction(c.Settings.ControlDB, true, func(d *DB) { d.Exec("UPDATE outbox SET next_attempt_at=0 WHERE state<>'done'") })
}
func subscriptionProfile(t *testing.T, c *Control, key, user string) (string, func() string) {
	t.Helper()
	link := c.SubscriptionLink("admin", key, user)
	token := strings.Split(str(link["url"]), "/")[4]
	return token, func() string { return string(c.Public.Read(token, "surge.conf")) }
}

func TestClusterCreateContinuesPastAnUnreachableNode(t *testing.T) {
	c := clusterControl(t)
	f := newFaultyCluster()
	f.down["node_sg"] = true
	w := NewWorker(c, f)
	op := str(c.CreateUser("admin", "cluster-create-key-01", CreateUser{DisplayName: "多节点", GroupIDs: []string{"grp_hk"}}, nil)["operation_id"])
	drain(w)
	if states := targetStates(c, op); states["node_hk"] != "verified" || states["node_jp"] != "verified" || states["node_sg"] != "unreachable" {
		t.Fatalf("reachable nodes must verify while the other waits: %v", states)
	}
	if state := operationState(c, op); state != "partial" {
		t.Fatalf("operation should report partial progress, got %s", state)
	}
	u := Views{c.Settings}.Users()[0]
	if u["status"] != "active" {
		t.Fatalf("user should be usable on the verified nodes, status=%v", u["status"])
	}
	_, profile := subscriptionProfile(t, c, "cluster-link-key-0001", str(u["id"]))
	if text := profile(); !strings.Contains(text, "node_hk-ANYTLS") || !strings.Contains(text, "node_jp-TUIC") || strings.Contains(text, "node_sg") {
		t.Fatalf("subscription must list exactly the verified nodes:\n%s", text)
	}
	f.mu.Lock()
	f.down["node_sg"] = false
	f.mu.Unlock()
	retryNow(c)
	drain(w)
	if state := operationState(c, op); state != "completed" {
		t.Fatalf("operation should complete after the node returns, got %s", state)
	}
	if text := profile(); !strings.Contains(text, "node_sg-ANYTLS") || !strings.Contains(text, "node_sg-TUIC") {
		t.Fatalf("the recovered node is missing from the same subscription link:\n%s", text)
	}
}

func TestClusterDisableRevokesSubscriptionBeforeAnUnreachableNodeRecovers(t *testing.T) {
	c := clusterControl(t)
	f := newFaultyCluster()
	w := NewWorker(c, f)
	c.CreateUser("admin", "cluster-create-key-02", CreateUser{DisplayName: "待禁用", GroupIDs: []string{"grp_hk"}}, nil)
	drain(w)
	u := Views{c.Settings}.Users()[0]
	user := str(u["id"])
	token, _ := subscriptionProfile(t, c, "cluster-link-key-0002", user)
	for _, node := range []string{"node_hk", "node_sg", "node_jp"} {
		if !holdsUser(f.latest(node), user) {
			t.Fatalf("%s never received the user", node)
		}
	}
	f.mu.Lock()
	f.down["node_jp"] = true
	f.mu.Unlock()
	revision := integer(u["revision"])
	op := str(c.DisableUser("admin", "cluster-disable-key-02", user, DisableUser{Revision: &revision, Reason: "撤销"})["operation_id"])
	expectFault(t, "NOT_FOUND", func() { c.Public.Read(token, "surge.conf") })
	drain(w)
	if states := targetStates(c, op); states["node_hk"] != "verified" || states["node_sg"] != "verified" || states["node_jp"] != "unreachable" {
		t.Fatalf("reachable nodes must revoke without waiting: %v", states)
	}
	if holdsUser(f.latest("node_hk"), user) || holdsUser(f.latest("node_sg"), user) {
		t.Fatal("a reachable node still holds the disabled user")
	}
	if state := operationState(c, op); state == "completed" {
		t.Fatal("the operation cannot complete while one node still holds the credentials")
	}
	f.mu.Lock()
	f.down["node_jp"] = false
	f.mu.Unlock()
	retryNow(c)
	drain(w)
	if last := f.latest("node_jp"); holdsUser(last, user) || len(last.Revoked) != 2 {
		t.Fatalf("the recovered node must drop the user and record both revocations: users=%d revoked=%d", len(last.Users), len(last.Revoked))
	}
	if state := operationState(c, op); state != "completed" {
		t.Fatalf("disable should complete after the node returns, got %s", state)
	}
}

func TestClusterNodeFailureIsContainedAndAlerted(t *testing.T) {
	c := clusterControl(t)
	f := newFaultyCluster()
	f.state["node_sg"] = "failed"
	w := NewWorker(c, f)
	op := str(c.CreateUser("admin", "cluster-create-key-03", CreateUser{DisplayName: "节点失败", GroupIDs: []string{"grp_hk"}}, nil)["operation_id"])
	drain(w)
	if states := targetStates(c, op); states["node_hk"] != "verified" || states["node_jp"] != "verified" || states["node_sg"] != "failed" {
		t.Fatalf("a failing node must not affect the others: %v", states)
	}
	alertState := func() string {
		var state string
		transaction(c.Settings.ControlDB, false, func(d *DB) {
			state = str(d.Value("SELECT state FROM alerts WHERE dedupe_key='node-operation:node_sg'"))
		})
		return state
	}
	if operationState(c, op) != "failed" || alertState() != "open" {
		t.Fatalf("failure should be reported and alerted: operation=%s alert=%s", operationState(c, op), alertState())
	}
	f.mu.Lock()
	f.state["node_sg"] = ""
	f.mu.Unlock()
	retryNow(c)
	drain(w)
	if operationState(c, op) != "completed" || alertState() != "resolved" {
		t.Fatalf("recovery should complete the operation and resolve the alert: operation=%s alert=%s", operationState(c, op), alertState())
	}
}

func TestClusterRecoveredNodeReceivesRevocationWithItsFirstRequest(t *testing.T) {
	c := clusterControl(t)
	f := newFaultyCluster()
	w := NewWorker(c, f)
	c.CreateUser("admin", "cluster-create-key-04", CreateUser{DisplayName: "先建后禁", GroupIDs: []string{"grp_hk"}}, nil)
	drain(w)
	first := Views{c.Settings}.Users()[0]
	f.mu.Lock()
	f.down["node_hk"] = true
	before := len(f.accepted["node_hk"])
	f.mu.Unlock()
	created := str(c.CreateUser("admin", "cluster-create-key-05", CreateUser{DisplayName: "失联期间新增", GroupIDs: []string{"grp_hk"}}, nil)["operation_id"])
	drain(w)
	revision := integer(first["revision"])
	disabled := str(c.DisableUser("admin", "cluster-disable-key-04", str(first["id"]), DisableUser{Revision: &revision, Reason: "撤销"})["operation_id"])
	drain(w)
	var second string
	users := Views{c.Settings}.Users()
	for _, u := range users {
		if u["id"] != first["id"] {
			second = str(u["id"])
		}
	}
	f.mu.Lock()
	f.down["node_hk"] = false
	f.mu.Unlock()
	retryNow(c)
	if !w.Step() {
		t.Fatal("no task was due for the recovered node")
	}
	f.mu.Lock()
	history := f.accepted["node_hk"]
	f.mu.Unlock()
	if len(history) != before+1 {
		t.Fatalf("expected exactly one request after recovery, got %d", len(history)-before)
	}
	if r := history[before]; holdsUser(r, str(first["id"])) || !holdsUser(r, second) || len(r.Revoked) != 2 {
		t.Fatalf("the first request after recovery must already revoke the disabled user: users=%d revoked=%d", len(r.Users), len(r.Revoked))
	}
	drain(w)
	if operationState(c, created) != "completed" || operationState(c, disabled) != "completed" {
		t.Fatalf("both operations should finish: create=%s disable=%s", operationState(c, created), operationState(c, disabled))
	}
}
