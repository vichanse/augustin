package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestShellLayout(t *testing.T) {
	m := shellModel{path: "/workspace", provider: "openai", model: "test", width: 80, height: 24}
	view := m.View()
	for _, want := range []string{"AUGUSTIN", "/workspace", "openai", "READ ONLY", "Prompt composer coming next"} {
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
