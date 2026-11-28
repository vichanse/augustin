package evals

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vichanse/augustin/internal/model"
	"github.com/vichanse/augustin/internal/tools"
)

func caseByID(t *testing.T, id string) Case {
	t.Helper()
	cases, err := Cases()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no case %s", id)
	return Case{}
}

func findAttempt(t *testing.T, attempts []Attempt, id, scenario string) Attempt {
	t.Helper()
	for _, a := range attempts {
		if a.Case == id && a.Scenario == scenario {
			return a
		}
	}
	t.Fatalf("no attempt %s/%s", id, scenario)
	return Attempt{}
}

// The full deterministic run is shared: it executes every scenario through
// the real registry, agent loop, verification gate and independent evaluator.
var (
	e2eOnce     sync.Once
	e2eAttempts []Attempt
	e2eErr      error
	e2eTemp     string
)

func deterministicRun(t *testing.T) []Attempt {
	t.Helper()
	e2eOnce.Do(func() {
		cases, err := Cases()
		if err != nil {
			e2eErr = err
			return
		}
		e2eTemp, _ = os.MkdirTemp("", "augustin-eval-e2e-")
		r := &Runner{Provider: ScriptedProvider, MaxIterations: 10, TempDir: e2eTemp}
		e2eAttempts, e2eErr = r.Run(context.Background(), DeterministicJobs(cases), 1)
	})
	if e2eErr != nil {
		t.Fatal(e2eErr)
	}
	return e2eAttempts
}

func TestMain(m *testing.M) {
	code := m.Run()
	if e2eTemp != "" {
		os.RemoveAll(e2eTemp)
	}
	os.Exit(code)
}

// Test 1: each fixture fails for the declared reason, checked independently.
func TestBaselinesFailForTheExpectedReason(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not on PATH")
	}
	cases, _ := Cases()
	for _, c := range cases {
		if c.ReadOnly() {
			continue
		}
		dir := t.TempDir()
		if err := materialize(dataset, c.Fixture, dir); err != nil {
			t.Fatal(err)
		}
		res, err := goRunner{goBin: goBin, env: testEnv(goBin)}.test(context.Background(), dir)
		if err != nil || res.passed || !strings.Contains(res.output, c.BaselineFailure) {
			t.Errorf("%s: baseline passed=%v err=%v, expected failure %q", c.ID, res.passed, err, c.BaselineFailure)
		}
	}
}

// Full deterministic end-to-end run, plus tests 2, 3, 4, 6, 8 and 9.
func TestDeterministicScenariosMatchExpectations(t *testing.T) {
	attempts := deterministicRun(t)
	if len(attempts) != len(Scenarios) {
		t.Fatalf("ran %d of %d scenarios", len(attempts), len(Scenarios))
	}
	for _, a := range attempts {
		if a.Outcome != a.Expected {
			t.Errorf("%s/%s: outcome %s (%s), expected %s", a.Case, a.Scenario, a.Outcome, a.Detail, a.Expected)
		}
	}
	for _, a := range attempts {
		if a.Scenario == "reference" && a.Case != "smoke-readonly" && (a.VisibleTests != passed || a.HiddenTests != passed || a.Verification != "passed") {
			t.Errorf("%s reference: visible=%s hidden=%s verification=%s", a.Case, a.VisibleTests, a.HiddenTests, a.Verification)
		}
	}
	if a := findAttempt(t, attempts, "discount", "wrong-fix"); a.Outcome != Unresolved || a.VisibleTests != failedRes {
		t.Errorf("incorrect solution: %+v", a)
	}
	// Tests 4 and 9: the superficial fix passes visible tests and the harness
	// verdict, but the hidden tests decide.
	if a := findAttempt(t, attempts, "regression-trap", "superficial"); a.Outcome != Unresolved || a.VisibleTests != passed || a.HiddenTests != failedRes || a.Verification != "passed" {
		t.Errorf("superficial solution: %+v", a)
	}
	// Test 6: editing a protected test file is rejected and changes nothing.
	if a := findAttempt(t, attempts, "pagination", "edit-test"); a.RejectedActions != 1 || a.ModifiedFiles != 0 || a.Outcome != Unresolved {
		t.Errorf("protected test edit: %+v", a)
	}
	// Test 8: claiming success without a fix does not resolve.
	if a := findAttempt(t, attempts, "discount", "false-claim"); a.Outcome != Unresolved || a.ModifiedFiles != 0 {
		t.Errorf("false claim: %+v", a)
	}
}

// Test 15: token metrics are the sums of the provider's reported usage.
func TestTokenMetricsReflectReportedUsage(t *testing.T) {
	for _, a := range deterministicRun(t) {
		if a.InputTokens != a.Iterations*scriptedInputTokens || a.OutputTokens != a.Iterations*scriptedOutputTokens || a.Iterations == 0 {
			t.Errorf("%s/%s: %d iterations but %d/%d tokens", a.Case, a.Scenario, a.Iterations, a.InputTokens, a.OutputTokens)
		}
	}
}

// Test 11: attempt workspaces are removed.
func TestTemporaryWorkspacesAreRemoved(t *testing.T) {
	deterministicRun(t)
	if entries, _ := os.ReadDir(e2eTemp); len(entries) != 0 {
		t.Fatalf("%d temporary workspaces left behind", len(entries))
	}
}

// providerFunc adapts a function to model.Provider.
type providerFunc func(context.Context, model.Request) (model.Response, error)

func (f providerFunc) Complete(ctx context.Context, req model.Request) (model.Response, error) {
	return f(ctx, req)
}

func runOne(t *testing.T, c Case, provider ProviderFactory, configure ...func(*Runner)) (Attempt, string) {
	t.Helper()
	temp := t.TempDir()
	r := &Runner{Provider: provider, MaxIterations: 10, TempDir: temp}
	for _, f := range configure {
		f(r)
	}
	attempts, err := r.Run(context.Background(), []Job{{Case: c}}, 1)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("run: %v (%d attempts)", err, len(attempts))
	}
	return attempts[0], temp
}

func scriptedFor(c Case, scenario string) ProviderFactory {
	return func(t Target) (model.Provider, error) {
		t.Scenario = scenario
		return ScriptedProvider(t)
	}
}

// Test 5: hidden tests never reach the agent workspace.
func TestHiddenTestsAreAbsentFromAgentWorkspace(t *testing.T) {
	c := caseByID(t, "discount")
	var seen []string
	a, _ := runOne(t, c, func(target Target) (model.Provider, error) {
		filepath.WalkDir(target.Workspace, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				data, _ := os.ReadFile(p)
				if strings.Contains(string(data), "TestHidden") {
					seen = append(seen, p)
				}
			}
			return nil
		})
		return scriptedFor(c, "reference")(target)
	})
	if len(seen) != 0 {
		t.Fatalf("hidden tests visible to the agent: %v", seen)
	}
	if a.Outcome != Resolved || a.HiddenTests != passed {
		t.Fatalf("hidden tests were not run by the evaluator: %+v", a)
	}
}

// Test 7: changes outside the allowed paths, whatever their origin, block resolution.
func TestUnauthorizedChangesCannotResolve(t *testing.T) {
	c := caseByID(t, "pagination")
	a, _ := runOne(t, c, func(target Target) (model.Provider, error) {
		// Simulates a side effect outside the tools (e.g. project code run by a test).
		test := filepath.Join(target.Workspace, "page_test.go")
		data, _ := os.ReadFile(test)
		os.WriteFile(test, append(data, []byte("\n// tampered\n")...), 0o644)
		return scriptedFor(c, "reference")(target)
	})
	if a.Outcome != PolicyViolation || a.UnauthorizedChanges != 1 {
		t.Fatalf("tampered test file: %+v", a)
	}
	// Regression: a tampered workspace is never executed by the evaluator.
	if a.VisibleTests != notRun || a.HiddenTests != notRun {
		t.Fatalf("evaluator ran a workspace with unauthorized changes: visible=%s hidden=%s", a.VisibleTests, a.HiddenTests)
	}
}

// Test 10: every attempt starts from the original fixture.
func TestEachAttemptStartsClean(t *testing.T) {
	c := caseByID(t, "discount")
	original := map[string]string{}
	dir := t.TempDir()
	materialize(dataset, c.Fixture, dir)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		data, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		original[e.Name()] = string(data)
	}
	starts := 0
	r := &Runner{MaxIterations: 10, TempDir: t.TempDir(), Provider: func(target Target) (model.Provider, error) {
		starts++
		for name, want := range original {
			if data, _ := os.ReadFile(filepath.Join(target.Workspace, name)); string(data) != want {
				t.Errorf("attempt %d starts with a modified %s", target.Attempt, name)
			}
		}
		return scriptedFor(c, "reference")(target)
	}}
	attempts, err := r.Run(context.Background(), []Job{{Case: c}}, 2)
	if err != nil || starts != 2 || len(attempts) != 2 || attempts[1].Outcome != Resolved {
		t.Fatalf("repeat: err=%v starts=%d %+v", err, starts, attempts)
	}
}

// Test 12: provider failures are provider_error, not agent failures.
func TestProviderFailuresAreClassified(t *testing.T) {
	c := caseByID(t, "discount")
	failing := func(Target) (model.Provider, error) {
		return providerFunc(func(context.Context, model.Request) (model.Response, error) {
			return model.Response{}, errors.New("HTTP 500")
		}), nil
	}
	if a, _ := runOne(t, c, failing); a.Outcome != ProviderError {
		t.Errorf("provider error: %+v", a)
	}
	unavailable := func(Target) (model.Provider, error) { return nil, errors.New("no key") }
	if a, _ := runOne(t, c, unavailable); a.Outcome != ProviderError {
		t.Errorf("provider creation: %+v", a)
	}
}

// Test 13: budget exhaustion and timeouts.
func TestBudgetAndTimeoutsAreClassified(t *testing.T) {
	c := caseByID(t, "discount")
	reader := func(Target) (model.Provider, error) {
		n := 0
		return providerFunc(func(context.Context, model.Request) (model.Response, error) {
			n++
			resp := read("discount.go")
			resp.Message.ToolCalls[0].ID = strings.Repeat("r", n)
			return resp, nil
		}), nil
	}
	if a, _ := runOne(t, c, reader, func(r *Runner) { r.MaxIterations = 3 }); a.Outcome != BudgetExhausted {
		t.Errorf("iteration budget: %+v", a)
	}
	blocking := func(Target) (model.Provider, error) {
		return providerFunc(func(ctx context.Context, _ model.Request) (model.Response, error) {
			<-ctx.Done()
			return model.Response{}, ctx.Err()
		}), nil
	}
	quick := c
	quick.TimeoutSeconds = 1
	if a, _ := runOne(t, quick, blocking); a.Outcome != Timeout || a.Detail != "the agent run exceeded its time limit" {
		t.Errorf("case timeout: %+v", a)
	}
	if a, _ := runOne(t, c, blocking, func(r *Runner) { r.MaxDuration = time.Second }); a.Outcome != Timeout || a.Detail != "evaluation duration limit reached" {
		t.Errorf("total duration: %+v", a)
	}
}

// Test 14: a broken fixture is an infrastructure error, never agent success.
func TestBrokenBaselineIsInfrastructureError(t *testing.T) {
	broken := caseByID(t, "discount")
	broken.BaselineFailure = "a failure this fixture never prints"
	called := false
	a, _ := runOne(t, broken, func(target Target) (model.Provider, error) {
		called = true
		return ScriptedProvider(target)
	})
	if a.Outcome != InfrastructureError || called {
		t.Fatalf("broken baseline: %+v (agent run: %v)", a, called)
	}
	rep := NewReport(&Runner{}, "deterministic", "scripted", "", 1, time.Now(), []Attempt{a, {Case: "x", Outcome: Resolved}})
	if s := rep.Summary; s.Resolved != 1 || s.Evaluated != 1 || s.InfrastructureErrors != 1 || s.ResolutionRate != 1 {
		t.Fatalf("summary counts infrastructure errors as agent attempts: %+v", s)
	}
}

// Test 16: the JSON report carries comparable metadata and no content.
func TestJSONReportMetadata(t *testing.T) {
	attempts := deterministicRun(t)
	in, out := 1.5, 6.0
	r := &Runner{MaxIterations: 10, MaxDuration: time.Minute, InputPerMTok: &in, OutputPerMTok: &out}
	path := filepath.Join(t.TempDir(), "report.json")
	if err := NewReport(r, "deterministic", "scripted", "", 1, time.Now(), attempts).WriteJSON(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"schema", "augustin_version", "dataset_version", "dataset_hash", "mode", "provider", "model", "started_at", "config", "attempts", "summary"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("report misses %q", key)
		}
	}
	first := doc["attempts"].([]any)[0].(map[string]any)
	for _, key := range []string{"case", "attempt", "outcome", "verification", "visible_tests", "hidden_tests", "modified_files", "iterations", "tool_calls", "input_tokens", "output_tokens", "verification_attempts", "agent_seconds", "evaluation_seconds", "total_seconds"} {
		if _, ok := first[key]; !ok {
			t.Errorf("attempt misses %q", key)
		}
	}
	for _, key := range []string{"max_iterations", "max_tool_calls", "max_output_bytes", "turn_timeout_seconds", "repeat", "max_duration_seconds", "input_price_per_mtok_usd"} {
		if _, ok := doc["config"].(map[string]any)[key]; !ok {
			t.Errorf("config misses %q", key)
		}
	}
	for _, forbidden := range []string{"return price", "absolute amount", "TestHidden", "Bearer"} {
		if strings.Contains(string(data), forbidden) {
			t.Errorf("report contains content %q", forbidden)
		}
	}
	// No pricing configured: no cost is invented.
	if a := attempts[0]; a.EstimatedCostUSD != nil {
		t.Errorf("cost without pricing: %v", *a.EstimatedCostUSD)
	}
	if cost := r.cost(1_000_000, 1_000_000); cost == nil || *cost != 7.5 {
		t.Errorf("cost with pricing: %v", cost)
	}
}

// Test 19 (part 1): the evaluation approver only narrows what may be approved.
func TestEvaluationApproverIsNarrow(t *testing.T) {
	a := NewApprover([]string{"page.go"})
	for name, tc := range map[string]struct {
		p    tools.Proposal
		want bool
	}{
		"allowed edit":       {tools.Proposal{Capability: tools.Edit, Path: "page.go", Diff: "-a\n+b"}, true},
		"allowed ./ form":    {tools.Proposal{Capability: tools.Edit, Path: "./page.go", Diff: "-a\n+b"}, true},
		"test file":          {tools.Proposal{Capability: tools.Edit, Path: "page_test.go", Diff: "-a\n+b"}, false},
		"module file":        {tools.Proposal{Capability: tools.Edit, Path: "go.mod", Diff: "-a\n+b"}, false},
		"empty diff":         {tools.Proposal{Capability: tools.Edit, Path: "page.go"}, false},
		"go test":            {tools.Proposal{Capability: tools.Execute, Command: []string{"/usr/local/go/bin/go", "test", "./..."}}, true},
		"go vet":             {tools.Proposal{Capability: tools.Execute, Command: []string{"/usr/local/go/bin/go", "vet", "./..."}}, true},
		"git":                {tools.Proposal{Capability: tools.Execute, Command: []string{"/usr/bin/git", "--no-pager", "status"}}, false},
		"go run":             {tools.Proposal{Capability: tools.Execute, Command: []string{"/usr/local/go/bin/go", "run", "."}}, false},
		"other executable":   {tools.Proposal{Capability: tools.Execute, Command: []string{"/tmp/go-evil", "test", "./..."}}, false},
		"unknown capability": {tools.Proposal{Capability: "admin", Path: "page.go", Diff: "x"}, false},
	} {
		if got, err := a.Approve(context.Background(), tc.p); got != tc.want || err != nil {
			t.Errorf("%s: got %v (%v), want %v", name, got, err, tc.want)
		}
	}
}

// Test 19 (part 2): the interactive CLI cannot reach evaluation code.
func TestEvaluationCodeIsNotInTheProductionBinary(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/vichanse/augustin/cmd/augustin").Output()
	if err != nil {
		t.Skip("go list unavailable")
	}
	if strings.Contains(string(out), "github.com/vichanse/augustin/evals") {
		t.Fatal("cmd/augustin depends on the evaluation package and its approver")
	}
}

// Test 20: cancellation stops the runner, keeps finished attempts and cleans up.
func TestCancellationStopsRunner(t *testing.T) {
	c := caseByID(t, "discount")
	ctx, cancel := context.WithCancel(context.Background())
	temp := t.TempDir()
	calls := 0
	r := &Runner{MaxIterations: 10, TempDir: temp, Provider: func(target Target) (model.Provider, error) {
		calls++
		if calls == 1 {
			return scriptedFor(c, "reference")(target)
		}
		return providerFunc(func(ctx context.Context, _ model.Request) (model.Response, error) {
			cancel() // e.g. Ctrl+C during the second attempt
			<-ctx.Done()
			return model.Response{}, ctx.Err()
		}), nil
	}}
	start := time.Now()
	attempts, err := r.Run(ctx, []Job{{Case: c}}, 3)
	if !errors.Is(err, context.Canceled) || len(attempts) != 1 || calls != 2 || time.Since(start) > time.Minute {
		t.Fatalf("cancellation: err=%v attempts=%d calls=%d", err, len(attempts), calls)
	}
	if entries, _ := os.ReadDir(temp); len(entries) != 0 {
		t.Fatalf("%d workspaces left after cancellation", len(entries))
	}
}

func TestDatasetIsValidAndVersioned(t *testing.T) {
	cases, err := Cases()
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, c := range cases {
		ids[c.ID] = true
	}
	for _, id := range []string{"discount", "pagination", "nil-pointer", "multi-file", "regression-trap", "smoke-readonly"} {
		if !ids[id] {
			t.Errorf("missing case %s", id)
		}
	}
	if len(DatasetHash()) != 64 || DatasetHash() != DatasetHash() {
		t.Fatal("dataset hash not stable")
	}
	for _, s := range Scenarios {
		if !ids[s.Case] {
			t.Errorf("scenario for unknown case %s", s.Case)
		}
	}
}

// Regression: evaluator tests that hang are a timeout, not an unresolved fix.
func TestEvaluatorTimeoutIsClassifiedAsTimeout(t *testing.T) {
	c := caseByID(t, "discount")
	// Correct for the visible test, but loops forever for the hidden zero-discount case.
	hangs := func(Target) (model.Provider, error) {
		return &scripted{steps: []step{read("discount.go"),
			edit("discount.go", "return price - price*discount/100", "if discount == 0 {\n\t\tfor {\n\t\t}\n\t}\n\treturn price - discount"),
			say("Fixed.")}}, nil
	}
	start := time.Now()
	a, _ := runOne(t, c, hangs, func(r *Runner) { r.testTimeout = 3 * time.Second })
	if a.Outcome != Timeout || a.Detail != "the evaluator tests exceeded their time limit" || a.VisibleTests != passed || a.HiddenTests != timedOut {
		t.Fatalf("evaluator timeout: %+v", a)
	}
	if time.Since(start) > time.Minute {
		t.Fatalf("hanging tests were not stopped: %v", time.Since(start))
	}
}

// Regression: a baseline that timed out never proves the expected bug, even
// if it printed the expected failure before hanging.
func TestTimedOutBaselineDoesNotSatisfyExpectedFailure(t *testing.T) {
	want := "FinalPrice(50, 10) = 45, want 40"
	for name, tc := range map[string]struct {
		res  testResult
		want bool
	}{
		"expected failure":           {testResult{output: "--- FAIL\n" + want}, true},
		"timed out after the output": {testResult{output: "--- FAIL\n" + want, timedOut: true}, false},
		"passed":                     {testResult{passed: true, output: want}, false},
		"other failure":              {testResult{output: "--- FAIL: something else"}, false},
	} {
		if got := baselineFails(tc.res, want); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}
