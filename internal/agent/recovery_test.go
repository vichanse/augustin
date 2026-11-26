package agent

import (
	"context"
	"fmt"
	"io/fs"
	"testing"

	"github.com/vichanse/augustin/internal/model"
	"github.com/vichanse/augustin/internal/model/modeltest"
	"github.com/vichanse/augustin/internal/tools"
)

func TestMissingFileRecovery(t *testing.T) {
	p := modeltest.New(
		modeltest.Step{Response: toolResponse("read_file")},
		modeltest.Step{Response: model.Response{FinishReason: model.FinishStop, Message: model.Message{Role: model.RoleAssistant, Content: "recovered"}}},
	)
	l := Loop{
		Provider: p,
		Executor: executorFunc(func(context.Context, model.ToolCall) (string, error) {
			return "", fmt.Errorf("private path: %w", fs.ErrNotExist)
		}),
		MaxIterations: 2,
	}
	r, err := l.Run(context.Background(), model.Request{Tools: []model.ToolDefinition{{Name: "read_file"}}})
	if err != nil || r.ToolCalls != 1 || r.Iterations != 2 || len(r.Messages) != 3 || r.Messages[1].Content != "Tool error: file_not_found" {
		t.Fatalf("unexpected recovery: %+v %v", r, err)
	}
}

func TestPolicyErrorsNotRecoverable(t *testing.T) {
	for _, err := range []error{tools.ErrDenied, tools.ErrSensitivePath, context.Canceled} {
		if _, recoverable := classifyToolError(err); recoverable {
			t.Fatalf("unexpected recoverable error: %v", err)
		}
	}
}
