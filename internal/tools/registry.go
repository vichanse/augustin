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

type Registry struct {
	handlers map[string]Handler
	allowed  map[string]bool
	policy   *Policy
}

// New copies the configuration. Unlisted tools are denied by default.
func New(handlers map[string]Handler, allowed []string) *Registry {
	r := &Registry{handlers: make(map[string]Handler), allowed: make(map[string]bool)}
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
	handler := r.handlers[call.Name]
	if !r.allowed[call.Name] || handler == nil {
		return "", fmt.Errorf("%w: %q", ErrDenied, call.Name)
	}
	if r.policy != nil {
		if err := r.policy.authorize(ctx, call); err != nil {
			return "", err
		}
	}
	return handler(ctx, call)
}
