package sbctl

import (
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func historySnapshot(seq int64, sampled string, upload, download string) Snapshot {
	s := Snapshot{SchemaVersion: 1, NodeID: "node_hk", LedgerID: "ledger_history", Seq: seq, StartedAt: "2026-01-01T00:00:00Z", SampledAt: &sampled, Inbounds: []LedgerInbound{{Tag: "vmess", Protocol: "vmess", Port: 23323, Upload: upload, Download: download, StartedAt: "2026-01-01T00:00:00Z", Active: true}}, LocalUsers: []LedgerLocalUser{{Name: "local", Upload: upload, Download: download, StartedAt: "2026-01-01T00:00:00Z", Active: true}}, TotalInbounds: 1, Quality: DataQuality{Integrity: "ok"}}
	s.Seal()
	return s
}
func TestTrafficHistoryUpgradeDatesAndFilters(t *testing.T) {
	c := testControl(t)
	tr := Traffic{c.Settings.TrafficDB, c.Settings.TrafficSnapshot}
	tr.Initialize()
	base := historySnapshot(1, "2026-01-01T15:59:40Z", "9007199254740993", "200")
	tr.Ingest("node_hk", base)
	next := historySnapshot(2, "2026-01-01T15:59:55Z", "9007199254741093", "400")
	tr.Ingest("node_hk", next)
	tr.Ingest("node_hk", next)
	next = historySnapshot(3, "2026-01-01T16:00:10Z", "9007199254741393", "800")
	tr.Ingest("node_hk", next)
	view := Views{c.Settings}
	result := view.TrafficReport(url.Values{"from": {"2026-01-02"}, "to": {"2026-01-02"}, "node": {"node_hk"}, "protocol": {"vmess"}, "q": {"23323"}})
	node := mapping(array(result["nodes"])[0])
	row := mapping(array(node["inbounds"])[0])
	if row["upload_bytes"] != "300" || row["download_bytes"] != "400" || node["inbound_upload_bytes"] != "300" {
		t.Fatal("UTC+8 day or integer precision", row)
	}
	result = view.TrafficReport(url.Values{"from": {"2026-01-01"}, "to": {"2026-01-01"}})
	row = mapping(array(mapping(array(result["nodes"])[0])["inbounds"])[0])
	if row["upload_bytes"] != "100" || row["download_bytes"] != "200" {
		t.Fatal("duplicate or baseline counted", row)
	}
	if mapping(array(result["periods"])[0])["status"] != "partial" {
		t.Fatal("baseline date must be partial")
	}
	if mapping(array(view.TrafficReport(url.Values{"from": {"2025-12-31"}, "to": {"2025-12-31"}})["periods"])[0])["status"] != "unavailable" {
		t.Fatal("missing history is not zero")
	}
	all := view.TrafficReport(url.Values{})
	if mapping(array(all["nodes"])[0])["inbound_upload_bytes"] != next.Inbounds[0].Upload {
		t.Fatal("lifetime changed")
	}
	if len(array(mapping(array(view.TrafficReport(url.Values{"protocol": {"shadowsocks"}})["nodes"])[0])["inbounds"])) != 0 {
		t.Fatal("protocol filter")
	}
	if len(array(view.TrafficReport(url.Values{"node": {"other"}})["nodes"])) != 0 {
		t.Fatal("node filter")
	}
	tr.Initialize() // Additive upgrade and restart retain both lifetime and daily totals.
	if !exists(filepath.Join(filepath.Dir(tr.Export), "history", "node_hk", "2026-01-02.json")) {
		t.Fatal("history not exported")
	}
}

func TestTrafficPortChangePreservesHistoryAndSnapshot(t *testing.T) {
	c := testControl(t)
	tr := Traffic{c.Settings.TrafficDB, c.Settings.TrafficSnapshot}
	tr.Initialize()
	first := historySnapshot(1, "2026-01-01T00:00:00Z", "100", "200")
	tr.Ingest("node_hk", first)
	next := historySnapshot(2, "2026-01-01T00:00:15Z", "140", "260")
	next.Inbounds[0].Port = 24424
	next.Seal()
	if !tr.Ingest("node_hk", next) {
		t.Fatal("port change did not advance central snapshot")
	}
	var stored Snapshot
	transaction(tr.Path, false, func(d *DB) {
		decode([]byte(str(d.Value("SELECT document FROM snapshots WHERE node_id='node_hk'"))), &stored)
	})
	if stored.Seq != 2 || stored.Inbounds[0].Port != 24424 || stored.Inbounds[0].Upload != "140" {
		t.Fatalf("port change lost central totals: %+v", stored.Inbounds)
	}
}

func TestTrafficConnectionsAreStoredAndQueryable(t *testing.T) {
	c := testControl(t)
	tr := Traffic{c.Settings.TrafficDB, c.Settings.TrafficSnapshot}
	tr.Initialize()
	first := historySnapshot(1, "2026-01-01T00:00:00Z", "100", "200")
	started := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339Nano)
	closed := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	first.Connections = []TrafficConnection{{ID: "conn_1", InboundID: "inb_one", Inbound: "anytls-in", InboundType: "anytls", Network: "tcp", Domain: "example.com", Destination: "93.184.216.34:443", Source: "10.0.0.2:50000", Outbound: "direct", OutboundType: "direct", Chain: []string{"direct"}, Rule: "final", Upload: "12", Download: "34", StartedAt: started, ClosedAt: closed}}
	first.Seal()
	tr.Ingest("node_hk", first)
	today := time.Now().In(trafficZone).Format(time.DateOnly)
	report := (Views{c.Settings}).TrafficConnections(url.Values{"from": {today}, "to": {today}, "inbound": {"anytls"}})
	items := array(report["items"])
	if len(items) != 1 || str(mapping(items[0])["connection_id"]) != "conn_1" || str(mapping(items[0])["upload_bytes"]) != "12" {
		t.Fatalf("connection detail missing: %#v", report)
	}
}

func TestConnectionSequenceResetsWithRuntimeEpoch(t *testing.T) {
	c := testControl(t)
	tr := Traffic{c.Settings.TrafficDB, c.Settings.TrafficSnapshot}
	tr.Initialize()
	first := historySnapshot(1, "2026-01-01T00:00:00Z", "100", "200")
	first.SourceEpoch = nullable("container-a@2026-01-01T00:00:00Z")
	first.ConnectionSeq = 8
	first.Seal()
	tr.Ingest("node_hk", first)
	second := historySnapshot(2, "2026-01-01T00:00:15Z", "110", "220")
	second.SourceEpoch = nullable("container-b@2026-01-01T00:00:15Z")
	second.ConnectionSeq = 0
	second.Seal()
	if !tr.Ingest("node_hk", second) {
		t.Fatal("runtime epoch change did not advance snapshot")
	}
}
func TestTrafficHistoryGapAndExportRecovery(t *testing.T) {
	c := testControl(t)
	tr := Traffic{c.Settings.TrafficDB, c.Settings.TrafficSnapshot}
	tr.Initialize()
	tr.Ingest("node_hk", historySnapshot(1, "2026-01-01T00:00:00Z", "100", "100"))
	tr.Ingest("node_hk", historySnapshot(2, "2026-01-01T02:00:00Z", "1000", "2000"))
	tr.Ingest("node_hk", historySnapshot(3, "2026-01-01T02:00:15Z", "1050", "2100"))
	result := Views{c.Settings}.TrafficReport(url.Values{"from": {"2026-01-01"}, "to": {"2026-01-01"}})
	if mapping(array(result["nodes"])[0])["inbound_upload_bytes"] != "50" {
		t.Fatal("unattributable gap must not be assigned to date")
	}
	if mapping(array(result["periods"])[0])["status"] != "partial" {
		t.Fatal("gap hidden")
	}
	transaction(tr.Path, true, func(d *DB) { d.Exec("UPDATE history_days SET dirty=1") })
	tr.WriteExport()
	transaction(tr.Path, false, func(d *DB) {
		if integer(d.Value("SELECT count(*) FROM history_days WHERE dirty=1")) != 0 {
			t.Fatal("export not recovered")
		}
	})
	bad := historySnapshot(4, "2026-01-01T02:00:30Z", "1", "1")
	expectFault(t, "TOTAL_REGRESSION", func() { tr.Ingest("node_hk", bad) })
}
func TestTrafficDateValidation(t *testing.T) {
	for _, q := range []url.Values{{"from": {"2026-01-01"}}, {"from": {"2026-02-30"}, "to": {"2026-03-01"}}, {"from": {"2026-02-02"}, "to": {"2026-02-01"}}, {"from": {"2020-01-01"}, "to": {"2026-01-01"}}, {"from": {"2099-01-01"}, "to": {"2099-01-02"}}, {"from": {"../../bad"}, "to": {"2026-01-01"}}} {
		expectFault(t, "DATE_RANGE_INVALID", func() { trafficDates(q) })
	}
	a, b := trafficDates(url.Values{"from": {"2026-01-01"}, "to": {"2026-01-01"}})
	if b.Sub(a) != 24*time.Hour || a.UTC().Format(time.RFC3339) != "2025-12-31T16:00:00Z" {
		t.Fatal("inclusive local date")
	}
}

func TestTrafficHistoryExistingSnapshotBaseline(t *testing.T) {
	c := testControl(t)
	tr := Traffic{c.Settings.TrafficDB, c.Settings.TrafficSnapshot}
	tr.Initialize()
	base := historySnapshot(7, "2026-01-01T10:00:00Z", "1000", "2000")
	tr.Ingest("node_hk", base)
	// Simulate a pre-history database: its latest cumulative snapshot remains.
	transaction(tr.Path, true, func(d *DB) {
		d.Exec("DROP TABLE history_days")
		d.Exec("DROP TABLE history_nodes")
	})
	tr.Initialize()
	if tr.Ingest("node_hk", base) {
		t.Fatal("unchanged snapshot must not advance the cumulative ledger")
	}
	tr.Ingest("node_hk", historySnapshot(8, "2026-01-01T10:00:15Z", "1100", "2200"))
	view := Views{c.Settings}
	result := view.TrafficReport(url.Values{"from": {"2026-01-01"}, "to": {"2026-01-01"}})
	node := mapping(array(result["nodes"])[0])
	if node["inbound_upload_bytes"] != "100" || node["inbound_download_bytes"] != "200" {
		t.Fatal("pre-upgrade cumulative traffic was assigned to the upgrade date", node)
	}
	if mapping(array(result["periods"])[0])["history_started_at"] != *base.SampledAt {
		t.Fatal("upgrade baseline was not retained")
	}
	if mapping(array(view.TrafficReport(url.Values{})["nodes"])[0])["inbound_upload_bytes"] != "1100" {
		t.Fatal("upgrade changed cumulative traffic")
	}
}

func TestTrafficTrendFiltersAndMissingDays(t *testing.T) {
	c := testControl(t)
	tr := Traffic{c.Settings.TrafficDB, c.Settings.TrafficSnapshot}
	tr.Initialize()
	for i, sampled := range []string{"2026-01-01T15:59:40Z", "2026-01-01T15:59:55Z", "2026-01-01T16:00:10Z"} {
		snapshot := historySnapshot(int64(i+1), sampled, []string{"9007199254740993", "9007199254741093", "9007199254741393"}[i], []string{"200", "400", "800"}[i])
		snapshot.LocalUsers = append(snapshot.LocalUsers, LedgerLocalUser{Name: "local-other", Upload: "500", Download: "500", StartedAt: snapshot.StartedAt, Active: true})
		snapshot.Seal()
		tr.Ingest("node_hk", snapshot)
	}
	for _, scope := range []string{"inbounds", "local"} {
		query := url.Values{"from": {"2026-01-01"}, "to": {"2026-01-03"}, "scope": {scope}, "local_user": {"local"}}
		report := Views{c.Settings}.TrafficReport(query)
		points := array(report["trend"])
		if len(points) != 3 || mapping(points[0])["upload_bytes"] != "100" || mapping(points[1])["upload_bytes"] != "300" {
			t.Fatal(scope, "daily totals", points)
		}
		if mapping(points[2])["upload_bytes"] != nil || mapping(points[2])["status"] != "unavailable" {
			t.Fatal("missing date fabricated as zero", points)
		}
		if len(array(mapping(array(report["nodes"])[0])["local_users"])) != 1 {
			t.Fatal("exact local user selection failed")
		}
	}
	noMatch := Views{c.Settings}.TrafficReport(url.Values{"from": {"2026-01-01"}, "to": {"2026-01-01"}, "protocol": {"shadowsocks"}})
	if mapping(array(noMatch["trend"])[0])["upload_bytes"] != "0" {
		t.Fatal("filtered known day must have zero usage")
	}
	unknown := Views{c.Settings}.TrafficReport(url.Values{"from": {"2026-01-01"}, "to": {"2026-01-01"}, "node": {"other"}})
	if mapping(array(unknown["trend"])[0])["upload_bytes"] != nil {
		t.Fatal("unsampled node fabricated as zero")
	}
	expectFault(t, "SCOPE_INVALID", func() { Views{c.Settings}.TrafficReport(url.Values{"scope": {"other"}}) })
}

func TestTrafficTrendMixedNodeCoverage(t *testing.T) {
	c := testControl(t)
	tr := Traffic{c.Settings.TrafficDB, c.Settings.TrafficSnapshot}
	tr.Initialize()
	tr.Ingest("node_hk", historySnapshot(1, "2026-01-01T10:00:00Z", "100", "200"))
	atomicWrite(historyPath(tr.Export, "node_hk", "2026-01-01"), canonical(M{"from": "2025-12-31T16:00:00Z", "to": "2026-01-01T16:00:00Z", "incomplete": false, "inbounds": M{"vmess": M{"upload_bytes": "100", "download_bytes": "200"}}}), 0644)
	query := url.Values{"from": {"2026-01-01"}, "to": {"2026-01-01"}}
	report := Views{c.Settings}.TrafficReport(query)
	if mapping(array(report["trend"])[0])["status"] != "complete" {
		t.Fatal("complete date not recognized")
	}
	c.Register(M{"id": "node_other", "name": "未采样节点", "region": "hk", "location": "香港", "connection": M{"address": "100.70.0.11"}, "endpoints": []Endpoint{{"anytls", "anytls-in", "other.test", 443, "other.test", nil, ""}, {"tuic", "tuic-in", "other.test", 443, "other.test", nil, ""}}, "template_hash": strings.Repeat("a", 64), "image_digest": "sha256:" + strings.Repeat("b", 64), "group_id": "grp_hk", "group_name": "香港"})
	report = Views{c.Settings}.TrafficReport(query)
	point := mapping(array(report["trend"])[0])
	if point["status"] != "partial" || point["upload_bytes"] != "100" {
		t.Fatal("missing node coverage hidden", point)
	}
	query.Set("node", "node_other")
	point = mapping(array(Views{c.Settings}.TrafficReport(query)["trend"])[0])
	if point["status"] != "unavailable" || point["upload_bytes"] != nil {
		t.Fatal("missing node should be unknown")
	}
}
