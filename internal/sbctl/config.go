package sbctl

import (
	"bytes"
	"github.com/pelletier/go-toml/v2"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strings"
)

var identityRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{2,79}$`)
var hashRE = regexp.MustCompile(`^[a-f0-9]{64}$`)

func validIdentity(s string) {
	if !identityRE.MatchString(s) {
		fail("INVALID_REQUEST", "身份标识无效", 422)
	}
}
func match(pattern, s string) bool { return regexp.MustCompile(pattern).MatchString(s) }

type Settings struct {
	ControlDB       string   `toml:"control_db"`
	TrafficDB       string   `toml:"traffic_db"`
	TrafficSnapshot string   `toml:"traffic_snapshot"`
	PublicDir       string   `toml:"public_dir"`
	KeyFile         string   `toml:"key_file"`
	PublicBaseURL   string   `toml:"public_base_url"`
	AllowedOrigins  []string `toml:"allowed_origins"`
	SessionSeconds  int      `toml:"session_seconds"`
	SecureCookie    bool     `toml:"secure_cookie"`
	MaxUsers        int      `toml:"max_users"`
	StaleSeconds    int      `toml:"stale_seconds"`
	SyncSeconds     int      `toml:"sync_seconds"`
	SSHDir          string   `toml:"ssh_dir"`
	WebDir          string   `toml:"web_dir"`
	InventoryFile   string   `toml:"inventory_file"`
	DiscoveryFile   string   `toml:"discovery_file"`
	TrustedProxies  []string `toml:"trusted_proxies"`
}

func DefaultSettings() Settings {
	return Settings{ControlDB: "/data/control/control.db", TrafficDB: "/data/traffic/traffic.db", TrafficSnapshot: "/data/telemetry/traffic.json", PublicDir: "/data/subscriptions", KeyFile: "/run/secrets/master.key", SessionSeconds: 28800, SecureCookie: true, MaxUsers: 100, StaleSeconds: 180, SyncSeconds: 30, SSHDir: "/run/secrets/ssh", WebDir: "/opt/sbctl/web", InventoryFile: "/data/inventory/nodes.json", DiscoveryFile: "/data/discovery/tailscale.json"}
}
func env(name, fallback string) string {
	if s := os.Getenv(name); s != "" {
		return s
	}
	return fallback
}
func LoadSettings() Settings {
	s := DefaultSettings()
	check(toml.NewDecoder(bytes.NewReader(read(env("SBCTL_CONFIG", "/opt/sbctl/config/server.host.toml")))).DisallowUnknownFields().Decode(&s))
	u, e := url.Parse(s.PublicBaseURL)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		fail("CONFIG_INVALID", "订阅基础地址必须为 HTTPS")
	}
	s.PublicBaseURL = strings.TrimRight(s.PublicBaseURL, "/")
	if len(s.AllowedOrigins) == 0 || s.SessionSeconds < 300 || s.SessionSeconds > 86400 || s.MaxUsers < 1 || s.MaxUsers > 1000 || s.StaleSeconds < 30 || s.StaleSeconds > 3600 || s.SyncSeconds < 5 || s.SyncSeconds > 300 {
		fail("CONFIG_INVALID", "主控配置超出范围")
	}
	for _, proxy := range s.TrustedProxies {
		if _, ok := proxyPrefix(proxy); !ok {
			fail("CONFIG_INVALID", "受信任反代必须是 IP 地址或 CIDR 网段")
		}
	}
	return s
}

// proxyPrefix accepts a single address or a CIDR range.
func proxyPrefix(value string) (netip.Prefix, bool) {
	if p, err := netip.ParsePrefix(value); err == nil {
		return p.Masked(), true
	}
	if a, err := netip.ParseAddr(value); err == nil {
		a = a.Unmap()
		return netip.PrefixFrom(a, a.BitLen()), true
	}
	return netip.Prefix{}, false
}
func (s Settings) trustsProxy(a netip.Addr) bool {
	for _, proxy := range s.TrustedProxies {
		if p, ok := proxyPrefix(proxy); ok && p.Contains(a) {
			return true
		}
	}
	return false
}

type Endpoint struct {
	Protocol string   `json:"protocol" toml:"protocol"`
	Tag      string   `json:"tag" toml:"tag"`
	Server   string   `json:"server" toml:"server"`
	Port     int      `json:"port" toml:"port"`
	SNI      string   `json:"sni" toml:"sni"`
	ALPN     []string `json:"alpn" toml:"alpn"`
	// InboundID is the immutable identity of this managed entry. Tag is a
	// display/configuration label and may be edited without changing history.
	InboundID string `json:"inbound_id,omitempty" toml:"inbound_id,omitempty"`
}

func (e *Endpoint) Validate() {
	if e.Protocol != "anytls" && e.Protocol != "tuic" || !match(`^[a-zA-Z0-9_-]{1,64}$`, e.Tag) || !match(`^[a-zA-Z0-9.:-]{1,253}$`, e.Server) || !match(`^[a-zA-Z0-9.-]{1,253}$`, e.SNI) || e.Port < 1 || e.Port > 65535 || len(e.ALPN) > 4 {
		fail("ENDPOINT_INVALID", "入口配置无效")
	}
	if e.InboundID != "" {
		validIdentity(e.InboundID)
	}
	if e.ALPN == nil {
		e.ALPN = []string{"h3"}
	}
	for _, a := range e.ALPN {
		if !match(`^[A-Za-z0-9./_-]{1,32}$`, a) {
			fail("ENDPOINT_INVALID", "ALPN 无效")
		}
	}
}

type NodeSettings struct {
	NodeID        string     `toml:"node_id" json:"node_id"`
	StateDir      string     `toml:"state_dir" json:"state_dir"`
	SafetyDir     string     `toml:"safety_dir" json:"safety_dir"`
	ConfigDir     string     `toml:"config_dir" json:"config_dir"`
	TemplateFile  string     `toml:"template_file" json:"template_file"`
	ProbeFile     string     `toml:"probe_file" json:"probe_file"`
	TemplateHash  string     `toml:"template_hash" json:"template_hash"`
	Image         string     `toml:"image" json:"image"`
	EngineBuildID string     `toml:"engine_build_id" json:"engine_build_id"`
	Container     string     `toml:"container" json:"container"`
	UID           int        `toml:"uid" json:"uid"`
	GID           int        `toml:"gid" json:"gid"`
	RevokeFailure string     `toml:"revoke_failure" json:"revoke_failure"`
	Endpoints     []Endpoint `toml:"endpoints" json:"endpoints"`
	ProbePort     int        `toml:"probe_port" json:"probe_port"`
	ProbeUDPPort  int        `toml:"probe_udp_port" json:"probe_udp_port"`
}

func LoadNodeSettings() NodeSettings {
	s := NodeSettings{StateDir: "/opt/sbctl/data/node", SafetyDir: "/opt/sbctl/data/safety", ConfigDir: "/etc/sing-box", TemplateFile: "/opt/sbctl/config/template.json", ProbeFile: "/opt/sbctl/config/probe-credentials.json", ProbePort: 31840, ProbeUDPPort: 53190}
	check(toml.NewDecoder(bytes.NewReader(read(env("SBCTL_NODE_CONFIG", "/opt/sbctl/config/node.toml")))).DisallowUnknownFields().Decode(&s))
	s.Validate()
	return s
}
func (s *NodeSettings) Validate() {
	validIdentity(s.NodeID)
	if !hashRE.MatchString(s.TemplateHash) || !hashRE.MatchString(s.EngineBuildID) || !imageReference(s.Image) || !containerName(s.Container) || s.UID < 1 || s.GID < 1 || !contains([]string{"stop", "preserve"}, s.RevokeFailure) || s.ProbePort < 1024 || s.ProbePort > 65000 || s.ProbeUDPPort < 1024 || s.ProbeUDPPort > 65000 || len(s.Endpoints) > 4096 {
		fail("CONFIG_INVALID", "节点配置无效")
	}
	s.Endpoints = normalizeEndpoints(s.Endpoints)
}

// legacyInboundID gives pre-ID configurations a deterministic identity. New
// entries must receive an explicit generated ID, so deleting and recreating an
// entry never inherits the old history merely because its tag or port matches.
func legacyInboundID(e Endpoint) string {
	return "inb_legacy_" + hash([]byte(e.Protocol + ":" + e.Tag))[:24]
}

func normalizeEndpoints(eps []Endpoint) []Endpoint {
	out := append([]Endpoint{}, eps...)
	ids, tags := map[string]bool{}, map[string]bool{}
	for i := range out {
		if out[i].InboundID == "" {
			out[i].InboundID = legacyInboundID(out[i])
		}
		out[i].Validate()
		if ids[out[i].InboundID] || tags[out[i].Tag] {
			fail("CONFIG_INVALID", "入口身份或标签重复")
		}
		ids[out[i].InboundID], tags[out[i].Tag] = true, true
	}
	return out
}

func credentialEndpoint(endpoints []Endpoint, credential Credential) (Endpoint, bool) {
	var found Endpoint
	for _, endpoint := range endpoints {
		legacy := credential.InboundID == "" && strings.HasPrefix(endpoint.InboundID, "inb_legacy_")
		if credential.Protocol != endpoint.Protocol || (!legacy && credential.InboundID != endpoint.InboundID) {
			continue
		}
		if found.InboundID != "" {
			return Endpoint{}, false
		}
		found = endpoint
	}
	return found, found.InboundID != ""
}

func decodeEndpoints(value any) []Endpoint {
	var eps []Endpoint
	switch raw := value.(type) {
	case []byte:
		decode(raw, &eps)
	case string:
		decode([]byte(raw), &eps)
	default:
		decode(canonical(value), &eps)
	}
	return normalizeEndpoints(eps)
}

func endpointMaps(value any) []M {
	eps := decodeEndpoints(value)
	out := make([]M, 0, len(eps))
	for _, ep := range eps {
		var m M
		decode(canonical(ep), &m)
		out = append(out, m)
	}
	return out
}

type NodeConnection struct {
	Address     string `json:"address"`
	Port        int    `json:"port"`
	PublishUser string `json:"publish_user"`
	StatsUser   string `json:"stats_user"`
	PublishKey  string `json:"publish_key"`
	StatsKey    string `json:"stats_key"`
	Version     string `json:"version"`
	HostKey     string `json:"host_key,omitempty"`
}

func parseConnection(v any) NodeConnection {
	n := NodeConnection{Port: 22, PublishUser: "sbctl-publish", StatsUser: "sbctl-stats", PublishKey: "publish_ed25519", StatsKey: "stats_ed25519", Version: "1.12.9"}
	decode(canonical(v), &n)
	a, e := netip.ParseAddr(n.Address)
	if e != nil || !(netip.MustParsePrefix("100.64.0.0/10").Contains(a) || netip.MustParsePrefix("fd7a:115c:a1e0::/48").Contains(a)) {
		fail("CONNECTION_INVALID", "管理地址必须为 Tailscale 地址")
	}
	if n.Port < 1 || n.Port > 65535 || !match(`^[a-z][a-z0-9_-]{0,31}$`, n.PublishUser) || !match(`^[a-z][a-z0-9_-]{0,31}$`, n.StatsUser) || !match(`^[a-z][a-z0-9_-]{1,63}$`, n.PublishKey) || !match(`^[a-z][a-z0-9_-]{1,63}$`, n.StatsKey) {
		fail("CONNECTION_INVALID", "SSH 配置无效")
	}
	if n.HostKey != "" {
		validateHostKey(n.HostKey)
	}
	return n
}
