package tools

import (
	"context"

	"github.com/vichanse/augustin/internal/model"
)

type Capability string

const (
	Read Capability = "read"
	Edit Capability = "edit"
	Execute Capability = "execute"
)

type Mode string

const (
	ReadOnly Mode = "read-only"
	Plan Mode = "plan"
	EditWithApproval Mode = "edit-with-approval"
	ExecuteWithApproval Mode = "execute-with-approval"
)

// Approver must approve the exact action, not merely its tool name.
type Approver func(context.Context, model.ToolCall) (bool, error)

type Policy struct {
	Mode Mode
	Capabilities map[string]Capability
	Approve Approver
}

func (p Policy) authorize(ctx context.Context, call model.ToolCall) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	capability := p.Capabilities[call.Name]
	switch p.Mode {
	case ReadOnly, Plan, EditWithApproval, ExecuteWithApproval:
	default:
		return ErrDenied
	}
	if capability == Read {
		return nil
	}
	if !((capability == Edit && (p.Mode == EditWithApproval || p.Mode == ExecuteWithApproval)) ||
		(capability == Execute && p.Mode == ExecuteWithApproval)) || p.Approve == nil {
		return ErrDenied
	}
	approved, err := p.Approve(ctx, call)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !approved {
		return ErrDenied
	}
	return nil
}

// NewWithPolicy adds a capability gate to the existing tool allowlist.
func NewWithPolicy(handlers map[string]Handler, allowed []string, policy Policy) *Registry {
	r := New(handlers, allowed)
	copyPolicy := Policy{Mode: policy.Mode, Approve: policy.Approve, Capabilities: make(map[string]Capability)}
	for name, capability := range policy.Capabilities {
		copyPolicy.Capabilities[name] = capability
	}
	r.policy = &copyPolicy
	return r
}

func ReadPolicy() Policy {
	return Policy{Mode: ReadOnly, Capabilities: map[string]Capability{
		"read_file": Read, "list_files": Read, "workspace_overview": Read,
	}}
}
