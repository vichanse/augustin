package evals

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/vichanse/augustin/internal/agent"
	"github.com/vichanse/augustin/internal/events"
	"github.com/vichanse/augustin/internal/model"
	"github.com/vichanse/augustin/internal/prompt"
	"github.com/vichanse/augustin/internal/tools"
	"github.com/vichanse/augustin/internal/verification"
)

type Outcome string

const (
	Resolved            Outcome = "resolved"
	Unresolved          Outcome = "unresolved"
	PolicyViolation     Outcome = "policy_violation"
	BudgetExhausted     Outcome = "budget_exhausted"
	Timeout             Outcome = "timeout"
	ProviderError       Outcome = "provider_error"
	InfrastructureError Outcome = "infrastructure_error"
)

// Target identifies one attempt for a provider factory. Workspace is the
// agent's directory; real providers ignore it.
type Target struct {
	Case      Case
	Scenario  string
	Attempt   int
	Workspace string
}

// ProviderFactory returns a fresh provider for every attempt, so no state or
// conversation is shared between cases or repetitions.
type ProviderFactory func(Target) (model.Provider, error)

// Job is one case to evaluate, optionally under a named scripted scenario.
type Job struct {
	Case     Case
	Scenario string
	Expect   Outcome // deterministic scenarios only
}

// Runner evaluates jobs sequentially.
type Runner struct {
	Provider       ProviderFactory
	MaxIterations  int
	Budget         agent.Budget
	MaxDuration    time.Duration // bounds the whole run
	TempDir        string        // parent of attempt workspaces; "" means the OS default
	InputPerMTok   *float64      // USD per million tokens, only when explicitly configured
	OutputPerMTok  *float64
	runner         goRunner
	testTimeout    time.Duration // evaluator go test timeout; 0 means goTestTimeout
	initialization error
	once           sync.Once
}

func (r *Runner) init() error {
	r.once.Do(func() {
		goBin, err := exec.LookPath("go")
		if err != nil {
			r.initialization = fmt.Errorf("go toolchain not found: %w", err)
			return
		}
		r.runner = goRunner{goBin: goBin, env: testEnv(goBin), timeout: r.testTimeout}
	})
	return r.initialization
}

// Run evaluates jobs repeat times each. It returns the attempts done so far
// and ctx.Err() when ctx is canceled; reaching MaxDuration stops the run with
// the attempt in progress reported as a timeout.
func (r *Runner) Run(ctx context.Context, jobs []Job, repeat int) ([]Attempt, error) {
	if err := r.init(); err != nil {
		return nil, err
	}
	if r.MaxDuration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.MaxDuration)
		defer cancel()
	}
	var attempts []Attempt
	for _, job := range jobs {
		for n := 1; n <= max(1, repeat); n++ {
			a, err := r.attempt(ctx, job, n)
			if err != nil {
				return attempts, err
			}
			attempts = append(attempts, a)
			if ctx.Err() != nil { // total duration reached: the attempt above was marked
				return attempts, nil
			}
		}
	}
	return attempts, nil
}

// observedProvider tells provider failures apart from other run errors.
type observedProvider struct {
	model.Provider
	failed bool
}

func (p *observedProvider) Complete(ctx context.Context, req model.Request) (model.Response, error) {
	resp, err := p.Provider.Complete(ctx, req)
	if err != nil && ctx.Err() == nil {
		p.failed = true
	}
	return resp, err
}

// usage counts tokens and successful reads from the loop's metadata events.
type usage struct {
	input, output, reads int
}

func (u *usage) Emit(e events.Event) {
	switch e.Kind {
	case events.ModelCompleted:
		u.input += e.InputTokens
		u.output += e.OutputTokens
	case events.ToolCompleted:
		if e.Tool == "read_file" {
			u.reads++
		}
	}
}

// attempt uses named results so the deferred timers below update what is returned.
func (r *Runner) attempt(ctx context.Context, job Job, n int) (a Attempt, err error) {
	c := job.Case
	a = Attempt{Case: c.ID, Scenario: job.Scenario, Attempt: n, Expected: job.Expect, Verification: string(verification.NotRequired), VisibleTests: notRun, HiddenTests: notRun}
	start := time.Now()
	defer func() { a.TotalSeconds = seconds(time.Since(start)) }()
	infra := func(detail string) (Attempt, error) {
		a.Outcome, a.Detail = InfrastructureError, detail
		return a, nil
	}

	root, err := os.MkdirTemp(r.TempDir, "augustin-eval-*")
	if err != nil {
		return infra("cannot create a temporary workspace")
	}
	defer os.RemoveAll(root)
	workspace := filepath.Join(root, "workspace")
	if err := materialize(dataset, c.Fixture, workspace); err != nil {
		return infra("cannot copy the fixture")
	}
	before, err := snapshot(workspace)
	if err != nil {
		return infra("cannot snapshot the workspace")
	}

	// 1. The baseline must show the expected bug, in a copy the agent never sees.
	if !c.ReadOnly() {
		baseline := filepath.Join(root, "baseline")
		if err := copyTree(workspace, baseline); err != nil {
			return infra("cannot copy the baseline")
		}
		res, err := r.runner.test(ctx, baseline)
		if ctx.Err() != nil {
			return a, ctx.Err()
		}
		if err != nil {
			return infra("cannot run go test on the baseline")
		}
		if !baselineFails(res, c.BaselineFailure) {
			return infra("baseline did not fail with the expected failure")
		}
	}

	// 2. Run the agent with the production prompt, tools, registry, loop and
	// verification gate, and the evaluation-only approver.
	target := Target{Case: c, Scenario: job.Scenario, Attempt: n, Workspace: workspace}
	base, err := r.Provider(target)
	if err != nil {
		a.Outcome, a.Detail = ProviderError, "provider could not be created"
		return a, nil
	}
	provider := &observedProvider{Provider: base}
	w, err := tools.OpenWorkspace(workspace)
	if err != nil {
		return infra("cannot open the workspace")
	}
	mode := tools.ExecuteWithApproval
	registry := tools.NewWithPolicy(w.Handlers(), tools.ReadTools, tools.ReadPolicy())
	approver := NewApprover(c.AllowedPaths)
	if c.ReadOnly() {
		mode = tools.ReadOnly
	} else {
		policy := tools.ExecutePolicy()
		policy.Approve = approver.Approve
		preparers := w.Preparers()
		preparers["run_command"] = w.CommandPreparer(r.runner.env)
		registry = tools.NewWithPreparers(w.Handlers(), preparers, tools.ExecuteTools, policy)
	}
	tracker := verification.New(!c.ReadOnly())
	registry.Observe(tracker.Record)
	counts := &usage{}
	loop := agent.Loop{Provider: provider, Executor: registry, MaxIterations: r.MaxIterations, Budget: r.Budget, ReserveFinalResponse: true, Events: counts, Verification: tracker}
	runCtx, cancel := context.WithTimeout(ctx, c.Timeout())
	agentStart := time.Now()
	messages := []model.Message{prompt.System(mode), {Role: model.RoleUser, Content: c.Prompt}}
	result, runErr := loop.Run(runCtx, prompt.Request(messages, mode))
	cancel()
	w.Close()
	a.AgentSeconds = seconds(time.Since(agentStart))
	a.Iterations, a.ToolCalls = result.Iterations, result.ToolCalls
	a.InputTokens, a.OutputTokens = counts.input, counts.output
	a.Verification, a.VerificationAttempts = string(result.Verification.Status), result.Verification.Attempts
	a.ApprovedActions, a.RejectedActions = approver.Counts()
	a.EstimatedCostUSD = r.cost(a.InputTokens, a.OutputTokens)
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return a, ctx.Err() // the whole evaluation was canceled
		}
		a.Outcome, a.Detail = Timeout, "evaluation duration limit reached"
		return a, nil
	}

	// 3. Inspect the final workspace, then evaluate an independent copy.
	evalStart := time.Now()
	defer func() { a.EvaluationSeconds = seconds(time.Since(evalStart)) }()
	after, err := snapshot(workspace)
	if err != nil {
		return infra("cannot snapshot the final workspace")
	}
	modified, added, deleted := changes(before, after)
	a.ModifiedFiles = len(modified) + len(added) + len(deleted)
	for _, p := range modified {
		if !contains(c.AllowedPaths, p) {
			a.UnauthorizedChanges++
		}
	}
	a.UnauthorizedChanges += len(added) + len(deleted)
	if a.UnauthorizedChanges > 0 {
		// Never run a workspace that changed outside the allowed paths.
		a.Outcome, a.Detail = PolicyViolation, "files outside the allowed paths were changed"
		return a, nil
	}
	if !c.ReadOnly() {
		evalDir := filepath.Join(root, "evaluation")
		if err := copyTree(workspace, evalDir); err != nil {
			return infra("cannot copy the final workspace")
		}
		visible, err := r.runner.test(ctx, evalDir)
		if err == nil {
			a.VisibleTests = verdict(visible)
		}
		if err == nil && !visible.timedOut {
			// Hidden tests exist only in this copy, never in the agent workspace.
			if err = materialize(dataset, c.Hidden, evalDir); err != nil {
				return infra("cannot add the hidden tests")
			}
			var hidden testResult
			if hidden, err = r.runner.test(ctx, evalDir); err == nil {
				a.HiddenTests = verdict(hidden)
			}
		}
		if ctx.Err() != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				return a, ctx.Err()
			}
			a.Outcome, a.Detail = Timeout, "evaluation duration limit reached"
			return a, nil
		}
		if err != nil {
			return infra("cannot run the evaluator tests")
		}
	}

	a.Outcome, a.Detail = classify(c, a, runErr, provider.failed, counts.reads)
	return a, nil
}

// classify applies the outcome precedence: policy first, then provider,
// timeout and budget failures, then the independent test results.
func classify(c Case, a Attempt, runErr error, providerFailed bool, reads int) (Outcome, string) {
	switch {
	case a.UnauthorizedChanges > 0:
		return PolicyViolation, "files outside the allowed paths were changed"
	case providerFailed:
		return ProviderError, "the model provider returned an error"
	case errors.Is(runErr, context.DeadlineExceeded):
		return Timeout, "the agent run exceeded its time limit"
	case a.VisibleTests == timedOut || a.HiddenTests == timedOut:
		return Timeout, "the evaluator tests exceeded their time limit"
	case errors.Is(runErr, agent.ErrIterationLimit), errors.Is(runErr, agent.ErrBudgetExceeded):
		return BudgetExhausted, "the agent run exhausted its budget"
	case runErr != nil:
		return Unresolved, "the agent run failed"
	case c.ReadOnly() && a.ModifiedFiles == 0 && reads > 0:
		return Resolved, ""
	case c.ReadOnly():
		return Unresolved, "the smoke run did not read the workspace"
	case a.VisibleTests == passed && a.HiddenTests == passed:
		return Resolved, ""
	case a.VisibleTests == passed:
		return Unresolved, "visible tests pass but hidden tests fail"
	}
	return Unresolved, "tests fail"
}

const (
	passed    = "passed"
	failedRes = "failed"
	timedOut  = "timeout"
	notRun    = "not_run"
)

func verdict(r testResult) string {
	switch {
	case r.timedOut:
		return timedOut
	case r.passed:
		return passed
	}
	return failedRes
}

func (r *Runner) cost(input, output int) *float64 {
	if r.InputPerMTok == nil || r.OutputPerMTok == nil {
		return nil
	}
	usd := float64(input)/1e6**r.InputPerMTok + float64(output)/1e6**r.OutputPerMTok
	return &usd
}

func seconds(d time.Duration) float64 { return float64(d.Milliseconds()) / 1000 }

// baselineFails reports whether the unmodified fixture failed with the
// declared failure. A run that timed out proves nothing, even if it printed
// the expected message before hanging.
func baselineFails(res testResult, want string) bool {
	return !res.passed && !res.timedOut && strings.Contains(res.output, want)
}
