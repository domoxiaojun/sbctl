package sbctl

import (
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// ingestSeries feeds one node with samples 170 seconds apart, each adding
// step bytes in both directions, and returns the time of the last sample.
func ingestSeries(t *testing.T, tr Traffic, first time.Time, samples int, step int64) time.Time {
	t.Helper()
	at := first
	for i := 0; i < samples; i++ {
		at = first.Add(time.Duration(i) * 170 * time.Second)
		total := fmt.Sprint(1000 + int64(i)*step)
		tr.Ingest("node_hk", historySnapshot(int64(i+1), at.UTC().Format(time.RFC3339Nano), total, total))
	}
	return at
}
func trendPoint(report M, date string) M {
	for _, raw := range array(report["trend"]) {
		if p := mapping(raw); p["date"] == date {
			return p
		}
	}
	return nil
}

func TestTrafficHourlyTrendMatchesDailyTotals(t *testing.T) {
	c := testControl(t)
	tr := Traffic{c.Settings.TrafficDB, c.Settings.TrafficSnapshot}
	tr.Initialize()
	// 23:58:20 on 1 January in UTC+8; 26 samples reach 01:09 on 2 January.
	first := time.Date(2026, 1, 1, 15, 58, 20, 0, time.UTC)
	ingestSeries(t, tr, first, 26, 100)
	view := Views{c.Settings}
	hourly := view.TrafficReport(url.Values{"from": {"2026-01-02"}, "to": {"2026-01-02"}, "granularity": {"hour"}})
	if hourly["granularity"] != "hour" || len(array(hourly["trend"])) != 24 {
		t.Fatalf("expected 24 hourly points, got granularity=%v points=%d", hourly["granularity"], len(array(hourly["trend"])))
	}
	midnight, one, two := trendPoint(hourly, "2026-01-02T00:00"), trendPoint(hourly, "2026-01-02T01:00"), trendPoint(hourly, "2026-01-02T02:00")
	if midnight["status"] != "complete" {
		t.Fatalf("a fully sampled hour must be complete: %v", midnight)
	}
	if one["status"] != "partial" {
		t.Fatalf("an hour that stops early must be partial: %v", one)
	}
	if two["status"] != "unavailable" || two["upload_bytes"] != nil {
		t.Fatalf("an hour without samples must be unavailable, not zero: %v", two)
	}
	daily := view.TrafficReport(url.Values{"from": {"2026-01-02"}, "to": {"2026-01-02"}})
	if daily["granularity"] != "day" {
		t.Fatalf("day is the default granularity, got %v", daily["granularity"])
	}
	day := trendPoint(daily, "2026-01-02")
	sum := new(big.Int)
	for _, raw := range array(hourly["trend"]) {
		if p := mapping(raw); p["upload_bytes"] != nil {
			sum.Add(sum, bytesInt(p["upload_bytes"]))
		}
	}
	if day == nil || sum.String() != str(day["upload_bytes"]) || sum.Sign() == 0 {
		t.Fatalf("hourly buckets must add up to the day: hours=%s day=%v", sum, day)
	}
	// 21 samples end inside 00:00-00:59 and each adds 100 bytes.
	if str(midnight["upload_bytes"]) != "2100" || str(midnight["download_bytes"]) != "2100" {
		t.Fatalf("unexpected bytes for the first hour: %v", midnight)
	}
	previous := view.TrafficReport(url.Values{"from": {"2026-01-01"}, "to": {"2026-01-01"}, "granularity": {"hour"}})
	if last := trendPoint(previous, "2026-01-01T23:00"); last["status"] != "partial" || str(last["upload_bytes"]) != "0" {
		t.Fatalf("the hour history starts in is partial and holds no delta yet: %v", last)
	}
}

func TestTrafficHourlyValidationAndDefaultRange(t *testing.T) {
	c := testControl(t)
	tr := Traffic{c.Settings.TrafficDB, c.Settings.TrafficSnapshot}
	tr.Initialize()
	view := Views{c.Settings}
	expectFault(t, "GRANULARITY_INVALID", func() { view.TrafficReport(url.Values{"granularity": {"week"}}) })
	expectFault(t, "DATE_RANGE_INVALID", func() {
		view.TrafficReport(url.Values{"from": {"2026-01-01"}, "to": {"2026-01-08"}, "granularity": {"hour"}})
	})
	week := view.TrafficReport(url.Values{"from": {"2026-01-01"}, "to": {"2026-01-07"}, "granularity": {"hour"}})
	if len(array(week["trend"])) != 7*24 {
		t.Fatalf("seven days are allowed and give 168 points, got %d", len(array(week["trend"])))
	}
	now := time.Now().In(trafficZone)
	recent := view.TrafficReport(url.Values{"granularity": {"hour"}})
	points := array(recent["trend"])
	if want := 24 + now.Hour() + 1; len(points) < want-1 || len(points) > want+1 {
		t.Fatalf("without dates the trend covers yesterday and today up to the current hour: got %d points, want about %d", len(points), want)
	}
	if recent["trend_from"] != now.AddDate(0, 0, -1).Format(time.DateOnly) && recent["trend_from"] != now.AddDate(0, 0, -2).Format(time.DateOnly) {
		t.Fatalf("unexpected start of the default hourly trend: %v", recent["trend_from"])
	}
}

func TestTrafficHourlyRetentionFollowsTheData(t *testing.T) {
	c := testControl(t)
	tr := Traffic{c.Settings.TrafficDB, c.Settings.TrafficSnapshot}
	tr.Initialize()
	first := time.Date(2026, 3, 10, 2, 0, 0, 0, time.UTC)
	tr.Ingest("node_hk", historySnapshot(1, first.Format(time.RFC3339Nano), "1000", "1000"))
	dir := filepath.Dir(hoursPath(c.Settings.TrafficSnapshot, "node_hk", "2026-03-10"))
	stale, kept := filepath.Join(dir, "2026-01-01.hours.json"), filepath.Join(dir, "2026-02-20.hours.json")
	for _, path := range []string{stale, kept} {
		atomicWrite(path, []byte(`{"hours":{}}`), 0644)
	}
	transaction(tr.Path, true, func(d *DB) {
		d.Exec("INSERT INTO history_hours VALUES ('node_hk','2026-01-01T00','{}',0),('node_hk','2026-02-20T00','{}',0),('node_sg','2026-01-01T00','{}',0)")
	})
	// The next sample opens a new hour and a new day in UTC+8.
	next := time.Date(2026, 3, 10, 16, 0, 30, 0, time.UTC)
	tr.Ingest("node_hk", historySnapshot(2, next.Format(time.RFC3339Nano), "1100", "1100"))
	var hours []M
	transaction(tr.Path, false, func(d *DB) { hours = d.Rows("SELECT node_id,hour FROM history_hours ORDER BY node_id,hour") })
	got := []string{}
	for _, h := range hours {
		got = append(got, str(h["node_id"])+" "+str(h["hour"]))
	}
	want := []string{"node_hk 2026-02-20T00", "node_hk 2026-03-10T10", "node_hk 2026-03-11T00", "node_sg 2026-01-01T00"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("only this node's hours beyond the retained window are dropped:\n got %v\nwant %v", got, want)
	}
	if exists(stale) || !exists(kept) || !exists(hoursPath(c.Settings.TrafficSnapshot, "node_hk", "2026-03-11")) {
		t.Fatalf("hourly exports must follow the same window: stale=%v kept=%v", exists(stale), exists(kept))
	}
	if !exists(historyPath(c.Settings.TrafficSnapshot, "node_hk", "2026-03-10")) {
		t.Fatal("daily history is never pruned")
	}
}

func TestHistoryDocumentReusesUnchangedFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "2026-01-01.json")
	if historyDocument(path) != nil {
		t.Fatal("a missing export must read as nil")
	}
	atomicWrite(path, []byte(`{"incomplete":false,"inbounds":{"a":{"upload_bytes":"1"}}}`), 0644)
	first := historyDocument(path)
	if reflect.ValueOf(first).Pointer() != reflect.ValueOf(historyDocument(path)).Pointer() {
		t.Fatal("an unchanged export must not be parsed again")
	}
	// Same size, replaced by rename: only the inode tells the two files apart.
	atomicWrite(path, []byte(`{"incomplete":false,"inbounds":{"a":{"upload_bytes":"2"}}}`), 0644)
	if got := mapping(mapping(historyDocument(path)["inbounds"])["a"])["upload_bytes"]; got != "2" {
		t.Fatalf("a replaced export must be read again, got %v", got)
	}
	check(os.Remove(path))
	if historyDocument(path) != nil {
		t.Fatal("a removed export must read as nil")
	}
}
