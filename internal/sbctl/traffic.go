package sbctl

import (
	"context"
	"strings"
	"sync"
	"time"
)

type Traffic struct{ Path, Export string }

// SyncNodes pulls every node's ledger export once. Fetching runs in parallel so
// an unreachable node costs only its own timeout; ingestion stays serialized
// because every ingest rewrites the shared telemetry export.
func SyncNodes(ctx context.Context, t Traffic, transport Transport, nodes []any, parallel int) {
	if parallel < 1 {
		parallel = 1
	}
	var ingest sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, parallel)
	for _, raw := range nodes {
		if ctx.Err() != nil {
			break
		}
		n := mapping(raw)
		slots <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			id := str(n["id"])
			err := Attempt(func() {
				link := parseConnection(n["connection"])
				v := transport.Call(link, nil, true)
				var snap Snapshot
				decode(canonical(v), &snap)
				func() {
					ingest.Lock()
					defer ingest.Unlock()
					t.Ingest(id, snap)
				}()
				if snap.ConnectionFeed {
					t.syncConnectionPages(transport, link, id, snap)
				}
			})
			if err != nil {
				ingest.Lock()
				defer ingest.Unlock()
				_ = Attempt(func() { t.Failed(id, PublicFault(err).Code) })
			}
		}()
	}
	wg.Wait()
}

func (t Traffic) Initialize() { initialize(t.Path, "traffic") }
func (t Traffic) Ingest(node string, s Snapshot) bool {
	return t.ingest(node, s, "")
}

func (t Traffic) RegisterReplacement(node, previousLedger string, s Snapshot) bool {
	validIdentity(previousLedger)
	if previousLedger == s.LedgerID || s.SampledAt == nil || s.Quality.Integrity != "ok" {
		fail("LEDGER_REPLACEMENT_INVALID", "新账本须使用新身份并提供有效采样")
	}
	return t.ingest(node, s, previousLedger)
}

func (t Traffic) ingest(node string, s Snapshot, previousLedger string) bool {
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
		replaced := false
		if previousLedger != "" {
			if prev == nil {
				fail("LEDGER_REPLACEMENT_INVALID", "主控没有待替换的旧账本")
			}
			old := str(prev["ledger_id"])
			if old == s.LedgerID && d.One("SELECT 1 FROM ledger_replacements WHERE node_id=? AND ledger_id=? AND replaced_by=?", node, previousLedger, s.LedgerID) != nil {
				return
			}
			if old != previousLedger || d.One("SELECT 1 FROM ledger_replacements WHERE node_id=? AND ledger_id=?", node, s.LedgerID) != nil {
				fail("LEDGER_CHANGED", "旧账本身份已变化或新身份已退役")
			}
			var oldSnapshot Snapshot
			decode([]byte(str(prev["document"])), &oldSnapshot)
			if oldSnapshot.SampledAt != nil && stamp(*s.SampledAt).Before(stamp(*oldSnapshot.SampledAt)) {
				fail("SNAPSHOT_OLD", "新账本采样时间早于旧账本最后采样")
			}
			d.Exec("INSERT INTO ledger_replacements VALUES (?,?,?,?,?)", node, old, s.LedgerID, prev["document"], now())
			replaced = true
		}
		if prev != nil && !replaced {
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
			if s.SourceEpoch != nil && old.SourceEpoch != nil && *s.SourceEpoch == *old.SourceEpoch && s.ConnectionSeq < old.ConnectionSeq {
				fail("CONNECTION_SEQUENCE_OLD", "连接生命周期序号回退")
			}
			oldInbounds := map[string]LedgerInbound{}
			for _, in := range old.Inbounds {
				oldInbounds[in.identity()] = in
			}
			currentInbounds := map[string]LedgerInbound{}
			for _, in := range s.Inbounds {
				currentInbounds[in.identity()] = in
			}
			for tag, in := range oldInbounds {
				cur, ok := currentInbounds[tag]
				if !ok || counter(cur.Upload) < counter(in.Upload) || counter(cur.Download) < counter(in.Download) {
					fail("TOTAL_REGRESSION", "入口累计下降或历史入口缺失，保留历史累计")
				}
				if cur.Protocol != in.Protocol || cur.StartedAt != in.StartedAt || cur.historyKey() != in.historyKey() {
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
		if len(s.Connections) > 0 {
			storeTrafficConnections(d, node, s.Connections)
		}
		if prev == nil || replaced || s.Seq > integer(prev["seq"]) {
			d.Exec("INSERT INTO snapshots VALUES (?,?,?,?,?,?) ON CONFLICT(node_id) DO UPDATE SET ledger_id=excluded.ledger_id,seq=excluded.seq,checksum=excluded.checksum,document=excluded.document,synced_at=excluded.synced_at", node, s.LedgerID, s.Seq, s.Checksum, string(canonical(s)), now())
			changed = true
		}
		d.Exec("INSERT INTO observations VALUES (?,NULL,?) ON CONFLICT(node_id) DO UPDATE SET error_code=NULL,checked_at=excluded.checked_at", node, now())
	})
	t.WriteExport()
	return changed
}

func storeTrafficConnections(d *DB, node string, connections []TrafficConnection) {
	seen := map[string]bool{}
	for _, c := range connections {
		if c.ID == "" || len(c.ID) > 128 || strings.ContainsAny(c.ID, "\x00\r\n") || seen[c.ID] {
			fail("CONNECTION_INVALID", "统计快照包含无效或重复连接")
		}
		seen[c.ID] = true
		up, down := counter(c.Upload), counter(c.Download)
		started := stamp(c.StartedAt)
		c.StartedAt = connectionTime(started)
		if c.ClosedAt != "" && stamp(c.ClosedAt).Before(started) {
			fail("CONNECTION_INVALID", "连接关闭时间早于创建时间")
		}
		d.Exec(`INSERT INTO connections(node_id,connection_id,inbound_id,inbound_tag,inbound_type,network,domain,destination,source,outbound,outbound_type,chain_json,rule,upload,download,started_at,closed_at,observed_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(node_id,connection_id) DO UPDATE SET inbound_id=excluded.inbound_id,inbound_tag=excluded.inbound_tag,inbound_type=excluded.inbound_type,network=excluded.network,domain=excluded.domain,destination=excluded.destination,source=excluded.source,outbound=excluded.outbound,outbound_type=excluded.outbound_type,chain_json=excluded.chain_json,rule=excluded.rule,upload=excluded.upload,download=excluded.download,started_at=excluded.started_at,closed_at=excluded.closed_at,observed_at=excluded.observed_at`, node, c.ID, c.InboundID, c.Inbound, c.InboundType, c.Network, c.Domain, c.Destination, c.Source, c.Outbound, c.OutboundType, string(canonical(c.Chain)), c.Rule, up, down, c.StartedAt, c.ClosedAt, now())
	}
	cutoff := connectionTime(time.Now().Add(-90 * 24 * time.Hour))
	d.Exec("DELETE FROM connections WHERE started_at<?", cutoff)
}
func (t Traffic) LedgerHistory(node string) []M {
	validIdentity(node)
	var rows []M
	transaction(t.Path, false, func(d *DB) {
		rows = d.Rows("SELECT ledger_id,replaced_by,registered_at,document FROM ledger_replacements WHERE node_id=? ORDER BY registered_at,ledger_id", node)
	})
	for _, row := range rows {
		row["last_snapshot"] = object([]byte(str(row["document"])))
		delete(row, "document")
	}
	return rows
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
