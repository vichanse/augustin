package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vichanse/augustin/internal/model"
)

// fakeBin writes test-only executables named like real tools. They are shell
// scripts, but Augustin itself never invokes a shell: it execs them directly.
func fakeBin(t *testing.T, scripts map[string]string) string {
	t.Helper()
	bin := t.TempDir()
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bin
}

type commandSetup struct {
	dir    string
	w      *Workspace
	runner *commandRunner
	asked  []Proposal
	answer bool
}

func newCommandSetup(t *testing.T, bin string, extraEnv ...string) *commandSetup {
	t.Helper()
	_, w := editWorkspace(t, map[string]string{"go.mod": "module demo\n"})
	env := append([]string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir()}, extraEnv...)
	return &commandSetup{dir: w.dir, w: w, runner: w.newCommandRunner(env), answer: true}
}

func (s *commandSetup) registry(policy Policy) *Registry {
	policy.Approve = func(_ context.Context, p Proposal) (bool, error) {
		s.asked = append(s.asked, p)
		return s.answer, nil
	}
	return NewWithPreparers(s.w.Handlers(), map[string]Preparer{"run_command": s.runner.prepare}, ExecuteTools, policy)
}

func runCall(command string, args ...string) model.ToolCall {
	raw, _ := json.Marshal(map[string]any{"command": command, "args": args})
	return model.ToolCall{Name: "run_command", Arguments: raw}
}

type commandResult struct {
	Command         string   `json:"command"`
	Args            []string `json:"args"`
	Executable      string   `json:"executable"`
	ExitCode        int      `json:"exit_code"`
	Status          string   `json:"status"`
	DurationMS      int64    `json:"duration_ms"`
	Stdout          string   `json:"stdout"`
	Stderr          string   `json:"stderr"`
	StdoutTruncated bool     `json:"stdout_truncated"`
	StderrTruncated bool     `json:"stderr_truncated"`
}

func execute(t *testing.T, r *Registry, ctx context.Context, call model.ToolCall) (commandResult, error) {
	t.Helper()
	out, err := r.Execute(ctx, call)
	var res commandResult
	if err == nil {
		if jerr := json.Unmarshal([]byte(out), &res); jerr != nil {
			t.Fatalf("result is not JSON: %q", out)
		}
	}
	return res, err
}

func TestCommandRunsAfterApprovalInWorkspaceRoot(t *testing.T) {
	bin := fakeBin(t, map[string]string{"go": `echo "args:$*"; echo "pwd:$(pwd -P)"; env`})
	s := newCommandSetup(t, bin, "OPENAI_API_KEY=sk-secret", "AWS_SECRET_ACCESS_KEY=aws-secret", "GITHUB_TOKEN=gh-secret", "GOFLAGS=-toolexec=/evil")
	res, err := execute(t, s.registry(ExecutePolicy()), context.Background(), runCall("go", "test", "./..."))
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || res.Status != "exited" || res.Command != "go" || strings.Join(res.Args, " ") != "test ./..." {
		t.Fatalf("result: %+v", res)
	}
	if !strings.Contains(res.Stdout, "args:test ./...\n") || !strings.Contains(res.Stdout, "pwd:"+s.dir+"\n") {
		t.Fatalf("wrong argv or working directory:\n%s", res.Stdout)
	}
	for _, secret := range []string{"sk-secret", "aws-secret", "gh-secret", "OPENAI_API_KEY", "GOFLAGS", "-toolexec"} {
		if strings.Contains(res.Stdout, secret) {
			t.Errorf("%s leaked into the command environment", secret)
		}
	}
	for _, want := range []string{"GOTOOLCHAIN=local", "GIT_CONFIG_NOSYSTEM=1", "HOME="} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("environment misses %s", want)
		}
	}
	exe, _ := filepath.EvalSymlinks(filepath.Join(bin, "go")) // the resolved path is what runs and what is shown
	if len(s.asked) != 1 || s.asked[0].Capability != Execute || s.asked[0].Path != s.dir ||
		strings.Join(s.asked[0].Command[1:], " ") != "test ./..." || s.asked[0].Command[0] != exe || res.Executable != exe {
		t.Fatalf("approval did not show the exact invocation: %+v", s.asked)
	}
}

func TestGitRunsWithoutExternalHelpers(t *testing.T) {
	bin := fakeBin(t, map[string]string{"git": `echo "$*"`})
	s := newCommandSetup(t, bin)
	for args, want := range map[string]string{
		"diff":   "--no-pager -c core.fsmonitor=false -c core.pager=cat diff --no-ext-diff --no-textconv --no-color",
		"status": "--no-pager -c core.fsmonitor=false -c core.pager=cat status",
	} {
		res, err := execute(t, s.registry(ExecutePolicy()), context.Background(), runCall("git", args))
		if err != nil || strings.TrimSpace(res.Stdout) != want {
			t.Errorf("git %s ran %q (%v)", args, res.Stdout, err)
		}
	}
}

func TestRejectedCommandNeverStarts(t *testing.T) {
	bin := fakeBin(t, map[string]string{"go": `touch "$0.started"`})
	s := newCommandSetup(t, bin)
	s.answer = false
	if _, err := execute(t, s.registry(ExecutePolicy()), context.Background(), runCall("go", "test", "./...")); !errors.Is(err, ErrRejected) {
		t.Fatalf("rejection: %v", err)
	}
	if _, err := os.Stat(filepath.Join(bin, "go.started")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rejected command started")
	}
}

func TestCommandsDeniedOutsideExecuteMode(t *testing.T) {
	bin := fakeBin(t, map[string]string{"go": `touch "$0.started"`})
	s := newCommandSetup(t, bin)
	for name, policy := range map[string]Policy{"read-only": ReadPolicy(), "edit": EditPolicy()} {
		if _, err := execute(t, s.registry(policy), context.Background(), runCall("go", "test", "./...")); !errors.Is(err, ErrDenied) {
			t.Errorf("%s mode: %v", name, err)
		}
	}
	noApprover := ExecutePolicy() // headless: no approver at all
	r := NewWithPreparers(s.w.Handlers(), map[string]Preparer{"run_command": s.runner.prepare}, ExecuteTools, noApprover)
	if _, err := r.Execute(context.Background(), runCall("go", "test", "./...")); !errors.Is(err, ErrDenied) {
		t.Errorf("no approver: %v", err)
	}
	if len(s.asked) != 0 {
		t.Fatal("denied command was proposed")
	}
	if _, err := os.Stat(filepath.Join(bin, "go.started")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("denied command started")
	}
}

func TestUnsupportedCommandsAndInjectionNeverRun(t *testing.T) {
	marker := "touch \"$0.started\""
	bin := fakeBin(t, map[string]string{"go": marker, "git": marker, "sh": marker, "rm": marker})
	s := newCommandSetup(t, bin)
	notAllowed := [][]string{
		{"go", "test"}, {"go", "test", "./...", "-run", "X"}, {"go", "test", "./...", "-exec=/tmp/x"}, {"go", "test ./..."},
		{"go", "run", "."}, {"go", "generate", "./..."}, {"git", "log"}, {"git", "diff", "HEAD"}, {"git", "-c", "core.pager=sh", "diff"},
		{"rm", "-rf", "."}, {"sh", "-c", "touch pwned"}, {"go; touch pwned", "test", "./..."}, {"go", "test", "./...; touch pwned"},
		{"go", "test", "$(touch pwned)"}, {"go", "test", "./... | sh"}, {"go", "test", "./...", ">", "out"}, {"/usr/local/go/bin/go", "test", "./..."},
		{"./go", "test", "./..."}, {"GO", "test", "./..."},
	}
	for _, c := range notAllowed {
		if _, err := execute(t, s.registry(ExecutePolicy()), context.Background(), runCall(c[0], c[1:]...)); !errors.Is(err, ErrCommandNotAllowed) {
			t.Errorf("%q: %v", c, err)
		}
	}
	for _, raw := range []string{
		`{"command":"go"}`, `{"args":["test","./..."]}`, `{"command":"go","args":["test","./..."],"cwd":"/"}`,
		`{"command":"go","args":["test","./..."],"env":{"A":"1"}}`, `{"command":"go","command":"git","args":["status"]}`,
		`{"command":"go","args":null}`, `{"command":null,"args":[]}`, `{"command":"go","args":["test",null]}`,
		`{"command":"go","args":["test",1]}`, `{"command":"go","args":"test ./..."}`, `{"command":"go","args":[["test"]]}`,
		`{"command":"go","args":["test","./..."]} {}`, `["go","test"]`,
	} {
		if _, err := s.registry(ExecutePolicy()).Execute(context.Background(), model.ToolCall{Name: "run_command", Arguments: json.RawMessage(raw)}); !errors.Is(err, ErrInvalidArguments) {
			t.Errorf("%s: %v", raw, err)
		}
	}
	if len(s.asked) != 0 {
		t.Fatalf("unsupported commands were proposed: %d", len(s.asked))
	}
	for _, name := range []string{"go", "git", "sh", "rm"} {
		if _, err := os.Stat(filepath.Join(bin, name+".started")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s was executed", name)
		}
	}
	if _, err := os.Stat(filepath.Join(s.dir, "pwned")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("injection created a file")
	}
}

func TestWorkspaceExecutablesAreNeverUsed(t *testing.T) {
	_, w := editWorkspace(t, map[string]string{"go.mod": "module demo\n"})
	local := filepath.Join(w.dir, "bin")
	if err := os.MkdirAll(local, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "go"), []byte("#!/bin/sh\ntouch \"$0.started\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	trusted := t.TempDir()
	if err := os.Symlink(filepath.Join(local, "go"), filepath.Join(trusted, "go")); err != nil { // trusted dir, but points into the workspace
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"workspace dir on PATH":  local,
		"relative PATH entries":  ".:bin:./bin",
		"symlink into workspace": trusted,
	} {
		r := w.newCommandRunner([]string{"PATH=" + path})
		if _, err := r.prepare(context.Background(), runCall("go", "test", "./...")); !errors.Is(err, ErrExecutableNotFound) {
			t.Errorf("%s: %v", name, err)
		}
		if strings.Contains(strings.Join(r.env, "\n"), local) {
			t.Errorf("%s: workspace directory passed on PATH", name)
		}
	}
	if _, err := os.Stat(filepath.Join(local, "go.started")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("workspace binary executed")
	}
}

func TestFailedCommandIsANormalResult(t *testing.T) {
	bin := fakeBin(t, map[string]string{"go": `echo "--- FAIL: TestDiscount"; echo "discount_test.go:9: got 110, want 90" >&2; exit 1`})
	s := newCommandSetup(t, bin)
	res, err := execute(t, s.registry(ExecutePolicy()), context.Background(), runCall("go", "test", "./..."))
	if err != nil {
		t.Fatalf("a failing test run must not be a tool error: %v", err)
	}
	if res.ExitCode != 1 || res.Status != "exited" || !strings.Contains(res.Stdout, "FAIL: TestDiscount") || !strings.Contains(res.Stderr, "got 110, want 90") {
		t.Fatalf("failure not reported: %+v", res)
	}
}

func TestCommandTimeoutIsReported(t *testing.T) {
	bin := fakeBin(t, map[string]string{"go": `sleep 30`})
	s := newCommandSetup(t, bin)
	s.runner.timeout = 200 * time.Millisecond
	start := time.Now()
	res, err := execute(t, s.registry(ExecutePolicy()), context.Background(), runCall("go", "test", "./..."))
	if err != nil || res.Status != "timeout" || res.ExitCode == 0 {
		t.Fatalf("timeout: %+v %v", res, err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("timeout did not stop the command: %v", elapsed)
	}
}

func TestTurnCancellationStopsCommand(t *testing.T) {
	bin := fakeBin(t, map[string]string{"go": `sleep 30`})
	s := newCommandSetup(t, bin)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()
	if _, err := execute(t, s.registry(ExecutePolicy()), ctx, runCall("go", "test", "./...")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("cancellation did not stop the command: %v", elapsed)
	}
}

func TestOutputIsBoundedWhileCaptured(t *testing.T) {
	bin := fakeBin(t, map[string]string{"go": `head -c 2000000 /dev/zero | tr '\0' a; head -c 2000000 /dev/zero | tr '\0' b >&2; echo END`})
	s := newCommandSetup(t, bin)
	res, err := execute(t, s.registry(ExecutePolicy()), context.Background(), runCall("go", "test", "./..."))
	if err != nil {
		t.Fatal(err)
	}
	if !res.StdoutTruncated || !res.StderrTruncated || len(res.Stdout) > maxStreamBytes+64 || len(res.Stderr) > maxStreamBytes+64 {
		t.Fatalf("output not bounded: stdout %d (%v), stderr %d (%v)", len(res.Stdout), res.StdoutTruncated, len(res.Stderr), res.StderrTruncated)
	}
	if !strings.HasSuffix(res.Stdout, "END\n") || !strings.Contains(res.Stdout, "bytes omitted") {
		t.Fatal("the end of the output, where failures are reported, must be kept")
	}
	c := &capped{limit: 1024}
	for i := 0; i < 1000; i++ {
		c.Write(make([]byte, 4096)) // larger than the limit
		c.Write(make([]byte, 300))  // smaller than half of it
	}
	if len(c.head)+len(c.tail) > 1024 || cap(c.head) > 1024 || cap(c.tail) > 1024 {
		t.Fatalf("capture grew: head %d tail %d cap %d", len(c.head), len(c.tail), cap(c.tail))
	}
}

func TestCommandOutputIsCleaned(t *testing.T) {
	bin := fakeBin(t, map[string]string{"go": `printf 'ok \033[31mred\033[0m \033]0;title\007done\r\n\342\200\256x\377\n'`})
	s := newCommandSetup(t, bin)
	res, err := execute(t, s.registry(ExecutePolicy()), context.Background(), runCall("go", "test", "./..."))
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "ok red done\nx�\n" {
		t.Fatalf("output not cleaned: %q", res.Stdout)
	}
}
