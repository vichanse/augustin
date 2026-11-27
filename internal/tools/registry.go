// Package tools provides explicitly authorized workspace operations.
package tools

import (
	"context"
	"errors"
	"fmt"

	"github.com/vichanse/augustin/internal/model"
)

var ErrDenied = errors.New("tool permission denied")

type Handler func(context.Context, model.ToolCall) (string, error)

// Prepared is a validated mutation that has not touched the workspace yet.
// Apply must re-check the target and apply exactly what Diff shows.
type Prepared struct {
	Path  string
	Diff  string
	Apply func(context.Context) (string, error)
}

// Preparer validates a mutating call and computes its effect without side effects.
type Preparer func(context.Context, model.ToolCall) (Prepared, error)

type Registry struct {
	handlers  map[string]Handler
	preparers map[string]Preparer
	allowed   map[string]bool
	policy    *Policy
}

// New copies the configuration. Unlisted tools are denied by default.
func New(handlers map[string]Handler, allowed []string) *Registry {
	r := &Registry{handlers: make(map[string]Handler), preparers: make(map[string]Preparer), allowed: make(map[string]bool)}
	for name, handler := range handlers {
		r.handlers[name] = handler
	}
	for _, name := range allowed {
		r.allowed[name] = true
	}
	return r
}

func (r *Registry) Execute(ctx context.Context, call model.ToolCall) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	handler, prepare := r.handlers[call.Name], r.preparers[call.Name]
	if !r.allowed[call.Name] || (handler == nil && prepare == nil) {
		return "", fmt.Errorf("%w: %q", ErrDenied, call.Name)
	}
	// Freeze the arguments so the handler runs exactly the action that was approved.
	call.Arguments = append([]byte(nil), call.Arguments...)
	if prepare != nil {
		return r.executePrepared(ctx, call, prepare)
	}
	if r.policy != nil {
		if err := r.policy.authorize(ctx, call); err != nil {
			return "", err
		}
	}
	return handler(ctx, call)
}

// executePrepared permits the capability before reading anything, prepares
// the change, asks for approval of that exact change, then applies it.
func (r *Registry) executePrepared(ctx context.Context, call model.ToolCall, prepare Preparer) (string, error) {
	if r.policy == nil {
		return "", ErrDenied // mutations always go through a policy
	}
	capability, err := r.policy.permit(ctx, call)
	if err != nil {
		return "", err
	}
	if capability == Read {
		return "", ErrDenied // a misconfigured mutation must never skip approval
	}
	prepared, err := prepare(ctx, call)
	if err != nil {
		return "", err
	}
	if err := r.policy.approve(ctx, Proposal{Capability: capability, Call: call, Path: prepared.Path, Diff: prepared.Diff}); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return prepared.Apply(ctx)
}

// NewWithPreparers is NewWithPolicy plus mutating tools that need preparation.
func NewWithPreparers(handlers map[string]Handler, preparers map[string]Preparer, allowed []string, policy Policy) *Registry {
	r := NewWithPolicy(handlers, allowed, policy)
	for name, prepare := range preparers {
		r.preparers[name] = prepare
	}
	return r
}
