// Package agent implements the bounded model/tool execution loop.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/vichanse/augustin/internal/model"
)

var ErrIterationLimit = errors.New("agent iteration limit reached")
var ErrInvalidResponse = errors.New("invalid model response")

// Executor must enforce authorization independently of model instructions.
type Executor interface {
	Execute(ctx context.Context, call model.ToolCall) (string, error)
}

type Loop struct {
	Provider      model.Provider
	Executor      Executor
	MaxIterations int
}

type Result struct {
	Messages   []model.Message
	Iterations int
}

func (l Loop) Run(ctx context.Context, req model.Request) (Result, error) {
	result := Result{Messages: append([]model.Message(nil), req.Messages...)}
	if l.Provider == nil || l.MaxIterations <= 0 {
		return result, errors.New("provider and positive iteration limit required")
	}
	allowed := make(map[string]bool, len(req.Tools))
	for _, tool := range req.Tools {
		allowed[tool.Name] = true
	}
	for result.Iterations < l.MaxIterations {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		req.Messages = append([]model.Message(nil), result.Messages...)
		result.Iterations++
		response, err := l.Provider.Complete(ctx, req)
		if err != nil {
			return result, fmt.Errorf("model completion: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		msg := response.Message
		if msg.Role != model.RoleAssistant {
			return result, ErrInvalidResponse
		}
		switch response.FinishReason {
		case model.FinishStop:
			if len(msg.ToolCalls) != 0 {
				return result, ErrInvalidResponse
			}
			result.Messages = append(result.Messages, msg)
			return result, nil
		case model.FinishToolCalls:
			if len(msg.ToolCalls) == 0 || l.Executor == nil {
				return result, ErrInvalidResponse
			}
		default:
			return result, fmt.Errorf("%w: finish reason %q", ErrInvalidResponse, response.FinishReason)
		}
		seen := make(map[string]bool)
		for _, call := range msg.ToolCalls {
			if call.ID == "" || seen[call.ID] || !allowed[call.Name] || !json.Valid(call.Arguments) {
				return result, ErrInvalidResponse
			}
			seen[call.ID] = true
		}
		result.Messages = append(result.Messages, msg)
		for _, call := range msg.ToolCalls {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			output, err := l.Executor.Execute(ctx, call)
			if err != nil {
				return result, fmt.Errorf("tool execution: %w", err)
			}
			if err := ctx.Err(); err != nil {
				return result, err
			}
			result.Messages = append(result.Messages, model.Message{
				Role: model.RoleTool, Content: output, ToolCallID: call.ID,
			})
		}
	}
	return result, ErrIterationLimit
}
