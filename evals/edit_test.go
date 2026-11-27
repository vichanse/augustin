package evals

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vichanse/augustin/internal/agent"
	"github.com/vichanse/augustin/internal/approval"
	"github.com/vichanse/augustin/internal/events"
	"github.com/vichanse/augustin/internal/model"
	"github.com/vichanse/augustin/internal/tools"
)

// fixProvider scripts a model that reads discount.go, proposes the minimal
// fix, then reports only what the last tool result actually says.
type fixProvider struct{ calls int }

func (p *fixProvider) Complete(_ context.Context, req model.Request) (model.Response, error) {
	p.calls++
	last := req.Messages[len(req.Messages)-1]
	call := func(id, name, args string) (model.Response, error) {
		return model.Response{FinishReason: model.FinishToolCalls, Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: id, Name: name, Arguments: json.RawMessage(args)},
		}}}, nil
	}
	switch {
	case p.calls == 1:
		return call("read-1", "read_file", `{"path":"discount.go"}`)
	case p.calls == 2 && last.ToolCallID == "read-1" && strings.Contains(last.Content, "return price + discount"):
		return call("edit-1", "edit_file", `{"path":"discount.go","old_string":"return price + discount","new_string":"return price - discount"}`)
	}
	answer := "unexpected state"
	switch {
	case last.ToolCallID == "edit-1" && strings.Contains(last.Content, `"status":"applied"`):
		answer = "Changed `price + discount` to `price - discount` in discount.go. Tests were not run."
	case last.ToolCallID == "edit-1" && last.Content == "Tool error: user_rejected":
		answer = "The edit was rejected; discount.go is unchanged."
	}
	return model.Response{FinishReason: model.FinishStop, Message: model.Message{Role: model.RoleAssistant, Content: answer}}, nil
}

func runFixScenario(t *testing.T, approve bool) (string, string, string) {
	t.Helper()
	fixture, err := os.ReadFile("fixtures/buggy-pricing/discount.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir() // fresh workspace per scenario
	path := filepath.Join(dir, "discount.go")
	if err := os.WriteFile(path, fixture, 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := tools.OpenWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	broker := approval.New()
	shown := make(chan string, 1)
	broker.Attach(func(u approval.Update) {
		if u.Open {
			shown <- u.Request.Diff
			go broker.Resolve(u.Request.ID, approve) // the human decision
		}
	})
	defer broker.Close()
	policy := tools.EditPolicy()
	policy.Approve = broker.Approve
	var telemetry bytes.Buffer
	l := agent.Loop{
		Events:        events.NewJSONL(&telemetry),
		Provider:      &fixProvider{},
		Executor:      tools.NewWithPreparers(w.Handlers(), w.Preparers(), tools.EditTools, policy),
		MaxIterations: 4,
	}
	r, err := l.Run(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "Find and fix the bug in discount.go."}},
		Tools:    []model.ToolDefinition{{Name: "read_file"}, {Name: "edit_file"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(telemetry.String(), "price") || !strings.Contains(telemetry.String(), `"tool":"edit_file"`) {
		t.Fatalf("telemetry must name the tool but never carry code or diffs:\n%s", telemetry.String())
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var diff string
	select {
	case diff = <-shown:
	default:
		t.Fatal("no approval was requested")
	}
	return r.Messages[len(r.Messages)-1].Content, string(after), diff
}

func TestFixDiscountWithApproval(t *testing.T) {
	answer, after, diff := runFixScenario(t, true)
	if !strings.Contains(diff, "-\treturn price + discount") || !strings.Contains(diff, "+\treturn price - discount") {
		t.Fatalf("approval did not show the change:\n%s", diff)
	}
	if !strings.Contains(after, "return price - discount") || strings.Contains(after, "return price + discount") {
		t.Fatalf("approved fix not applied:\n%s", after)
	}
	if !strings.Contains(answer, "Tests were not run") {
		t.Fatalf("final answer: %q", answer)
	}
	t.Log("scenario=fix_discount_approved outcome=pass")
}

func TestFixDiscountRejected(t *testing.T) {
	fixture, _ := os.ReadFile("fixtures/buggy-pricing/discount.go.txt")
	answer, after, _ := runFixScenario(t, false)
	if after != string(fixture) {
		t.Fatalf("rejected fix modified the file:\n%s", after)
	}
	if answer != "The edit was rejected; discount.go is unchanged." {
		t.Fatalf("final answer: %q", answer)
	}
	t.Log("scenario=fix_discount_rejected outcome=pass")
}
