package evals

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"github.com/vichanse/augustin/internal/tools"
)

const (
	goTestTimeout  = 2 * time.Minute
	maxGoTestBytes = 64 << 10
)

// goRunner runs go test ./... for the evaluator, outside the agent's tools.
type goRunner struct {
	goBin string
	env   []string
}

// testEnv is the minimal environment for evaluation subprocesses and for the
// agent's command tool: no provider credentials, no network module fetches,
// no toolchain downloads, no GOFLAGS.
func testEnv(goBin string) []string {
	cache := os.Getenv("GOCACHE")
	if cache == "" {
		if dir, err := os.UserCacheDir(); err == nil {
			cache = filepath.Join(dir, "go-build")
		}
	}
	env := []string{"PATH=" + filepath.Dir(goBin), "GOTOOLCHAIN=local", "GOPROXY=off", "GOFLAGS=", "GOCACHE=" + cache}
	for _, k := range []string{"HOME", "TMPDIR"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

type testResult struct {
	passed   bool
	timedOut bool
	output   string // bounded; used for baseline checks only, never reported
}

type capOutput struct{ bytes.Buffer }

func (c *capOutput) Write(p []byte) (int, error) {
	if room := maxGoTestBytes - c.Len(); room > 0 {
		c.Buffer.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

// test runs go test ./... in dir. An error means go itself could not run,
// which is an infrastructure problem, not a test failure.
func (g goRunner) test(ctx context.Context, dir string) (testResult, error) {
	ctx, cancel := context.WithTimeout(ctx, goTestTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, g.goBin, "test", "-count=1", "./...")
	var out capOutput
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = dir, g.env, &out, &out
	cleanup := tools.Isolate(cmd) // evaluated code was written by the agent
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	cleanup()
	r := testResult{output: out.String(), passed: err == nil, timedOut: ctx.Err() != nil}
	var exitErr *exec.ExitError
	if err != nil && !r.timedOut && !errors.As(err, &exitErr) && cmd.ProcessState == nil {
		return r, err
	}
	return r, nil
}

// snapshot hashes every file below dir; symlinks and other non-regular files
// are recorded as such, so creating one is a visible change.
func snapshot(dir string) (map[string][32]byte, error) {
	files := map[string][32]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if !d.Type().IsRegular() {
			files[rel] = sha256.Sum256([]byte("non-regular:" + d.Type().String()))
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[rel] = sha256.Sum256(data)
		return nil
	})
	return files, err
}

// changes lists modified, added and deleted paths between two snapshots.
func changes(before, after map[string][32]byte) (modified, added, deleted []string) {
	for p, sum := range after {
		if old, ok := before[p]; !ok {
			added = append(added, p)
		} else if old != sum {
			modified = append(modified, p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			deleted = append(deleted, p)
		}
	}
	sort.Strings(modified)
	sort.Strings(added)
	sort.Strings(deleted)
	return modified, added, deleted
}

// copyTree copies the regular files of src into dst (a fresh directory).
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o700)
		case !d.Type().IsRegular():
			return nil // snapshot already reports it as a change
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
