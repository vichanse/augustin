package tools

import (
	"context"
	"errors"

	"github.com/vichanse/augustin/internal/model"
)

type Capability string

const (
	Read    Capability = "read"
	Edit    Capability = "edit"
	Execute Capability = "execute"
)

type Mode string

const (
	ReadOnly            Mode = "read-only"
	Plan                Mode = "plan"
	EditWithApproval    Mode = "edit-with-approval"
	ExecuteWithApproval Mode = "execute-with-approval"
)

// ErrRejected is an explicit user refusal of one action. Unlike ErrDenied
// (a policy violation), the agent may recover and explain or propose another way.
var ErrRejected = errors.New("action rejected by user")

// Proposal is exactly what the user approves: the call and, for prepared
// tools such as edit_file, the target path and the diff that will be applied.
type Proposal struct {
	Capability Capability
	Call       model.ToolCall
	Path       string
	Diff       string
}

// Approver must approve the exact action, not merely its tool name.
// It returns (false, nil) for an explicit rejection and an error when no
// decision was made (canceled, deadline, UI closed); both fail closed.
type Approver func(context.Context, Proposal) (bool, error)

type Policy struct {
	Mode         Mode
	Capabilities map[string]Capability
	Approve      Approver
}

func (p Policy) authorize(ctx context.Context, call model.ToolCall) error {
	capability, err := p.permit(ctx, call)
	if err != nil {
		return err
	}
	return p.approve(ctx, Proposal{Capability: capability, Call: call})
}

// permit checks mode and capability before anything is prepared or asked.
func (p Policy) permit(ctx context.Context, call model.ToolCall) (Capability, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	capability := p.Capabilities[call.Name]
	switch p.Mode {
	case ReadOnly, Plan, EditWithApproval, ExecuteWithApproval:
	default:
		return "", ErrDenied
	}
	if capability == Read {
		return Read, nil
	}
	if !((capability == Edit && (p.Mode == EditWithApproval || p.Mode == ExecuteWithApproval)) ||
		(capability == Execute && p.Mode == ExecuteWithApproval)) || p.Approve == nil {
		return "", ErrDenied
	}
	return capability, nil
}

// approve asks for an explicit decision on a permitted, non-read proposal.
func (p Policy) approve(ctx context.Context, proposal Proposal) error {
	if proposal.Capability == Read {
		return nil
	}
	approved, err := p.Approve(ctx, proposal)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !approved {
		return ErrRejected
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

// EditPolicy adds edit_file with per-action approval. It still needs an Approver.
func EditPolicy() Policy {
	p := ReadPolicy()
	p.Mode = EditWithApproval
	p.Capabilities["edit_file"] = Edit
	return p
}

// ReadTools and EditTools are the tool allowlists of each mode.
var (
	ReadTools = []string{"read_file", "list_files", "workspace_overview"}
	EditTools = append(append([]string(nil), ReadTools...), "edit_file")
)
