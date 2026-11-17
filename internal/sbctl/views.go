package sbctl

import (
	"math/big"
	"sort"
	"strings"
	"time"
)

type Filters struct {
	Q, Status, Region, Sort, Archived string
	Page, PageSize                    int
}

func defaultsFilters() Filters { return Filters{Sort: "created", Page: 1, PageSize: 8} }
func clean(m M) M {
	r := M{}
	for k, v := range m {
		if !strings.HasPrefix(k, "_") {
			r[k] = v
		}
	}
	return r
}
func totalOf(r M) *big.Int {
	n := new(big.Int)
	for _, k := range []string{"upload_bytes", "download_bytes"} {
		v, ok := new(big.Int).SetString(str(r[k]), 10)
		if ok {
			n.Add(n, v)
		}
	}
	return n
}
func Paginate(rows []M, f Filters) M {
	filtered := []M{}
	for _, r := range rows {
		if _, isUser := r["archived"]; isUser {
			if f.Archived != "all" && boolean(r["archived"]) != (f.Archived == "true") {
				continue
			}
		}
		if f.Q != "" {
			texts := []string{}
			for _, k := range []string{"id", "name", "display_name", "subject", "title", "note"} {
				texts = append(texts, str(r[k]))
			}
			if !strings.Contains(strings.ToLower(strings.Join(texts, " ")), strings.ToLower(f.Q)) {
				continue
			}
		}
		status := str(r["status"])
		if status == "" {
			status = str(r["state"])
		}
		if f.Status != "" && status != f.Status {
			continue
		}
		if f.Region != "" && str(r["region"]) != f.Region && !contains(stringsOf(r["_regions"]), f.Region) {
			continue
		}
		filtered = append(filtered, r)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		a, b := filtered[i], filtered[j]
		switch f.Sort {
		case "bytes":
			return totalOf(a).Cmp(totalOf(b)) > 0
		case "name":
			name := func(r M) string {
				for _, k := range []string{"display_name", "name", "subject", "title"} {
					if r[k] != nil {
						return strings.ToLower(str(r[k]))
					}
				}
				return ""
			}
			return name(a) < name(b)
		default:
			return str(a["created_at"]) > str(b["created_at"])
		}
	})
	total := len(filtered)
	start := min((f.Page-1)*f.PageSize, total)
	end := min(start+f.PageSize, total)
	items := []M{}
	for _, r := range filtered[start:end] {
		items = append(items, clean(r))
	}
	return M{"items": items, "total": total, "page": f.Page, "page_size": f.PageSize}
}

type Views struct{ Settings Settings }

func (v Views) Telemetry() M {
	m := M{}
	_ = Attempt(func() { m = mapping(object(read(v.Settings.TrafficSnapshot))["nodes"]) })
	return m
}
func (v Views) Quality(nodes []string, started string, t M) M {
	q := M{"freshness": "current", "integrity_state": "ok", "has_historical_gap": false, "gap_count": int64(0), "started_at": started, "sampled_at": nil, "synced_at": nil}
	allSample, allSync := true, true
	sample, syncTime := "", ""
	if len(nodes) == 0 {
		q["freshness"] = "unknown"
	}
	rank := func(s string) int {
		switch s {
		case "unknown", "state_lost":
			return 2
		case "stale", "counter_error":
			return 1
		}
		return 0
	}
	for _, node := range nodes {
		entry := mapping(t[node])
		snap := mapping(entry["snapshot"])
		quality := mapping(snap["data_quality"])
		sampled, synced := str(snap["sampled_at"]), str(entry["synced_at"])
		fresh := "unknown"
		if sampled != "" && synced != "" {
			fresh = "stale"
			err := Attempt(func() {
				if time.Since(stamp(sampled)).Seconds() <= float64(v.Settings.StaleSeconds) && time.Since(stamp(synced)).Seconds() <= float64(v.Settings.StaleSeconds) && str(entry["error_code"]) == "" {
					fresh = "current"
				}
			})
			if err != nil {
				fresh = "unknown"
			}
		}
		integrity := str(quality["integrity_state"])
		if integrity == "" {
			integrity = "ok"
		}
		if contains([]string{"LEDGER_CHANGED", "SNAPSHOT_OLD", "TOTAL_REGRESSION", "HISTORY_REGRESSION"}, str(entry["error_code"])) {
			integrity = "state_lost"
		}
		if rank(fresh) > rank(str(q["freshness"])) {
			q["freshness"] = fresh
		}
		if rank(integrity) > rank(str(q["integrity_state"])) {
			q["integrity_state"] = integrity
		}
		q["has_historical_gap"] = boolean(q["has_historical_gap"]) || boolean(quality["has_historical_gap"])
		q["gap_count"] = integer(q["gap_count"]) + integer(quality["gap_count"])
		if s := str(snap["started_at"]); s != "" && s < str(q["started_at"]) {
			q["started_at"] = s
		}
		if sampled == "" {
			allSample = false
		} else if sample == "" || sampled < sample {
			sample = sampled
		}
		if synced == "" {
			allSync = false
		} else if syncTime == "" || synced < syncTime {
			syncTime = synced
		}
	}
	if allSample && sample != "" {
		q["sampled_at"] = sample
	}
	if allSync && syncTime != "" {
		q["synced_at"] = syncTime
	}
	return q
}
func (v Views) Nodes() []M {
	return v.nodesFrom(v.Telemetry())
}
func (v Views) nodesFrom(t M) []M {
	var rows []M
	transaction(v.Settings.ControlDB, false, func(d *DB) {
		rows = d.Rows("SELECT n.*,(SELECT count(DISTINCT user_id) FROM credentials WHERE node_id=n.id AND revoked=0) AS users_count FROM nodes n ORDER BY n.id LIMIT 1001")
	})
	result := []M{}
	for _, r := range rows {
		id := str(r["id"])
		q := v.Quality([]string{id}, str(r["created_at"]), t)
		status := "offline"
		if q["freshness"] == "current" {
			status = "online"
			if q["integrity_state"] != "ok" || integer(r["revision"]) != integer(r["applied_revision"]) {
				status = "degraded"
			}
		}
		protocols := []string{}
		var eps []M
		decode([]byte(str(r["endpoints_json"])), &eps)
		for _, e := range eps {
			p := "TUIC v5"
			if e["protocol"] == "anytls" {
				p = "AnyTLS"
			}
			protocols = append(protocols, p)
		}
		row := pick(r, "id", "name", "region", "location", "management_address", "public_host", "revision", "users_count")
		// The snapshot stores entrance counters under its inbounds field.
		inbounds := []M{}
		inboundUpload, inboundDownload := new(big.Int), new(big.Int)
		snapshot := mapping(t[id])["snapshot"]
		for _, raw := range array(mapping(snapshot)["inbounds"]) {
			inbounds = append(inbounds, mapping(raw))
		}
		for _, in := range inbounds {
			if value, ok := new(big.Int).SetString(str(in["upload_bytes"]), 10); ok {
				inboundUpload.Add(inboundUpload, value)
			}
			if value, ok := new(big.Int).SetString(str(in["download_bytes"]), 10); ok {
				inboundDownload.Add(inboundDownload, value)
			}
		}
		localUsers := []M{}
		for _, raw := range array(mapping(snapshot)["local_users"]) {
			localUsers = append(localUsers, mapping(raw))
		}
		merge(row, M{"protocols": protocols, "status": status, "version": object([]byte(str(r["connection_json"])))["version"], "quality": q, "inbounds": inbounds, "local_users": localUsers, "total_inbounds": integer(mapping(snapshot)["total_inbounds"]), "inbound_upload_bytes": inboundUpload.String(), "inbound_download_bytes": inboundDownload.String()})
		result = append(result, row)
	}
	return result
}
func (v Views) Users() []M {
	t := v.Telemetry()
	var users []M
	grants, nodes, regions := map[string][]string{}, map[string][]string{}, map[string][]string{}
	authorized := map[string]int{}
	transaction(v.Settings.ControlDB, false, func(d *DB) {
		users = d.Rows("SELECT u.*,s.state AS subscription_state,s.generation AS subscription_generation,COALESCE(m.version,0) AS metadata_version,m.archived_at FROM users u JOIN subscriptions s ON s.user_id=u.id LEFT JOIN user_management m ON m.user_id=u.id ORDER BY u.created_at DESC LIMIT 10001")
		for _, r := range d.Rows("SELECT * FROM grants") {
			id := str(r["user_id"])
			grants[id] = append(grants[id], str(r["group_id"]))
		}
		for _, r := range d.Rows("SELECT DISTINCT c.user_id,c.node_id,n.region FROM credentials c JOIN nodes n ON n.id=c.node_id") {
			id := str(r["user_id"])
			nodes[id] = append(nodes[id], str(r["node_id"]))
			regions[id] = append(regions[id], str(r["region"]))
		}
		for _, r := range d.Rows("SELECT user_id,count(DISTINCT node_id) AS total FROM credentials WHERE revoked=0 GROUP BY user_id") {
			authorized[str(r["user_id"])] = int(integer(r["total"]))
		}
	})
	for _, u := range users {
		id := str(u["id"])
		u["archived"] = u["archived_at"] != nil
		ns := unique(nodes[id])
		merge(u, M{"group_ids": unique(grants[id]), "node_count": len(ns), "quality": v.Quality(ns, str(u["created_at"]), t), "_regions": unique(regions[id])})
		u["node_count"] = authorized[id]
	}
	return users
}
func (v Views) Operations() []M {
	out := []M{}
	transaction(v.Settings.ControlDB, false, func(d *DB) {
		rows := d.Rows("SELECT o.*,COALESCE(s.state,'pending') AS subscription_state FROM operations o LEFT JOIN subscriptions s ON s.user_id=o.user_id ORDER BY created_at DESC LIMIT 10000")
		targets := map[string][]M{}
		for _, r := range d.Rows("SELECT t.operation_id,t.node_id,n.name,t.state,t.message FROM operation_targets t JOIN nodes n ON n.id=t.node_id") {
			id := str(r["operation_id"])
			targets[id] = append(targets[id], pick(r, "node_id", "name", "state", "message"))
		}
		for _, r := range rows {
			m := pick(r, "id", "kind", "user_id", "subject", "state", "created_at", "subscription_state")
			if r["state"] == "completed" {
				m["subscription_state"] = "published"
				if r["kind"] == "disable_user" {
					m["subscription_state"] = "revoked"
				}
			}
			ts := targets[str(r["id"])]
			if ts == nil {
				ts = []M{}
			}
			m["targets"] = ts
			out = append(out, m)
		}
	})
	return out
}
func (v Views) Groups() []M {
	out := []M{}
	transaction(v.Settings.ControlDB, false, func(d *DB) {
		for _, r := range d.Rows("SELECT * FROM groups ORDER BY name LIMIT 1000") {
			ids := []string{}
			for _, n := range d.Rows("SELECT node_id FROM group_nodes WHERE group_id=? ORDER BY node_id", r["id"]) {
				ids = append(ids, str(n["node_id"]))
			}
			out = append(out, M{"id": r["id"], "name": r["name"], "node_ids": ids})
		}
	})
	return out
}
func (v Views) Alerts() []M {
	var rows []M
	transaction(v.Settings.ControlDB, false, func(d *DB) {
		rows = d.Rows("SELECT id,title,description,severity,state,node_id,created_at FROM alerts ORDER BY created_at DESC LIMIT 10000")
	})
	return rows
}
func one(rows []M, id string) M {
	for _, r := range rows {
		if str(r["id"]) == id {
			return clean(r)
		}
	}
	fail("NOT_FOUND", "对象不存在", 404)
	return nil
}
func (v Views) Subscription(id string) M {
	var out M
	transaction(v.Settings.ControlDB, false, func(d *DB) {
		r := d.One("SELECT state FROM subscriptions WHERE user_id=?", id)
		if r == nil {
			fail("NOT_FOUND", "用户不存在", 404)
		}
		total := integer(d.Value("SELECT count(DISTINCT node_id) FROM credentials WHERE user_id=? AND (revoked=0 OR EXISTS(SELECT 1 FROM users WHERE id=? AND status='disabled'))", id, id))
		ready := integer(d.Value("SELECT count(DISTINCT node_id) FROM credentials WHERE user_id=? AND verified_revision IS NOT NULL AND revoked=0", id))
		if r["state"] != "published" {
			ready = 0
		}
		out = M{"state": r["state"], "published_nodes": ready, "total_nodes": total}
	})
	return out
}
func (v Views) Overview() M {
	nodes, users, ops, alerts := v.Nodes(), v.Users(), v.Operations(), v.Alerts()
	r := M{"node_count": len(nodes), "online_count": 0, "offline_count": 0, "degraded_count": 0, "user_count": 0, "pending_count": 0, "as_of": now()}
	regions := map[string]M{}
	for _, n := range nodes {
		status := str(n["status"])
		r[status+"_count"] = integer(r[status+"_count"]) + 1
		region := str(n["region"])
		if regions[region] == nil {
			regions[region] = M{"id": region, "name": region, "node_count": 0, "online_count": 0}
		}
		rr := regions[region]
		rr["node_count"] = integer(rr["node_count"]) + 1
		if status == "online" {
			rr["online_count"] = integer(rr["online_count"]) + 1
		}
	}
	for _, u := range users {
		if u["status"] == "active" {
			r["user_count"] = integer(r["user_count"]) + 1
		}
	}
	for _, o := range ops {
		if !contains([]string{"completed", "failed", "superseded"}, str(o["state"])) {
			r["pending_count"] = integer(r["pending_count"]) + 1
		}
	}
	rs := []M{}
	for _, rr := range regions {
		rs = append(rs, rr)
	}
	sort.Slice(rs, func(i, j int) bool { return str(rs[i]["id"]) < str(rs[j]["id"]) })
	as := []M{}
	for _, a := range alerts {
		if a["state"] == "open" && len(as) < 5 {
			as = append(as, a)
		}
	}
	r["regions"] = rs
	r["operations"] = ops[:min(5, len(ops))]
	r["alerts"] = as
	return r
}
