package sbctl

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"
)

// limitedOutput bounds memory even if a child process returns unbounded data.
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
		fail("COMMAND_FAILED", "本地辅助命令执行失败或超时", 503)
	}
	return out.Bytes()
}

func actionTimeout(body M) time.Duration {
	if contains([]string{"configure", "protocol_change", "config_change", "restart", "adopt_config"}, str(body["action"])) {
		return 300 * time.Second
	}
	return 20 * time.Second
}

type Transport interface {
	Call(NodeConnection, M, bool) M
}
