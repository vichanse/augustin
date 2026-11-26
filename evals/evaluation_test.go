package evals

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vichanse/augustin/internal/agent"
	"github.com/vichanse/augustin/internal/model"
	"github.com/vichanse/augustin/internal/tools"
)

type diagnosisProvider struct {
	calls int
}

func (p *diagnosisProvider) Complete(_ context.Context, req model.Request) (model.Response, error) {
	p.calls++
	if p.calls == 1 {
		return model.Response{
			FinishReason: model.FinishToolCalls,
			Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
				{ID: "read-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"discount.go"}`)},
			}},
		}, nil
	}
	answer := "unknown"
	last := req.Messages[len(req.Messages)-1]
	if last.Role == model.RoleTool && last.ToolCallID == "read-1" && strings.Contains(last.Content, "price + price*percent/100") {
		answer = "discount_added_instead_of_subtracted"
	}
	return model.Response{
		FinishReason: model.FinishStop,
		Message:      model.Message{Role: model.RoleAssistant, Content: answer},
	}, nil
}

type observedExecutor struct {
	registry *tools.Registry
	reads    int
}

func (e *observedExecutor) Execute(ctx context.Context, call model.ToolCall) (string, error) {
	out, err := e.registry.Execute(ctx, call)
	if err == nil && call.Name == "read_file" {
		e.reads++
	}
	return out, err
}

func TestDiscountDiagnosis(t *testing.T) {
	fixture, err := os.ReadFile("fixtures/buggy-discount/discount.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "discount.go")
	if err := os.WriteFile(path, fixture, 0600); err != nil {
		t.Fatal(err)
	}
	w, err := tools.OpenWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	executor := &observedExecutor{registry: tools.New(w.Handlers(), []string{"read_file"})}
	l := agent.Loop{Provider: &diagnosisProvider{}, Executor: executor, MaxIterations: 3}
	r, err := l.Run(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "Read discount.go and identify the discount calculation bug."}},
		Tools: []model.ToolDefinition{{Name: "read_file", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if executor.reads != 1 || r.Iterations != 2 || len(r.Messages) != 4 {
		t.Fatalf("unexpected execution: reads=%d iterations=%d messages=%d", executor.reads, r.Iterations, len(r.Messages))
	}
	last := r.Messages[len(r.Messages)-1]
	if last.Role != model.RoleAssistant || last.Content != "discount_added_instead_of_subtracted" {
		t.Fatalf("incorrect diagnosis: %q", last.Content)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(fixture) {
		t.Fatalf("fixture changed or unreadable: %v", err)
	}
	t.Log("scenario=discount_diagnosis outcome=pass reads=1 iterations=2")
}
