package ui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vichanse/augustin/internal/approval"
	"github.com/vichanse/augustin/internal/events"
	"github.com/vichanse/augustin/internal/model"
	"github.com/vichanse/augustin/internal/tools"
)

type decision struct {
	ok  bool
	err error
}

// pendingShell returns a shell showing a real broker request and the channel
// on which the waiting (agent-side) Approve call reports its outcome.
func pendingShell(t *testing.T, args string) (shellModel, <-chan decision) {
	t.Helper()
	b := approval.New()
	updates := make(chan approval.Update, 4)
	b.Attach(func(u approval.Update) { updates <- u })
	t.Cleanup(b.Close)
	out := make(chan decision, 1)
	go func() {
		ok, err := b.Approve(context.Background(), tools.Edit, model.ToolCall{Name: "fake_write", Arguments: json.RawMessage(args)})
		out <- decision{ok, err}
	}()
	m := newShellModel("/workspace", "openai", "test", false)
	m.approvals, m.busy = b, true
	next, _ := m.Update(approvalUpdate(<-updates))
	return next.(shellModel), out
}

func outcome(t *testing.T, ch <-chan decision) decision {
	t.Helper()
	select {
	case d := <-ch:
		return d
	case <-time.After(2 * time.Second):
		t.Fatal("approval not resolved")
		return decision{}
	}
}

func press(m tea.Model, keys ...tea.KeyMsg) tea.Model {
	for _, k := range keys {
		m, _ = m.Update(k)
	}
	return m
}

var (
	enter = tea.KeyMsg{Type: tea.KeyEnter}
	esc   = tea.KeyMsg{Type: tea.KeyEsc}
	down  = tea.KeyMsg{Type: tea.KeyDown}
)

func TestApprovalPanelDefaultsToDeny(t *testing.T) {
	m, ch := pendingShell(t, `{"path":"a.txt"}`)
	view := m.View()
	for _, want := range []string{"Approval required", "Capability: edit", "Tool: fake_write", `{"path":"a.txt"}`, "❯ Deny", "Esc deny"} {
		if !strings.Contains(view, want) {
			t.Errorf("panel missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Enter send") {
		t.Fatal("composer still active during approval")
	}
	next := press(m, enter)
	if d := outcome(t, ch); d.ok || d.err != nil {
		t.Fatalf("Enter on the default must deny: %+v", d)
	}
	if next.(shellModel).approval != nil || !strings.Contains(next.View(), "Esc interrupt") {
		t.Fatal("normal input not restored after the decision")
	}
}

func TestApprovalApproveOnceAndEscapeDenies(t *testing.T) {
	m, ch := pendingShell(t, `{}`)
	press(m, down, enter)
	if d := outcome(t, ch); !d.ok || d.err != nil {
		t.Fatalf("moving to Approve once then Enter must approve: %+v", d)
	}
	m, ch = pendingShell(t, `{}`)
	press(m, down, esc) // Esc denies even with Approve highlighted
	if d := outcome(t, ch); d.ok || d.err != nil {
		t.Fatalf("Esc must deny: %+v", d)
	}
}

func TestApprovalBlocksPromptsAndCommands(t *testing.T) {
	m, ch := pendingShell(t, `{}`)
	m.busy = false // even an idle shell must not submit while a decision is pending
	m.turn = func(context.Context, []model.Message, string, events.Sink) (string, error) {
		t.Fatal("prompt submitted during approval")
		return "", nil
	}
	var next tea.Model = m
	for _, r := range "/exit" {
		next = press(next, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	got := next.(shellModel)
	if got.composer.Value() != "" || got.approval == nil {
		t.Fatalf("keys leaked into the composer: %q", got.composer.Value())
	}
	select {
	case d := <-ch:
		t.Fatalf("typing resolved the approval: %+v", d)
	default:
	}
}

func TestCtrlCDuringApprovalInterruptsTurn(t *testing.T) {
	m, ch := pendingShell(t, `{}`)
	canceled := false
	m.cancelTurn = func() { canceled = true }
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !canceled || !next.(shellModel).interrupting || isQuit(cmd) {
		t.Fatal("Ctrl+C must keep its interrupt meaning during approval")
	}
	select {
	case d := <-ch:
		t.Fatalf("Ctrl+C resolved the approval as a decision: %+v", d)
	default:
	}
}

func TestWithdrawnApprovalClosesPanel(t *testing.T) {
	m, _ := pendingShell(t, `{}`)
	id := m.approval.ID
	next, _ := m.Update(approvalUpdate{Request: approval.Request{ID: id + 1}})
	if next.(shellModel).approval == nil {
		t.Fatal("withdrawal of another request closed this panel")
	}
	next, _ = next.Update(approvalUpdate{Request: approval.Request{ID: id}})
	if next.(shellModel).approval != nil {
		t.Fatal("withdrawn request still shown")
	}
	m, _ = pendingShell(t, `{}`)
	if next, _ = m.Update(turnDone{canceled: true}); next.(shellModel).approval != nil {
		t.Fatal("finished turn left the panel open")
	}
}

func TestApprovalPanelSanitizesContent(t *testing.T) {
	m := newShellModel("/workspace", "openai", "test", false)
	m.approval = &approval.Request{ID: 1, Capability: "edit\x1b[2J", Tool: "fake\x1b]0;pwned\x07_write", Action: "{\"path\":\"\u202egnp.exe\"}\x1b[31m" + strings.Repeat("\nline", 20)}
	panel := m.approvalPanel()
	if strings.ContainsAny(panel, "\x1b\x07\u202e") {
		t.Fatalf("control characters reached the terminal: %q", panel)
	}
	if !strings.Contains(panel, "… truncated") || strings.Count(panel, "line") > maxActionLines {
		t.Fatal("action not bounded")
	}
	if m.approval.Truncated {
		t.Fatal("rendering mutated the request snapshot")
	}
}

func TestClosingShellReleasesPendingApproval(t *testing.T) {
	b := approval.New()
	in, keys := io.Pipe()
	defer keys.Close()
	released := make(chan error, 1)
	turn := func(context.Context, []model.Message, string, events.Sink) (string, error) {
		// Background, not the turn context: only closing the shell may release it.
		_, err := b.Approve(context.Background(), tools.Edit, model.ToolCall{Name: "fake_write", Arguments: json.RawMessage(`{}`)})
		released <- err
		return "", err
	}
	done := make(chan error, 1)
	go func() {
		done <- RunShell(context.Background(), in, io.Discard, t.TempDir(), "openai", "test", false, turn, b)
	}()
	keys.Write([]byte("hi\r"))
	deadline := time.Now().Add(2 * time.Second)
	for _, ok := b.Pending(); !ok; _, ok = b.Pending() {
		if time.Now().After(deadline) {
			t.Fatal("no approval requested")
		}
		time.Sleep(5 * time.Millisecond)
	}
	keys.Write([]byte{0x03}) // interrupt: cancels the turn context only
	time.Sleep(50 * time.Millisecond)
	keys.Write([]byte{0x03}) // force exit while interrupting
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shell: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shell did not exit")
	}
	select {
	case err := <-released:
		if !errors.Is(err, approval.ErrClosed) {
			t.Fatalf("pending approval released with %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("closing the shell did not release the pending approval")
	}
}
