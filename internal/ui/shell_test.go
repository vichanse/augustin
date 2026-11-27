package ui

import (
	"context"
	"strings"

	"testing"

	"github.com/vichanse/augustin/internal/events"
	"github.com/vichanse/augustin/internal/model"
	"github.com/vichanse/augustin/internal/tools"

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
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc}); cmd != nil {
		t.Fatal("idle escape must not exit")
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
	m.turn = textTurn(func(_ context.Context, history []model.Message, prompt string, _ tools.Mode, _ events.Sink) (string, error) {
		if len(history) != 0 || prompt != "hello" {
			t.Fatal("unexpected turn input")
		}
		return "world", nil
	})
	m.composer.SetValue("hello")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !next.(shellModel).busy {
		t.Fatal("turn did not start")
	}
	next, _ = next.Update(turnResult(t, cmd))
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
	if got.toolCalls != 1 || got.tokens != 15 || strings.Join(got.activity, "\n") != "✗ list_files (directory_required)" {
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

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestEscapeInterruptsTurn(t *testing.T) {
	m := newShellModel("/workspace", "openai", "test", false)
	m.ctx = context.Background()
	m.turn = textTurn(func(ctx context.Context, _ []model.Message, _ string, _ tools.Mode, _ events.Sink) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	m.composer.SetValue("long task")
	next, run := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, cmd := next.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if isQuit(cmd) || !next.(shellModel).interrupting {
		t.Fatal("escape did not interrupt the turn")
	}
	next, _ = next.Update(turnResult(t, run)) // returns only because the turn context was canceled
	got := next.(shellModel)
	if got.busy || got.composer.Value() != "long task" || !strings.Contains(got.status, "Interrupted") || len(got.history) != 0 {
		t.Fatalf("interrupt not handled: busy=%v value=%q status=%q", got.busy, got.composer.Value(), got.status)
	}
}

func TestCtrlCExitsOnlyOnSecondPress(t *testing.T) {
	ctrlC := tea.KeyMsg{Type: tea.KeyCtrlC}
	m := newShellModel("/workspace", "openai", "test", false)
	m.composer.SetValue("draft")
	next, cmd := m.Update(ctrlC)
	if cmd != nil || next.(shellModel).composer.Value() != "" {
		t.Fatal("first Ctrl+C should clear the draft")
	}
	next, _ = next.Update(ctrlC) // returns the 2s disarm tick; running it would block
	if !next.(shellModel).quitArmed || !strings.Contains(next.View(), "again to exit") {
		t.Fatal("Ctrl+C on empty input should arm exit")
	}
	if _, cmd = next.Update(ctrlC); !isQuit(cmd) {
		t.Fatal("second Ctrl+C did not exit")
	}
	disarmed, _ := next.Update(disarmQuit{})
	if again, _ := disarmed.Update(ctrlC); !again.(shellModel).quitArmed {
		t.Fatal("expired Ctrl+C should arm again, not exit")
	}
}

func TestCtrlCForceExitsStuckInterrupt(t *testing.T) {
	m := newShellModel("/workspace", "openai", "test", false)
	m.busy = true
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if isQuit(cmd) || !next.(shellModel).interrupting {
		t.Fatal("Ctrl+C during a turn should interrupt first")
	}
	if _, cmd = next.Update(tea.KeyMsg{Type: tea.KeyCtrlC}); !isQuit(cmd) {
		t.Fatal("Ctrl+C during interrupt should exit")
	}
}

// turnResult runs the batched commands of a submit and returns the turn outcome.
func turnResult(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatal("submit should batch the spinner and the turn")
	}
	for _, c := range batch {
		if msg, ok := c().(turnDone); ok {
			return msg
		}
	}
	t.Fatal("no turn result")
	return nil
}

func TestToolActivityShowsTarget(t *testing.T) {
	m := newShellModel("/workspace", "openai", "test", false)
	m.busy = true
	steps := []tea.Msg{
		liveEvent(events.Event{Kind: events.ToolRequested, Tool: "read_file"}),
		liveToolCall(model.ToolCall{Name: "read_file", Arguments: []byte(`{"path":"internal/agent/loop.go"}`)}),
		liveEvent(events.Event{Kind: events.ToolCompleted, Tool: "read_file"}),
		liveEvent(events.Event{Kind: events.ToolRequested, Tool: "list_files"}),
		liveToolCall(model.ToolCall{Name: "list_files", Arguments: []byte(`{}`)}),
	}
	var next tea.Model = m
	for _, msg := range steps {
		next, _ = next.Update(msg)
	}
	got := strings.Join(next.(shellModel).activity, "\n")
	if got != "✓ Read(internal/agent/loop.go)\n◌ List(.)" {
		t.Fatalf("unexpected activity:\n%s", got)
	}
}

func TestDescribeToolSanitizesTarget(t *testing.T) {
	got := describeTool(model.ToolCall{Name: "read_file", Arguments: []byte(`{"path":"a\u001b[31m\nb/` + strings.Repeat("x", 80) + `"}`)})
	if strings.ContainsAny(got, "\x1b\n") || len([]rune(got)) > 67 || !strings.HasPrefix(got, "Read(…") {
		t.Fatalf("unsafe or unbounded label: %q", got)
	}
}

func TestSpinnerStopsBetweenTurns(t *testing.T) {
	m := newShellModel("/workspace", "openai", "test", false)
	if _, cmd := m.Update(m.spinner.Tick()); cmd != nil {
		t.Fatal("spinner kept ticking while idle")
	}
	m.busy, m.status = true, "Working…"
	next, cmd := m.Update(m.spinner.Tick())
	if cmd == nil || !strings.Contains(next.View(), "Working…") {
		t.Fatal("spinner not ticking during a turn")
	}
}

func TestPromptHistoryRecall(t *testing.T) {
	up, down := tea.KeyMsg{Type: tea.KeyUp}, tea.KeyMsg{Type: tea.KeyDown}
	m := newShellModel("/workspace", "openai", "test", false)
	m.composer.Focus()
	for _, p := range []string{"first", "/model", "/model", "second\nline"} {
		m.remember(p)
	}
	m.composer.SetValue("draft")
	var next tea.Model = m
	value := func() string { return next.(shellModel).composer.Value() }
	press := func(k tea.KeyMsg) { next, _ = next.Update(k) }

	press(up)
	if value() != "second\nline" {
		t.Fatalf("↑ should recall the newest prompt, got %q", value())
	}
	press(up) // cursor ends on line 2 of the recalled prompt: ↑ moves inside it first
	press(up)
	if value() != "/model" {
		t.Fatalf("repeated prompt not collapsed or ↑ stuck, got %q", value())
	}
	press(up) // the slash menu must not capture ↑ while browsing
	press(up) // already oldest: stays put
	if value() != "first" {
		t.Fatalf("↑ did not reach the oldest prompt, got %q", value())
	}
	press(down)
	press(down)
	press(down)
	if value() != "draft" {
		t.Fatalf("↓ past the newest prompt should restore the draft, got %q", value())
	}
}

func TestSubmitRecordsPrompt(t *testing.T) {
	m := newShellModel("/workspace", "openai", "test", false)
	m.composer.SetValue("/ver")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // runs /version from the menu
	if got := next.(shellModel).prompts; len(got) != 1 || got[0] != "/version" {
		t.Fatalf("menu command not recorded: %q", got)
	}
}
