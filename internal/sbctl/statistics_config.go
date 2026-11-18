package sbctl

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

type InboundIdentity struct {
	Tag       string `json:"tag"`
	Protocol  string `json:"protocol"`
	Port      int    `json:"port"`
	InboundID string `json:"inbound_id,omitempty"`
}
type StatisticsManifest struct {
	Inbounds      []InboundIdentity `json:"inbounds"`
	Outbounds     []string          `json:"outbounds"`
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
func configureStatistics(config M, excludedUsers []string) {
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
}
func statisticsManifest(config M, excludedUsers []string) *StatisticsManifest {
	out := &StatisticsManifest{Inbounds: []InboundIdentity{}, Outbounds: []string{}, LocalUsers: []string{}, TotalInbounds: len(array(config["inbounds"]))}
	for _, raw := range array(config["outbounds"]) {
		if tag := str(mapping(raw)["tag"]); tag != "" {
			out.Outbounds = append(out.Outbounds, tag)
		}
	}
	for _, raw := range array(config["inbounds"]) {
		in := mapping(raw)
		tag := str(in["tag"])
		if tag != "" {
			out.Inbounds = append(out.Inbounds, InboundIdentity{Tag: tag, Protocol: str(in["type"]), Port: int(integer(in["listen_port"]))})
		}
		for _, rawUser := range array(in["users"]) {
			name := str(mapping(rawUser)["name"])
			if name != "" && name != "sys_probe" && !contains(excludedUsers, name) {
				out.LocalUsers = append(out.LocalUsers, name)
			}
		}
	}
	out.LocalUsers = unique(out.LocalUsers)
	out.Outbounds = unique(out.Outbounds)
	sort.Slice(out.Inbounds, func(i, j int) bool { return out.Inbounds[i].Tag < out.Inbounds[j].Tag })
	return out
}
