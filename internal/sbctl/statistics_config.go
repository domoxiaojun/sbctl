package sbctl

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

type InboundIdentity struct {
	Tag      string `json:"tag"`
	Protocol string `json:"protocol"`
	Port     int    `json:"port"`
}
type StatisticsManifest struct {
	Inbounds      []InboundIdentity `json:"inbounds"`
	LocalUsers    []string          `json:"local_users"`
	TotalInbounds int               `json:"total_inbounds"`
}

func validStatName(s string) bool {
	if s == "" || len(s) > 256 || strings.Contains(s, ">>>") {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// Only metadata is added; credentials, listeners, routing and TLS remain intact.
func configureStatistics(config M) {
	used := map[string]bool{}
	for _, raw := range array(config["inbounds"]) {
		tag := str(mapping(raw)["tag"])
		if tag != "" {
			if !validStatName(tag) || used[tag] {
				fail("STATS_TAG", "入口标签无效或重复")
			}
			used[tag] = true
		}
	}
	for index, raw := range array(config["inbounds"]) {
		in := mapping(raw)
		if str(in["tag"]) == "" {
			tag := fmt.Sprintf("sbctl-in-%s-%d", str(in["type"]), integer(in["listen_port"]))
			if used[tag] {
				tag += fmt.Sprintf("-%d", index+1)
			}
			if used[tag] {
				fail("STATS_TAG", "自动统计标签冲突，请设置唯一入口标签")
			}
			in["tag"], used[tag] = tag, true
		}
		// These protocols expose a separate display name without changing authentication.
		if contains([]string{"vmess", "vless", "trojan", "shadowsocks", "hysteria2", "tuic", "anytls"}, str(in["type"])) {
			for n, rawUser := range array(in["users"]) {
				user := mapping(rawUser)
				if str(user["name"]) == "" {
					user["name"] = fmt.Sprintf("sbctl-local-%s-%d", str(in["tag"]), n+1)
				}
			}
		}
	}
	tags, users := []string{}, []string{}
	for _, raw := range array(config["inbounds"]) {
		in := mapping(raw)
		tags = append(tags, str(in["tag"]))
		for _, rawUser := range array(in["users"]) {
			name := str(mapping(rawUser)["name"])
			if name != "" && name != "sys_probe" {
				if !validStatName(name) {
					fail("STATS_USER", "用户统计名称无效")
				}
				users = append(users, name)
			}
		}
	}
	experimental := mapping(config["experimental"])
	api := mapping(experimental["v2ray_api"])
	stats := mapping(api["stats"])
	stats["enabled"], stats["inbounds"], stats["users"] = true, unique(tags), unique(users)
	api["listen"], api["stats"] = "127.0.0.1:10085", stats
	experimental["v2ray_api"], config["experimental"] = api, experimental
}
func statisticsManifest(config M, managed []string) *StatisticsManifest {
	api := mapping(mapping(config["experimental"])["v2ray_api"])
	stats := mapping(api["stats"])
	if api["listen"] != "127.0.0.1:10085" || !boolean(stats["enabled"]) {
		fail("STATS_CONFIG", "统计服务未按登记配置启用")
	}
	tags, names := stringsOf(stats["inbounds"]), stringsOf(stats["users"])
	for _, name := range managed {
		if !contains(names, name) {
			fail("STATS_CONFIG", "受管用户缺少统计配置")
		}
	}
	out := &StatisticsManifest{Inbounds: []InboundIdentity{}, LocalUsers: []string{}, TotalInbounds: len(array(config["inbounds"]))}
	for _, raw := range array(config["inbounds"]) {
		in := mapping(raw)
		tag := str(in["tag"])
		if tag != "" && contains(tags, tag) {
			out.Inbounds = append(out.Inbounds, InboundIdentity{tag, str(in["type"]), int(integer(in["listen_port"]))})
		}
		for _, rawUser := range array(in["users"]) {
			name := str(mapping(rawUser)["name"])
			if name != "" && name != "sys_probe" && !contains(managed, name) && contains(names, name) {
				out.LocalUsers = append(out.LocalUsers, name)
			}
		}
	}
	out.LocalUsers = unique(out.LocalUsers)
	sort.Slice(out.Inbounds, func(i, j int) bool { return out.Inbounds[i].Tag < out.Inbounds[j].Tag })
	return out
}
