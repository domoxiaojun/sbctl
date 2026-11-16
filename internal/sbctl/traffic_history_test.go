package sbctl

import (
	"net/url"
	"path/filepath"
	"testing"
	"time"
)

func historySnapshot(seq int64, sampled string, upload, download string) Snapshot {
	s := Snapshot{SchemaVersion: 1, NodeID: "node_hk", LedgerID: "ledger_history", Seq: seq, StartedAt: "2026-01-01T00:00:00Z", SampledAt: &sampled, Users: []LedgerUser{{"usr_test", upload, download}}, Inbounds: []LedgerInbound{{Tag: "vmess", Protocol: "vmess", Port: 23323, Upload: upload, Download: download, StartedAt: "2026-01-01T00:00:00Z", Active: true}}, LocalUsers: []LedgerLocalUser{{Name: "local", Upload: upload, Download: download, StartedAt: "2026-01-01T00:00:00Z", Active: true}}, TotalInbounds: 1, Quality: DataQuality{Integrity: "ok"}}
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
