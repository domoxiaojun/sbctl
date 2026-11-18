package sbctl

import (
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Calendar days match the console's displayed timezone. Deltas belong to the
// sample's ending day and ending hour; normal boundary attribution is bounded
// by 180 seconds.
var trafficZone = time.FixedZone("Asia/Singapore", 8*60*60)

// Daily history is kept indefinitely; hourly detail only for recent weeks.
const hourlyRetentionDays = 62
const hourKey = "2006-01-02T15"

func historyPath(export, node, day string) string {
	return filepath.Join(filepath.Dir(export), "history", node, day+".json")
}

// hoursPath is one file per node and day holding that day's hourly buckets.
func hoursPath(export, node, day string) string {
	return filepath.Join(filepath.Dir(export), "history", node, day+".hours.json")
}

// Exports of past days never change. Parsed documents are reused until the
// file does, so long date ranges do not re-read hundreds of files per request.
// Callers must treat the returned document as read-only.
var historyCache = struct {
	sync.Mutex
	entries map[string]historyEntry
	bytes   int64
}{entries: map[string]historyEntry{}}

type historyEntry struct {
	modified time.Time
	size     int64
	inode    uint64
	document M
}

const historyCacheBytes = 32 << 20

// Exports are replaced by rename, so a rewritten file always has a new inode
// even when its size and a coarse modification time happen to match.
func inode(info os.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Ino)
	}
	return 0
}

// historyDocument returns the export at path, or nil when it does not exist.
func historyDocument(path string) M {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		check(err)
	}
	historyCache.Lock()
	entry, ok := historyCache.entries[path]
	historyCache.Unlock()
	if ok && entry.size == info.Size() && entry.inode == inode(info) && entry.modified.Equal(info.ModTime()) {
		return entry.document
	}
	document := object(read(path))
	historyCache.Lock()
	defer historyCache.Unlock()
	if ok {
		historyCache.bytes -= entry.size
	}
	for key, old := range historyCache.entries {
		if historyCache.bytes+info.Size() <= historyCacheBytes {
			break
		}
		historyCache.bytes -= old.size
		delete(historyCache.entries, key)
	}
	historyCache.entries[path] = historyEntry{info.ModTime(), info.Size(), inode(info), document}
	historyCache.bytes += info.Size()
	return document
}
func snapshotSeries(s Snapshot) M {
	groups := M{"inbounds": M{}, "local_users": M{}}
	for _, r := range s.Inbounds {
		mapping(groups["inbounds"])[r.historyKey()] = M{"upload_bytes": r.Upload, "download_bytes": r.Download}
	}
	for _, r := range s.LocalUsers {
		mapping(groups["local_users"])[r.Name] = M{"upload_bytes": r.Upload, "download_bytes": r.Download}
	}
	return groups
}
func recordTrafficDay(d *DB, node string, prev M, s Snapshot) {
	if s.SampledAt == nil {
		return
	}
	started := d.Value("SELECT started_at FROM history_nodes WHERE node_id=?", node)
	switchedLedger := prev != nil && str(prev["ledger_id"]) != s.LedgerID
	if started != nil && prev != nil && !switchedLedger && s.Seq <= integer(prev["seq"]) {
		return
	}
	end := stamp(*s.SampledAt)
	from := end
	var old Snapshot
	if prev != nil {
		decode([]byte(str(prev["document"])), &old)
	}
	if started != nil && old.SampledAt != nil {
		from = stamp(*old.SampledAt)
	}
	if started == nil {
		d.Exec("INSERT INTO history_nodes VALUES (?,?)", node, *s.SampledAt)
	}
	gap := switchedLedger || end.Sub(from) > 180*time.Second || s.Quality.Integrity != "ok" || (started != nil && (old.Quality.Integrity != "ok" || s.Quality.Gaps > old.Quality.Gaps))
	measured := started != nil && old.SampledAt != nil && !gap
	var before, after M
	if measured {
		before, after = snapshotSeries(old), snapshotSeries(s)
	}
	local := end.In(trafficZone)
	for _, bucket := range []struct{ table, column, key string }{
		{"history_days", "day", local.Format(time.DateOnly)},
		{"history_hours", "hour", local.Format(hourKey)},
	} {
		row := d.One("SELECT document FROM "+bucket.table+" WHERE node_id=? AND "+bucket.column+"=?", node, bucket.key)
		doc := M{"from": from.Format(time.RFC3339Nano), "to": *s.SampledAt, "incomplete": false, "inbounds": M{}, "local_users": M{}}
		if row != nil {
			doc = pick(object([]byte(str(row["document"]))), "from", "to", "incomplete", "inbounds", "local_users")
			doc["to"] = *s.SampledAt
		} else if bucket.table == "history_hours" {
			d.Exec("DELETE FROM history_hours WHERE node_id=? AND hour<?", node, local.AddDate(0, 0, -hourlyRetentionDays).Format(hourKey))
		}
		doc["incomplete"] = boolean(doc["incomplete"]) || gap
		if measured {
			for _, group := range []string{"inbounds", "local_users"} {
				for key, raw := range mapping(after[group]) {
					current, prior := mapping(raw), mapping(mapping(before[group])[key])
					// A newly observed identity already contains a cumulative value. Baseline it.
					if len(prior) == 0 {
						if bytesInt(current["upload_bytes"]).Sign() > 0 || bytesInt(current["download_bytes"]).Sign() > 0 {
							doc["incomplete"] = true
						}
						continue
					}
					aggregate := mapping(mapping(doc[group])[key])
					for _, direction := range []string{"upload_bytes", "download_bytes"} {
						delta := new(big.Int).Sub(bytesInt(current[direction]), bytesInt(prior[direction]))
						if delta.Sign() < 0 {
							fail("TOTAL_REGRESSION", "区间累计回退")
						}
						aggregate[direction] = new(big.Int).Add(bytesInt(aggregate[direction]), delta).String()
					}
					mapping(doc[group])[key] = aggregate
				}
			}
		}
		d.Exec("INSERT INTO "+bucket.table+" VALUES (?,?,?,1) ON CONFLICT(node_id,"+bucket.column+") DO UPDATE SET document=excluded.document,dirty=1", node, bucket.key, string(canonical(doc)))
	}
}
func bytesInt(v any) *big.Int {
	n := new(big.Int)
	if v != nil {
		if _, ok := n.SetString(str(v), 10); !ok || n.Sign() < 0 {
			fail("COUNTER_INVALID", "流量数据无效")
		}
	}
	return n
}
func (t Traffic) writeHistoryExport() {
	var rows, days []M
	transaction(t.Path, false, func(d *DB) {
		rows = d.Rows("SELECT node_id,day,document FROM history_days WHERE dirty=1")
		days = d.Rows("SELECT DISTINCT node_id,substr(hour,1,10) AS day FROM history_hours WHERE dirty=1")
	})
	for _, row := range rows {
		atomicWrite(historyPath(t.Export, str(row["node_id"]), str(row["day"])), []byte(str(row["document"])), 0644)
		transaction(t.Path, true, func(d *DB) {
			d.Exec("UPDATE history_days SET dirty=0 WHERE node_id=? AND day=? AND document=?", row["node_id"], row["day"], row["document"])
		})
	}
	for _, changed := range days {
		node, day := str(changed["node_id"]), str(changed["day"])
		var hours []M
		transaction(t.Path, false, func(d *DB) {
			hours = d.Rows("SELECT hour,document FROM history_hours WHERE node_id=? AND hour>=? AND hour<=? ORDER BY hour", node, day+"T00", day+"T23")
		})
		buckets := M{}
		for _, h := range hours {
			buckets[str(h["hour"])[11:]] = object([]byte(str(h["document"])))
		}
		path := hoursPath(t.Export, node, day)
		firstOfDay := !exists(path)
		atomicWrite(path, canonical(M{"hours": buckets}), 0644)
		transaction(t.Path, true, func(d *DB) {
			for _, h := range hours {
				d.Exec("UPDATE history_hours SET dirty=0 WHERE node_id=? AND hour=? AND document=?", node, h["hour"], h["document"])
			}
		})
		if firstOfDay {
			// Retention follows the data's own clock, like the ledger rows above.
			if written, err := time.Parse(time.DateOnly, day); err == nil {
				pruneHourlyExports(filepath.Dir(path), written.AddDate(0, 0, -hourlyRetentionDays).Format(time.DateOnly))
			}
		}
	}
}

// pruneHourlyExports removes hourly files older than the retained window.
func pruneHourlyExports(dir, cutoff string) {
	entries, err := os.ReadDir(dir)
	check(err)
	for _, entry := range entries {
		if day, ok := strings.CutSuffix(entry.Name(), ".hours.json"); ok && day < cutoff {
			check(os.Remove(filepath.Join(dir, entry.Name())))
		}
	}
}
func trafficDates(q url.Values) (time.Time, time.Time) {
	from, to := q.Get("from"), q.Get("to")
	if from == "" && to == "" {
		return time.Time{}, time.Time{}
	}
	start, a := time.ParseInLocation(time.DateOnly, from, trafficZone)
	finish, b := time.ParseInLocation(time.DateOnly, to, trafficZone)
	today := time.Now().In(trafficZone).Format(time.DateOnly)
	if a != nil || b != nil || from > to || to > today || finish.Sub(start) > 365*24*time.Hour {
		fail("DATE_RANGE_INVALID", "请选择有效日期，结束日期不能早于开始日期或晚于今天，最多查询 366 天", 422)
	}
	return start, finish.AddDate(0, 0, 1)
}
func (v Views) TrafficReport(q url.Values) M {
	start, end := trafficDates(q)
	scope := q.Get("scope")
	if scope == "" {
		scope = "inbounds"
	}
	group := map[string]string{"inbounds": "inbounds", "local": "local_users"}[scope]
	if group == "" {
		fail("SCOPE_INVALID", "统计范围无效", 422)
	}
	trendStart, trendEnd := start, end
	granularity := q.Get("granularity")
	if granularity == "" {
		granularity = "day"
	}
	if !contains([]string{"hour", "day", "month"}, granularity) {
		fail("GRANULARITY_INVALID", "趋势粒度无效", 422)
	}
	if granularity == "hour" && end.Sub(start) > 7*24*time.Hour {
		fail("DATE_RANGE_INVALID", "按小时查看最多选择 7 天", 422)
	}
	if start.IsZero() {
		today := time.Now().In(trafficZone)
		trendEnd = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, trafficZone).AddDate(0, 0, 1)
		trendStart = trendEnd.AddDate(0, 0, -7)
		if granularity == "hour" {
			trendStart = trendEnd.AddDate(0, 0, -2)
		}
		if granularity == "month" {
			// The current month and the eleven before it: at most 366 days.
			trendStart = time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, trafficZone).AddDate(0, -11, 0)
		}
	}
	daily := map[string]M{}
	selectedNodes := 0
	selected, selectedKeys := []string{}, map[string][]string{}
	for day := trendStart; day.Before(trendEnd); day = day.AddDate(0, 0, 1) {
		daily[day.Format(time.DateOnly)] = M{"known": 0, "partial": false, "upload_bytes": "0", "download_bytes": "0"}
	}
	telemetry := v.Telemetry()
	nodes := v.nodesFrom(telemetry)
	result := []M{}
	periods := []M{}
	for _, node := range nodes {
		id := str(node["id"])
		if q.Get("node") != "" && id != q.Get("node") {
			continue
		}
		selectedNodes++
		entry := mapping(telemetry[id])
		period := M{"node_id": id, "history_started_at": entry["history_started_at"], "status": "cumulative"}
		aggregates := M{"inbounds": M{}, "local_users": M{}}
		documents := map[string]M{}
		seen, incomplete := false, false
		for day := trendStart; day.Before(trendEnd); day = day.AddDate(0, 0, 1) {
			key := day.Format(time.DateOnly)
			doc := historyDocument(historyPath(v.Settings.TrafficSnapshot, id, key))
			if doc == nil {
				incomplete = true
				continue
			}
			documents[key] = doc
			seen = true
			requiredEnd := day.AddDate(0, 0, 1)
			if requiredEnd.After(time.Now()) {
				requiredEnd = time.Now()
			}
			partial := stamp(str(doc["from"])).After(day) || stamp(str(doc["to"])).Before(requiredEnd.Add(-180*time.Second)) || boolean(doc["incomplete"])
			incomplete = incomplete || partial
			daily[key]["known"] = integer(daily[key]["known"]) + 1
			daily[key]["partial"] = boolean(daily[key]["partial"]) || partial
			if !start.IsZero() {
				for _, category := range []string{"inbounds", "local_users"} {
					for key, raw := range mapping(doc[category]) {
						agg := mapping(mapping(aggregates[category])[key])
						for _, direction := range []string{"upload_bytes", "download_bytes"} {
							agg[direction] = new(big.Int).Add(bytesInt(agg[direction]), bytesInt(mapping(raw)[direction])).String()
						}
						mapping(aggregates[category])[key] = agg
					}
				}
			}
		}
		if !start.IsZero() {
			period["status"] = "complete"
			if incomplete {
				period["status"] = "partial"
			}
			if !seen {
				period["status"] = "unavailable"
			}
		}
		periods = append(periods, period)
		for _, group := range []string{"inbounds", "local_users"} {
			filtered := []M{}
			keyName := "tag"
			if group == "local_users" {
				keyName = "name"
			}
			for _, raw := range array(node[group]) {
				row := mapping(raw)
				if group == "local_users" && q.Get("local_user") != "" && str(row["name"]) != q.Get("local_user") {
					continue
				}
				if q.Get("protocol") != "" && group == "inbounds" && str(row["protocol"]) != q.Get("protocol") {
					continue
				}
				if q.Get("activity") == "active" && !boolean(row["active"]) || q.Get("activity") == "removed" && boolean(row["active"]) {
					continue
				}
				if query := strings.ToLower(q.Get("q")); query != "" && !strings.Contains(strings.ToLower(str(row[keyName])+" "+str(row["protocol"])+" "+str(row["port"])+" "+str(node["name"])), query) {
					continue
				}
				if !start.IsZero() {
					key := str(row[keyName])
					if group == "inbounds" {
						key = inboundHistoryKey(key, str(row["inbound_id"]), str(row["history_tag"]))
					}
					agg := mapping(mapping(aggregates[group])[key])
					for _, direction := range []string{"upload_bytes", "download_bytes"} {
						row[direction] = bytesInt(agg[direction]).String()
					}
				}
				filtered = append(filtered, row)
			}
			node[group] = filtered
		}
		for _, direction := range []string{"upload_bytes", "download_bytes"} {
			sum := new(big.Int)
			for _, raw := range array(node["inbounds"]) {
				sum.Add(sum, bytesInt(mapping(raw)[direction]))
			}
			node["inbound_"+direction] = sum.String()
		}
		keys := []string{}
		identity := "tag"
		if group == "local_users" {
			identity = "name"
		}
		for _, raw := range array(node[group]) {
			row := mapping(raw)
			key := str(row[identity])
			if group == "inbounds" {
				key = inboundHistoryKey(key, str(row["inbound_id"]), str(row["history_tag"]))
			}
			keys = append(keys, key)
		}
		for day, doc := range documents {
			for _, key := range keys {
				row := mapping(mapping(doc[group])[key])
				for _, direction := range []string{"upload_bytes", "download_bytes"} {
					daily[day][direction] = new(big.Int).Add(bytesInt(daily[day][direction]), bytesInt(row[direction])).String()
				}
			}
		}
		selected = append(selected, id)
		selectedKeys[id] = keys
		result = append(result, node)
	}
	trend := []M{}
	if granularity == "hour" {
		trend = v.hourlyTrend(trendStart, trendEnd, group, selected, selectedKeys)
	}
	for day := trendStart; granularity != "hour" && day.Before(trendEnd); day = day.AddDate(0, 0, 1) {
		key := day.Format(time.DateOnly)
		point := M{"date": key, "upload_bytes": daily[key]["upload_bytes"], "download_bytes": daily[key]["download_bytes"], "status": "complete"}
		known := integer(daily[key]["known"])
		if known == 0 {
			point["status"] = "unavailable"
			point["upload_bytes"] = nil
			point["download_bytes"] = nil
		} else if known < int64(selectedNodes) || boolean(daily[key]["partial"]) {
			point["status"] = "partial"
		}
		trend = append(trend, point)
	}
	if granularity == "month" {
		trend = monthlyTrend(trend)
	}
	return M{"nodes": result, "periods": periods, "from": q.Get("from"), "to": q.Get("to"), "timezone": "Asia/Singapore", "scope": scope, "granularity": granularity, "trend": trend, "trend_from": trendStart.Format(time.DateOnly), "trend_to": trendEnd.AddDate(0, 0, -1).Format(time.DateOnly), "as_of": now()}
}

// TrafficConnections exposes the recent routing ledger used by the traffic
// dashboard. It is deliberately a separate endpoint so the regular usage
// report remains small and aggregate-only. The node ledger retains the same
// bounded 90-day window.
func (v Views) TrafficConnections(q url.Values) M {
	start, end := trafficDates(q)
	if start.IsZero() {
		end = time.Now().UTC()
		start = end.Add(-24 * time.Hour)
	}
	if end.Sub(start) > 90*24*time.Hour {
		fail("DATE_RANGE_INVALID", "连接明细最多查询 90 天", 422)
	}
	page, pageSize := 1, 100
	if value := q.Get("page"); value != "" {
		if _, err := fmt.Sscanf(value, "%d", &page); err != nil || page < 1 {
			fail("PAGE_INVALID", "页码无效", 422)
		}
	}
	if value := q.Get("page_size"); value != "" {
		if _, err := fmt.Sscanf(value, "%d", &pageSize); err != nil || pageSize < 1 || pageSize > 500 {
			fail("PAGE_INVALID", "每页数量无效", 422)
		}
	}
	filters := []string{"started_at>=?", "started_at<?"}
	args := []any{connectionTime(start), connectionTime(end)}
	for _, filter := range []struct{ key, column string }{{"node", "node_id"}, {"inbound", "inbound_tag"}, {"protocol", "inbound_type"}, {"outbound", "outbound"}, {"domain", "domain"}, {"rule", "rule"}} {
		if value := strings.TrimSpace(q.Get(filter.key)); value != "" {
			if filter.key == "node" || filter.key == "protocol" {
				filters = append(filters, filter.column+"=?")
				args = append(args, value)
				continue
			}
			filters = append(filters, filter.column+" LIKE ?")
			args = append(args, "%"+value+"%")
		}
	}
	where := strings.Join(filters, " AND ")
	result := M{"items": []M{}, "page": page, "page_size": pageSize, "retention_days": 90, "from": start.UTC().Format(time.RFC3339Nano), "to": end.UTC().Format(time.RFC3339Nano), "as_of": now()}
	readTrafficDB(v.Settings.TrafficDB, func(d *DB) {
		gapWhere, gapArgs := "", []any{}
		if node := q.Get("node"); node != "" {
			gapWhere, gapArgs = " WHERE node_id=?", []any{node}
		}
		result["history_gap"] = integer(d.Value("SELECT COALESCE(max(history_gap),0) FROM connection_cursors"+gapWhere, gapArgs...)) != 0
		result["total"] = integer(d.Value("SELECT count(*) FROM connections WHERE "+where, args...))
		result["summary"] = M{"domains": connectionSummary(d, where, args, "domain"), "destinations": connectionSummary(d, where, args, "destination"), "rules": connectionSummary(d, where, args, "rule"), "outbounds": connectionSummary(d, where, args, "outbound")}
		rows := d.Rows("SELECT * FROM connections WHERE "+where+" ORDER BY started_at DESC,connection_id DESC LIMIT ? OFFSET ?", append(args, pageSize, (page-1)*pageSize)...)
		items := []M{}
		for _, row := range rows {
			var chain []string
			decode([]byte(str(row["chain_json"])), &chain)
			if chain == nil {
				chain = []string{}
			}
			items = append(items, M{"node_id": row["node_id"], "connection_id": row["connection_id"], "inbound_id": row["inbound_id"], "inbound": row["inbound_tag"], "inbound_type": row["inbound_type"], "network": row["network"], "domain": row["domain"], "destination": row["destination"], "source": row["source"], "outbound": row["outbound"], "outbound_type": row["outbound_type"], "chain": chain, "rule": row["rule"], "upload_bytes": fmt.Sprint(row["upload"]), "download_bytes": fmt.Sprint(row["download"]), "started_at": row["started_at"], "closed_at": row["closed_at"]})
		}
		result["items"] = items
	})
	return result
}

func connectionSummary(d *DB, where string, args []any, column string) []M {
	rows := d.Rows("SELECT "+column+" AS name,count(*) AS connections,COALESCE(sum(upload),0) AS upload,COALESCE(sum(download),0) AS download FROM connections WHERE "+where+" AND "+column+"<>'' GROUP BY "+column+" ORDER BY connections DESC,name LIMIT 20", args...)
	result := make([]M, 0, len(rows))
	for _, row := range rows {
		result = append(result, M{"name": row["name"], "connections": integer(row["connections"]), "upload_bytes": fmt.Sprint(row["upload"]), "download_bytes": fmt.Sprint(row["download"])})
	}
	return result
}

// monthlyTrend folds daily points into calendar months. A month is complete
// only when each of its days in the range is; days without detail make it
// partial, and a month with no detail at all stays unavailable.
func monthlyTrend(days []M) []M {
	months := []M{}
	sums := map[string][2]*big.Int{}
	for _, day := range days {
		key := str(day["date"])[:7]
		if len(months) == 0 || months[len(months)-1]["date"] != key {
			months = append(months, M{"date": key, "upload_bytes": nil, "download_bytes": nil, "status": day["status"]})
			sums[key] = [2]*big.Int{new(big.Int), new(big.Int)}
		}
		month := months[len(months)-1]
		if day["status"] != month["status"] {
			month["status"] = "partial"
		}
		if day["status"] == "unavailable" {
			continue
		}
		sums[key][0].Add(sums[key][0], bytesInt(day["upload_bytes"]))
		sums[key][1].Add(sums[key][1], bytesInt(day["download_bytes"]))
		month["upload_bytes"], month["download_bytes"] = sums[key][0].String(), sums[key][1].String()
	}
	return months
}

// hourlyTrend sums the selected series of each node per hour. Hours are filed
// the same way as days, so the same completeness rules apply.
func (v Views) hourlyTrend(start, end time.Time, group string, nodes []string, keys map[string][]string) []M {
	current := time.Now()
	if limit := current.In(trafficZone).Truncate(time.Hour).Add(time.Hour); end.After(limit) {
		end = limit
	}
	files := map[string]M{}
	trend := []M{}
	for hour := start; hour.Before(end); hour = hour.Add(time.Hour) {
		day := hour.Format(time.DateOnly)
		upload, download := new(big.Int), new(big.Int)
		known, partial := 0, false
		for _, id := range nodes {
			file, loaded := files[id+"/"+day]
			if !loaded {
				file = mapping(historyDocument(hoursPath(v.Settings.TrafficSnapshot, id, day))["hours"])
				files[id+"/"+day] = file
			}
			doc := mapping(file[hour.Format("15")])
			if len(doc) == 0 {
				continue
			}
			known++
			required := hour.Add(time.Hour)
			if required.After(current) {
				required = current
			}
			partial = partial || stamp(str(doc["from"])).After(hour) || stamp(str(doc["to"])).Before(required.Add(-180*time.Second)) || boolean(doc["incomplete"])
			for _, key := range keys[id] {
				row := mapping(mapping(doc[group])[key])
				upload.Add(upload, bytesInt(row["upload_bytes"]))
				download.Add(download, bytesInt(row["download_bytes"]))
			}
		}
		point := M{"date": hour.Format("2006-01-02T15:04"), "upload_bytes": upload.String(), "download_bytes": download.String(), "status": "complete"}
		if known == 0 {
			point["status"], point["upload_bytes"], point["download_bytes"] = "unavailable", nil, nil
		} else if known < len(nodes) || partial {
			point["status"] = "partial"
		}
		trend = append(trend, point)
	}
	return trend
}
