package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vichanse/augustin/internal/events"
	"github.com/vichanse/augustin/internal/model"
	"github.com/vichanse/augustin/internal/tools"
)

// pasteMsg is exactly what Bubble Tea sends for a bracketed paste.
func pasteMsg(text string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text), Paste: true}
}

func typeText(m tea.Model, text string) tea.Model {
	for _, r := range text {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func lines(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "\tline %d of the spec\n", i)
	}
	return b.String()
}

func pasteShell() (shellModel, *[]string) {
	m := newShellModel("/workspace", "openai", "test", false)
	m.composer.Focus()
	m.ctx = context.Background()
	prompts := &[]string{}
	m.turn = func(_ context.Context, _ []model.Message, prompt string, _ tools.Mode, _ events.Sink) (string, error) {
		*prompts = append(*prompts, prompt)
		return "done", nil
	}
	return m, prompts
}

func sh(m tea.Model) shellModel { return m.(shellModel) }

func submit(t *testing.T, m tea.Model) tea.Model {
	t.Helper()
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !sh(next).busy {
		t.Fatal("prompt was not submitted")
	}
	next, _ = next.Update(turnResult(t, cmd))
	return next
}

func TestSmallPasteStaysOrdinaryText(t *testing.T) {
	m, _ := pasteShell()
	next, _ := m.Update(pasteMsg("go test ./...\nmake check"))
	if got := sh(next); got.composer.Value() != "go test ./...\nmake check" || len(got.pastes) != 0 {
		t.Fatalf("small paste changed: %q (%d blocks)", got.composer.Value(), len(got.pastes))
	}
}

func TestLargeMultilinePasteBecomesOneBlock(t *testing.T) {
	m, _ := pasteShell()
	spec := lines(216)
	next := typeText(m, "Implement this specification: ")
	next, _ = next.Update(pasteMsg(spec))
	got := sh(next)
	if len(got.pastes) != 1 || got.expand(got.composer.Value()) != "Implement this specification: "+spec {
		t.Fatalf("paste not stored as one exact block (%d blocks)", len(got.pastes))
	}
	view := got.View()
	if !strings.Contains(view, "❯ Implement this specification: [Pasted text #1 +216 lines]") || strings.Contains(view, "line 2 of the spec") {
		t.Fatalf("composer not compact:\n%s", view)
	}
}

func TestLargeSingleLinePasteBecomesOneBlock(t *testing.T) {
	m, _ := pasteShell()
	next, _ := m.Update(pasteMsg(strings.Repeat("x", 2400)))
	got := sh(next)
	if len(got.pastes) != 1 || !strings.Contains(got.View(), "[Pasted text #1 · 2400 chars]") {
		t.Fatalf("single-line paste not compact:\n%s", got.View())
	}
}

func TestPastedBlocksGetDistinctNeverReusedIDs(t *testing.T) {
	m, _ := pasteShell()
	next, _ := m.Update(pasteMsg(lines(10)))
	next, _ = next.Update(pasteMsg(lines(48)))
	view := sh(next).View()
	if !strings.Contains(view, "[Pasted text #1 +10 lines][Pasted text #2 +48 lines]") {
		t.Fatalf("ids:\n%s", view)
	}
	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyBackspace}) // removes #2
	next, _ = next.Update(pasteMsg(lines(7)))
	if view := sh(next).View(); !strings.Contains(view, "[Pasted text #3 +7 lines]") || strings.Contains(view, "#2") {
		t.Fatalf("a deleted id was reused:\n%s", view)
	}
}

func TestTextAroundBlocksIsPreserved(t *testing.T) {
	m, prompts := pasteShell()
	one, two := lines(20), strings.Repeat("y", 1500)
	next := typeText(m, "before ")
	next, _ = next.Update(pasteMsg(one))
	next = typeText(next, " between ")
	next, _ = next.Update(pasteMsg(two))
	next = typeText(next, " after")
	submit(t, next)
	if want := "before " + one + " between " + two + " after"; len(*prompts) != 1 || (*prompts)[0] != want {
		t.Fatalf("expanded prompt differs from what was typed and pasted")
	}
}

func TestBackspaceRemovesWholeBlock(t *testing.T) {
	m, _ := pasteShell()
	next := typeText(m, "keep ")
	next, _ = next.Update(pasteMsg(lines(30)))
	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if got := sh(next); got.composer.Value() != "keep " || len(got.pastes) != 0 {
		t.Fatalf("backspace left %q and %d blocks", got.composer.Value(), len(got.pastes))
	}
}

func TestCursorMovesOverBlockAtomically(t *testing.T) {
	m, prompts := pasteShell()
	block := lines(12)
	next := typeText(m, "x")
	next, _ = next.Update(pasteMsg(block))
	next = typeText(next, "y")
	left := tea.KeyMsg{Type: tea.KeyLeft}
	next, _ = next.Update(left) // before y
	next, _ = next.Update(left) // before the block, in one step
	next = typeText(next, "z")
	if got := sh(next); got.expand(got.composer.Value()) != "xz"+block+"y" {
		t.Fatalf("left did not step over the block: %q", got.compact(got.composer.Value()))
	}
	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyRight}) // after the block, in one step
	next = typeText(next, "w")
	next, _ = next.Update(left)
	next, _ = next.Update(left)                            // before the block
	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyDelete}) // forward delete removes it whole
	got := sh(next)
	if got.composer.Value() != "xzwy" || len(got.pastes) != 0 {
		t.Fatalf("navigation or delete corrupted the block: %q", got.compact(got.composer.Value()))
	}
	submit(t, next)
	if (*prompts)[0] != "xzwy" {
		t.Fatalf("prompt: %q", (*prompts)[0])
	}
}

func TestExpandedPromptMatchesPasteExactly(t *testing.T) {
	m, prompts := pasteShell()
	original := "\n\n  leading blank lines\r\nCRLF line\r\n\ttab indented\ttrailing  \n" + "unicode é 🚀 ​\n" + lines(5) + "   \n\n"
	next, _ := m.Update(pasteMsg(original))
	submit(t, next)
	if len(*prompts) != 1 || (*prompts)[0] != original {
		t.Fatalf("model received altered content:\n%q\nwant\n%q", (*prompts)[0], original)
	}
}

func TestPlaceholderLikeTextIsNotExpanded(t *testing.T) {
	m, prompts := pasteShell()
	next, _ := m.Update(pasteMsg(lines(9))) // block #1 exists
	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	literal := "[Pasted text #1 +9 lines] and [Pasted text #2 · 2400 chars]"
	next = typeText(next, literal)
	submit(t, next)
	if (*prompts)[0] != literal {
		t.Fatalf("typed label was expanded: %q", (*prompts)[0])
	}
}

func TestHistoryRecallRestoresBlocksAndModelGetsExpandedText(t *testing.T) {
	m, prompts := pasteShell()
	spec := lines(216)
	next := typeText(m, "do ")
	next, _ = next.Update(pasteMsg(spec))
	next = submit(t, next)
	got := sh(next)
	if got.history[0].Content != "do "+spec {
		t.Fatal("model history must keep the expanded prompt")
	}
	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyUp})
	recalled := sh(next)
	if !strings.Contains(recalled.View(), "❯ do [Pasted text #1 +216 lines]") || recalled.expand(recalled.composer.Value()) != "do "+spec {
		t.Fatalf("recall lost the block:\n%s", recalled.View())
	}
	if lipgloss.Height(recalled.composerView()) != 1 {
		t.Fatal("recalled prompt is not compact")
	}
	submit(t, next)
	if len(*prompts) != 2 || (*prompts)[1] != "do "+spec {
		t.Fatal("resubmitted prompt lost its content")
	}
}

func TestScrollbackShowsCompactPrompt(t *testing.T) {
	m, _ := pasteShell()
	spec := lines(50)
	next, _ := m.Update(pasteMsg(spec))
	next, cmd := next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if live := sh(next).View(); !strings.Contains(live, "❯ [Pasted text #1 +50 lines]") || strings.Contains(live, "line 2 of the spec") {
		t.Fatalf("running turn shows the full paste:\n%s", live)
	}
	done, _ := sh(next).update(turnResult(t, cmd))
	printed := strings.Join(done.(shellModel).pending, "\n")
	if !strings.Contains(printed, "❯ [Pasted text #1 +50 lines]") || strings.Contains(printed, "line 2 of the spec") {
		t.Fatalf("scrollback not compact:\n%s", printed)
	}
}

func TestClearingComposerDropsPastedContent(t *testing.T) {
	m, _ := pasteShell()
	next, _ := m.Update(pasteMsg(lines(40)))
	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if got := sh(next); got.composer.Value() != "" || len(got.pastes) != 0 {
		t.Fatalf("clear kept %d blocks", len(got.pastes))
	}
}

func TestFailedOrCanceledTurnRestoresBlocks(t *testing.T) {
	for name, outcome := range map[string]turnDone{
		"failed":   {err: errors.New("provider down")},
		"canceled": {err: context.Canceled, canceled: true},
	} {
		m, _ := pasteShell()
		spec := lines(30)
		next := typeText(m, "fix: ")
		next, _ = next.Update(pasteMsg(spec))
		next, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
		next, _ = next.Update(outcome)
		got := sh(next)
		if got.busy || got.expand(got.composer.Value()) != "fix: "+spec || !strings.Contains(got.View(), "fix: [Pasted text #1 +30 lines]") {
			t.Errorf("%s turn did not restore the pasted block:\n%s", name, got.View())
		}
		if len(got.history) != 0 {
			t.Errorf("%s turn entered the model history", name)
		}
	}
}

func TestPasteNeverSubmitsRunsCommandsOrApproves(t *testing.T) {
	// Each case gets its own shell: textarea copies share their buffer.
	for _, text := range []string{"/exit\r", "\r", "/mode execute\n", "hello\r\n"} {
		m, prompts := pasteShell()
		next, cmd := m.Update(pasteMsg(text))
		if got := sh(next); got.busy || got.mode != tools.ReadOnly || quitsSoon(cmd) || len(*prompts) != 0 {
			t.Fatalf("paste %q acted as keys", text)
		}
	}
	// A large paste that starts like a command is still sent as a prompt.
	m, prompts := pasteShell()
	command := "/exit\n" + lines(10)
	next, _ := m.Update(pasteMsg(command))
	submit(t, next)
	if len(*prompts) != 1 || (*prompts)[0] != command {
		t.Fatal("pasted text was treated as a local command")
	}
	// Nothing pasted can answer an approval.
	a, ch := commandShell(t, "/usr/local/go/bin/go", "test", "./...")
	for _, text := range []string{"\r", "\x1b", "y\n", lines(10)} {
		next, _ := a.Update(pasteMsg(text))
		if sh(next).approval == nil {
			t.Fatalf("paste %q resolved the approval", text)
		}
	}
	select {
	case d := <-ch:
		t.Fatalf("paste decided the approval: %+v", d)
	default:
	}
}

func TestControlSequencesInPasteNeverReachTheTerminal(t *testing.T) {
	m, prompts := pasteShell()
	hostile := "ok\x1b[2J\x1b]0;title\x07\x03done"
	next, cmd := m.Update(pasteMsg(hostile))
	got := sh(next)
	if quitsSoon(cmd) || strings.ContainsAny(got.View(), "\x1b\x07\x03") || len(got.pastes) != 1 {
		t.Fatalf("control characters reached the composer view: %q", got.View())
	}
	submit(t, next)
	if (*prompts)[0] != hostile {
		t.Fatal("content not preserved exactly")
	}
}

func TestComposerHeightBoundedForLargePastes(t *testing.T) {
	m, _ := pasteShell()
	next, _ := m.Update(pasteMsg(lines(5000)))
	next, _ = next.Update(pasteMsg(strings.Repeat("z", 200000)))
	got := sh(next)
	if got.composer.Height() != 1 || lipgloss.Height(got.composerView()) > 2 || lipgloss.Height(got.View()) > 8 {
		t.Fatalf("composer grew: height %d, view %d lines", got.composer.Height(), lipgloss.Height(got.View()))
	}
}

func TestOversizedPasteIsRefusedExplicitly(t *testing.T) {
	m, _ := pasteShell()
	next, cmd := m.Update(pasteMsg(strings.Repeat("a", maxPasteBytes+1)))
	got := sh(next)
	if got.composer.Value() != "" || len(got.pastes) != 0 || cmd == nil {
		t.Fatal("oversized paste was inserted or silently dropped")
	}
	if done, _ := m.update(pasteMsg(strings.Repeat("a", maxPasteBytes+1))); !strings.Contains(done.(shellModel).commandNotice, "exceeds") {
		t.Fatal("refusal not reported")
	}
}

func TestSmallPasteThatWouldBeCutBecomesBlock(t *testing.T) {
	m, prompts := pasteShell()
	m.composer.SetValue(strings.Repeat("a", m.composer.CharLimit-10))
	text := "twenty characters!!!"
	next, _ := m.Update(pasteMsg(text))
	if len(sh(next).pastes) != 1 {
		t.Fatal("a paste the textarea would truncate was inserted as text")
	}
	submit(t, next)
	if !strings.HasSuffix((*prompts)[0], text) {
		t.Fatal("paste near the character limit was truncated")
	}
}

func TestPasteWorksInEveryMode(t *testing.T) {
	for _, mode := range []tools.Mode{tools.ReadOnly, tools.EditWithApproval, tools.ExecuteWithApproval} {
		m, prompts := pasteShell()
		m.mode = mode
		next, _ := m.Update(pasteMsg(lines(8)))
		submit(t, next)
		if (*prompts)[0] != lines(8) {
			t.Errorf("%s: paste not expanded", mode)
		}
	}
}

// quitsSoon is isQuit without blocking on the textarea's cursor-blink command.
func quitsSoon(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				if quitsSoon(c) {
					return true
				}
			}
		}
		_, ok := msg.(tea.QuitMsg)
		return ok
	case <-time.After(100 * time.Millisecond):
		return false
	}
}
