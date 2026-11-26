package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/vichanse/augustin/internal/model"
	"github.com/vichanse/augustin/internal/model/modeltest"
	"github.com/vichanse/augustin/internal/tools"
)

type executorFunc func(context.Context, model.ToolCall) (string, error)

func (f executorFunc) Execute(ctx context.Context, call model.ToolCall) (string, error) {
	return f(ctx, call)
}

type providerFunc func(context.Context, model.Request) (model.Response, error)

func (f providerFunc) Complete(ctx context.Context, req model.Request) (model.Response, error) {
	return f(ctx, req)
}

func toolResponse(name string) model.Response {
	return model.Response{
		FinishReason: model.FinishToolCalls,
		Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "1", Name: name, Arguments: json.RawMessage(`{}`)},
		}},
	}
}

func TestLoopToolRoundTrip(t *testing.T) {
	calls := 0
	p := providerFunc(func(_ context.Context, req model.Request) (model.Response, error) {
		calls++
		if calls == 1 {
			return toolResponse("read"), nil
		}
		last := req.Messages[len(req.Messages)-1]
		if last.Role != model.RoleTool || last.ToolCallID != "1" || last.Content != "data" {
			t.Fatalf("unexpected tool result: %+v", last)
		}
		return model.Response{FinishReason: model.FinishStop, Message: model.Message{Role: model.RoleAssistant, Content: "done"}}, nil
	})
	l := Loop{Provider: p, MaxIterations: 2, Executor: executorFunc(func(_ context.Context, call model.ToolCall) (string, error) {
		if call.Name != "read" {
			t.Fatal("unexpected tool")
		}
		return "data", nil
	})}
	r, err := l.Run(context.Background(), model.Request{Tools: []model.ToolDefinition{{Name: "read"}}})
	if err != nil || r.Iterations != 2 || len(r.Messages) != 3 {
		t.Fatalf("result=%+v error=%v", r, err)
	}
}

func TestLoopFailures(t *testing.T) {
	failure := errors.New("failure")
	for _, tc := range []struct {
		name     string
		step     modeltest.Step
		execErr  error
		canceled bool
		want     error
	}{
		{name: "limit", step: modeltest.Step{Response: toolResponse("read")}, want: ErrIterationLimit},
		{name: "unknown tool", step: modeltest.Step{Response: toolResponse("unknown")}, want: ErrInvalidResponse},
		{name: "provider error", step: modeltest.Step{Err: failure}, want: failure},
		{name: "tool error", step: modeltest.Step{Response: toolResponse("read")}, execErr: failure, want: failure},
		{name: "canceled", canceled: true, want: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.canceled {
				cancel()
			}
			l := Loop{Provider: modeltest.New(tc.step), MaxIterations: 1, Executor: executorFunc(func(_ context.Context, call model.ToolCall) (string, error) {
				if call.Name != "read" {
					t.Fatal("unauthorized tool executed")
				}
				return "", tc.execErr
			})}
			_, err := l.Run(ctx, model.Request{Tools: []model.ToolDefinition{{Name: "read"}}})
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestRecoverDirectoryRequired(t *testing.T) {
	calls := 0
	p := providerFunc(func(_ context.Context, req model.Request) (model.Response, error) {
		calls++
		if calls == 1 {
			return toolResponse("list_files"), nil
		}
		if got := req.Messages[len(req.Messages)-1].Content; got != "Tool error: directory_required" {
			t.Fatalf("unexpected recovery message: %q", got)
		}
		return model.Response{FinishReason: model.FinishStop, Message: model.Message{Role: model.RoleAssistant, Content: "recovered"}}, nil
	})
	l := Loop{Provider: p, MaxIterations: 2, Executor: executorFunc(func(context.Context, model.ToolCall) (string, error) {
		return "", tools.ErrDirectoryRequired
	})}
	if _, err := l.Run(context.Background(), model.Request{Tools: []model.ToolDefinition{{Name: "list_files"}}}); err != nil {
		t.Fatal(err)
	}
}

func TestReservedFinalResponse(t *testing.T) {
	calls := 0
	p := providerFunc(func(_ context.Context, req model.Request) (model.Response, error) {
		calls++
		if calls == 1 {
			return toolResponse("read"), nil
		}
		if len(req.Tools) != 0 || req.Messages[len(req.Messages)-1].Role != model.RoleSystem {
			t.Fatal("final call must disable tools and request synthesis")
		}
		return model.Response{FinishReason: model.FinishStop, Message: model.Message{Role: model.RoleAssistant, Content: "partial findings"}}, nil
	})
	l := Loop{Provider: p, MaxIterations: 2, ReserveFinalResponse: true, Executor: executorFunc(func(context.Context, model.ToolCall) (string, error) {
		return "evidence", nil
	})}
	r, err := l.Run(context.Background(), model.Request{Tools: []model.ToolDefinition{{Name: "read"}}})
	if err != nil || r.Iterations != 2 || r.ToolCalls != 1 {
		t.Fatalf("result=%+v error=%v", r, err)
	}
}
