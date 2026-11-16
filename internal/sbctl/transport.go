package sbctl

import (
	"bytes"
	"context"
	"fmt"
	"golang.org/x/crypto/ssh"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func validateHostKey(key string) {
	_, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(key))
	if err != nil || len(rest) != 0 || strings.ContainsAny(key, "\r\n") {
		fail("HOST_KEY_INVALID", "SSH 主机公钥无效", 422)
	}
}

// limitedOutput bounds memory even if a remote process returns unbounded data.
type limitedOutput struct {
	bytes.Buffer
	limit int
}

func (b *limitedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, fmt.Errorf("process output limit exceeded")
	}
	return b.Buffer.Write(p)
}
func runCommand(args []string, input []byte, timeout time.Duration) []byte {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin = bytes.NewReader(input)
	out := &limitedOutput{limit: MaxDocument}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		fail("COMMAND_FAILED", "固定命令执行失败或超时", 503)
	}
	return out.Bytes()
}

type Transport interface {
	Call(NodeConnection, M, bool) M
}
type SSH struct{ Directory string }

func (s SSH) Call(c NodeConnection, body M, stats bool) M {
	c = parseConnection(c)
	key, user := c.PublishKey, c.PublishUser
	if stats {
		key, user = c.StatsKey, c.StatsUser
	}
	args := []string{"/usr/bin/ssh", "-T", "-F", "/dev/null", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "IdentitiesOnly=yes", "-o", "ConnectTimeout=5", "-o", "ServerAliveInterval=5", "-o", "ServerAliveCountMax=1", "-o", "ClearAllForwardings=yes", "-o", "UserKnownHostsFile=" + filepath.Join(s.Directory, "known_hosts"), "-i", filepath.Join(s.Directory, key), "-p", fmt.Sprint(c.Port), user + "@" + c.Address}
	var input []byte
	if c.HostKey != "" {
		validateHostKey(c.HostKey)
		file, err := os.CreateTemp("", "sbctl-known-hosts-")
		check(err)
		defer os.Remove(file.Name())
		host := c.Address
		if c.Port != 22 {
			host = fmt.Sprintf("[%s]:%d", c.Address, c.Port)
		}
		_, err = file.WriteString(host + " " + c.HostKey + "\n")
		check(err)
		check(file.Close())
		for i, s := range args {
			if strings.HasPrefix(s, "UserKnownHostsFile=") {
				args[i] = "UserKnownHostsFile=" + file.Name()
			}
		}
	}
	if body != nil {
		input = canonical(body)
	}
	var out []byte
	timeout := 20 * time.Second
	if contains([]string{"configure", "protocol_change", "restart"}, str(body["action"])) {
		timeout = 300 * time.Second
	}
	if err := Attempt(func() { out = runCommand(args, input, timeout) }); err != nil {
		fail("NODE_UNREACHABLE", "节点固定命令执行失败或管理通道不可达", 503)
	}
	value := object(out)
	if value["error"] != nil {
		f := mapping(value["error"])
		fail(str(f["code"]), str(f["message"]), 409)
	}
	return value
}
