package sbctl

import (
	"net/url"
	"path/filepath"
	"testing"
	"time"
)

type testConnectionFeed struct{ ledger Ledger }

func (f testConnectionFeed) Call(_ NodeConnection, body M, stats bool) M {
	if !stats || body["action"] != "connections" {
		panic("unexpected transport call")
	}
	return object(canonical(f.ledger.connectionPage(integer(body["after"]))))
}

func TestConnectionFeedPagesAndResumes(t *testing.T) {
	c := testControl(t)
	root := t.TempDir()
	ledger := Ledger{filepath.Join(root, "node.db"), filepath.Join(root, "snapshot.json"), "node_hk"}
	ledger.Create()
	runtime := testRuntime()
	runtime.Statistics = &StatisticsManifest{Inbounds: []InboundIdentity{{Tag: "entry", Protocol: "anytls", InboundID: "inb_test"}}}
	end := time.Now().UTC().Add(-time.Minute)
	var rows []TrafficConnection
	for range connectionPageSize + 1 {
		rows = append(rows, TrafficConnection{ID: identifier("conn"), Inbound: "entry", InboundType: "anytls", Domain: "example.com", Upload: "42", Download: "24", StartedAt: connectionTime(end.Add(-time.Second)), ClosedAt: connectionTime(end)})
	}
	ledger.IngestWithConnections(runtime, runtime, nil, rows[:connectionPageSize], "epoch_a", connectionPageSize, true, false)
	ledger.IngestWithConnections(runtime, runtime, nil, rows[connectionPageSize:], "epoch_a", connectionPageSize+1, false, false)
	snapshot := ledger.Snapshot()
	if !snapshot.ConnectionFeed || len(snapshot.Connections) != 0 {
		t.Fatal("cumulative snapshot contains connection history")
	}
	batch := ledger.connectionPage(0)
	if !batch.More || batch.Next != connectionPageSize || batch.Items[0].InboundID != "inb_test" {
		t.Fatalf("invalid page cursor or attribution: %d, %t", batch.Next, batch.More)
	}
	traffic := Traffic{c.Settings.TrafficDB, c.Settings.TrafficSnapshot}
	traffic.Initialize()
	traffic.Ingest(ledger.NodeID, snapshot)
	for range 2 {
		traffic.syncConnectionPages(testConnectionFeed{ledger}, NodeConnection{}, ledger.NodeID, snapshot)
	}
	report := (Views{c.Settings}).TrafficConnections(url.Values{"node": {ledger.NodeID}, "protocol": {"anytls"}})
	if integer(report["total"]) != connectionPageSize+1 || report["history_gap"] != true {
		t.Fatalf("pagination failed to persist traffic or gap: total=%v gap=%v", report["total"], report["history_gap"])
	}
}
