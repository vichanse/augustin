package modeltest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/vichanse/augustin/internal/model"
)

func TestScripted(t *testing.T) {
	failure := errors.New("provider unavailable")
	p := New(Step{Response: model.Response{
		Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"main.go"}`)},
		}},
		FinishReason: model.FinishToolCalls,
	}}, Step{Err: failure})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Complete(ctx, model.Request{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	r, err := p.Complete(context.Background(), model.Request{})
	if err != nil || r.FinishReason != model.FinishToolCalls || len(r.Message.ToolCalls) != 1 || r.Message.ToolCalls[0].ID != "call-1" {
		t.Fatalf("unexpected response: %+v, %v", r, err)
	}
	if _, err := p.Complete(context.Background(), model.Request{}); !errors.Is(err, failure) {
		t.Fatalf("provider error: %v", err)
	}
	if _, err := p.Complete(context.Background(), model.Request{}); !errors.Is(err, ErrExhausted) {
		t.Fatalf("exhaustion: %v", err)
	}
}
