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
		}}, []string{"edit"}, Policy{Mode: tc.mode, Capabilities: map[string]Capability{"edit": Edit}, Approve: func(context.Context, Proposal) (bool, error) {
			return tc.approve, nil
		}})
		_, err := r.Execute(ctx, call)
		if called != tc.want || (err == nil) != tc.want || (!tc.want && !errors.Is(err, ErrDenied) && !errors.Is(err, ErrRejected)) {
			t.Fatalf("unexpected authorization for %s: %v", tc.mode, err)
		}
	}
}

func TestSensitiveActionsDeniedWithoutAskingTheUser(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mode       Mode
		capability Capability
	}{
		{"read-only edit", ReadOnly, Edit},
		{"read-only execute", ReadOnly, Execute},
		{"plan edit", Plan, Edit},
		{"edit mode execute", EditWithApproval, Execute},
		{"unknown capability", ExecuteWithApproval, Capability("admin")},
		{"missing capability", ExecuteWithApproval, ""},
	} {
		asked, ran := false, false
		r := NewWithPolicy(map[string]Handler{"tool": func(context.Context, model.ToolCall) (string, error) {
			ran = true
			return "", nil
		}}, []string{"tool"}, Policy{Mode: tc.mode, Capabilities: map[string]Capability{"tool": tc.capability}, Approve: func(context.Context, Proposal) (bool, error) {
			asked = true
			return true, nil
		}})
		_, err := r.Execute(context.Background(), model.ToolCall{Name: "tool"})
		if !errors.Is(err, ErrDenied) || errors.Is(err, ErrRejected) || asked || ran {
			t.Errorf("%s: err=%v asked=%v ran=%v", tc.name, err, asked, ran)
		}
	}
}

func TestUserRejectionIsDistinctFromPolicyDenial(t *testing.T) {
	var gotCapability Capability
	r := NewWithPolicy(map[string]Handler{"edit": func(context.Context, model.ToolCall) (string, error) {
		t.Fatal("rejected action executed")
		return "", nil
	}}, []string{"edit"}, Policy{Mode: EditWithApproval, Capabilities: map[string]Capability{"edit": Edit}, Approve: func(_ context.Context, p Proposal) (bool, error) {
		gotCapability = p.Capability
		return false, nil
	}})
	_, err := r.Execute(context.Background(), model.ToolCall{Name: "edit"})
	if !errors.Is(err, ErrRejected) || errors.Is(err, ErrDenied) || gotCapability != Edit {
		t.Fatalf("rejection: err=%v capability=%q", err, gotCapability)
	}
}

func TestApproverWithoutDecisionFailsClosed(t *testing.T) {
	r := NewWithPolicy(map[string]Handler{"edit": func(context.Context, model.ToolCall) (string, error) {
		t.Fatal("undecided action executed")
		return "", nil
	}}, []string{"edit"}, Policy{Mode: EditWithApproval, Capabilities: map[string]Capability{"edit": Edit}, Approve: func(context.Context, Proposal) (bool, error) {
		return true, errors.New("ui closed") // an error wins even with true
	}})
	if _, err := r.Execute(context.Background(), model.ToolCall{Name: "edit"}); err == nil || errors.Is(err, ErrRejected) {
		t.Fatalf("undecided approval: %v", err)
	}
}

func TestHeadlessEditModeWithoutApproverDenies(t *testing.T) {
	r := NewWithPolicy(map[string]Handler{"edit": func(context.Context, model.ToolCall) (string, error) {
		t.Fatal("headless action executed")
		return "", nil
	}}, []string{"edit"}, Policy{Mode: EditWithApproval, Capabilities: map[string]Capability{"edit": Edit}})
	if _, err := r.Execute(context.Background(), model.ToolCall{Name: "edit"}); !errors.Is(err, ErrDenied) {
		t.Fatalf("headless approval: %v", err)
	}
}
