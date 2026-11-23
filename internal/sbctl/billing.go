package sbctl

import (
	"math"
	"math/big"
	"time"
)

type NodeBilling struct {
	QuotaBytes     string `json:"quota_bytes"`
	ResetDay       int    `json:"reset_day"`
	AccountingMode string `json:"accounting_mode"`
	WarnPercent    int    `json:"warn_percent"`
}

func defaultNodeBilling() NodeBilling {
	return NodeBilling{
		QuotaBytes:     "0",
		ResetDay:       1,
		AccountingMode: "both",
		WarnPercent:    80,
	}
}

func (b *NodeBilling) NormalizeAndValidate() int64 {
	if b.QuotaBytes == "" {
		b.QuotaBytes = "0"
	}
	quota := counter(b.QuotaBytes)
	if len(b.QuotaBytes) > 1 && b.QuotaBytes[0] == '0' {
		fail("INVALID_REQUEST", "月度流量配额格式无效", 422)
	}
	if b.ResetDay == 0 {
		b.ResetDay = 1
	}
	if b.ResetDay < 1 || b.ResetDay > 31 {
		fail("INVALID_REQUEST", "账单重置日须在 1 到 31 之间", 422)
	}
	if b.AccountingMode == "" {
		b.AccountingMode = "both"
	}
	if !contains([]string{"both", "download_only", "upload_only", "max_direction"}, b.AccountingMode) {
		fail("INVALID_REQUEST", "计费方向模式无效", 422)
	}
	if b.WarnPercent == 0 {
		b.WarnPercent = 80
	}
	if b.WarnPercent < 1 || b.WarnPercent > 100 {
		fail("INVALID_REQUEST", "告警阈值百分比须在 1 到 100 之间", 422)
	}
	return quota
}

func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func clampedDay(year int, month time.Month, day int) int {
	last := daysInMonth(year, month)
	if day > last {
		return last
	}
	if day < 1 {
		return 1
	}
	return day
}

func billingCycleWindow(nowTime time.Time, resetDay int) (time.Time, time.Time) {
	local := nowTime.In(trafficZone)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, trafficZone)
	thisReset := time.Date(today.Year(), today.Month(), clampedDay(today.Year(), today.Month(), resetDay), 0, 0, 0, 0, trafficZone)
	if !today.Before(thisReset) {
		nextFirst := time.Date(today.Year(), today.Month()+1, 1, 0, 0, 0, 0, trafficZone)
		nextReset := time.Date(nextFirst.Year(), nextFirst.Month(), clampedDay(nextFirst.Year(), nextFirst.Month(), resetDay), 0, 0, 0, 0, trafficZone)
		return thisReset, nextReset
	}
	prevFirst := time.Date(today.Year(), today.Month()-1, 1, 0, 0, 0, 0, trafficZone)
	prevReset := time.Date(prevFirst.Year(), prevFirst.Month(), clampedDay(prevFirst.Year(), prevFirst.Month(), resetDay), 0, 0, 0, 0, trafficZone)
	return prevReset, thisReset
}

func loadNodeBillingMap(d *DB) map[string]NodeBilling {
	out := map[string]NodeBilling{}
	for _, r := range d.Rows("SELECT node_id,quota_bytes,reset_day,accounting_mode,warn_percent FROM node_billing") {
		out[str(r["node_id"])] = NodeBilling{
			QuotaBytes:     str(r["quota_bytes"]),
			ResetDay:       int(integer(r["reset_day"])),
			AccountingMode: str(r["accounting_mode"]),
			WarnPercent:    int(integer(r["warn_percent"])),
		}
	}
	return out
}

func saveNodeBilling(d *DB, nodeID string, b *NodeBilling) {
	if b == nil {
		return
	}
	quota := b.NormalizeAndValidate()
	d.Exec(`INSERT INTO node_billing(node_id,quota_bytes,reset_day,accounting_mode,warn_percent,updated_at)
VALUES (?,?,?,?,?,?) ON CONFLICT(node_id) DO UPDATE SET
quota_bytes=excluded.quota_bytes,reset_day=excluded.reset_day,accounting_mode=excluded.accounting_mode,warn_percent=excluded.warn_percent,updated_at=excluded.updated_at`,
		nodeID, quota, b.ResetDay, b.AccountingMode, b.WarnPercent, now())
}

func computeNodeBillingView(s Settings, nodeID string, cfg NodeBilling, nowTime time.Time) M {
	if cfg.ResetDay < 1 || cfg.ResetDay > 31 {
		cfg = defaultNodeBilling()
	}
	cycleStart, cycleEnd := billingCycleWindow(nowTime, cfg.ResetDay)
	localNow := nowTime.In(trafficZone)
	tomorrow := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, trafficZone).AddDate(0, 0, 1)
	scanEnd := cycleEnd
	if scanEnd.After(tomorrow) {
		scanEnd = tomorrow
	}

	upSum, downSum := new(big.Int), new(big.Int)
	seen, incomplete := false, false
	for day := cycleStart; day.Before(scanEnd); day = day.AddDate(0, 0, 1) {
		doc := historyDocument(historyPath(s.TrafficSnapshot, nodeID, day.Format(time.DateOnly)))
		if doc == nil {
			incomplete = true
			continue
		}
		seen = true
		reqEnd := day.AddDate(0, 0, 1)
		if reqEnd.After(nowTime) {
			reqEnd = nowTime
		}
		partial := stamp(str(doc["from"])).After(day) || stamp(str(doc["to"])).Before(reqEnd.Add(-180*time.Second)) || boolean(doc["incomplete"])
		incomplete = incomplete || partial
		for _, raw := range mapping(doc["inbounds"]) {
			m := mapping(raw)
			upSum.Add(upSum, bytesInt(m["upload_bytes"]))
			downSum.Add(downSum, bytesInt(m["download_bytes"]))
		}
	}

	used := new(big.Int)
	switch cfg.AccountingMode {
	case "download_only":
		used.Set(downSum)
	case "upload_only":
		used.Set(upSum)
	case "max_direction":
		if upSum.Cmp(downSum) >= 0 {
			used.Set(upSum)
		} else {
			used.Set(downSum)
		}
	default:
		used.Add(upSum, downSum)
	}

	quota := bytesInt(cfg.QuotaBytes)
	state := "unconfigured"
	percent := 0.0
	if quota.Sign() > 0 {
		scaled := new(big.Int).Mul(used, big.NewInt(10000))
		scaled.Div(scaled, quota)
		if scaled.IsInt64() {
			percent = math.Round(float64(scaled.Int64())) / 100.0
		} else {
			percent = 9999.99
		}
		state = "normal"
		if used.Cmp(quota) >= 0 {
			state = "exceeded"
		} else if percent >= float64(cfg.WarnPercent) {
			state = "warning"
		}
	}

	completeness := "complete"
	if !seen {
		completeness = "unavailable"
	} else if incomplete {
		completeness = "partial"
	}

	return M{
		"quota_bytes":     quota.String(),
		"used_bytes":      used.String(),
		"upload_bytes":    upSum.String(),
		"download_bytes":  downSum.String(),
		"used_percent":    percent,
		"reset_day":       cfg.ResetDay,
		"accounting_mode": cfg.AccountingMode,
		"warn_percent":    cfg.WarnPercent,
		"cycle_from":      cycleStart.Format(time.DateOnly),
		"cycle_to":        cycleEnd.AddDate(0, 0, -1).Format(time.DateOnly),
		"state":           state,
		"completeness":    completeness,
	}
}
