package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

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
		ok, err := b.Approve(context.Background(), tools.Proposal{Capability: tools.Edit, Call: model.ToolCall{Name: "fake_write", Arguments: json.RawMessage(args)}})
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
	m.turn = func(context.Context, []model.Message, string, tools.Mode, events.Sink) (string, error) {
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

func TestApprovalPanelEscapesInsteadOfHiding(t *testing.T) {
	m := newShellModel("/workspace", "openai", "test", false)
	m.approval = &approval.Request{ID: 1, Capability: "edit\x1b[2J", Tool: "fake\x1b]0;pwned\x07_write", Action: "{\"path\":\"a\u200bb\u202egnp.exe\"}\x1b[31m\xff"}
	panel := m.approvalPanel()
	if strings.ContainsAny(panel, "\x1b\x07\u202e\u200b") {
		t.Fatalf("control characters reached the terminal: %q", panel)
	}
	for _, want := range []string{`edit\u001b[2J`, `fake\u001b]0;pwned\u0007_write`, `a\u200bb\u202egnp.exe`, `\xff`} {
		if !strings.Contains(panel, want) {
			t.Errorf("hidden detail: %q not shown in\n%s", want, panel)
		}
	}
	// Different actions must never look the same once rendered.
	if visible("ab") == visible("a\u200bb") {
		t.Fatal("distinct actions render identically")
	}
	if m.approval.Truncated {
		t.Fatal("rendering mutated the request snapshot")
	}
}

func TestActionLinesKeepEveryCharacter(t *testing.T) {
	action := `{"path":"a  b   c","content":"` + strings.Repeat("x y ", 30) + `"}`
	lines := actionLines(action, 16)
	if strings.Join(lines, "") != visible(action) {
		t.Fatal("wrapping dropped characters")
	}
	for _, l := range lines {
		if lipgloss.Width(l) > 16 {
			t.Fatalf("line wider than the panel: %q", l)
		}
	}
}

func TestBrokerTruncatedActionCannotBeApproved(t *testing.T) {
	m, ch := pendingShell(t, `{"content":"`+strings.Repeat("x", 5000)+`"}`)
	m.width, m.height = 400, 400 // even a huge terminal cannot show what the broker cut
	if !m.approval.Truncated || m.previewComplete() {
		t.Fatal("broker truncation not detected")
	}
	if !strings.Contains(m.approvalPanel(), "exceeds the preview size limit") {
		t.Fatal("truncation not explained")
	}
	next := press(m, down, tea.KeyMsg{Type: tea.KeyUp}, tea.KeyMsg{Type: tea.KeyTab})
	if got := next.(shellModel); got.approval == nil || got.approvalChoice != choiceDeny {
		t.Fatal("navigation reached Approve on a truncated action")
	}
	forced := next.(shellModel)
	forced.approvalChoice = choiceApprove // even a forced choice is re-checked on Enter
	if press(forced, enter).(shellModel).approval == nil {
		t.Fatal("Enter approved a truncated action")
	}
	// The broker refuses too, whatever the UI does.
	if m.approvals.Resolve(m.approval.ID, true) {
		t.Fatal("broker approved a truncated request")
	}
	press(next, esc)
	if d := outcome(t, ch); d.ok || d.err != nil {
		t.Fatalf("truncated action must end denied: %+v", d)
	}
}

func TestPreviewTooTallForTerminalCannotBeApproved(t *testing.T) {
	m, ch := pendingShell(t, `{"content":"`+strings.Repeat("y", 1500)+`"}`)
	m.width, m.height = 60, 24 // 1500 chars at 50 per line: 31 lines > 10
	if m.approval.Truncated || m.previewComplete() {
		t.Fatal("terminal-size truncation not detected")
	}
	panel := m.approvalPanel()
	if !strings.Contains(panel, "Preview incomplete: action does not fit") || !strings.Contains(panel, "(unavailable)") {
		t.Fatalf("disabled approval not shown:\n%s", panel)
	}
	if press(m, down).(shellModel).approvalChoice != choiceDeny {
		t.Fatal("navigation reached Approve on an incomplete preview")
	}
	forced := m
	forced.approvalChoice = choiceApprove
	var next tea.Model = press(forced, enter)
	if next.(shellModel).approval == nil {
		t.Fatal("Enter approved an incomplete preview")
	}
	big, _ := next.Update(tea.WindowSizeMsg{Width: 200, Height: 60})
	if !big.(shellModel).previewComplete() || big.(shellModel).approvalChoice != choiceDeny {
		t.Fatal("growing the terminal must enable approval without selecting it")
	}
	press(big, down, enter)
	if d := outcome(t, ch); !d.ok || d.err != nil {
		t.Fatalf("complete preview should be approvable: %+v", d)
	}
}

func TestShrinkingTerminalDeselectsApprove(t *testing.T) {
	m, ch := pendingShell(t, `{"content":"`+strings.Repeat("z", 600)+`"}`)
	m.width, m.height = 200, 60
	next := press(m, down)
	if next.(shellModel).approvalChoice != choiceApprove {
		t.Fatal("Approve not selectable on a complete preview")
	}
	next, _ = next.Update(tea.WindowSizeMsg{Width: 40, Height: 20})
	if next.(shellModel).approvalChoice != choiceDeny {
		t.Fatal("resize left Approve selected on an incomplete preview")
	}
	// Even if the choice was stale, the decision path re-checks the current size.
	stale := next.(shellModel)
	stale.approvalChoice = choiceApprove
	if press(stale, enter).(shellModel).approval == nil {
		t.Fatal("stale Approve choice approved an incomplete preview")
	}
	press(stale, esc)
	if d := outcome(t, ch); d.ok {
		t.Fatal("incomplete preview was approved")
	}
}

func TestClosingShellReleasesPendingApproval(t *testing.T) {
	b := approval.New()
	in, keys := io.Pipe()
	defer keys.Close()
	released := make(chan error, 1)
	turn := func(context.Context, []model.Message, string, tools.Mode, events.Sink) (string, error) {
		// Background, not the turn context: only closing the shell may release it.
		_, err := b.Approve(context.Background(), tools.Proposal{Capability: tools.Edit, Call: model.ToolCall{Name: "fake_write", Arguments: json.RawMessage(`{}`)}})
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

func TestVisibleActionDecodesToTheExecutedValue(t *testing.T) {
	for _, raw := range []string{
		`{"path":"a\u200bb.go"}`,
		"{\"path\":\"a\u200bb\u202e\u00a0\U000e0041 \\n.go\",\"n\":1}",
		`{"content":"line1\nline2\t\"quoted\" \\ backslash"}`,
	} {
		var want, got any
		if err := json.Unmarshal([]byte(raw), &want); err != nil {
			t.Fatal(err)
		}
		shown := visible(raw)
		if err := json.Unmarshal([]byte(shown), &got); err != nil {
			t.Fatalf("preview is not valid JSON: %q: %v", shown, err)
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("preview means something else:\nshown %q\nexecuted %q", shown, raw)
		}
		if strings.ContainsAny(shown, "\u200b\u202e\u00a0") || strings.Contains(shown, "\U000e0041") {
			t.Fatalf("invisible character shown raw: %q", shown)
		}
	}
}

func TestApprovalNavigationIsBidirectional(t *testing.T) {
	m, ch := pendingShell(t, `{"path":"a.txt"}`)
	up := tea.KeyMsg{Type: tea.KeyUp}
	steps := []struct {
		key  tea.KeyMsg
		want int
	}{{down, choiceApprove}, {down, choiceDeny}, {up, choiceApprove}, {up, choiceDeny}, {tea.KeyMsg{Type: tea.KeyTab}, choiceApprove}, {tea.KeyMsg{Type: tea.KeyShiftTab}, choiceDeny}}
	var next tea.Model = m
	for i, step := range steps {
		next = press(next, step.key)
		if got := next.(shellModel).approvalChoice; got != step.want {
			t.Fatalf("step %d (%s): choice %d, want %d", i, step.key, got, step.want)
		}
	}
	press(next, enter) // back on Deny
	if d := outcome(t, ch); d.ok || d.err != nil {
		t.Fatalf("Deny → Approve → Deny then Enter must deny: %+v", d)
	}
}

// fitShell is a busy shell with activity, so the live area competes for space.
func fitShell(t *testing.T, width, height int) shellModel {
	t.Helper()
	m, _ := pendingShell(t, `{"path":"internal/agent/loop.go","content":"`+strings.Repeat("abc ", 40)+`"}`)
	m.submitted, m.status = "edit the loop", "Working…"
	m.activity = []string{"✓ Read(internal/agent/loop.go)", "◌ fake_write"}
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return next.(shellModel)
}

func TestApprovalExactHeightBoundary(t *testing.T) {
	const width = 60
	h := 1
	for ; h <= 200 && !fitShell(t, width, h).previewComplete(); h++ {
	}
	if h > 200 {
		t.Fatal("never fits")
	}
	m := fitShell(t, width, h)
	if got := lipgloss.Height(m.approvalPanel()) + footerChrome; got != h {
		t.Fatalf("boundary at %d rows but panel needs %d", h, got)
	}
	if fitShell(t, width, h-1).previewComplete() {
		t.Fatal("one row short still approvable")
	}
	view := m.View()
	if lipgloss.Height(view) > h || lipgloss.Width(view) > width {
		t.Fatalf("view %dx%d exceeds terminal %dx%d", lipgloss.Width(view), lipgloss.Height(view), width, h)
	}
	for _, line := range actionLines(m.approval.Action, m.actionWidth()) {
		if !strings.Contains(view, line) {
			t.Fatalf("action line missing from a complete view: %q", line)
		}
	}
	if !strings.Contains(view, "Approve once") || strings.Contains(view, "unavailable") || !strings.Contains(view, "Esc deny") {
		t.Fatal("panel options or key hint cut at the boundary")
	}
}

func TestApprovalExactWidthBoundary(t *testing.T) {
	const height = 40
	w := 1
	for ; w <= 300 && !fitShell(t, w, height).previewComplete(); w++ {
	}
	if w > 300 {
		t.Fatal("never fits")
	}
	if fitShell(t, w-1, height).previewComplete() {
		t.Fatal("one column short still approvable")
	}
	m := fitShell(t, w, height)
	if view := m.View(); lipgloss.Width(m.approvalPanel()) > w || lipgloss.Height(view) > height {
		t.Fatalf("panel %d wide in %d columns", lipgloss.Width(m.approvalPanel()), w)
	}
}

func TestTinyTerminalsDisableApproval(t *testing.T) {
	for _, size := range [][2]int{{0, 0}, {1, 1}, {6, 100}, {7, 100}, {100, 1}, {100, footerChrome}, {12, 12}} {
		m := fitShell(t, size[0], size[1])
		if m.previewComplete() {
			t.Errorf("%dx%d: approval enabled on a terminal that cannot show the panel", size[0], size[1])
		}
		_ = m.View() // must not panic
		if got := press(m, down).(shellModel).approvalChoice; got != choiceDeny {
			t.Errorf("%dx%d: navigation reached Approve", size[0], size[1])
		}
	}
}

const sampleDiff = "--- a/discount.go\n+++ b/discount.go\n@@ -1,3 +1,3 @@\n func CalculateDiscount(price, discount float64) float64 {\n-\treturn price + discount\n+\treturn price - discount\n }"

func editShell(t *testing.T, diff string) (shellModel, <-chan decision) {
	t.Helper()
	b := approval.New()
	updates := make(chan approval.Update, 4)
	b.Attach(func(u approval.Update) { updates <- u })
	t.Cleanup(b.Close)
	out := make(chan decision, 1)
	go func() {
		ok, err := b.Approve(context.Background(), tools.Proposal{Capability: tools.Edit, Call: model.ToolCall{Name: "edit_file", Arguments: json.RawMessage(`{"path":"discount.go","old_string":"+","new_string":"-"}`)}, Path: "discount.go", Diff: diff})
		out <- decision{ok, err}
	}()
	m := newShellModel("/workspace", "openai", "test", false)
	m.approvals, m.busy, m.mode = b, true, tools.EditWithApproval
	next, _ := m.Update(approvalUpdate(<-updates))
	return next.(shellModel), out
}

func TestApprovalPanelShowsDiff(t *testing.T) {
	m, ch := editShell(t, sampleDiff)
	view := m.View()
	for _, want := range []string{"Path: discount.go", "Diff:", "--- a/discount.go", "-→   return price + discount", "+→   return price - discount", "❯ Deny", "● EDIT"} {
		if !strings.Contains(view, want) {
			t.Errorf("diff panel missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, `"old_string"`) {
		t.Fatal("raw JSON shown instead of the diff")
	}
	if !m.previewComplete() {
		t.Fatal("small diff should be approvable")
	}
	press(m, down, enter)
	if d := outcome(t, ch); !d.ok {
		t.Fatalf("diff approval: %+v", d)
	}
}

func TestDiffPanelColorsLines(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256) // tests do not run in a color terminal
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	m, _ := editShell(t, sampleDiff)
	m.color = true
	panel := m.approvalPanel()
	red, green := m.paint("-→   return price + discount", "203"), m.paint("+→   return price - discount", "78")
	if !strings.Contains(panel, red) || !strings.Contains(panel, green) {
		t.Fatal("removed/added lines not styled red/green")
	}
}

func TestIncompleteDiffCannotBeApproved(t *testing.T) {
	m, ch := editShell(t, sampleDiff+strings.Repeat("\n+added line", 40))
	if m.previewComplete() {
		t.Fatal("a 40-line diff fits a 24-row terminal")
	}
	forced := m
	forced.approvalChoice = choiceApprove
	if press(forced, enter).(shellModel).approval == nil {
		t.Fatal("incomplete diff approved")
	}
	if !strings.Contains(m.View(), "does not fit") {
		t.Fatal("reason not shown")
	}
	press(m, esc)
	if d := outcome(t, ch); d.ok {
		t.Fatal("incomplete diff approved")
	}
}

func TestVisibleDiffIsUnambiguous(t *testing.T) {
	for _, pair := range [][2]string{
		{"\tx", "→   x"},
		{"a\u200bb", "ab"},
		{"⟨U+200B⟩", "\u200b"},
		{"\t", "    "},
		{"x\r", "x"},
	} {
		if visibleDiff(pair[0]) == visibleDiff(pair[1]) {
			t.Errorf("%q and %q render identically: %q", pair[0], pair[1], visibleDiff(pair[0]))
		}
	}
	if got := visibleDiff("\treturn \"\\n\" // é"); got != `→   return "\n" // é` {
		t.Fatalf("ordinary source altered: %q", got)
	}
	if strings.ContainsAny(visibleDiff("\x1b[31m\u202e\u00a0"), "\x1b\u202e\u00a0") {
		t.Fatal("control characters reached the terminal")
	}
}
