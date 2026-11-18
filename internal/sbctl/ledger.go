package sbctl

import (
	"fmt"
	"strings"
	"time"
)

type Ledger struct{ Path, Export, NodeID string }

func (l Ledger) connectionCursor() (string, uint64) {
	var value int64
	var source string
	transaction(l.Path, false, func(d *DB) {
		value = integer(d.Value("SELECT value FROM meta WHERE key='connection_seq'"))
		source = str(d.Value("SELECT value FROM meta WHERE key='connection_epoch'"))
	})
	if value < 0 {
		fail("CONNECTION_SEQUENCE_OLD", "连接生命周期序号无效")
	}
	return source, uint64(value)
}

// Proto3 JSON omits the value field for a present counter whose value is zero.
// A missing counter row remains distinct and is checked for disappearance.
func statsCounter(row M) int64 {
	value, present := row["value"]
	if !present {
		return 0
	}
	return counter(value)
}

func (l Ledger) Create() {
	validIdentity(l.NodeID)
	if exists(l.Path) || exists(l.Export) {
		fail("LEDGER_EXISTS", "账本已存在或存在旧导出，拒绝自动重建")
	}
	initialize(l.Path, "ledger")
	transaction(l.Path, true, func(d *DB) {
		for k, v := range (M{"ledger_id": identifier("ledger"), "node_id": l.NodeID, "started_at": now(), "snapshot_seq": "0", "connection_seq": "0", "gap_count": "0", "integrity_state": "ok", "source_epoch": "", "sampled_at": "", "config_hash": ""}) {
			putMeta(d, k, str(v))
		}
	})
}
func (l Ledger) Ingest(before, after Runtime, raw []M, initialFromZero bool) {
	l.ingest(before, after, raw, nil, "", 0, false, initialFromZero)
}

func (l Ledger) IngestWithConnections(before, after Runtime, raw []M, connections []TrafficConnection, connectionEpoch string, connectionSeq uint64, connectionGap bool, initialFromZero bool) {
	l.ingest(before, after, raw, connections, connectionEpoch, connectionSeq, connectionGap, initialFromZero)
}

func (l Ledger) ingest(before, after Runtime, raw []M, connections []TrafficConnection, connectionEpoch string, connectionSeq uint64, connectionGap bool, initialFromZero bool) {
	if !before.Stable(after) {
		fail("RUNTIME_CHANGED", "采样期间运行实例或配置发生变化")
	}
	invalid := false
	transaction(l.Path, true, func(d *DB) {
		m := meta(d)
		if str(m["node_id"]) != l.NodeID {
			fail("LEDGER_IDENTITY", "账本节点身份不匹配")
		}
		oldEpoch := str(m["source_epoch"])
		if connectionEpoch != "" {
			before.StartedAt += "#" + connectionEpoch
		}
		epoch := before.Epoch()
		gaps := integer(m["gap_count"])
		if oldEpoch != "" && oldEpoch != epoch {
			gaps++
		}
		metricChanges, metricInvalid := prepareSeries(d, before, raw, initialFromZero)
		invalid = invalid || metricInvalid
		if invalid {
			putMeta(d, "integrity_state", "counter_error")
			putMeta(d, "snapshot_seq", fmt.Sprint(integer(m["snapshot_seq"])+1))
			return
		}
		commitSeries(d, before, metricChanges)
		if connectionEpoch != "" {
			recordConnectionInbounds(before.Statistics, connections)
			storeConnections(d, connections)
		}
		if connectionEpoch != "" {
			current := uint64(integer(m["connection_seq"]))
			if m["connection_epoch"] == connectionEpoch && connectionSeq < current {
				fail("CONNECTION_SEQUENCE_OLD", "连接生命周期序号回退")
			}
			putMeta(d, "connection_seq", fmt.Sprint(connectionSeq))
			putMeta(d, "connection_epoch", connectionEpoch)
			if connectionGap {
				putMeta(d, "connection_gap", "1")
			}
		}
		for k, v := range (M{"source_epoch": epoch, "config_hash": before.ConfigHash, "sampled_at": now(), "gap_count": fmt.Sprint(gaps), "integrity_state": "ok", "snapshot_seq": fmt.Sprint(integer(m["snapshot_seq"]) + 1)}) {
			putMeta(d, k, str(v))
		}
	})
	l.WriteExport()
	if invalid {
		fail("COUNTER_REGRESSION", "计数下降或已出现的计数消失，保留上次游标")
	}
}

func storeConnections(d *DB, connections []TrafficConnection) {
	seen := map[string]bool{}
	for _, c := range connections {
		if c.ID == "" || len(c.ID) > 128 || strings.ContainsAny(c.ID, "\x00\r\n") {
			fail("CONNECTION_INVALID", "连接身份无效")
		}
		if seen[c.ID] {
			fail("DUPLICATE_CONNECTION", "统计响应包含重复连接")
		}
		seen[c.ID] = true
		up, down := counter(c.Upload), counter(c.Download)
		started := stamp(c.StartedAt)
		c.StartedAt = connectionTime(started)
		closed := ""
		if c.ClosedAt != "" {
			if stamp(c.ClosedAt).Before(started) {
				fail("CONNECTION_INVALID", "连接关闭时间早于创建时间")
			}
			closed = c.ClosedAt
		}
		if len(c.Chain) > 32 {
			fail("CONNECTION_INVALID", "连接代理链过长")
		}
		d.Exec(`INSERT INTO connections(connection_id,inbound_id,inbound_tag,inbound_type,network,domain,destination,source,outbound,outbound_type,chain_json,rule,upload,download,started_at,closed_at,observed_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(connection_id) DO NOTHING`, c.ID, c.InboundID, c.Inbound, c.InboundType, c.Network, c.Domain, c.Destination, c.Source, c.Outbound, c.OutboundType, string(canonical(c.Chain)), c.Rule, up, down, c.StartedAt, closed, now())
	}
	cutoff := connectionTime(time.Now().Add(-90 * 24 * time.Hour))
	d.Exec("DELETE FROM connections WHERE started_at<?", cutoff)
}
func (l Ledger) Snapshot() Snapshot {
	var m M
	inbounds := []LedgerInbound{}
	outbounds := []LedgerOutbound{}
	localUsers := []LedgerLocalUser{}
	transaction(l.Path, false, func(d *DB) {
		m = meta(d)
		for _, r := range d.Rows("SELECT * FROM series_totals ORDER BY scope,tag,key") {
			if r["scope"] == "inbound" {
				in := LedgerInbound{Tag: str(r["tag"]), Protocol: str(r["protocol"]), Upload: str(r["upload"]), Download: str(r["download"]), Port: int(integer(r["port"])), StartedAt: str(r["started_at"]), Active: integer(r["active"]) == 1}
				if id := str(r["key"]); id != legacyInboundID(Endpoint{Protocol: in.Protocol, Tag: in.Tag}) {
					in.InboundID, in.HistoryTag = id, str(r["history_tag"])
				}
				inbounds = append(inbounds, in)
			} else if r["scope"] == "outbound" {
				outbounds = append(outbounds, LedgerOutbound{Tag: str(r["tag"]), Upload: str(r["upload"]), Download: str(r["download"]), StartedAt: str(r["started_at"]), Active: integer(r["active"]) == 1})
			} else {
				localUsers = append(localUsers, LedgerLocalUser{Name: str(r["key"]), Upload: str(r["upload"]), Download: str(r["download"]), StartedAt: str(r["started_at"]), Active: integer(r["active"]) == 1})
			}
		}
	})
	if integer(m["snapshot_seq"]) == 0 {
		fail("NO_SAMPLE", "账本尚无已提交采样", 503)
	}
	s := Snapshot{SchemaVersion: 1, NodeID: l.NodeID, LedgerID: str(m["ledger_id"]), Seq: integer(m["snapshot_seq"]), StartedAt: str(m["started_at"]), SampledAt: nullable(str(m["sampled_at"])), SourceEpoch: nullable(str(m["source_epoch"])), ConfigHash: nullable(str(m["config_hash"])), Inbounds: inbounds, Outbounds: outbounds, LocalUsers: localUsers, ConnectionFeed: str(m["connection_epoch"]) != "", ConnectionSeq: uint64(integer(m["connection_seq"])), TotalInbounds: int(integer(m["total_inbounds"])), Quality: DataQuality{str(m["integrity_state"]), integer(m["gap_count"]) > 0, integer(m["gap_count"])}}
	s.Seal()
	return s
}
func (l Ledger) WriteExport() { atomicWrite(l.Export, canonical(l.Snapshot()), 0640) }
