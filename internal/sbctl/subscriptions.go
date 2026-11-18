package sbctl

import (
	"fmt"
	"go.yaml.in/yaml/v3"
	"path/filepath"
	"strings"
	"time"
)

type PublicStore struct{ Root string }

func (p PublicStore) Lock() func() {
	return fileLock(filepath.Join(filepath.Dir(p.Root), "publication.lock"), 3*time.Second)
}
func (p PublicStore) Current() M {
	path := filepath.Join(p.Root, "index.json")
	if !exists(path) {
		return M{}
	}
	return object(read(path))
}
func (p PublicStore) Revoke(key string) {
	idx := p.Current()
	delete(idx, key)
	atomicWrite(filepath.Join(p.Root, "index.json"), canonical(idx), 0644)
}
func (p PublicStore) Publish(key string, rev int64, files map[string]string) {
	if !hashRE.MatchString(key) || rev < 1 || len(files) != 2 || files["surge.conf"] == "" || files["mihomo.yaml"] == "" {
		fail("PUBLICATION_INVALID", "订阅发布标识或文件无效")
	}
	manifest := M{}
	for name, body := range files {
		if name != "surge.conf" && name != "mihomo.yaml" {
			fail("PUBLICATION_INVALID", "订阅文件名无效")
		}
		manifest[name] = hash([]byte(body))
		atomicWrite(filepath.Join(p.Root, "data", key, fmt.Sprint(rev), name), []byte(body), 0644)
	}
	idx := p.Current()
	idx[key] = M{"revision": rev, "files": manifest}
	atomicWrite(filepath.Join(p.Root, "index.json"), canonical(idx), 0644)
}
func (p PublicStore) Read(t, name string) []byte {
	if !match(`^[A-Za-z0-9_-]{40,100}$`, t) || !contains([]string{"surge.conf", "mihomo.yaml"}, name) {
		fail("NOT_FOUND", "订阅不存在或已撤销", 404)
	}
	key := hash([]byte(t))
	entry := mapping(p.Current()[key])
	if len(entry) == 0 {
		fail("NOT_FOUND", "订阅不存在或已撤销", 404)
	}
	rev := integer(entry["revision"])
	if rev < 1 {
		fail("PUBLICATION_INVALID", "订阅暂不可用", 503)
	}
	b := read(filepath.Join(p.Root, "data", key, fmt.Sprint(rev), name))
	if len(b) > 1024*1024 || hash(b) != str(mapping(entry["files"])[name]) {
		fail("PUBLICATION_INVALID", "订阅暂不可用", 503)
	}
	return b
}
func renderSubscriptions(endpoints []M) map[string]string {
	proxies := []M{}
	surge, names := []string{}, []string{}
	nameCounts := map[string]int{}
	for _, e := range endpoints {
		protocol := str(e["protocol"])
		name := str(e["node_id"]) + "-" + strings.ToUpper(protocol)
		nameCounts[name]++
		if nameCounts[name] > 1 {
			name += "-" + str(e["inbound_id"])
		}
		for _, k := range []string{"server", "sni", "password", "uuid"} {
			s := str(e[k])
			if strings.ContainsAny(s, ",;[]\r\n\x00") {
				fail("SUBSCRIPTION_INVALID", "订阅字段包含不支持的分隔符")
			}
		}
		p := M{"name": name, "type": protocol, "server": e["server"], "port": integer(e["port"]), "password": e["password"], "sni": e["sni"], "skip-cert-verify": false, "udp": true}
		switch protocol {
		case "tuic":
			alpn := stringsOf(e["alpn"])
			if len(alpn) == 0 {
				alpn = []string{"h3"}
			}
			if !match(`^[A-Za-z0-9./_-]{1,32}$`, alpn[0]) {
				fail("SUBSCRIPTION_INVALID", "ALPN 字段无效")
			}
			merge(p, M{"uuid": e["uuid"], "alpn": alpn, "congestion-controller": "cubic", "udp-relay-mode": "native"})
			surge = append(surge, fmt.Sprintf("%s = tuic-v5, %s, %d, uuid=%s, password=%s, sni=%s, alpn=%s, skip-cert-verify=false", name, e["server"], integer(e["port"]), e["uuid"], e["password"], e["sni"], alpn[0]))
		case "anytls":
			surge = append(surge, fmt.Sprintf("%s = anytls, %s, %d, password=%s, sni=%s, skip-cert-verify=false", name, e["server"], integer(e["port"]), e["password"], e["sni"]))
		default:
			fail("SUBSCRIPTION_INVALID", "协议不受支持")
		}
		proxies = append(proxies, p)
		names = append(names, name)
	}
	if len(proxies) == 0 {
		fail("NOT_READY", "尚无已验证的订阅节点", 409)
	}
	profile := "[General]\nloglevel = notify\n\n[Proxy]\n" + strings.Join(surge, "\n") + "\n\n[Proxy Group]\nSBCTL = select, " + strings.Join(names, ", ") + "\n\n[Rule]\nFINAL,SBCTL\n"
	m := M{"mixed-port": 7890, "allow-lan": false, "mode": "rule", "log-level": "warning", "proxies": proxies, "proxy-groups": []M{{"name": "SBCTL", "type": "select", "proxies": names}}, "rules": []string{"MATCH,SBCTL"}}
	y, e := yaml.Marshal(m)
	check(e)
	return map[string]string{"surge.conf": profile, "mihomo.yaml": string(y)}
}
