package sbctl

import (
	"fmt"
	"math"
	"strings"
)

type seriesChange struct {
	scope, key, protocol, dir, started string
	port                               int
	value, seen, delta                 int64
}

func prepareSeries(d *DB, runtime Runtime, raw []M, initialFromZero bool) ([]seriesChange, bool) {
	if runtime.Statistics == nil {
		return nil, false
	}
	type identity struct {
		scope, key, protocol string
		port                 int
	}
	expected := []identity{}
	for _, in := range runtime.Statistics.Inbounds {
		expected = append(expected, identity{"inbound", in.Tag, in.Protocol, in.Port})
	}
	for _, user := range runtime.Statistics.LocalUsers {
		expected = append(expected, identity{"user", user, "", 0})
	}
	keys := map[string]bool{}
	for _, in := range expected {
		if !validStatName(in.key) || keys[in.scope+">>>"+in.key] {
			fail("COUNTER_INVALID", "统计清单存在无效或重复标识")
		}
		keys[in.scope+">>>"+in.key] = true
	}
	counters := map[string]int64{}
	for _, row := range raw {
		name := str(row["name"])
		parts := strings.Split(name, ">>>")
		if len(parts) != 4 || parts[2] != "traffic" {
			continue
		}
		scope, key, dir := parts[0], parts[1], parts[3]
		if scope != "inbound" && scope != "user" {
			continue
		}
		if scope == "user" && contains(runtime.Users, key) {
			continue
		}
		if !keys[scope+">>>"+key] || !contains([]string{"uplink", "downlink"}, dir) {
			fail("COUNTER_UNKNOWN", "出现清单外的入口或本地用户计数")
		}
		if _, ok := counters[name]; ok {
			fail("DUPLICATE_COUNTER", "统计响应包含重复计数")
		}
		counters[name] = statsCounter(row)
	}
	changes := []seriesChange{}
	for _, in := range expected {
		record := d.One("SELECT started_at,protocol,port FROM series_totals WHERE scope=? AND key=?", in.scope, in.key)
		started := now()
		if record != nil {
			started = str(record["started_at"])
			if str(record["protocol"]) != in.protocol || integer(record["port"]) != int64(in.port) {
				fail("STATS_IDENTITY_CHANGED", "入口标签已用于不同协议或端口，请使用新标签保留历史")
			}
		}
		for _, dir := range []string{"uplink", "downlink"} {
			value, found := counters[in.scope+">>>"+in.key+">>>traffic>>>"+dir]
			cursor := d.One("SELECT value,seen FROM series_cursors WHERE scope=? AND key=? AND direction=? AND epoch=?", in.scope, in.key, dir, runtime.Epoch())
			prev, seen := int64(0), int64(0)
			if cursor != nil {
				prev, seen = integer(cursor["value"]), integer(cursor["seen"])
			}
			if (!found && seen == 1) || (found && value < prev) {
				return nil, true
			}
			if !found {
				value = prev
			} else {
				seen = 1
			}
			delta := value - prev
			// Newly enrolled series starts at its first observation, not historical process totals.
			if record == nil && !initialFromZero {
				delta = 0
			}
			changes = append(changes, seriesChange{in.scope, in.key, in.protocol, dir, started, in.port, value, seen, delta})
		}
	}
	return changes, false
}
func commitSeries(d *DB, runtime Runtime, changes []seriesChange) {
	if runtime.Statistics == nil {
		return
	}
	d.Exec("UPDATE series_totals SET active=0")
	for _, c := range changes {
		d.Exec("INSERT OR IGNORE INTO series_totals VALUES (?,?,?,?,0,0,?,1)", c.scope, c.key, c.protocol, c.port, c.started)
		col := "upload"
		if c.dir == "downlink" {
			col = "download"
		}
		total := integer(d.Value("SELECT "+col+" FROM series_totals WHERE scope=? AND key=?", c.scope, c.key))
		if c.delta > math.MaxInt64-total {
			fail("COUNTER_OVERFLOW", "统计累计超过存储范围")
		}
		d.Exec("UPDATE series_totals SET "+col+"=?,active=1 WHERE scope=? AND key=?", total+c.delta, c.scope, c.key)
		d.Exec("INSERT INTO series_cursors VALUES (?,?,?,?,?,?) ON CONFLICT(scope,key,direction,epoch) DO UPDATE SET value=excluded.value,seen=excluded.seen", c.scope, c.key, c.dir, runtime.Epoch(), c.value, c.seen)
	}
	d.Exec("DELETE FROM series_cursors WHERE epoch<>?", runtime.Epoch())
	putMeta(d, "total_inbounds", fmt.Sprint(runtime.Statistics.TotalInbounds))
}
