package ui

import (
	"context"

	"github.com/vichanse/augustin/internal/events"
	"github.com/vichanse/augustin/internal/model"
	"github.com/vichanse/augustin/internal/tools"
)

// textTurn adapts a test turn that only returns an answer.
func textTurn(f func(context.Context, []model.Message, string, tools.Mode, events.Sink) (string, error)) TurnFunc {
	return func(ctx context.Context, h []model.Message, p string, mode tools.Mode, s events.Sink) (TurnResult, error) {
		answer, err := f(ctx, h, p, mode, s)
		return TurnResult{Answer: answer}, err
	}
}
