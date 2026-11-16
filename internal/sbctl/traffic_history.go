package sbctl

import (
	"math/big"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// Calendar days match the console's displayed timezone. Deltas belong to the
// sample's ending day; normal midnight attribution is bounded by 180 seconds.
var trafficZone = time.FixedZone("Asia/Singapore", 8*60*60)

func historyPath(export, node, day string) string {
	return filepath.Join(filepath.Dir(export), "history", node, day+".json")
}
func snapshotSeries(s Snapshot) M {
	groups := M{"inbounds": M{}, "local_users": M{}, "users": M{}}
	for _, r := range s.Inbounds {
		mapping(groups["inbounds"])[r.Tag] = M{"upload_bytes": r.Upload, "download_bytes": r.Download}
	}
	for _, r := range s.LocalUsers {
		mapping(groups["local_users"])[r.Name] = M{"upload_bytes": r.Upload, "download_bytes": r.Download}
	}
	for _, r := range s.Users {
		mapping(groups["users"])[r.UserID] = M{"upload_bytes": r.Upload, "download_bytes": r.Download}
	}
	return groups
}
func recordTrafficDay(d *DB, node string, prev M, s Snapshot) {
	if s.SampledAt == nil {
		return
	}
	started := d.Value("SELECT started_at FROM history_nodes WHERE node_id=?", node)
	if started != nil && prev != nil && s.Seq <= integer(prev["seq"]) {
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
	day := end.In(trafficZone).Format(time.DateOnly)
	row := d.One("SELECT document FROM history_days WHERE node_id=? AND day=?", node, day)
	doc := M{"from": from.Format(time.RFC3339Nano), "to": *s.SampledAt, "incomplete": false, "inbounds": M{}, "local_users": M{}, "users": M{}}
	if row != nil {
		doc = object([]byte(str(row["document"])))
		doc["to"] = *s.SampledAt
	}
	gap := end.Sub(from) > 180*time.Second || s.Quality.Integrity != "ok" || (started != nil && (old.Quality.Integrity != "ok" || s.Quality.Gaps > old.Quality.Gaps))
	doc["incomplete"] = boolean(doc["incomplete"]) || gap
	if started != nil && old.SampledAt != nil && !gap {
		before, after := snapshotSeries(old), snapshotSeries(s)
		for _, group := range []string{"inbounds", "local_users", "users"} {
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
	d.Exec("INSERT INTO history_days VALUES (?,?,?,1) ON CONFLICT(node_id,day) DO UPDATE SET document=excluded.document,dirty=1", node, day, string(canonical(doc)))
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
	var rows []M
	transaction(t.Path, false, func(d *DB) { rows = d.Rows("SELECT node_id,day,document FROM history_days WHERE dirty=1") })
	for _, row := range rows {
		atomicWrite(historyPath(t.Export, str(row["node_id"]), str(row["day"])), []byte(str(row["document"])), 0644)
		transaction(t.Path, true, func(d *DB) {
			d.Exec("UPDATE history_days SET dirty=0 WHERE node_id=? AND day=? AND document=?", row["node_id"], row["day"], row["document"])
		})
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
	telemetry := v.Telemetry()
	nodes := v.nodesFrom(telemetry)
	result := []M{}
	periods := []M{}
	// User names come from the control database, totals from the selected nodes.
	names := map[string]string{}
	transaction(v.Settings.ControlDB, false, func(d *DB) {
		for _, u := range d.Rows("SELECT id,display_name FROM users") {
			names[str(u["id"])] = str(u["display_name"])
		}
	})
	users := []M{}
	for _, node := range nodes {
		id := str(node["id"])
		if q.Get("node") != "" && id != q.Get("node") {
			continue
		}
		entry := mapping(telemetry[id])
		snap := mapping(entry["snapshot"])
		period := M{"node_id": id, "history_started_at": entry["history_started_at"], "status": "cumulative"}
		aggregates := M{"inbounds": M{}, "local_users": M{}, "users": M{}}
		if !start.IsZero() {
			seen, incomplete := false, false
			for day := start; day.Before(end); day = day.AddDate(0, 0, 1) {
				path := historyPath(v.Settings.TrafficSnapshot, id, day.Format(time.DateOnly))
				if !exists(path) {
					incomplete = true
					continue
				}
				doc := object(read(path))
				seen = true
				requiredEnd := day.AddDate(0, 0, 1)
				if requiredEnd.After(time.Now()) {
					requiredEnd = time.Now()
				}
				if stamp(str(doc["from"])).After(day) || stamp(str(doc["to"])).Before(requiredEnd.Add(-180*time.Second)) || boolean(doc["incomplete"]) {
					incomplete = true
				}
				for _, group := range []string{"inbounds", "local_users", "users"} {
					for key, raw := range mapping(doc[group]) {
						agg := mapping(mapping(aggregates[group])[key])
						for _, direction := range []string{"upload_bytes", "download_bytes"} {
							agg[direction] = new(big.Int).Add(bytesInt(agg[direction]), bytesInt(mapping(raw)[direction])).String()
						}
						mapping(aggregates[group])[key] = agg
					}
				}
			}
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
					agg := mapping(mapping(aggregates[group])[str(row[keyName])])
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
		for _, raw := range array(snap["users"]) {
			u := mapping(raw)
			uid := str(u["user_id"])
			if names[uid] == "" {
				continue
			}
			if query := strings.ToLower(q.Get("q")); query != "" && !strings.Contains(strings.ToLower(names[uid]+" "+uid+" "+str(node["name"])), query) {
				continue
			}
			row := M{"user_id": uid, "display_name": names[uid], "node_id": id, "node_name": node["name"], "quality": node["quality"], "upload_bytes": u["upload_bytes"], "download_bytes": u["download_bytes"]}
			if !start.IsZero() {
				agg := mapping(mapping(aggregates["users"])[uid])
				row["upload_bytes"] = bytesInt(agg["upload_bytes"]).String()
				row["download_bytes"] = bytesInt(agg["download_bytes"]).String()
			}
			users = append(users, row)
		}
		result = append(result, node)
	}
	return M{"nodes": result, "users": users, "periods": periods, "from": q.Get("from"), "to": q.Get("to"), "timezone": "Asia/Singapore", "as_of": now()}
}
