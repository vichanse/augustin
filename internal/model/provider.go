// Package model defines the provider-neutral model contract.
package model

import (
	"context"
	"encoding/json"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
}

type ToolDefinition struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

type Request struct {
	Messages []Message
	Tools    []ToolDefinition
}

type Usage struct {
	InputTokens  int
	OutputTokens int
}

type FinishReason string

const (
	FinishStop      FinishReason = "stop"
	FinishToolCalls FinishReason = "tool_calls"
	FinishLength    FinishReason = "length"
	FinishOther     FinishReason = "other"
)

type Response struct {
	Message      Message
	Usage        Usage
	FinishReason FinishReason
}

// Provider implementations must honor context cancellation and return errors
// rather than silently retrying or executing tools.
type Provider interface {
	Complete(ctx context.Context, request Request) (Response, error)
}
