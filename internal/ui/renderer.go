// Package ui renders execution metadata independently of the harness.
package ui

import (
	"fmt"
	"io"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/vichanse/augustin/internal/events"
)

type Renderer struct {
	out                     io.Writer
	color                   bool
	provider, model, prompt string
	err                     error
	start                   time.Time
	input, output, calls    int
}

func New(out io.Writer) *Renderer {
	return &Renderer{out: out}
}

func NewStyled(out io.Writer, color bool) *Renderer {
	return &Renderer{out: out, color: color}
}

func NewSession(out io.Writer, color bool, provider, model, prompt string) *Renderer {
	return &Renderer{out: out, color: color, provider: provider, model: model, prompt: prompt}
}

func (r *Renderer) paint(value, color string, bold bool) string {
	if !r.color {
		return value
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(bold).Render(value)
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
		r.line("")
		r.line("%s   %s", r.paint("   ▲   ", "99", true), r.paint("A U G U S T I N", "99", true))
		r.line("%s   The Harness-First Coding Agent", r.paint("  ▲ ▲  ", "99", true))
		r.line("%s", r.paint(" ▲▄▄▄▲ ", "63", true))
		r.line("────────────────────────────────────────────────")
		r.line("%s · %s   %s", SafeText(r.provider), SafeText(r.model), r.paint("READ ONLY", "78", true))
		r.line("❯ %s", SafeText(r.prompt))
		r.line("")
	case events.ModelRequested:
		r.line("  Analyzing (iteration %d)...", e.Iteration)
	case events.ModelCompleted:
		r.input += e.InputTokens
		r.output += e.OutputTokens
	case events.ToolRequested:
		r.calls++
		r.line("  Tool: %s", SafeText(e.Tool))
	case events.ToolCompleted:
		r.line("  %s %s  (%s)", r.paint("[OK]", "78", true), toolLabel(e.Tool), SafeText(e.Tool))
	case events.ToolFailed:
		r.line("  %s %s: %s (recoverable: %t)", r.paint("[!]", "214", true), SafeText(e.Tool), SafeText(string(e.Reason)), e.Recoverable)
	case events.AgentCompleted, events.AgentFailed:
		color := "78"
		if e.Kind == events.AgentFailed {
			color = "203"
		}
		r.line("────────────────────────────────────────────────")
		r.line("  %s | %d iterations | %d tools | %d input / %d output tokens | %s", r.paint(SafeText(string(e.Reason)), color, true), e.Iteration, r.calls, r.input, r.output, time.Since(r.start).Round(time.Millisecond))
	}
}

func toolLabel(name string) string {
	switch name {
	case "list_files":
		return "Exploring workspace"
	case "read_file":
		return "Reading source code"
	default:
		return "Tool completed"
	}
}
