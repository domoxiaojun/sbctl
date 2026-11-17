package sbctl

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type DockerEngine struct{ Settings NodeSettings }

func (e DockerEngine) docker(timeout time.Duration, args ...string) []byte {
	return runCommand(append([]string{"/usr/bin/docker"}, args...), nil, timeout)
}
func (e DockerEngine) compose(args ...string) []byte {
	return e.docker(45*time.Second, append([]string{"compose", "--project-directory", e.Settings.ConfigDir, "-f", filepath.Join(e.Settings.ConfigDir, "compose.yaml")}, args...)...)
}
func (e DockerEngine) Check(path string) {
	if os.Geteuid() == 0 {
		check(os.Chown(path, 0, e.Settings.GID))
	}
	check(os.Chmod(path, 0640))
	err := Attempt(func() {
		e.docker(20*time.Second, "run", "--rm", "--network", "none", "--user", fmt.Sprintf("%d:%d", e.Settings.UID, e.Settings.GID), "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--mount", "type=bind,src="+e.Settings.ConfigDir+",dst=/etc/sing-box,readonly", "--mount", "type=bind,src="+path+",dst=/run/candidate.json,readonly", "sha256:"+e.Settings.EngineBuildID, "-c", "/run/candidate.json", "check")
	})
	if err != nil {
		fail("PREFLIGHT_FAILED", "候选配置预检失败")
	}
}
func (e DockerEngine) Restart() {
	e.compose("up", "-d", "--no-deps", "--pull", "never", "--force-recreate", "sing-box")
	for range 30 {
		if e.Runtime().Running {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	fail("START_FAILED", "新容器没有进入可用运行状态")
}
func (e DockerEngine) Stop() { e.compose("stop", "sing-box") }
func (e DockerEngine) Runtime() Runtime {
	var objs []M
	decode(e.docker(15*time.Second, "container", "inspect", e.Settings.Container), &objs)
	if len(objs) != 1 {
		fail("RUNTIME_INVALID", "运行容器不存在")
	}
	o := objs[0]
	state, config := mapping(o["State"]), mapping(o["Config"])
	r := Runtime{ContainerID: str(o["Id"]), StartedAt: str(state["StartedAt"]), Running: boolean(state["Running"]), Paused: boolean(state["Paused"]), Restarting: boolean(state["Restarting"]), ImageDigest: str(o["Image"]), Users: []string{}}
	// Physical liveness is independent of a later edit to a bind-mounted file
	// or a preparing/blocked manifest installed for the next restart.
	_ = Attempt(func() {
		binding := object(read(filepath.Join(e.Settings.SafetyDir, "runtime.json")))
		r.NodeRevision = integer(binding["node_revision"])
		r.Users = stringsOf(binding["users"])
	})
	if r.Running {
		_ = Attempt(func() {
			started := strings.Split(strings.TrimSpace(string(e.docker(5*time.Second, "exec", e.Settings.Container, "cat", "/var/lib/sing-box/sbctl-started"))), "\n")
			if len(started) >= 2 && hashRE.MatchString(started[0]) {
				r.ConfigHash = started[0]
			}
		})
	}
	_ = Attempt(func() {
		data := read(filepath.Join(e.Settings.ConfigDir, "config.json"))
		if hash(data) == r.ConfigHash {
			config := object(data)
			r.SemanticHash = digest(config)
			r.Statistics = statisticsManifest(config, r.Users)
		} else {
			observed := configObservation(e.Settings)
			if observed["loaded_epoch"] == r.Epoch() {
				r.SemanticHash = str(observed["loaded_semantic_hash"])
				if r.SemanticHash == digest(object(data)) {
					r.Statistics = statisticsManifest(object(data), r.Users)
				}
			}
		}
	})
	guardMount, configMount := false, false
	for _, raw := range array(o["Mounts"]) {
		m := mapping(raw)
		guardMount = guardMount || (m["Source"] == e.Settings.SafetyDir && m["Destination"] == "/run/sbctl" && !boolean(m["RW"]))
		configMount = configMount || (m["Source"] == e.Settings.ConfigDir && m["Destination"] == "/etc/sing-box" && !boolean(m["RW"]))
	}
	r.Running = r.Running && guardMount && configMount && str(o["Image"]) == "sha256:"+e.Settings.EngineBuildID && config["Image"] == e.Settings.Image && strings.Join(stringsOf(config["Entrypoint"]), " ") == "/bin/sh /run/sbctl/guard.sh"
	return r
}
func (e DockerEngine) Verify(req NodeRequest, previous *NodeRequest) {
	old := map[string]Credential{}
	if previous != nil {
		for _, c := range previous.Users {
			old[c.CredentialID] = c
		}
	}
	current := map[string]Credential{}
	changed := []Credential{}
	for _, c := range req.Users {
		current[c.CredentialID] = c
		if _, ok := old[c.CredentialID]; !ok {
			changed = append(changed, c)
		}
	}
	controls := NewAgent(e.Settings, e).Probes()
	seen := map[string]bool{}
	for _, c := range append(changed, controls...) {
		if seen[c.CredentialID] {
			continue
		}
		seen[c.CredentialID] = true
		if !e.Probe(c, true) {
			fail("PROBE_FAILED", "新凭据或保留用户的实际代理请求未通过")
		}
	}
	removed := false
	for _, c := range old {
		if _, ok := current[c.CredentialID]; !ok {
			removed = true
			if e.Probe(c, false) {
				fail("REVOKE_FAILED", "已撤销凭据仍能完成代理请求")
			}
		}
	}
	if removed {
		for _, c := range controls {
			if !e.Probe(c, false) {
				fail("REVOKE_UNCERTAIN", "撤销探测后的有效凭据对照失败")
			}
		}
	}
}
func dnsProbe(port int) bool {
	tx := random(2)
	q := append(append([]byte{}, tx...), []byte("\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00\x07example\x03com\x00\x00\x01\x00\x01")...)
	c, err := net.DialTimeout("udp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		return false
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err = c.Write(q); err != nil {
		return false
	}
	buf := make([]byte, 4096)
	n, err := c.Read(buf)
	return err == nil && n >= 12 && bytes.Equal(buf[:2], tx) && buf[3]&15 == 0 && binary.BigEndian.Uint16(buf[6:8]) > 0
}
func probePorts(tcp, udp int) {
	lc := net.ListenConfig{Control: func(network, address string, raw syscall.RawConn) error {
		var err error
		if e := raw.Control(func(fd uintptr) { err = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1) }); e != nil {
			return e
		}
		return err
	}}
	l, err := lc.Listen(context.Background(), "tcp4", fmt.Sprintf("127.0.0.1:%d", tcp))
	if err != nil {
		fail("PROBE_PORT_BUSY", "运维探针端口已被占用")
	}
	defer l.Close()
	u, err := net.ListenPacket("udp4", fmt.Sprintf("127.0.0.1:%d", udp))
	if err != nil {
		fail("PROBE_PORT_BUSY", "运维探针端口已被占用")
	}
	u.Close()
}
func (e DockerEngine) Probe(c Credential, udp bool) bool {
	var endpoint Endpoint
	for _, p := range e.Settings.Endpoints {
		if p.Protocol == c.Protocol {
			endpoint = p
		}
	}
	if endpoint.Protocol == "" {
		fail("PROBE_PROTOCOL", "缺少协议探针入口")
	}
	tls := M{"enabled": true, "server_name": endpoint.SNI}
	out := M{"type": c.Protocol, "tag": "proxy", "server": endpoint.Server, "server_port": endpoint.Port, "password": c.Password, "tls": tls}
	if c.Protocol == "tuic" {
		out["uuid"] = c.UUID
		tls["alpn"] = endpoint.ALPN
	}
	config := M{"log": M{"disabled": true}, "inbounds": []M{{"type": "socks", "listen": "127.0.0.1", "listen_port": e.Settings.ProbePort}, {"type": "direct", "listen": "127.0.0.1", "listen_port": e.Settings.ProbeUDPPort, "network": "udp", "override_address": "1.1.1.1", "override_port": 53}}, "outbounds": []M{out}, "route": M{"final": "proxy"}}
	name := identifier("sbctl_verify")
	probePorts(e.Settings.ProbePort, e.Settings.ProbeUDPPort)
	dir, err := os.MkdirTemp(e.Settings.StateDir, "sbctl-verify-")
	check(err)
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "client.json")
	atomicWrite(path, canonical(config), 0640)
	if os.Geteuid() == 0 {
		check(os.Chown(path, 0, e.Settings.GID))
	}
	defer func() { _ = Attempt(func() { e.docker(10*time.Second, "rm", "-f", name) }) }()
	e.docker(15*time.Second, "run", "-d", "--name", name, "--network", "host", "--user", fmt.Sprintf("%d:%d", e.Settings.UID, e.Settings.GID), "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--mount", "type=bind,src="+path+",dst=/run/client.json,readonly", e.Settings.Image, "-c", "/run/client.json", "run")
	for range 20 {
		conn, e := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", e.Settings.ProbePort), 200*time.Millisecond)
		if e == nil {
			conn.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	var objs []M
	decode(e.docker(10*time.Second, "container", "inspect", name), &objs)
	if len(objs) != 1 || !boolean(mapping(objs[0]["State"])["Running"]) {
		fail("PROBE_START_FAILED", "运维探针实例未成功运行")
	}
	var code []byte
	err = Attempt(func() {
		code = runCommand([]string{"/usr/bin/curl", "--silent", "--max-time", "8", "--proxy", fmt.Sprintf("socks5h://127.0.0.1:%d", e.Settings.ProbePort), "--output", "/dev/null", "--write-out", "%{http_code}", "https://www.gstatic.com/generate_204"}, nil, 10*time.Second)
	})
	return err == nil && string(code) == "204" && (!udp || dnsProbe(e.Settings.ProbeUDPPort))
}
