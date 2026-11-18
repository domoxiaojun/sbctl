package sbctl

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInventoryPublishReadsAfterTakingSharedLock(t *testing.T) {
	c := testControl(t)
	unlock := fileLock(c.Settings.InventoryFile+".lock", time.Second)
	var once sync.Once
	release := func() { once.Do(unlock) }
	defer release()
	done := make(chan error, 2)
	for range 2 {
		w := NewWorker(c, nil)
		go func() { done <- Attempt(w.ExportInventory) }()
	}
	select {
	case err := <-done:
		t.Fatalf("inventory publisher ignored the shared lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		connection := object([]byte(str(d.Value("SELECT connection_json FROM nodes WHERE id='node_hk'"))))
		connection["address"] = "100.70.0.99"
		d.Exec("UPDATE nodes SET connection_json=? WHERE id='node_hk'", string(canonical(connection)))
	})
	release()
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	nodes := array(object(read(c.Settings.InventoryFile))["nodes"])
	if len(nodes) != 1 || mapping(mapping(nodes[0])["connection"])["address"] != "100.70.0.99" {
		t.Fatalf("publisher used a snapshot taken before acquiring the lock: %v", nodes)
	}
}

type maintenanceQueueTransport struct{ *gatedTransport }

func (tr *maintenanceQueueTransport) Call(n NodeConnection, body M, stats bool) M {
	switch body["action"] {
	case "maintenance_receipt", "restart", "protocol_change":
		tr.mu.Lock()
		tr.calls[str(body["action"])]++
		tr.mu.Unlock()
		if body["action"] == "maintenance_receipt" {
			return M{"state": "missing"}
		}
		return M{"node_id": "node_hk", "ready": true, "template_hash": strings.Repeat("a", 64),
			"catalog":   ProtocolCatalog{NodeID: "node_hk", Hash: strings.Repeat("c", 64), CheckedAt: now(), Items: []ProtocolItem{}},
			"endpoints": []Endpoint{{"anytls", "anytls-in", "example.test", 443, "example.test", nil, ""}, {"tuic", "tuic-in", "example.test", 4443, "example.test", nil, ""}}}
	}
	return tr.gatedTransport.Call(n, body, stats)
}

func TestQueuedMaintenanceWaitsForInFlightRevocation(t *testing.T) {
	for _, kind := range []string{"restart", "protocol_change"} {
		t.Run(kind, func(t *testing.T) {
			c := testControl(t)
			tr := &maintenanceQueueTransport{newGatedTransport()}
			w := NewWorker(c, tr)
			c.CreateUser("admin", "review-queue-create-01", CreateUser{DisplayName: "维护排队", GroupIDs: []string{"grp_hk"}}, nil)
			w.Step()
			u := Views{c.Settings}.Users()[0]
			input := NodeJobInput{Kind: kind, Draft: draftFor(c)}
			if kind == "protocol_change" {
				input.Change = &ProtocolChange{Action: "edit", Tag: "legacy", BaseHash: strings.Repeat("b", 64), Listen: "127.0.0.1", Port: 18090}
			}
			job := c.SubmitNodeJob("admin", "review-queue-maintenance-01", input)
			revision := integer(u["revision"])
			op := c.DisableUser("admin", "review-queue-disable-01", str(u["id"]), DisableUser{Revision: &revision, Reason: "撤销"})
			release := tr.block("node_hk", "submit")
			stop := runWorker(t, w, 2)
			defer stop()
			defer release()
			waitFor(t, "revocation to start", func() bool { return tr.started("submit") == 2 })
			// Exercise the same claim query while the remote call is blocked.
			if claimed := NewWorker(c, tr).ClaimNodeJob(); claimed != nil {
				t.Fatalf("maintenance overlapped its node's authorization: %v", claimed)
			}
			if state := c.NodeJob(str(job["job_id"]))["state"]; state != "queued" {
				t.Fatalf("waiting maintenance became %v", state)
			}
			release()
			waitFor(t, "revocation and maintenance to complete", func() bool {
				return operationState(c, str(op["operation_id"])) == "completed" && c.NodeJob(str(job["job_id"]))["state"] == "completed"
			})
			if tr.started(kind) != 1 {
				t.Fatalf("maintenance should execute once, got %d", tr.started(kind))
			}
		})
	}
}

func TestMaintenanceWithoutReceiptStaysQueuedUntilAuthorizationCompletes(t *testing.T) {
	c := testControl(t)
	tr := &maintenanceQueueTransport{newGatedTransport()}
	w := NewWorker(c, tr)
	c.CreateUser("admin", "review-delay-create-01", CreateUser{DisplayName: "等待授权", GroupIDs: []string{"grp_hk"}}, nil)
	w.Step()
	u := Views{c.Settings}.Users()[0]
	job := c.SubmitNodeJob("admin", "review-delay-restart-01", NodeJobInput{Kind: "restart", Draft: draftFor(c)})
	revision := integer(u["revision"])
	c.DisableUser("admin", "review-delay-disable-01", str(u["id"]), DisableUser{Revision: &revision, Reason: "撤销"})
	// No authorization lease is held yet. The receipt query must still be
	// allowed for recovery, but a missing receipt should only delay the job.
	w.NodeStep()
	if result := c.NodeJob(str(job["job_id"])); result["state"] != "queued" || result["error_code"] != "" {
		t.Fatalf("normal authorization wait became a failure: %v", result)
	}
	if w.NodeStep() || tr.started("restart") != 0 {
		t.Fatal("delayed job was immediately reclaimed or remotely executed")
	}
	w.Step()
	transaction(c.Settings.ControlDB, true, func(d *DB) {
		d.Exec("UPDATE node_jobs SET lease_until=0 WHERE id=?", job["job_id"])
	})
	w.NodeStep()
	if result := c.NodeJob(str(job["job_id"])); result["state"] != "completed" {
		t.Fatalf("maintenance did not resume after authorization completed: %v", result)
	}
}
