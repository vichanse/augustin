// Package ui renders execution metadata independently of the harness.
package ui

import (
	"fmt"
	"io"
	"time"

	"github.com/vichanse/augustin/internal/events"
)

type Renderer struct {
	out                  io.Writer
	err                  error
	start                time.Time
	input, output, calls int
}

func New(out io.Writer) *Renderer {
	return &Renderer{out: out}
}

func (r *Renderer) Err() error { return r.err }

func (r *Renderer) line(format string, args ...any) {
	if r.err == nil {
		_, r.err = fmt.Fprintf(r.out, format+"\n", args...)
	}
}

func (r *Renderer) Emit(e events.Event) {
	switch e.Kind {
	case events.AgentStarted:
		r.start = time.Now()
		r.input, r.output, r.calls = 0, 0, 0
		r.line("Augustin | Read-only coding agent")
	case events.ModelRequested:
		r.line("  Analyzing (iteration %d)...", e.Iteration)
	case events.ModelCompleted:
		r.input += e.InputTokens
		r.output += e.OutputTokens
	case events.ToolRequested:
		r.calls++
		r.line("  Tool: %s", e.Tool)
	case events.ToolCompleted:
		r.line("  [OK] %s", e.Tool)
	case events.ToolFailed:
		r.line("  [!] %s: %s (recoverable: %t)", e.Tool, e.Reason, e.Recoverable)
	case events.AgentCompleted, events.AgentFailed:
		r.line("  %s | %d iterations | %d tools | %d input / %d output tokens | %s", e.Reason, e.Iteration, r.calls, r.input, r.output, time.Since(r.start).Round(time.Millisecond))
	}
}
