package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vichanse/augustin/internal/model"
	"github.com/vichanse/augustin/internal/model/modeltest"
)

func TestToolCallBudget(t *testing.T) {
	response := toolResponse("read")
	second := response.Message.ToolCalls[0]
	second.ID = "2"
	response.Message.ToolCalls = append(response.Message.ToolCalls, second)
	l := Loop{
		Provider:      modeltest.New(modeltest.Step{Response: response}),
		MaxIterations: 2,
		Budget:        Budget{MaxToolCalls: 1},
		Executor: executorFunc(func(context.Context, model.ToolCall) (string, error) {
			t.Fatal("over-budget batch executed")
			return "", nil
		}),
	}
	r, err := l.Run(context.Background(), model.Request{Tools: []model.ToolDefinition{{Name: "read"}}})
	if !errors.Is(err, ErrBudgetExceeded) || r.ToolCalls != 0 {
		t.Fatalf("result=%+v error=%v", r, err)
	}
}

func TestOutputBudget(t *testing.T) {
	l := Loop{
		Provider:      modeltest.New(modeltest.Step{Response: toolResponse("read")}),
		MaxIterations: 2,
		Budget:        Budget{MaxOutputBytes: 3},
		Executor: executorFunc(func(context.Context, model.ToolCall) (string, error) {
			return "four", nil
		}),
	}
	r, err := l.Run(context.Background(), model.Request{Tools: []model.ToolDefinition{{Name: "read"}}})
	if !errors.Is(err, ErrBudgetExceeded) || r.ToolCalls != 1 || r.OutputBytes != 0 {
		t.Fatalf("result=%+v error=%v", r, err)
	}
}

func TestRunTimeout(t *testing.T) {
	l := Loop{
		MaxIterations: 1,
		Budget:        Budget{Timeout: time.Millisecond},
		Provider: providerFunc(func(ctx context.Context, _ model.Request) (model.Response, error) {
			<-ctx.Done()
			return model.Response{}, ctx.Err()
		}),
	}
	_, err := l.Run(context.Background(), model.Request{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline, got %v", err)
	}
}
