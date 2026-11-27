//go:build unix

package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The fake command starts a background child and records its PID. Whether the
// command is canceled, times out or exits, no child may survive it.
func TestCommandLeavesNoChildRunning(t *testing.T) {
	for name, tc := range map[string]struct {
		script  string
		cancel  bool
		timeout time.Duration
	}{
		"turn canceled":   {script: `sleep 60 & echo $! > "$0.child"; wait`, cancel: true, timeout: time.Minute},
		"command timeout": {script: `sleep 60 & echo $! > "$0.child"; wait`, timeout: time.Second}, // room to start the child under load
		"normal exit":     {script: `sleep 60 & echo $! > "$0.child"; exit 0`, timeout: time.Minute},
	} {
		bin := fakeBin(t, map[string]string{"go": tc.script})
		s := newCommandSetup(t, bin)
		s.runner.timeout = tc.timeout
		ctx, cancel := context.WithCancel(context.Background())
		if tc.cancel {
			// Cancel once the child exists, not after a fixed delay: under load the
			// script may not have started it yet, leaving nothing to check.
			go func() {
				for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
					if _, err := os.Stat(filepath.Join(bin, "go.child")); err == nil {
						break
					}
				}
				cancel()
			}()
		}
		_, err := execute(t, s.registry(ExecutePolicy()), ctx, runCall("go", "test", "./..."))
		cancel()
		if tc.cancel && !errors.Is(err, context.Canceled) {
			t.Errorf("%s: %v", name, err)
		}
		data, rerr := os.ReadFile(filepath.Join(bin, "go.child"))
		pid, perr := strconv.Atoi(strings.TrimSpace(string(data)))
		if rerr != nil || perr != nil {
			t.Fatalf("%s: child PID not recorded: %v %v", name, rerr, perr)
		}
		deadline := time.Now().Add(3 * time.Second)
		for syscall.Kill(pid, 0) == nil {
			if time.Now().After(deadline) {
				syscall.Kill(pid, syscall.SIGKILL)
				t.Fatalf("%s: child %d still running", name, pid)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}
