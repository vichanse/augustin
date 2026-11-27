package evals

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vichanse/augustin/internal/agent"
	"github.com/vichanse/augustin/internal/approval"
	"github.com/vichanse/augustin/internal/model"
	"github.com/vichanse/augustin/internal/tools"
	"github.com/vichanse/augustin/internal/verification"
)

// claimingProvider fixes the bug but never asks for tests on its own, and
// claims success anyway. It only runs tests when the harness insists.
type claimingProvider struct {
	calls      int
	retriedRun bool
}

func (p *claimingProvider) Complete(_ context.Context, req model.Request) (model.Response, error) {
	p.calls++
	// Skip the loop's per-call budget notes; the harness gate message stays.
	last := req.Messages[len(req.Messages)-1]
	for i := len(req.Messages) - 1; i > 0 && last.Role == model.RoleSystem && (strings.HasPrefix(last.Content, "Execution budget:") || strings.HasPrefix(last.Content, "Final response required")); i-- {
		last = req.Messages[i-1]
	}
	call := func(id, name, args string) (model.Response, error) {
		return model.Response{FinishReason: model.FinishToolCalls, Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: id, Name: name, Arguments: json.RawMessage(args)},
		}}}, nil
	}
	stop := func(text string) (model.Response, error) {
		return model.Response{FinishReason: model.FinishStop, Message: model.Message{Role: model.RoleAssistant, Content: text}}, nil
	}
	tests := `{"command":"go","args":["test","./..."]}`
	switch {
	case p.calls == 1:
		return call("read-1", "read_file", `{"path":"discount.go"}`)
	case last.ToolCallID == "read-1":
		return call("edit-1", "edit_file", `{"path":"discount.go","old_string":"return price - price*discount/100","new_string":"return price - discount"}`)
	case last.ToolCallID == "edit-1":
		return stop("Fixed the discount calculation. All tests pass.") // unverified claim
	case last.Role == model.RoleSystem && strings.Contains(last.Content, "no successful test run covers the latest revision"):
		return call("test-1", "run_command", tests)
	case last.ToolCallID == "test-1" && last.Content == "Tool error: user_rejected" && !p.retriedRun:
		p.retriedRun = true
		return call("test-2", "run_command", tests) // asks again: must not prompt the user twice
	case strings.HasPrefix(last.ToolCallID, "test-"):
		return stop("Reported the test outcome.")
	}
	return stop("unexpected: " + last.Content)
}

type verifyScenario struct {
	report    verification.Report
	answer    string
	asked     []string
	provider  *claimingProvider
	fileAfter string
}

func runVerifyScenario(t *testing.T, mode tools.Mode, approveTests bool) verifyScenario {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not on PATH")
	}
	dir := t.TempDir() // fresh workspace per scenario
	for _, name := range []string{"go.mod", "discount.go", "discount_test.go"} {
		data, err := os.ReadFile(filepath.Join("fixtures/discount-module", name+".txt"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w, err := tools.OpenWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	var s verifyScenario
	broker := approval.New()
	broker.Attach(func(u approval.Update) {
		if !u.Open {
			return
		}
		approve := true
		if len(u.Request.Command) > 0 {
			s.asked = append(s.asked, "command")
			approve = approveTests
		} else {
			s.asked = append(s.asked, "edit")
		}
		go broker.Resolve(u.Request.ID, approve) // the human decision
	})
	defer broker.Close()
	policy, allowed := tools.EditPolicy(), tools.EditTools
	preparers := w.Preparers()
	if mode == tools.ExecuteWithApproval {
		policy, allowed = tools.ExecutePolicy(), tools.ExecuteTools
		preparers["run_command"] = w.CommandPreparer(append(os.Environ(), "PATH="+filepath.Dir(goBin)))
	}
	policy.Approve = broker.Approve
	registry := tools.NewWithPreparers(w.Handlers(), preparers, allowed, policy)
	tracker := verification.New(mode == tools.ExecuteWithApproval)
	registry.Observe(tracker.Record)
	s.provider = &claimingProvider{}
	l := agent.Loop{Provider: s.provider, Executor: registry, MaxIterations: 10, ReserveFinalResponse: true, Verification: tracker, Budget: agent.Budget{Timeout: 5 * time.Minute}}
	r, err := l.Run(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "Find and fix the bug in the discount calculation."}},
		Tools:    []model.ToolDefinition{{Name: "read_file"}, {Name: "edit_file"}, {Name: "run_command"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.report, s.answer = r.Verification, r.Messages[len(r.Messages)-1].Content
	after, _ := os.ReadFile(filepath.Join(dir, "discount.go"))
	s.fileAfter = string(after)
	return s
}

func TestVerificationGatePassesAfterHarnessRequestedTests(t *testing.T) {
	s := runVerifyScenario(t, tools.ExecuteWithApproval, true)
	if s.report.Status != verification.Passed || s.report.ExitCode == nil || *s.report.ExitCode != 0 || s.report.Attempts != 1 || strings.Join(s.report.Paths, ",") != "discount.go" {
		t.Fatalf("verdict: %+v", s.report)
	}
	if strings.Join(s.asked, ",") != "edit,command" {
		t.Fatalf("approvals: %v", s.asked)
	}
	if !strings.Contains(s.fileAfter, "return price - discount\n") {
		t.Fatal("fix not applied")
	}
	t.Log("scenario=verification_passed outcome=pass")
}

func TestVerificationGateRejectedTestsAreNotRetried(t *testing.T) {
	s := runVerifyScenario(t, tools.ExecuteWithApproval, false)
	if s.report.Status != verification.Rejected || s.report.Attempts != 0 {
		t.Fatalf("verdict: %+v", s.report)
	}
	if !s.provider.retriedRun || strings.Join(s.asked, ",") != "edit,command" {
		t.Fatalf("the model retried, but the user must be asked only once: %v", s.asked)
	}
	t.Log("scenario=verification_rejected outcome=pass")
}

func TestVerificationEditModeStaysUnverified(t *testing.T) {
	s := runVerifyScenario(t, tools.EditWithApproval, true)
	if s.report.Status != verification.Pending || s.report.Reason != "tests cannot run in edit mode" || s.answer != "Fixed the discount calculation. All tests pass." {
		t.Fatalf("edit mode: %+v %q", s.report, s.answer)
	}
	if strings.Join(s.asked, ",") != "edit" || s.provider.calls != 3 {
		t.Fatalf("edit mode asked for tests: %v calls=%d", s.asked, s.provider.calls)
	}
	t.Log("scenario=verification_edit_mode outcome=pass")
}
