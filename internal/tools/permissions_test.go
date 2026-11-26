package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/vichanse/augustin/internal/model"
)

func TestCapabilityApproval(t *testing.T) {
	ctx := context.Background()
	call := model.ToolCall{Name: "edit"}
	for _, tc := range []struct {
		mode    Mode
		approve bool
		want    bool
	}{
		{ReadOnly, true, false},
		{Plan, true, false},
		{EditWithApproval, false, false},
		{EditWithApproval, true, true},
		{ExecuteWithApproval, true, true},
		{Mode("unknown"), true, false},
	} {
		called := false
		r := NewWithPolicy(map[string]Handler{"edit": func(context.Context, model.ToolCall) (string, error) {
			called = true
			return "ok", nil
		}}, []string{"edit"}, Policy{Mode: tc.mode, Capabilities: map[string]Capability{"edit": Edit}, Approve: func(context.Context, model.ToolCall) (bool, error) {
			return tc.approve, nil
		}})
		_, err := r.Execute(ctx, call)
		if called != tc.want || (err == nil) != tc.want || (!tc.want && !errors.Is(err, ErrDenied)) {
			t.Fatalf("unexpected authorization for %s: %v", tc.mode, err)
		}
	}
}
