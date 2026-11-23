package sbctl

import (
	"bytes"
	"net"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
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
	ControlDB         string   `toml:"control_db"`
	TrafficDB         string   `toml:"traffic_db"`
	TrafficSnapshot   string   `toml:"traffic_snapshot"`
	PublicDir         string   `toml:"public_dir"`
	KeyFile           string   `toml:"key_file"`
	PublicBaseURL     string   `toml:"public_base_url"`
	AllowedOrigins    []string `toml:"allowed_origins"`
	SessionSeconds    int      `toml:"session_seconds"`
	SecureCookie      bool     `toml:"secure_cookie"`
	MaxUsers          int      `toml:"max_users"`
	StaleSeconds      int      `toml:"stale_seconds"`
	SyncSeconds       int      `toml:"sync_seconds"`
	LegacySSHDir      string   `toml:"ssh_dir,omitempty"`
	WebDir            string   `toml:"web_dir"`
	InventoryFile     string   `toml:"inventory_file"`
	DiscoveryFile     string   `toml:"discovery_file"`
	TrustedProxies    []string `toml:"trusted_proxies"`
	AgentAllowedCIDRs []string `toml:"agent_allowed_cidrs"`
}

func DefaultSettings() Settings {
	return Settings{
		ControlDB:         "/data/control/control.db",
		TrafficDB:         "/data/traffic/traffic.db",
		TrafficSnapshot:   "/data/telemetry/traffic.json",
		PublicDir:         "/data/subscriptions",
		KeyFile:           "/run/secrets/master.key",
		SessionSeconds:    28800,
		SecureCookie:      true,
		MaxUsers:          100,
		StaleSeconds:      180,
		SyncSeconds:       30,
		WebDir:            "/opt/sbctl/web",
		InventoryFile:     "/data/inventory/nodes.json",
		DiscoveryFile:     "/data/discovery/tailscale.json",
		AgentAllowedCIDRs: []string{"100.64.0.0/10", "fd7a:115c:a1e0::/48", "127.0.0.0/8", "::1/128"},
	}
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
	for _, cidr := range s.AgentAllowedCIDRs {
		if _, ok := proxyPrefix(cidr); !ok {
			fail("CONFIG_INVALID", "Agent 来源网段必须是 IP 地址或 CIDR 网段")
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
func (s Settings) allowsAgentPeer(rawAddr string) bool {
	host := rawAddr
	if h, _, err := net.SplitHostPort(rawAddr); err == nil {
		host = h
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	a = a.Unmap()
	cidrs := s.AgentAllowedCIDRs
	if cidrs == nil {
		cidrs = []string{"100.64.0.0/10", "fd7a:115c:a1e0::/48", "127.0.0.0/8", "::1/128"}
	}
	for _, cidr := range cidrs {
		if p, ok := proxyPrefix(cidr); ok && p.Contains(a) {
			return true
		}
	}
	return false
}

type Endpoint struct {
	Protocol  string   `json:"protocol" toml:"protocol"`
	Tag       string   `json:"tag" toml:"tag"`
	Server    string   `json:"server" toml:"server"`
	Port      int      `json:"port" toml:"port"`
	SNI       string   `json:"sni" toml:"sni"`
	ALPN      []string `json:"alpn" toml:"alpn"`
	InboundID string   `json:"inbound_id,omitempty" toml:"inbound_id,omitempty"`
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

func legacyInboundID(e Endpoint) string {
	return "inb_legacy_" + hash([]byte(e.Protocol+":"+e.Tag))[:24]
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

// NodeConnection identifies a node's management peer on the Tailscale tailnet.
// All SSH fields have been removed in favor of the embedded sing-box Agent channel.
type NodeConnection struct {
	Address string `json:"address"`
	Version string `json:"version,omitempty"`
}

func parseConnection(v any) NodeConnection {
	m := mapping(object(canonical(v)))
	n := NodeConnection{
		Address: strings.TrimSpace(str(m["address"])),
		Version: strings.TrimSpace(str(m["version"])),
	}
	if n.Version == "" {
		n.Version = "1.14.4-sbctl.2"
	}
	if len(n.Version) > 80 || strings.ContainsAny(n.Version, "\r\n\x00") {
		fail("CONNECTION_INVALID", "内核版本格式无效")
	}
	a, e := netip.ParseAddr(n.Address)
	if e != nil || !(netip.MustParsePrefix("100.64.0.0/10").Contains(a) || netip.MustParsePrefix("fd7a:115c:a1e0::/48").Contains(a)) {
		fail("CONNECTION_INVALID", "管理地址必须为 Tailscale 地址")
	}
	return n
}
