package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestShellLayout(t *testing.T) {
	m := newShellModel("/workspace", "openai", "test", false)
	view := m.View()
	for _, want := range []string{"AUGUSTIN", "/workspace", "openai", "READ ONLY", "Ctrl+S submit preview"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q", want)
		}
	}
	if lipgloss.Height(view) != 24 {
		t.Fatalf("unexpected height: %d", lipgloss.Height(view))
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 20, Height: 8})
	if !strings.Contains(next.View(), "Resize terminal") {
		t.Fatal("missing small terminal fallback")
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
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	got := next.(shellModel)
	if got.submitted != "first line\nsecond line" || got.composer.Value() != "" {
		t.Fatal("multiline prompt was not captured and reset")
	}
}
