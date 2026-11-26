// Package events defines metadata-only execution telemetry.
package events

import (
	"encoding/json"
	"io"
	"sync"
)

type Kind string

const (
	AgentStarted   Kind = "agent.started"
	ModelRequested Kind = "model.requested"
	ModelCompleted Kind = "model.completed"
	ModelFailed    Kind = "model.failed"
	ToolRequested  Kind = "tool.requested"
	ToolCompleted  Kind = "tool.completed"
	ToolFailed     Kind = "tool.failed"
	AgentCompleted Kind = "agent.completed"
	AgentFailed    Kind = "agent.failed"
)

type Reason string

const (
	Success        Reason = "success"
	Canceled       Reason = "canceled"
	Deadline       Reason = "deadline"
	IterationLimit Reason = "iteration_limit"
	InvalidModel   Reason = "invalid_model_response"
	ExecutionError Reason = "execution_error"
)

// Event deliberately excludes prompts, arguments, outputs and raw errors.
type Event struct {
	Sequence     int    `json:"sequence"`
	Kind         Kind   `json:"kind"`
	Iteration    int    `json:"iteration"`
	Reason       Reason `json:"reason,omitempty"`
	InputTokens  int    `json:"input_tokens,omitempty"`
	OutputTokens int    `json:"output_tokens,omitempty"`
}

// Sink receives events synchronously. Implementations must return promptly.
type Sink interface {
	Emit(Event)
}

// JSONL serializes events and retains the first write error for inspection.
type JSONL struct {
	mu  sync.Mutex
	enc *json.Encoder
	err error
}

func NewJSONL(w io.Writer) *JSONL {
	return &JSONL{enc: json.NewEncoder(w)}
}

func (s *JSONL) Emit(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		s.err = s.enc.Encode(e)
	}
}

func (s *JSONL) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}
