package ui

import (
	"context"
	"strings"

	"testing"

	"github.com/vichanse/augustin/internal/events"
	"github.com/vichanse/augustin/internal/model"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestShellLayout(t *testing.T) {
	m := newShellModel("/workspace", "openai", "test", false)
	banner := m.banner()
	for _, want := range []string{"AUGUSTIN", "/workspace", "openai"} {
		if !strings.Contains(banner, want) {
			t.Errorf("banner missing %q", want)
		}
	}
	view := m.View()
	for _, want := range []string{"READ ONLY", "Enter send"} {
		if !strings.Contains(view, want) {
			t.Errorf("live area missing %q", want)
		}
	}
	if strings.Contains(view, "AUGUSTIN") || lipgloss.Height(view) > 8 {
		t.Fatalf("live area should stay compact:\n%s", view)
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("escape did not request exit")
	}
}

func TestComposerSubmission(t *testing.T) {
	m := newShellModel("/workspace", "openai", "test", false)
	m.composer.Focus()
	m.composer.SetValue("first line\nsecond line")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(shellModel)
	if got.submitted != "first line\nsecond line" || got.composer.Value() != "" {
		t.Fatal("multiline prompt was not captured and reset")
	}
}

func TestConversationTurn(t *testing.T) {
	m := newShellModel("/workspace", "openai", "test", false)
	m.ctx = context.Background()
	m.turn = func(_ context.Context, history []model.Message, prompt string, _ events.Sink) (string, error) {
		if len(history) != 0 || prompt != "hello" {
			t.Fatal("unexpected turn input")
		}
		return "world", nil
	}
	m.composer.SetValue("hello")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !next.(shellModel).busy {
		t.Fatal("turn did not start")
	}
	next, _ = next.Update(cmd())
	got := next.(shellModel)
	if got.busy || len(got.history) != 2 || got.history[1].Content != "world" {
		t.Fatal("turn was not retained")
	}
}

func TestLiveHarnessEvents(t *testing.T) {
	m := newShellModel("/workspace", "openai", "test", false)
	m.busy = true
	next, _ := m.Update(liveEvent(events.Event{Kind: events.ToolRequested, Tool: "list_files"}))
	next, _ = next.Update(liveEvent(events.Event{Kind: events.ModelCompleted, InputTokens: 10, OutputTokens: 5}))
	next, _ = next.Update(liveEvent(events.Event{Kind: events.ToolFailed, Tool: "list_files", Reason: "directory_required", Recoverable: true}))
	got := next.(shellModel)
	if got.toolCalls != 1 || got.tokens != 15 || !strings.Contains(strings.Join(got.activity, "\n"), "Recoverable: list_files") {
		t.Fatalf("unexpected telemetry: %+v", got.activity)
	}
}

func TestFinishedTurnGoesToScrollback(t *testing.T) {
	m := newShellModel("/workspace", "openai", "test", false)
	m.busy, m.submitted = true, "hello"
	next, cmd := m.Update(turnDone{answer: "world"})
	got := next.(shellModel)
	if cmd == nil || len(got.pending) != 0 {
		t.Fatal("finished turn was not printed")
	}
	if view := got.View(); strings.Contains(view, "world") || strings.Contains(view, "hello") {
		t.Fatalf("finished turn still drawn in live area:\n%s", view)
	}
	next, cmd = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || next.(shellModel).commandNotice != "" {
		t.Fatal("empty submit should do nothing")
	}
}

func TestCommandOutputGoesToScrollback(t *testing.T) {
	m := newShellModel("/workspace", "openai", "test", false)
	m.composer.SetValue("/model")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || next.(shellModel).commandNotice != "" || strings.Contains(next.View(), "openai · test") {
		t.Fatal("command output was not moved to scrollback")
	}
}
