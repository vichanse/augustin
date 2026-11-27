package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/vichanse/augustin/internal/model"
)

// Recoverable command errors: the model can pick an allowed command instead.
var (
	ErrCommandNotAllowed  = errors.New("command not in the allowlist")
	ErrExecutableNotFound = errors.New("executable not found on the trusted PATH")
)

const (
	commandTimeout = 2 * time.Minute // further bounded by the turn deadline
	maxStreamBytes = 16 << 10        // per stream: first and last 8 KiB are kept
	killGrace      = time.Second     // wait for pipes after the process exits or is killed
)

// allowedCommand maps an exact request to the argv actually executed. Git
// runs without pager, external diff, textconv or fsmonitor helpers.
type allowedCommand struct {
	command string
	args    []string
	run     []string
}

var allowedCommands = []allowedCommand{
	{"go", []string{"test", "./..."}, []string{"test", "./..."}},
	{"go", []string{"vet", "./..."}, []string{"vet", "./..."}},
	{"go", []string{"build", "./..."}, []string{"build", "./..."}},
	{"git", []string{"diff"}, []string{"--no-pager", "-c", "core.fsmonitor=false", "-c", "core.pager=cat", "diff", "--no-ext-diff", "--no-textconv", "--no-color"}},
	{"git", []string{"status"}, []string{"--no-pager", "-c", "core.fsmonitor=false", "-c", "core.pager=cat", "status"}},
}

// AllowedCommands describes the allowlist for prompts and help.
func AllowedCommands() []string {
	var out []string
	for _, c := range allowedCommands {
		out = append(out, strings.Join(append([]string{c.command}, c.args...), " "))
	}
	return out
}

// passedEnv is the only host environment a command sees, besides PATH and
// fixed settings. Provider credentials are excluded by construction.
var passedEnv = []string{"HOME", "TMPDIR", "LANG", "GOCACHE", "GOMODCACHE", "GOPATH", "GOPROXY", "XDG_CACHE_HOME"}

var fixedEnv = []string{
	"GOTOOLCHAIN=local", // never download and run another Go toolchain
	"GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "PAGER=cat", "GIT_OPTIONAL_LOCKS=0",
	"NO_COLOR=1",
}

type commandRunner struct {
	w       *Workspace
	path    []string // absolute PATH entries outside the workspace
	env     []string
	timeout time.Duration
	limit   int
}

// CommandPreparer returns the run_command preparer. hostEnv is os.Environ():
// only PATH and passedEnv are taken from it.
func (w *Workspace) CommandPreparer(hostEnv []string) Preparer {
	return w.newCommandRunner(hostEnv).prepare
}

func (w *Workspace) newCommandRunner(hostEnv []string) *commandRunner {
	host := map[string]string{}
	for _, kv := range hostEnv {
		if k, v, ok := strings.Cut(kv, "="); ok {
			host[k] = v
		}
	}
	r := &commandRunner{w: w, timeout: commandTimeout, limit: maxStreamBytes}
	for _, dir := range filepath.SplitList(host["PATH"]) {
		if filepath.IsAbs(dir) && !w.contains(dir) {
			r.path = append(r.path, filepath.Clean(dir))
		}
	}
	r.env = append([]string{"PATH=" + strings.Join(r.path, string(filepath.ListSeparator))}, fixedEnv...)
	for _, k := range passedEnv {
		if v := host[k]; v != "" {
			r.env = append(r.env, k+"="+v)
		}
	}
	return r
}

// contains reports whether path is inside the workspace, following symlinks.
func (w *Workspace) contains(path string) bool {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	rel, err := filepath.Rel(w.dir, filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// lookup finds an executable on the trusted PATH, never inside the workspace.
func (r *commandRunner) lookup(name string) (string, error) {
	for _, dir := range r.path {
		resolved, err := filepath.EvalSymlinks(filepath.Join(dir, name))
		if err != nil || r.w.contains(resolved) {
			continue
		}
		if info, err := os.Stat(resolved); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return resolved, nil
		}
	}
	return "", ErrExecutableNotFound
}

// parseCommandArgs requires exactly a command string and an args array of
// strings, rejecting unknown, duplicate and null fields and trailing data.
func parseCommandArgs(raw []byte) (string, []string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	str := func() (string, bool) {
		tok, err := dec.Token()
		s, ok := tok.(string)
		return s, err == nil && ok
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return "", nil, ErrInvalidArguments
	}
	var command string
	var args []string
	seen := map[string]bool{}
	for dec.More() {
		key, ok := str()
		if !ok || seen[key] {
			return "", nil, ErrInvalidArguments
		}
		seen[key] = true
		switch key {
		case "command":
			if command, ok = str(); !ok {
				return "", nil, ErrInvalidArguments
			}
		case "args":
			if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
				return "", nil, ErrInvalidArguments
			}
			args = []string{}
			for dec.More() {
				arg, ok := str()
				if !ok {
					return "", nil, ErrInvalidArguments
				}
				args = append(args, arg)
			}
			if tok, err := dec.Token(); err != nil || tok != json.Delim(']') {
				return "", nil, ErrInvalidArguments
			}
		default:
			return "", nil, ErrInvalidArguments
		}
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') || len(seen) != 2 {
		return "", nil, ErrInvalidArguments
	}
	if _, err := dec.Token(); err != io.EOF {
		return "", nil, ErrInvalidArguments
	}
	return command, args, nil
}

func (r *commandRunner) prepare(_ context.Context, call model.ToolCall) (Prepared, error) {
	command, args, err := parseCommandArgs(call.Arguments)
	if err != nil {
		return Prepared{}, err
	}
	i := slices.IndexFunc(allowedCommands, func(c allowedCommand) bool {
		return c.command == command && slices.Equal(c.args, args)
	})
	if i < 0 {
		return Prepared{}, ErrCommandNotAllowed
	}
	exe, err := r.lookup(command)
	if err != nil {
		return Prepared{}, err
	}
	argv := append([]string{exe}, allowedCommands[i].run...)
	request := Evidence{Request: append([]string{command}, args...)}
	return Prepared{Path: r.w.dir, Command: argv, Evidence: request, Apply: func(ctx context.Context) (string, Evidence, error) {
		return r.run(ctx, request, argv)
	}}, nil
}

// run executes exactly argv in the workspace. A non-zero exit or the command
// timeout is a normal result; cancellation or the turn deadline ends the turn.
func (r *commandRunner) run(ctx context.Context, request Evidence, argv []string) (string, Evidence, error) {
	command, args := request.Request[0], request.Request[1:]
	if err := ctx.Err(); err != nil {
		return "", request, err
	}
	runCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	cmd.Dir, cmd.Env = r.w.dir, r.env // stdin stays nil: /dev/null
	stdout, stderr := &capped{limit: r.limit}, &capped{limit: r.limit}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cleanup := isolate(cmd)
	cmd.WaitDelay = killGrace
	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)
	cleanup()
	if ctx.Err() != nil {
		return "", request, ctx.Err()
	}
	if cmd.ProcessState == nil {
		return "", request, err // never started
	}
	status := "exited"
	var exitErr *exec.ExitError
	switch {
	case runCtx.Err() != nil:
		status = "timeout"
	case err != nil && !errors.As(err, &exitErr):
		// Not a clean exit (output copy failure, pipes held open past
		// WaitDelay): whatever the exit code, this cannot verify anything.
		status = "error"
	}
	request.Status, request.ExitCode = status, cmd.ProcessState.ExitCode()
	result, err := json.Marshal(map[string]any{
		"command": command, "args": args, "executable": argv[0],
		"exit_code": cmd.ProcessState.ExitCode(), "status": status, "duration_ms": elapsed.Milliseconds(),
		"stdout": stdout.String(), "stderr": stderr.String(),
		"stdout_truncated": stdout.truncated(), "stderr_truncated": stderr.truncated(),
	})
	return string(result), request, err
}

// capped keeps the first and last limit/2 bytes of a stream and only counts
// the rest, so memory stays bounded however much a process prints.
type capped struct {
	limit      int
	head, tail []byte
	total      int
}

func (c *capped) Write(p []byte) (int, error) {
	n := len(p)
	c.total += n
	half := c.limit / 2
	if room := half - len(c.head); room > 0 {
		k := min(room, len(p))
		c.head = append(c.head, p[:k]...)
		p = p[k:]
	}
	if c.tail == nil {
		c.tail = make([]byte, 0, c.limit) // half kept + one write under half: never reallocated
	}
	if len(p) >= half {
		c.tail = append(c.tail[:0], p[len(p)-half:]...)
		return n, nil
	}
	c.tail = append(c.tail, p...)
	if len(c.tail) > half {
		c.tail = append(c.tail[:0], c.tail[len(c.tail)-half:]...)
	}
	return n, nil
}

func (c *capped) truncated() bool { return c.total > len(c.head)+len(c.tail) }

func (c *capped) String() string {
	text := string(c.head)
	if c.truncated() {
		text += fmt.Sprintf("\n… [%d bytes omitted] …\n", c.total-len(c.head)-len(c.tail))
	}
	return cleanOutput(text + string(c.tail))
}

var escapeSequence = regexp.MustCompile(`\x1b(\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(\x07|\x1b\\)?|[@-Z\\-_])`)

// cleanOutput removes terminal escape sequences and control or format
// characters (keeping newlines and tabs) from untrusted process output.
func cleanOutput(s string) string {
	s = escapeSequence.ReplaceAllString(strings.ToValidUTF8(s, "�"), "")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
}
