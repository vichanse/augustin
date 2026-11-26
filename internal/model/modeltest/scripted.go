// Package modeltest provides deterministic model fixtures for tests.
package modeltest

import (
	"context"
	"errors"
	"sync"

	"github.com/vichanse/augustin/internal/model"
)

var ErrExhausted = errors.New("scripted model responses exhausted")

type Step struct {
	Response model.Response
	Err      error
}

// Scripted consumes one step per non-canceled call. Configure steps before use.
type Scripted struct {
	mu    sync.Mutex
	steps []Step
	next  int
}

func New(steps ...Step) *Scripted {
	return &Scripted{steps: append([]Step(nil), steps...)}
}

var _ model.Provider = (*Scripted)(nil)

func (s *Scripted) Complete(ctx context.Context, _ model.Request) (model.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return model.Response{}, err
	}
	if s.next >= len(s.steps) {
		return model.Response{}, ErrExhausted
	}
	step := s.steps[s.next]
	s.next++
	if step.Err != nil {
		return model.Response{}, step.Err
	}
	return step.Response, nil
}
