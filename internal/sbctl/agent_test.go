package sbctl

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeEngine struct {
	s                         NodeSettings
	epoch, restarts           int
	stopped, preflight, crash bool
	binding                   M
}

func (f *fakeEngine) Check(path string) {
	if f.preflight {
		fail("PREFLIGHT_FAILED", "preflight failed")
	}
}
func (f *fakeEngine) Restart() {
	f.restarts++
	f.epoch++
	f.binding = object(read(filepath.Join(f.s.SafetyDir, "runtime.json")))
	if f.crash {
		f.crash = false
		panic("simulated process crash")
	}
}
func (f *fakeEngine) Stop() { f.stopped = true }
func (f *fakeEngine) Runtime() Runtime {
	return Runtime{ContainerID: "container", StartedAt: fmt.Sprint(f.epoch), Running: !f.stopped, ImageDigest: f.s.Image, ConfigHash: str(f.binding["config_hash"]), NodeRevision: integer(f.binding["node_revision"]), Users: stringsOf(f.binding["users"])}
}
func (f *fakeEngine) Verify(r NodeRequest, p *NodeRequest) {}
func testAgent(t *testing.T) (*Agent, *fakeEngine) {
	t.Helper()
	dir := t.TempDir()
	template := filepath.Join(dir, "template.json")
	atomicWrite(template, canonical(M{"inbounds": []M{{"type": "anytls", "tag": "anytls-in", "users": []M{}}, {"type": "tuic", "tag": "tuic-in", "users": []M{}}, {"type": "direct", "tag": "legacy", "listen_port": 8080}}}), 0600)
	probes := filepath.Join(dir, "probes.json")
	u := "d754b796-4d5c-4b91-83e9-5f2951e85c97"
	atomicWrite(probes, canonical([]Credential{{"sys_probe", "anytls", "cred_probe_anytls", 1, "probe-password", nil}, {"sys_probe", "tuic", "cred_probe_tuic", 1, "probe-password", &u}}), 0600)
	s := NodeSettings{NodeID: "node_hk", StateDir: filepath.Join(dir, "state"), SafetyDir: filepath.Join(dir, "safety"), ConfigDir: filepath.Join(dir, "config"), TemplateFile: template, ProbeFile: probes, TemplateHash: hash(read(template)), Image: "ghcr.io/owner/image@sha256:" + strings.Repeat("a", 64), EngineBuildID: strings.Repeat("a", 64), Container: "sbctl-test", UID: os.Getuid(), GID: os.Getgid(), RevokeFailure: "stop", Endpoints: []Endpoint{{"anytls", "anytls-in", "example.test", 443, "example.test", []string{"h3"}}, {"tuic", "tuic-in", "example.test", 443, "example.test", []string{"h3"}}}, ProbePort: 31840, ProbeUDPPort: 53190}
	f := &fakeEngine{s: s, binding: M{"config_hash": "old", "node_revision": 0, "users": []string{}}}
	a := NewAgent(s, f)
	a.Initialize()
	return a, f
}
func agentRequest(a *Agent, rev int64, users []Credential, revoked []string) NodeRequest {
	if users == nil {
		users = []Credential{}
	}
	if revoked == nil {
		revoked = []string{}
	}
	r := NodeRequest{1, a.Settings.NodeID, fmt.Sprintf("req_%d", rev), rev, a.Settings.TemplateHash, a.Settings.EngineBuildID, users, revoked, ""}
	r.Seal()
	return r
}
func testCredential() Credential {
	return Credential{"usr_alice", "anytls", "cred_alice", 1, "alice-password", nil}
}
func TestNodeRevocationAndReplay(t *testing.T) {
	a, e := testAgent(t)
	c := testCredential()
	original := agentRequest(a, 1, []Credential{c}, nil)
	a.Accept(original)
	a.ApplyOnce()
	if a.Accept(original)["state"] != "verified" || a.ApplyOnce() || e.restarts != 1 {
		t.Fatal("replayed")
	}
	r := agentRequest(a, 2, nil, []string{c.Fingerprint()})
	a.Accept(r)
	if !strings.HasPrefix(string(read(filepath.Join(a.Settings.SafetyDir, "manifest"))), "blocked") {
		t.Fatal("guard")
	}
	a.ApplyOnce()
	if mapping(a.Status()["latest"])["state"] != "verified" {
		t.Fatal("revoke")
	}
	if a.Accept(original)["state"] != "superseded" {
		t.Fatal("late request")
	}
	expectFault(t, "REVOKED_CREDENTIAL", func() { a.Accept(agentRequest(a, 3, []Credential{c}, nil)) })
	expectFault(t, "REQUEST_CONFLICT", func() { a.Accept(agentRequest(a, 2, nil, nil)) })
	config := object(read(filepath.Join(a.Settings.ConfigDir, "config.json")))
	inbounds := array(config["inbounds"])
	if integer(mapping(inbounds[2])["listen_port"]) != 8080 {
		t.Fatal("legacy mutated")
	}
	for _, i := range inbounds[:2] {
		for _, u := range array(mapping(i)["users"]) {
			if !strings.HasPrefix(str(mapping(u)["name"]), "sys_") {
				t.Fatal("old user restored")
			}
		}
	}
}
func TestNodeEnableUsesNewGenerationAndRetainsRevocations(t *testing.T) {
	a, _ := testAgent(t)
	old := testCredential()
	a.Accept(agentRequest(a, 1, []Credential{old}, nil))
	a.ApplyOnce()
	a.Accept(agentRequest(a, 2, nil, []string{old.Fingerprint()}))
	a.ApplyOnce()
	fresh := Credential{old.UserID, old.Protocol, "cred_fresh", 2, "new-test-password", nil}
	a.Accept(agentRequest(a, 3, []Credential{fresh}, []string{old.Fingerprint()}))
	a.ApplyOnce()
	if mapping(a.Status()["latest"])["state"] != "verified" {
		t.Fatal("new generation was not applied")
	}
	config := string(read(filepath.Join(a.Settings.ConfigDir, "config.json")))
	if strings.Contains(config, old.Password) || !strings.Contains(config, fresh.Password) || !strings.Contains(config, old.UserID) {
		t.Fatal("rendered identity or credential invalid")
	}
	expectFault(t, "REVOKED_CREDENTIAL", func() { a.Accept(agentRequest(a, 4, []Credential{old}, nil)) })
}

func TestNodeFailedRevocationPolicy(t *testing.T) {
	for _, policy := range []string{"stop", "preserve"} {
		t.Run(policy, func(t *testing.T) {
			a, e := testAgent(t)
			a.Settings.RevokeFailure = policy
			c := testCredential()
			a.Accept(agentRequest(a, 1, []Credential{c}, nil))
			a.ApplyOnce()
			e.preflight = true
			a.Accept(agentRequest(a, 2, nil, []string{c.Fingerprint()}))
			a.ApplyOnce()
			if e.stopped != (policy == "stop") || mapping(a.Status()["latest"])["state"] != "revoke_blocked" {
				t.Fatal("policy")
			}
			if !strings.HasPrefix(string(read(filepath.Join(a.Settings.SafetyDir, "manifest"))), "blocked") {
				t.Fatal("guard not blocked")
			}
		})
	}
}
func TestNodeResumesWithoutSecondRestart(t *testing.T) {
	a, e := testAgent(t)
	a.Accept(agentRequest(a, 1, []Credential{testCredential()}, nil))
	e.crash = true
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected crash")
			}
		}()
		a.ApplyOnce()
	}()
	a.ApplyOnce()
	if e.restarts != 1 || mapping(a.Status()["latest"])["state"] != "verified" {
		t.Fatal("restart repeated")
	}
}
func TestNodeRejectsLostState(t *testing.T) {
	a, _ := testAgent(t)
	a.Guard(false, "", 0)
	check(os.Remove(a.DBPath))
	expectFault(t, "NODE_STATE_LOST", a.Initialize)
}
