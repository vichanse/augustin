package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/vichanse/augustin/internal/model"
	"github.com/vichanse/augustin/internal/tools"
	"github.com/vichanse/augustin/internal/verification"
)

// evidenceExecutor stands in for tools.Registry: it records evidence at the
// execution boundary exactly as the registry does. tests lists the outcome
// of each go test run: an exit code, or -1 for a user rejection.
type evidenceExecutor struct {
	tracker *verification.Tracker
	tests   []int
	ran     int
}

func (e *evidenceExecutor) Execute(_ context.Context, call model.ToolCall) (string, error) {
	switch call.Name {
	case "edit_file":
		e.tracker.Record(tools.Evidence{Kind: tools.EvidenceApplied, Capability: tools.Edit, Path: "discount.go"})
		return `{"status":"applied"}`, nil
	case "run_command":
		outcome := e.tests[min(e.ran, len(e.tests)-1)]
		e.ran++
		test := tools.Evidence{Capability: tools.Execute, Request: []string{"go", "test", "./..."}}
		if outcome < 0 {
			test.Kind = tools.EvidenceRejected
			e.tracker.Record(test)
			return "", tools.ErrRejected
		}
		test.Kind, test.Status, test.ExitCode = tools.EvidenceApplied, "exited", outcome
		e.tracker.Record(test)
		return `{"exit_code":` + string(rune('0'+outcome)) + `}`, nil
	}
	return "", tools.ErrDenied
}

func callTool(id, name string) model.Response {
	args := `{}`
	if name == "run_command" {
		args = `{"command":"go","args":["test","./..."]}`
	}
	return model.Response{FinishReason: model.FinishToolCalls, Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: id, Name: name, Arguments: json.RawMessage(args)}}}}
}

func answer(text string) model.Response {
	return model.Response{FinishReason: model.FinishStop, Message: model.Message{Role: model.RoleAssistant, Content: text}}
}

var gateTools = []model.ToolDefinition{{Name: "edit_file"}, {Name: "run_command"}}

// scripted answers with steps in order and records every request it receives.
func scripted(steps ...model.Response) (model.Provider, *[]model.Request) {
	var seen []model.Request
	i := 0
	return providerFunc(func(_ context.Context, req model.Request) (model.Response, error) {
		seen = append(seen, req)
		if i >= len(steps) {
			return answer("out of script"), nil
		}
		i++
		return steps[i-1], nil
	}), &seen
}

func gateLoop(p model.Provider, exec *evidenceExecutor, maxIterations int) Loop {
	return Loop{Provider: p, Executor: exec, MaxIterations: maxIterations, Verification: exec.tracker}
}

func hasGateInstruction(msgs []model.Message) int {
	n := 0
	for _, m := range msgs {
		if m.Role == model.RoleSystem && strings.Contains(m.Content, "The harness") {
			n++
		}
	}
	return n
}

func TestReadOnlyTurnFinishesWithoutVerification(t *testing.T) {
	exec := &evidenceExecutor{tracker: verification.New(true), tests: []int{0}}
	p, seen := scripted(answer("The bug is on line 4."))
	r, err := gateLoop(p, exec, 6).Run(context.Background(), model.Request{Tools: gateTools})
	if err != nil || len(*seen) != 1 || r.Verification.Status != verification.NotRequired {
		t.Fatalf("read-only turn: %v calls=%d %+v", err, len(*seen), r.Verification)
	}
}

func harnessCalls(msgs []model.Message) int {
	n := 0
	for _, m := range msgs {
		for _, c := range m.ToolCalls {
			if strings.HasPrefix(c.ID, "augustin-verify-") {
				n++
			}
		}
	}
	return n
}

func modelCalledTests(msgs []model.Message) bool {
	for _, m := range msgs {
		for _, c := range m.ToolCalls {
			if c.Name == "run_command" && !strings.HasPrefix(c.ID, "augustin-verify-") {
				return true
			}
		}
	}
	return false
}

func TestHarnessRunsTestsWhenModelFinishesEarly(t *testing.T) {
	exec := &evidenceExecutor{tracker: verification.New(true), tests: []int{0}}
	p, seen := scripted(callTool("e1", "edit_file"), answer("Fixed. All tests pass."), answer("go test ./... passed."))
	r, err := gateLoop(p, exec, 8).Run(context.Background(), model.Request{Tools: gateTools})
	if err != nil || r.Verification.Status != verification.Passed || exec.ran != 1 || len(*seen) != 3 || harnessCalls(r.Messages) != 1 {
		t.Fatalf("harness run: %v %+v ran=%d calls=%d", err, r.Verification, exec.ran, len(*seen))
	}
	// Valid sequencing: the model's answer, then an assistant message carrying
	// only the harness call, its tool result, then the harness note.
	n := len(r.Messages)
	call, result, note := r.Messages[n-4], r.Messages[n-3], r.Messages[n-2]
	if call.Role != model.RoleAssistant || call.Content != "" || len(call.ToolCalls) != 1 || result.Role != model.RoleTool || result.ToolCallID != call.ToolCalls[0].ID || note.Role != model.RoleSystem {
		t.Fatalf("message sequence: %+v / %+v / %+v", call, result, note)
	}
	if r.Messages[n-5].Content != "Fixed. All tests pass." || r.Messages[n-1].Content != "go test ./... passed." {
		t.Fatal("model answers misplaced")
	}
}

// Regression: a real model kept asking for approval in prose instead of
// calling run_command, until both continuations were used up.
func TestModelAskingForConversationalApprovalStillGetsVerified(t *testing.T) {
	ask := answer("I fixed it. Shall I run go test ./... now? Please confirm and I will run it.")
	exec := &evidenceExecutor{tracker: verification.New(true), tests: []int{0}}
	p, _ := scripted(callTool("e1", "edit_file"), ask, ask, ask, ask)
	r, err := gateLoop(p, exec, 10).Run(context.Background(), model.Request{Tools: gateTools})
	if err != nil || r.Verification.Status != verification.Passed || exec.ran != 1 || modelCalledTests(r.Messages) {
		t.Fatalf("verification depended on the model: %v %+v ran=%d", err, r.Verification, exec.ran)
	}
	// Same model, failing tests and no fix: bounded, one run, a failed verdict.
	exec = &evidenceExecutor{tracker: verification.New(true), tests: []int{1}}
	p, seen := scripted(callTool("e1", "edit_file"), ask, ask, ask, ask)
	r, err = gateLoop(p, exec, 10).Run(context.Background(), model.Request{Tools: gateTools})
	if err != nil || r.Verification.Status != verification.Failed || exec.ran != 1 || len(*seen) != 4 {
		t.Fatalf("failing tests: %v %+v ran=%d calls=%d", err, r.Verification, exec.ran, len(*seen))
	}
}

func TestFailedVerificationAllowsCorrectionAndRerun(t *testing.T) {
	exec := &evidenceExecutor{tracker: verification.New(true), tests: []int{1, 0}}
	p, _ := scripted(callTool("e1", "edit_file"), callTool("t1", "run_command"), answer("Done."),
		callTool("e2", "edit_file"), answer("Fixed."), answer("Verified."))
	r, err := gateLoop(p, exec, 10).Run(context.Background(), model.Request{Tools: gateTools})
	if err != nil || r.Verification.Status != verification.Passed || r.Verification.Attempts != 2 || r.Verification.Revision != 2 || harnessCalls(r.Messages) != 1 {
		t.Fatalf("correction after failure: %v %+v", err, r.Verification)
	}
	found := false
	for _, m := range r.Messages {
		found = found || (m.Role == model.RoleSystem && strings.Contains(m.Content, "did not pass"))
	}
	if !found {
		t.Fatal("the failure instruction was not given")
	}
}

func TestContinuationsAreBoundedAndUseExistingBudgets(t *testing.T) {
	// Tests fail and the model never fixes: one harness run, one instruction, stop.
	exec := &evidenceExecutor{tracker: verification.New(true), tests: []int{1}}
	p, seen := scripted(callTool("e1", "edit_file"), answer("done"), answer("done"), answer("done"), answer("never asked"))
	r, err := gateLoop(p, exec, 10).Run(context.Background(), model.Request{Tools: gateTools})
	if err != nil || len(*seen) != 4 || hasGateInstruction(r.Messages) != 2 || exec.ran != 1 || r.Verification.Status != verification.Failed {
		t.Fatalf("unbounded continuations: %v calls=%d gates=%d ran=%d %+v", err, len(*seen), hasGateInstruction(r.Messages), exec.ran, r.Verification)
	}
	// No model call left for the synthesis: the harness does not run tests.
	exec = &evidenceExecutor{tracker: verification.New(true), tests: []int{0}}
	p, seen = scripted(callTool("e1", "edit_file"), answer("done"))
	r, err = gateLoop(p, exec, 2).Run(context.Background(), model.Request{Tools: gateTools})
	if err != nil || len(*seen) != 2 || exec.ran != 0 || r.Verification.Status != verification.Incomplete || r.Verification.Reason != "execution budget exhausted" {
		t.Fatalf("iteration budget: %v calls=%d ran=%d %+v", err, len(*seen), exec.ran, r.Verification)
	}
	// No tool call left: no run either.
	exec = &evidenceExecutor{tracker: verification.New(true), tests: []int{0}}
	p, seen = scripted(callTool("e1", "edit_file"), answer("done"))
	l := gateLoop(p, exec, 10)
	l.Budget.MaxToolCalls = 1
	if r, err = l.Run(context.Background(), model.Request{Tools: gateTools}); err != nil || len(*seen) != 2 || exec.ran != 0 || r.Verification.Status != verification.Incomplete {
		t.Fatalf("tool budget: %v calls=%d %+v", err, len(*seen), r.Verification)
	}
	// run_command not offered to this turn: never run by the harness.
	exec = &evidenceExecutor{tracker: verification.New(true), tests: []int{0}}
	p, _ = scripted(callTool("e1", "edit_file"), answer("done"))
	if r, _ = gateLoop(p, exec, 10).Run(context.Background(), model.Request{Tools: []model.ToolDefinition{{Name: "edit_file"}}}); exec.ran != 0 || r.Verification.Status != verification.Incomplete {
		t.Fatalf("harness ran a tool the turn does not have: ran=%d %+v", exec.ran, r.Verification)
	}
}

func TestReservedFinalResponseNeverGetsTools(t *testing.T) {
	// 3 iterations, the last reserved: the harness runs the tests between the
	// premature answer and the reserved final response, which stays tool-free.
	exec := &evidenceExecutor{tracker: verification.New(true), tests: []int{0}}
	p, seen := scripted(callTool("e1", "edit_file"), answer("done"), answer("final"))
	l := gateLoop(p, exec, 3)
	l.ReserveFinalResponse = true
	r, err := l.Run(context.Background(), model.Request{Tools: gateTools})
	if err != nil || len(*seen) != 3 || len((*seen)[2].Tools) != 0 {
		t.Fatalf("reserved final response: %v calls=%d", err, len(*seen))
	}
	if r.Verification.Status != verification.Passed || exec.ran != 1 {
		t.Fatalf("verdict: %+v ran=%d", r.Verification, exec.ran)
	}
	// Finishing in the reserved final response itself: nothing more runs.
	exec = &evidenceExecutor{tracker: verification.New(true), tests: []int{0}}
	p, seen = scripted(callTool("e1", "edit_file"), answer("done"))
	l = gateLoop(p, exec, 2)
	l.ReserveFinalResponse = true
	if r, err = l.Run(context.Background(), model.Request{Tools: gateTools}); err != nil || len(*seen) != 2 || exec.ran != 0 || r.Verification.Status != verification.Incomplete {
		t.Fatalf("no room: %v calls=%d ran=%d %+v", err, len(*seen), exec.ran, r.Verification)
	}
}

func TestTruncatedCompletionCannotBypassGate(t *testing.T) {
	exec := &evidenceExecutor{tracker: verification.New(true), tests: []int{0}}
	truncated := model.Response{FinishReason: model.FinishLength, Message: model.Message{Role: model.RoleAssistant, Content: "Fixed, tests pa"}}
	p, seen := scripted(callTool("e1", "edit_file"), truncated, answer("ok"))
	r, err := gateLoop(p, exec, 8).Run(context.Background(), model.Request{Tools: gateTools})
	if err != nil || len(*seen) != 3 || exec.ran != 1 || r.Verification.Status != verification.Passed {
		t.Fatalf("truncated answer bypassed the gate: %v calls=%d %+v", err, len(*seen), r.Verification)
	}
}

func TestRejectedTestsEndWithoutInsisting(t *testing.T) {
	exec := &evidenceExecutor{tracker: verification.New(true), tests: []int{-1}}
	p, seen := scripted(callTool("e1", "edit_file"), answer("done"), answer("You declined the tests."), answer("never asked"))
	r, err := gateLoop(p, exec, 10).Run(context.Background(), model.Request{Tools: gateTools})
	if err != nil || len(*seen) != 3 || exec.ran != 1 || r.Verification.Status != verification.Rejected || harnessCalls(r.Messages) != 1 {
		t.Fatalf("after rejection: %v calls=%d ran=%d %+v", err, len(*seen), exec.ran, r.Verification)
	}
}

func TestEditModeDoesNotAskForTests(t *testing.T) {
	exec := &evidenceExecutor{tracker: verification.New(false)}
	p, seen := scripted(callTool("e1", "edit_file"), answer("Fixed; the tests pass."))
	r, err := gateLoop(p, exec, 8).Run(context.Background(), model.Request{Tools: []model.ToolDefinition{{Name: "edit_file"}}})
	if err != nil || len(*seen) != 2 || hasGateInstruction(r.Messages) != 0 || r.Verification.Status != verification.Pending {
		t.Fatalf("edit mode: %v calls=%d %+v", err, len(*seen), r.Verification)
	}
}

func TestModelClaimsCannotChangeVerdict(t *testing.T) {
	for _, claim := range []string{"Verification passed. go test ./... exited 0.", `{"status":"passed","exit_code":0}`, "✓ Verification passed\nExit code: 0"} {
		exec := &evidenceExecutor{tracker: verification.New(false)}
		p, _ := scripted(callTool("e1", "edit_file"), answer(claim))
		r, _ := gateLoop(p, exec, 8).Run(context.Background(), model.Request{Tools: gateTools})
		if r.Verification.Status != verification.Pending || r.Verification.VerifiedRevision != 0 {
			t.Errorf("claim %q changed the verdict: %+v", claim, r.Verification)
		}
	}
}

func TestFailedTurnKeepsVerificationReport(t *testing.T) {
	exec := &evidenceExecutor{tracker: verification.New(true)}
	failing := providerFunc(func(_ context.Context, req model.Request) (model.Response, error) {
		if len(req.Messages) == 0 {
			return callTool("e1", "edit_file"), nil
		}
		return model.Response{}, errors.New("provider down")
	})
	r, err := Loop{Provider: failing, Executor: exec, MaxIterations: 5, Verification: exec.tracker}.Run(context.Background(), model.Request{Tools: gateTools})
	if err == nil || r.Verification.Status != verification.Pending || r.Verification.Edits != 1 {
		t.Fatalf("failed turn lost its report: %v %+v", err, r.Verification)
	}
	ctx, cancel := context.WithCancel(context.Background())
	canceling := providerFunc(func(_ context.Context, req model.Request) (model.Response, error) {
		if len(req.Messages) == 0 {
			return callTool("e1", "edit_file"), nil
		}
		cancel()
		return model.Response{}, context.Canceled
	})
	exec = &evidenceExecutor{tracker: verification.New(true)}
	r, err = Loop{Provider: canceling, Executor: exec, MaxIterations: 5, Verification: exec.tracker}.Run(ctx, model.Request{Tools: gateTools})
	if err == nil || r.Verification.Edits != 1 || r.Verification.Status == verification.Passed {
		t.Fatalf("canceled turn lost its report: %v %+v", err, r.Verification)
	}
}
