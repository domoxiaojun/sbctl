package sbctl

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestStatisticsConfigPreservesCredentials(t *testing.T) {
	config := object([]byte(`{"inbounds":[{"type":"vless","listen_port":9443,"users":[{"uuid":"unchanged"}]},{"type":"direct","tag":"relay","listen_port":443},{"type":"anytls","tag":"any","users":[{"name":"usr_alice","password":"secret"},{"name":"sys_probe","password":"probe"}]}],"experimental":{"clash_api":{"secret":"preserved"}}}`))
	configureStatistics(config, []string{"usr_alice"})
	once := digest(config)
	configureStatistics(config, []string{"usr_alice"})
	if digest(config) != once {
		t.Fatal("configuration not stable")
	}
	vless := mapping(array(config["inbounds"])[0])
	user := mapping(array(vless["users"])[0])
	if user["uuid"] != "unchanged" || user["name"] == nil || vless["tag"] == nil {
		t.Fatal("metadata or credentials")
	}
	manifest := statisticsManifest(config, []string{"usr_alice"})
	if len(manifest.Inbounds) != 3 || len(manifest.LocalUsers) != 1 || mapping(mapping(config["experimental"])["clash_api"])["secret"] != "preserved" {
		t.Fatal("statistics scope")
	}
	if contains(stringsOf(mapping(mapping(mapping(config["experimental"])["v2ray_api"])["stats"])["users"]), "sys_probe") {
		t.Fatal("probe counted as user")
	}
	if contains(stringsOf(mapping(mapping(mapping(config["experimental"])["v2ray_api"])["stats"])["users"]), "usr_alice") {
		t.Fatal("managed user counted as local traffic")
	}
}
func TestStatisticsSeriesLifecycleAndUpgrade(t *testing.T) {
	root := t.TempDir()
	l := Ledger{filepath.Join(root, "ledger.db"), filepath.Join(root, "export.json"), "node_hk"}
	l.Create()
	rt := testRuntime()
	rt.Statistics = &StatisticsManifest{Inbounds: []InboundIdentity{{"relay", "direct", 443}}, TotalInbounds: 1}
	l.Ingest(rt, rt, []M{}, false)
	// Preserve the exact checksum of a snapshot without optional series.
	old := l.Snapshot()
	var decoded Snapshot
	decode(canonical(old), &decoded)
	decoded.Verify()
	rt.Statistics = &StatisticsManifest{Inbounds: []InboundIdentity{{"ss-in", "shadowsocks", 443}, {"idle", "direct", 444}}, LocalUsers: []string{"本地用户"}, TotalInbounds: 2}
	raw := func(up, down string) []M {
		return []M{{"name": "inbound>>>ss-in>>>traffic>>>uplink", "value": up}, {"name": "inbound>>>ss-in>>>traffic>>>downlink", "value": down}, {"name": "user>>>本地用户>>>traffic>>>uplink", "value": up}, {"name": "user>>>本地用户>>>traffic>>>downlink", "value": down}, {"name": "inbound>>>idle>>>traffic>>>uplink", "value": up}, {"name": "inbound>>>idle>>>traffic>>>downlink", "value": down}}
	}
	l.Ingest(rt, rt, raw("1000", "2000"), false)
	s := l.Snapshot()
	s.Verify()
	if len(s.Inbounds) != 2 || s.Inbounds[1].Upload != "0" || s.LocalUsers[0].Upload != "0" {
		t.Fatal("new series must baseline including upgrade")
	}
	l.Ingest(rt, rt, raw("1100", "2200"), false)
	s = l.Snapshot()
	if s.Inbounds[1].Upload != "100" || s.LocalUsers[0].Download != "200" {
		t.Fatal("series delta", s)
	}
	l.Ingest(rt, rt, raw("1100", "2200"), false)
	expectFault(t, "COUNTER_REGRESSION", func() { l.Ingest(rt, rt, raw("1000", "2000"), false) })
	if l.Snapshot().Inbounds[1].Upload != "100" {
		t.Fatal("regression changed total")
	}
	l.Ingest(rt, rt, raw("1100", "2200"), false)
	rt.StartedAt = "epoch-2"
	l.Ingest(rt, rt, raw("50", "60"), false)
	s = l.Snapshot()
	if s.Inbounds[1].Upload != "150" || s.LocalUsers[0].Download != "260" {
		t.Fatal("restart continuity")
	}
	traffic := Traffic{filepath.Join(root, "traffic.db"), filepath.Join(root, "traffic.json")}
	traffic.Initialize()
	traffic.Ingest("node_hk", old)
	traffic.Ingest("node_hk", s)
	bad := s
	bad.Seq++
	bad.LocalUsers = nil
	bad.Seal()
	expectFault(t, "TOTAL_REGRESSION", func() { traffic.Ingest("node_hk", bad) })
	rt.Statistics = &StatisticsManifest{Inbounds: []InboundIdentity{}, LocalUsers: []string{}, TotalInbounds: 0}
	l.Ingest(rt, rt, []M{}, false)
	s = l.Snapshot()
	if s.Inbounds[1].Active || s.Inbounds[1].Upload != "150" || s.LocalUsers[0].Active {
		t.Fatal("removed series history")
	}
	traffic.Ingest("node_hk", s)
}

func TestStatisticsSeriesPortChangeKeepsAccumulating(t *testing.T) {
	root := t.TempDir()
	l := Ledger{filepath.Join(root, "ledger.db"), filepath.Join(root, "export.json"), "node_hk"}
	l.Create()
	rt := testRuntime()
	rt.Statistics = &StatisticsManifest{Inbounds: []InboundIdentity{{"relay", "direct", 443}}, LocalUsers: []string{}, TotalInbounds: 1}
	first := []M{{"name": "inbound>>>relay>>>traffic>>>uplink", "value": "100"}, {"name": "inbound>>>relay>>>traffic>>>downlink", "value": "200"}}
	l.Ingest(rt, rt, first, false)
	rt.Statistics = &StatisticsManifest{Inbounds: []InboundIdentity{{"relay", "direct", 5443}}, LocalUsers: []string{}, TotalInbounds: 1}
	second := []M{{"name": "inbound>>>relay>>>traffic>>>uplink", "value": "140"}, {"name": "inbound>>>relay>>>traffic>>>downlink", "value": "260"}}
	if err := Attempt(func() { l.Ingest(rt, rt, second, false) }); err != nil {
		t.Fatalf("port edit must not freeze the ledger: %v", err)
	}
	s := l.Snapshot()
	if len(s.Inbounds) != 1 || s.Inbounds[0].Port != 5443 || s.Inbounds[0].Upload != "40" || s.Inbounds[0].Download != "60" {
		t.Fatalf("port change was not recorded while preserving deltas: %+v", s.Inbounds)
	}
}
func TestSnapshotChecksumWithoutOptionalSeries(t *testing.T) {
	s := Snapshot{SchemaVersion: 1, NodeID: "node_hk", LedgerID: "ledger_1", Seq: 1, StartedAt: now(), Quality: DataQuality{"ok", false, 0}}
	s.Seal()
	raw := string(canonical(s))
	if strings.Contains(raw, "inbounds") || strings.Contains(raw, "local_users") {
		t.Fatal("snapshot checksum changed")
	}
	var decoded Snapshot
	decode([]byte(raw), &decoded)
	decoded.Verify()
}

func TestStatisticsProtoZeroValues(t *testing.T) {
	root := t.TempDir()
	l := Ledger{filepath.Join(root, "ledger.db"), filepath.Join(root, "export.json"), "node_hk"}
	l.Create()
	rt := testRuntime()
	rt.Statistics = &StatisticsManifest{Inbounds: []InboundIdentity{{"relay", "direct", 443}}, LocalUsers: []string{"local"}, TotalInbounds: 1}
	raw := []M{}
	raw = append(raw, M{"name": "inbound>>>relay>>>traffic>>>uplink"}, M{"name": "inbound>>>relay>>>traffic>>>downlink"}, M{"name": "user>>>local>>>traffic>>>uplink"}, M{"name": "user>>>local>>>traffic>>>downlink"})
	for _, row := range raw {
		delete(row, "value")
	}
	l.Ingest(rt, rt, raw, false)
	l.Snapshot().Verify()
	for _, row := range raw {
		if strings.HasSuffix(str(row["name"]), ">>>uplink") {
			row["value"] = "12"
		}
	}
	l.Ingest(rt, rt, raw, false)
	s := l.Snapshot()
	if s.Inbounds[0].Upload != "12" || s.LocalUsers[0].Upload != "12" || s.Inbounds[0].Download != "0" {
		t.Fatal("proto zero or single-direction counter not recorded", s)
	}
	// A formerly positive counter reverting to implicit zero is still a regression.
	delete(raw[2], "value")
	expectFault(t, "COUNTER_REGRESSION", func() { l.Ingest(rt, rt, raw, false) })
	if l.Snapshot().Inbounds[0].Upload != "12" {
		t.Fatal("regression changed total")
	}
	expectFault(t, "COUNTER_INVALID", func() { statsCounter(M{"value": nil}) })
}
