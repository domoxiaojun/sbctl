package sbctl

import "time"

type Traffic struct{ Path, Export string }

func (t Traffic) Initialize() { initialize(t.Path, "traffic") }
func (t Traffic) Ingest(node string, s Snapshot) bool {
	s.Verify()
	if s.NodeID != node {
		fail("NODE_IDENTITY", "统计快照与 SSH 节点身份不匹配")
	}
	times := []string{s.StartedAt}
	if s.SampledAt != nil {
		times = append(times, *s.SampledAt)
	}
	for _, in := range s.Inbounds {
		times = append(times, in.StartedAt)
	}
	for _, u := range s.LocalUsers {
		times = append(times, u.StartedAt)
	}
	for _, v := range times {
		if stamp(v).After(time.Now().Add(2 * time.Minute)) {
			fail("SNAPSHOT_TIME", "统计快照时间无效")
		}
	}
	changed := false
	transaction(t.Path, true, func(d *DB) {
		prev := d.One("SELECT * FROM snapshots WHERE node_id=?", node)
		if prev != nil {
			if s.LedgerID != str(prev["ledger_id"]) {
				fail("LEDGER_CHANGED", "节点账本身份发生变化，保留历史并等待维护确认")
			}
			if s.Seq < integer(prev["seq"]) {
				fail("SNAPSHOT_OLD", "节点快照序号退回，保留历史累计")
			}
			if s.Seq == integer(prev["seq"]) && s.Checksum != str(prev["checksum"]) {
				fail("SNAPSHOT_CONFLICT", "同一快照序号的内容发生变化")
			}
			var old Snapshot
			decode([]byte(str(prev["document"])), &old)
			current := map[string]LedgerUser{}
			for _, u := range s.Users {
				current[u.UserID] = u
			}
			for _, u := range old.Users {
				v, ok := current[u.UserID]
				if !ok || counter(v.Upload) < counter(u.Upload) || counter(v.Download) < counter(u.Download) {
					fail("TOTAL_REGRESSION", "节点累计下降或历史用户缺失，保留历史累计")
				}
			}
			oldInbounds := map[string]LedgerInbound{}
			for _, in := range old.Inbounds {
				oldInbounds[in.Tag] = in
			}
			currentInbounds := map[string]LedgerInbound{}
			for _, in := range s.Inbounds {
				currentInbounds[in.Tag] = in
			}
			for tag, in := range oldInbounds {
				cur, ok := currentInbounds[tag]
				if !ok || counter(cur.Upload) < counter(in.Upload) || counter(cur.Download) < counter(in.Download) {
					fail("TOTAL_REGRESSION", "入口累计下降或历史入口缺失，保留历史累计")
				}
				if cur.Protocol != in.Protocol || cur.Port != in.Port || cur.StartedAt != in.StartedAt {
					fail("HISTORY_REGRESSION", "入口统计身份或起点变化")
				}
			}
			currentLocal := map[string]LedgerLocalUser{}
			for _, u := range s.LocalUsers {
				currentLocal[u.Name] = u
			}
			for _, u := range old.LocalUsers {
				cur, ok := currentLocal[u.Name]
				if !ok || counter(cur.Upload) < counter(u.Upload) || counter(cur.Download) < counter(u.Download) || cur.StartedAt != u.StartedAt {
					fail("TOTAL_REGRESSION", "本地用户累计回退或历史缺失")
				}
			}
			if !stamp(s.StartedAt).Equal(stamp(old.StartedAt)) || s.Quality.Gaps < old.Quality.Gaps {
				fail("HISTORY_REGRESSION", "节点统计历史退回")
			}
			if old.SampledAt != nil && (s.SampledAt == nil || stamp(*s.SampledAt).Before(stamp(*old.SampledAt))) {
				fail("SNAPSHOT_OLD", "节点采样时间退回")
			}
		}
		recordTrafficDay(d, node, prev, s)
		if prev == nil || s.Seq > integer(prev["seq"]) {
			d.Exec("INSERT INTO snapshots VALUES (?,?,?,?,?,?) ON CONFLICT(node_id) DO UPDATE SET seq=excluded.seq,checksum=excluded.checksum,document=excluded.document,synced_at=excluded.synced_at", node, s.LedgerID, s.Seq, s.Checksum, string(canonical(s)), now())
			changed = true
		}
		d.Exec("INSERT INTO observations VALUES (?,NULL,?) ON CONFLICT(node_id) DO UPDATE SET error_code=NULL,checked_at=excluded.checked_at", node, now())
	})
	t.WriteExport()
	return changed
}
func (t Traffic) Failed(node, code string) {
	transaction(t.Path, true, func(d *DB) {
		d.Exec("INSERT INTO observations VALUES (?,?,?) ON CONFLICT(node_id) DO UPDATE SET error_code=excluded.error_code,checked_at=excluded.checked_at", node, code, now())
	})
	t.WriteExport()
}
func (t Traffic) WriteExport() {
	t.writeHistoryExport()
	nodes := M{}
	transaction(t.Path, false, func(d *DB) {
		for _, r := range d.Rows("SELECT * FROM snapshots") {
			nodes[str(r["node_id"])] = M{"snapshot": object([]byte(str(r["document"]))), "synced_at": r["synced_at"]}
		}
		for _, r := range d.Rows("SELECT * FROM observations") {
			id := str(r["node_id"])
			entry := mapping(nodes[id])
			entry["error_code"] = r["error_code"]
			entry["checked_at"] = r["checked_at"]
			nodes[id] = entry
		}
		for _, r := range d.Rows("SELECT * FROM history_nodes") {
			mapping(nodes[str(r["node_id"])])["history_started_at"] = r["started_at"]
		}
	})
	atomicWrite(t.Export, canonical(M{"schema_version": 1, "nodes": nodes}), 0644)
}
