package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTrustRequiresExplicitSelection(t *testing.T) {
	m := trustModel{path: "/workspace"}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if next.(trustModel).accepted {
		t.Fatal("default selection granted trust")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	selected := next.(trustModel)
	next, _ = selected.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !next.(trustModel).accepted {
		t.Fatal("explicit confirmation did not grant trust")
	}
	next, _ = selected.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if next.(trustModel).accepted {
		t.Fatal("escape granted trust")
	}
}
