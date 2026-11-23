package sbctl

import (
	"time"
)

const connectionPageSize = 128

type connectionPage struct {
	NodeID   string              `json:"node_id"`
	LedgerID string              `json:"ledger_id"`
	Next     int64               `json:"next"`
	Gap      bool                `json:"gap"`
	More     bool                `json:"more"`
	Items    []TrafficConnection `json:"items"`
}

func recordConnectionInbounds(manifest *StatisticsManifest, rows []TrafficConnection) {
	if manifest == nil {
		return
	}
	byTag := map[string]string{}
	for _, in := range manifest.Inbounds {
		byTag[in.Tag] = in.InboundID
	}
	for i := range rows {
		rows[i].InboundID = byTag[rows[i].Inbound]
	}
}

func (l Ledger) connectionPage(after int64) (p connectionPage) {
	transaction(l.Path, false, func(d *DB) {
		m := meta(d)
		p = connectionPage{NodeID: str(m["node_id"]), LedgerID: str(m["ledger_id"]), Next: after, Gap: m["connection_gap"] == "1", Items: []TrafficConnection{}}
		rows := d.Rows("SELECT * FROM connections WHERE seq>? ORDER BY seq LIMIT ?", after, connectionPageSize+1)
		p.More = len(rows) > connectionPageSize
		for _, row := range rows[:min(len(rows), connectionPageSize)] {
			var chain []string
			decode([]byte(str(row["chain_json"])), &chain)
			p.Items = append(p.Items, TrafficConnection{
				ID: str(row["connection_id"]), InboundID: str(row["inbound_id"]), Inbound: str(row["inbound_tag"]), InboundType: str(row["inbound_type"]),
				Network: str(row["network"]), Domain: str(row["domain"]), Destination: str(row["destination"]), Source: str(row["source"]), Outbound: str(row["outbound"]),
				OutboundType: str(row["outbound_type"]), Chain: chain, Rule: str(row["rule"]),
				Upload: str(row["upload"]), Download: str(row["download"]), StartedAt: str(row["started_at"]), ClosedAt: str(row["closed_at"]),
			})
			seq := integer(row["seq"])
			p.Gap = p.Gap || seq != p.Next+1
			p.Next = seq
		}
		if len(rows) == 0 && integer(d.Value("SELECT seq FROM sqlite_sequence WHERE name='connections'")) > after {
			p.Gap = true
		}
	})
	return p
}

// IngestConnectionBatch commits a pushed connection lifecycle page from an
// embedded sing-box Agent in a single write transaction.
func (t Traffic) IngestConnectionBatch(node, ledgerID string, after, next int64, pageGap bool, items []TrafficConnection) {
	if after < 0 || next < after || next-after < int64(len(items)) {
		fail("STATS_PROTOCOL", "连接明细游标范围无效", 422)
	}
	transaction(t.Path, true, func(d *DB) {
		row := d.One("SELECT ledger_id FROM snapshots WHERE node_id=?", node)
		if row == nil || row["ledger_id"] != ledgerID {
			fail("LEDGER_CHANGED", "连接明细对应账本已更换", 409)
		}
		existing := d.One("SELECT * FROM connection_cursors WHERE node_id=?", node)
		if existing != nil && existing["ledger_id"] == ledgerID && next <= integer(existing["seq"]) {
			if pageGap {
				d.Exec("UPDATE connection_cursors SET history_gap=1 WHERE node_id=?", node)
			}
			return
		}
		if len(items) > 0 {
			storeTrafficConnections(d, t.Path, node, items)
		}
		gap := pageGap || next-after > int64(len(items)) || (existing == nil && after > 0) || (existing != nil && (existing["ledger_id"] != ledgerID || integer(existing["history_gap"]) != 0))
		if existing != nil && existing["ledger_id"] == ledgerID && after > integer(existing["seq"]) {
			gap = true
		}
		d.Exec("INSERT INTO connection_cursors VALUES (?,?,?,?) ON CONFLICT(node_id) DO UPDATE SET ledger_id=excluded.ledger_id,seq=excluded.seq,history_gap=excluded.history_gap", node, ledgerID, next, gap)
	})
}

func (t Traffic) syncConnectionPages(transport Transport, link NodeConnection, node string, s Snapshot) {
	var after int64
	transaction(t.Path, false, func(d *DB) {
		after = integer(d.Value("SELECT seq FROM connection_cursors WHERE node_id=? AND ledger_id=?", node, s.LedgerID))
	})
	for range 16 {
		var p connectionPage
		decode(canonical(transport.Call(link, M{"action": "connections", "node_id": node, "after": after}, true)), &p)
		if p.NodeID != node || p.LedgerID != s.LedgerID || p.Next < after || len(p.Items) > 256 ||
			(len(p.Items) > 0 && p.Next-after < int64(len(p.Items))) || (p.More && p.Next <= after) {
			fail("STATS_PROTOCOL", "连接明细回执无效", 503)
		}
		transaction(t.Path, true, func(d *DB) {
			row := d.One("SELECT ledger_id FROM snapshots WHERE node_id=?", node)
			if row == nil || row["ledger_id"] != p.LedgerID {
				fail("LEDGER_CHANGED", "连接明细对应账本已更换")
			}
			existing := d.One("SELECT * FROM connection_cursors WHERE node_id=?", node)
			if existing != nil && existing["ledger_id"] == p.LedgerID && integer(existing["seq"]) != after {
				fail("STATS_CURSOR_CHANGED", "连接明细游标已变化，请重试")
			}
			storeTrafficConnections(d, t.Path, node, p.Items)
			gap := p.Gap || (existing != nil && (existing["ledger_id"] != p.LedgerID || integer(existing["history_gap"]) != 0))
			d.Exec("INSERT INTO connection_cursors VALUES (?,?,?,?) ON CONFLICT(node_id) DO UPDATE SET ledger_id=excluded.ledger_id,seq=excluded.seq,history_gap=excluded.history_gap", node, p.LedgerID, p.Next, gap)
		})
		if !p.More {
			return
		}
		after = p.Next
	}
}

func connectionTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000000000Z")
}

func readTrafficDB(path string, fn func(*DB)) {
	transaction(path, false, fn)
}
