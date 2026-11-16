package sbctl

import (
	"github.com/google/uuid"
	"reflect"
	"strings"
	"unicode/utf8"
)

type Credential struct {
	UserID       string  `json:"user_id"`
	Protocol     string  `json:"protocol"`
	CredentialID string  `json:"credential_id"`
	Generation   int64   `json:"generation"`
	Password     string  `json:"password"`
	UUID         *string `json:"uuid"`
}

func (c Credential) Fingerprint() string {
	s := c.Password
	if c.Protocol == "tuic" && c.UUID != nil {
		s = *c.UUID
	}
	return hash([]byte(c.Protocol + ":" + s))
}
func (c *Credential) Validate() {
	validIdentity(c.UserID)
	validIdentity(c.CredentialID)
	if c.Generation < 1 || c.Generation > 1000000 || utf8.RuneCountInString(c.Password) < 1 || utf8.RuneCountInString(c.Password) > 1024 {
		fail("CREDENTIAL_INVALID", "凭据字段无效")
	}
	switch c.Protocol {
	case "tuic":
		if c.UUID == nil {
			fail("CREDENTIAL_INVALID", "TUIC 需要 UUID")
		}
		u, e := uuid.Parse(*c.UUID)
		if e != nil {
			fail("CREDENTIAL_INVALID", "UUID 无效")
		}
		s := u.String()
		c.UUID = &s
	case "anytls":
		if c.UUID != nil {
			fail("CREDENTIAL_INVALID", "AnyTLS 不接受 UUID")
		}
	default:
		fail("CREDENTIAL_INVALID", "不支持的凭据协议")
	}
}

type NodeRequest struct {
	SchemaVersion int          `json:"schema_version"`
	NodeID        string       `json:"node_id"`
	RequestID     string       `json:"request_id"`
	NodeRevision  int64        `json:"node_revision"`
	TemplateHash  string       `json:"template_hash"`
	EngineBuildID string       `json:"engine_build_id"`
	Users         []Credential `json:"users"`
	Revoked       []string     `json:"revoked_credentials"`
	PayloadHash   string       `json:"payload_hash"`
}

func (r *NodeRequest) Validate() {
	validIdentity(r.NodeID)
	validIdentity(r.RequestID)
	if r.SchemaVersion != 1 || r.NodeRevision < 1 || !hashRE.MatchString(r.TemplateHash) || !hashRE.MatchString(r.EngineBuildID) || len(r.Users) > 400 || len(r.Revoked) > 10000 || r.Users == nil || r.Revoked == nil {
		fail("REQUEST_INVALID", "节点请求字段无效")
	}
	revoked := map[string]bool{}
	for _, f := range r.Revoked {
		if revoked[f] || !hashRE.MatchString(f) {
			fail("REQUEST_INVALID", "撤销指纹无效或重复")
		}
		revoked[f] = true
	}
	ids, prints := map[string]bool{}, map[string]bool{}
	comb := map[string]int{}
	for i := range r.Users {
		c := &r.Users[i]
		c.Validate()
		fp := c.Fingerprint()
		if ids[c.CredentialID] || prints[fp] || revoked[fp] {
			fail("REQUEST_INVALID", "重复或已撤销凭据")
		}
		ids[c.CredentialID] = true
		prints[fp] = true
		k := c.UserID + ":" + c.Protocol
		comb[k]++
		if comb[k] > 2 {
			fail("REQUEST_INVALID", "凭据代数过多")
		}
	}
}
func (r NodeRequest) ComputedHash() string {
	return digest(without(object(canonical(r)), "payload_hash"))
}
func (r *NodeRequest) Seal() { r.Validate(); r.PayloadHash = r.ComputedHash() }
func (r *NodeRequest) Verify() {
	r.Validate()
	if r.PayloadHash != r.ComputedHash() {
		fail("HASH_MISMATCH", "请求摘要不匹配")
	}
}
func parseRequest(v any) NodeRequest {
	var r NodeRequest
	decode(canonical(v), &r)
	r.Verify()
	return r
}

type Runtime struct {
	ContainerID  string              `json:"container_id"`
	StartedAt    string              `json:"started_at"`
	Running      bool                `json:"running"`
	Paused       bool                `json:"paused"`
	Restarting   bool                `json:"restarting"`
	ConfigHash   string              `json:"config_hash"`
	NodeRevision int64               `json:"node_revision"`
	Users        []string            `json:"users"`
	ImageDigest  string              `json:"image_digest"`
	Statistics   *StatisticsManifest `json:"statistics,omitempty"`
}

func (r Runtime) Epoch() string { return r.ContainerID + "@" + r.StartedAt }
func (r Runtime) Stable(s Runtime) bool {
	return reflect.DeepEqual(r, s) && r.Running && !r.Paused && !r.Restarting && r.ContainerID != "" && r.StartedAt != ""
}

type LedgerUser struct {
	UserID   string `json:"user_id"`
	Upload   string `json:"upload_bytes"`
	Download string `json:"download_bytes"`
}
type LedgerInbound struct {
	Tag       string `json:"tag"`
	Protocol  string `json:"protocol"`
	Upload    string `json:"upload_bytes"`
	Download  string `json:"download_bytes"`
	Port      int    `json:"port"`
	StartedAt string `json:"started_at"`
	Active    bool   `json:"active"`
}
type LedgerLocalUser struct {
	Name      string `json:"name"`
	Upload    string `json:"upload_bytes"`
	Download  string `json:"download_bytes"`
	StartedAt string `json:"started_at"`
	Active    bool   `json:"active"`
}
type DataQuality struct {
	Integrity string `json:"integrity_state"`
	HasGap    bool   `json:"has_historical_gap"`
	Gaps      int64  `json:"gap_count"`
}
type Snapshot struct {
	SchemaVersion int               `json:"schema_version"`
	NodeID        string            `json:"node_id"`
	LedgerID      string            `json:"ledger_id"`
	Seq           int64             `json:"snapshot_seq"`
	StartedAt     string            `json:"started_at"`
	SampledAt     *string           `json:"sampled_at"`
	SourceEpoch   *string           `json:"source_epoch"`
	ConfigHash    *string           `json:"config_hash"`
	Users         []LedgerUser      `json:"users"`
	Inbounds      []LedgerInbound   `json:"inbounds,omitempty"`
	LocalUsers    []LedgerLocalUser `json:"local_users,omitempty"`
	TotalInbounds int               `json:"total_inbounds,omitempty"`
	Quality       DataQuality       `json:"data_quality"`
	Checksum      string            `json:"checksum"`
}

func (s Snapshot) ComputedHash() string { return digest(without(object(canonical(s)), "checksum")) }
func (s *Snapshot) Seal()               { s.Checksum = s.ComputedHash() }
func (s Snapshot) Verify() {
	validIdentity(s.NodeID)
	validIdentity(s.LedgerID)
	if s.SchemaVersion != 1 || s.Seq < 1 || len(s.Users) > 10000 || len(s.Inbounds) > 4096 || len(s.LocalUsers) > 10000 || s.TotalInbounds < 0 || s.TotalInbounds > 4096 || s.Users == nil || !contains([]string{"ok", "counter_error", "state_lost"}, s.Quality.Integrity) || s.Quality.Gaps < 0 {
		fail("SNAPSHOT_INVALID", "快照字段无效")
	}
	if s.Checksum != s.ComputedHash() {
		fail("SNAPSHOT_HASH", "统计快照摘要不匹配")
	}
	seen := map[string]bool{}
	for _, u := range s.Users {
		validIdentity(u.UserID)
		if seen[u.UserID] {
			fail("DUPLICATE_COUNTER", "统计快照包含重复用户")
		}
		seen[u.UserID] = true
		for _, v := range []string{u.Upload, u.Download} {
			counter(v)
			if len(v) > 1 && strings.HasPrefix(v, "0") {
				fail("COUNTER_INVALID", "流量计数格式无效")
			}
		}
	}
	seenInbound := map[string]bool{}
	for _, in := range s.Inbounds {
		if !validStatName(in.Tag) || !match(`^[a-zA-Z0-9_-]{1,32}$`, in.Protocol) || seenInbound[in.Tag] || in.Port < 0 || in.Port > 65535 {
			fail("DUPLICATE_COUNTER", "统计快照包含重复入口")
		}
		seenInbound[in.Tag] = true
		stamp(in.StartedAt)
		for _, value := range []string{in.Upload, in.Download} {
			counter(value)
			if len(value) > 1 && strings.HasPrefix(value, "0") {
				fail("COUNTER_INVALID", "入口流量计数格式无效")
			}
		}
	}
	seenLocal := map[string]bool{}
	for _, u := range s.LocalUsers {
		if !validStatName(u.Name) || seenLocal[u.Name] {
			fail("DUPLICATE_COUNTER", "本地用户名称无效或重复")
		}
		seenLocal[u.Name] = true
		stamp(u.StartedAt)
		counter(u.Upload)
		counter(u.Download)
	}
}
func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

type CreateUser struct {
	DisplayName string   `json:"display_name"`
	Note        string   `json:"note"`
	GroupIDs    []string `json:"group_ids"`
}

func (c *CreateUser) Validate() {
	c.DisplayName = strings.TrimSpace(c.DisplayName)
	c.Note = strings.TrimSpace(c.Note)
	if utf8.RuneCountInString(c.DisplayName) < 1 || utf8.RuneCountInString(c.DisplayName) > 40 || utf8.RuneCountInString(c.Note) > 160 || len(c.GroupIDs) < 1 || len(c.GroupIDs) > 100 {
		fail("INVALID_REQUEST", "用户名称、备注或授权组无效", 422)
	}
	for _, s := range []string{c.DisplayName, c.Note} {
		for _, r := range s {
			if r < 32 || r == 127 {
				fail("INVALID_REQUEST", "文本包含控制字符", 422)
			}
		}
	}
	for _, g := range c.GroupIDs {
		validIdentity(g)
	}
}

type DisableUser struct {
	Revision *int64 `json:"revision"`
	Reason   string `json:"reason"`
}

func (d DisableUser) Validate() {
	if d.Revision == nil || *d.Revision < 0 || utf8.RuneCountInString(d.Reason) < 1 || utf8.RuneCountInString(d.Reason) > 160 {
		fail("INVALID_REQUEST", "禁用参数无效", 422)
	}
}

type EnableUser struct {
	Revision *int64 `json:"revision"`
	Reason   string `json:"reason"`
}

func (e *EnableUser) Validate() {
	e.Reason = strings.TrimSpace(e.Reason)
	if e.Revision == nil || *e.Revision < 0 || utf8.RuneCountInString(e.Reason) < 1 || utf8.RuneCountInString(e.Reason) > 160 {
		fail("INVALID_REQUEST", "重新启用参数无效", 422)
	}
	for _, r := range e.Reason {
		if r < 32 || r == 127 {
			fail("INVALID_REQUEST", "原因不能包含控制字符", 422)
		}
	}
}
