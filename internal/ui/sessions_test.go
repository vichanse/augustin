package ui

import (
	"strings"
	"testing"

	"github.com/vichanse/augustin/internal/model"
)

func TestSessionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := savedSession{ID: strings.Repeat("a", 32), Name: "Test", History: []model.Message{
		{Role: model.RoleUser, Content: "hello"},
		{Role: model.RoleAssistant, Content: "world"},
	}}
	if err := writeSession(dir, s); err != nil {
		t.Fatal(err)
	}
	got, err := readSession(dir, s.ID)
	if err != nil || len(got.History) != 2 || got.History[1].Content != "world" {
		t.Fatalf("session round trip failed: %v", err)
	}
	if _, err := readSession(dir, "../other"); err == nil {
		t.Fatal("invalid ID accepted")
	}
}
