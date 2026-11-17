package sbctl

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the installed POSIX shell script with redirected fixture paths. This
// tests the actual entrypoint without Docker, root access, or a sing-box process.
func TestGuardAllowsLocalConfigChangesAndRejectsBlockedState(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "manifest")
	config := filepath.Join(root, "config.json")
	binary := filepath.Join(root, "sing-box")
	atomicWrite(binary, []byte("#!/bin/sh\nprintf 'sing-box reached: %s\\n' \"$*\"\n"), 0700)
	script := string(read("../../ops/controller/node/guard.sh"))
	script = strings.NewReplacer("/run/sbctl/manifest", manifest, "/etc/sing-box/config.json", config, "/var/lib/sing-box", root, "/usr/local/bin/sing-box", binary).Replace(script)
	run := func() bool {
		cmd := exec.Command("/bin/sh", "-s", "--", "version")
		cmd.Stdin = strings.NewReader(script)
		return cmd.Run() == nil
	}
	atomicWrite(manifest, []byte("allowed\n2\n7\n"), 0600)
	for _, data := range []string{"{}\n", " { \"log\": {\"level\":\"warn\"} }\n", "{\"password\":\"a-new-local-password\"}\n"} {
		atomicWrite(config, []byte(data), 0600)
		if !run() {
			t.Fatal("allowed guard did not reach sing-box after local edit")
		}
		marker := strings.Split(string(read(filepath.Join(root, "sbctl-started"))), "\n")
		if len(marker) < 3 || marker[0] != hash([]byte(data)) || marker[1] != "7" {
			t.Fatal("startup marker does not describe loaded bytes")
		}
	}
	for _, data := range []string{"blocked\n2\n7\n", "allowed\n3\n7\n", "allowed\n2\ninvalid\n", "allowed\n2\n-1\n"} {
		atomicWrite(manifest, []byte(data), 0600)
		if run() {
			t.Fatal("invalid guard state started sing-box")
		}
	}
	atomicWrite(manifest, []byte("allowed\n"+strings.Repeat("a", 64)+"\n7\n"), 0600)
	if !run() {
		t.Fatal("legacy manifest was not accepted")
	}
	check(os.Remove(manifest))
	if run() {
		t.Fatal("missing manifest was accepted")
	}
}
