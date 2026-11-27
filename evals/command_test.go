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
)

// testFixProvider scripts: run tests, read the code, fix it, rerun the tests,
// then report only what the tool results actually say.
type testFixProvider struct{ calls int }

func (p *testFixProvider) Complete(_ context.Context, req model.Request) (model.Response, error) {
	p.calls++
	last := req.Messages[len(req.Messages)-1]
	call := func(id, name, args string) (model.Response, error) {
		return model.Response{FinishReason: model.FinishToolCalls, Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: id, Name: name, Arguments: json.RawMessage(args)},
		}}}, nil
	}
	exitCode := func() int {
		var r struct {
			ExitCode int `json:"exit_code"`
		}
		if json.Unmarshal([]byte(last.Content), &r) != nil {
			return -100
		}
		return r.ExitCode
	}
	switch {
	case p.calls == 1:
		return call("test-1", "run_command", `{"command":"go","args":["test","./..."]}`)
	case last.ToolCallID == "test-1" && exitCode() != 0 && strings.Contains(last.Content, "want 40"):
		return call("read-1", "read_file", `{"path":"discount.go"}`)
	case last.ToolCallID == "read-1" && strings.Contains(last.Content, "return price - price*discount/100"):
		return call("edit-1", "edit_file", `{"path":"discount.go","old_string":"return price - price*discount/100","new_string":"return price - discount"}`)
	case last.ToolCallID == "edit-1" && strings.Contains(last.Content, `"status":"applied"`):
		return call("test-2", "run_command", `{"command":"go","args":["test","./..."]}`)
	case last.ToolCallID == "test-2" && exitCode() == 0:
		return model.Response{FinishReason: model.FinishStop, Message: model.Message{Role: model.RoleAssistant,
			Content: "go test ./... failed (exit code 1), discount.go now subtracts the absolute discount, and go test ./... passed (exit code 0)."}}, nil
	}
	return model.Response{FinishReason: model.FinishStop, Message: model.Message{Role: model.RoleAssistant, Content: "unexpected: " + last.Content}}, nil
}

func TestRunTestsFixAndVerify(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not on PATH")
	}
	dir := t.TempDir()
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
	broker := approval.New()
	var approved []string
	broker.Attach(func(u approval.Update) {
		if u.Open {
			label := u.Request.Diff
			if len(u.Request.Command) > 0 {
				label = filepath.Base(u.Request.Command[0]) + " " + strings.Join(u.Request.Command[1:], " ")
			}
			approved = append(approved, label)
			go broker.Resolve(u.Request.ID, true) // the human approves each action
		}
	})
	defer broker.Close()
	policy := tools.ExecutePolicy()
	policy.Approve = broker.Approve
	preparers := w.Preparers()
	preparers["run_command"] = w.CommandPreparer(append(os.Environ(), "PATH="+filepath.Dir(goBin)))
	l := agent.Loop{
		Provider:      &testFixProvider{},
		Executor:      tools.NewWithPreparers(w.Handlers(), preparers, tools.ExecuteTools, policy),
		MaxIterations: 6,
		Budget:        agent.Budget{Timeout: 5 * time.Minute},
	}
	r, err := l.Run(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "Run the tests, identify the bug, fix it and run the tests again. The discount is an absolute amount, not a percentage."}},
		Tools:    []model.ToolDefinition{{Name: "read_file"}, {Name: "edit_file"}, {Name: "run_command"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	answer := r.Messages[len(r.Messages)-1].Content
	if !strings.Contains(answer, "passed (exit code 0)") {
		t.Fatalf("scenario did not complete: %s", answer)
	}
	if len(approved) != 3 || approved[0] != "go test ./..." || !strings.Contains(approved[1], "+\treturn price - discount") || approved[2] != "go test ./..." {
		t.Fatalf("approvals out of order: %q", approved)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "discount.go"))
	if !strings.Contains(string(after), "return price - discount\n") {
		t.Fatalf("fix not applied:\n%s", after)
	}
	t.Log("scenario=run_tests_fix_verify outcome=pass approvals=3")
}
