package ui

import (
	"testing"

	"github.com/vichanse/augustin/internal/model"
)

func TestMatchingCommands(t *testing.T) {
	if got := matchingCommands("/"); len(got) != 7 {
		t.Fatalf("expected seven commands, got %d", len(got))
	}
	if got := matchingCommands("/mo"); len(got) != 1 || got[0].name != "/model" {
		t.Fatal("model completion missing")
	}
	if got := matchingCommands("explain /model"); len(got) != 0 {
		t.Fatal("ordinary prompt matched")
	}
}

func TestClearCommand(t *testing.T) {
	m := newShellModel(".", "openai", "test", false)
	m.history = []model.Message{{Role: model.RoleUser, Content: "hello"}}
	m.partial = "unfinished"
	m.tokens = 42
	next, cmd := m.runCommand("/clear")
	got := next.(shellModel)
	if cmd != nil || len(got.history) != 0 || got.partial != "" || got.tokens != 0 {
		t.Fatal("conversation not cleared")
	}
}
