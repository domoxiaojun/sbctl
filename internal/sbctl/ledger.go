package sbctl

import "fmt"

type Ledger struct{ Path, Export, NodeID string }

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
		for k, v := range (M{"ledger_id": identifier("ledger"), "node_id": l.NodeID, "started_at": now(), "snapshot_seq": "0", "gap_count": "0", "integrity_state": "ok", "source_epoch": "", "sampled_at": "", "config_hash": ""}) {
			putMeta(d, k, str(v))
		}
	})
}
func (l Ledger) Ingest(before, after Runtime, raw []M, initialFromZero bool) {
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
		for k, v := range (M{"source_epoch": epoch, "config_hash": before.ConfigHash, "sampled_at": now(), "gap_count": fmt.Sprint(gaps), "integrity_state": "ok", "snapshot_seq": fmt.Sprint(integer(m["snapshot_seq"]) + 1)}) {
			putMeta(d, k, str(v))
		}
	})
	l.WriteExport()
	if invalid {
		fail("COUNTER_REGRESSION", "计数下降或已出现的计数消失，保留上次游标")
	}
}
func (l Ledger) Snapshot() Snapshot {
	var m M
	inbounds := []LedgerInbound{}
	localUsers := []LedgerLocalUser{}
	transaction(l.Path, false, func(d *DB) {
		m = meta(d)
		for _, r := range d.Rows("SELECT * FROM series_totals ORDER BY scope,key") {
			if r["scope"] == "inbound" {
				inbounds = append(inbounds, LedgerInbound{Tag: str(r["key"]), Protocol: str(r["protocol"]), Upload: str(r["upload"]), Download: str(r["download"]), Port: int(integer(r["port"])), StartedAt: str(r["started_at"]), Active: integer(r["active"]) == 1})
			} else {
				localUsers = append(localUsers, LedgerLocalUser{Name: str(r["key"]), Upload: str(r["upload"]), Download: str(r["download"]), StartedAt: str(r["started_at"]), Active: integer(r["active"]) == 1})
			}
		}
	})
	if integer(m["snapshot_seq"]) == 0 {
		fail("NO_SAMPLE", "账本尚无已提交采样", 503)
	}
	s := Snapshot{SchemaVersion: 1, NodeID: l.NodeID, LedgerID: str(m["ledger_id"]), Seq: integer(m["snapshot_seq"]), StartedAt: str(m["started_at"]), SampledAt: nullable(str(m["sampled_at"])), SourceEpoch: nullable(str(m["source_epoch"])), ConfigHash: nullable(str(m["config_hash"])), Inbounds: inbounds, LocalUsers: localUsers, TotalInbounds: int(integer(m["total_inbounds"])), Quality: DataQuality{str(m["integrity_state"]), integer(m["gap_count"]) > 0, integer(m["gap_count"])}}
	s.Seal()
	return s
}
func (l Ledger) WriteExport() { atomicWrite(l.Export, canonical(l.Snapshot()), 0640) }
