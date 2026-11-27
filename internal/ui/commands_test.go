package ui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vichanse/augustin/internal/approval"
	"github.com/vichanse/augustin/internal/events"
	"github.com/vichanse/augustin/internal/model"
	"github.com/vichanse/augustin/internal/tools"
)

func TestMatchingCommands(t *testing.T) {
	if got := matchingCommands("/"); len(got) != 15 {
		t.Fatalf("expected fifteen commands, got %d", len(got))
	}
	if got := matchingCommands("/mo"); len(got) != 2 || got[0].name != "/mode" || got[1].name != "/model" {
		t.Fatal("mode and model completions missing")
	}
	if got := matchingCommands("/model"); len(got) != 1 || got[0].name != "/model" {
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

func TestModeCommand(t *testing.T) {
	m := newShellModel(".", "openai", "test", false)
	if m.mode != tools.ReadOnly || !strings.Contains(m.View(), "READ ONLY") {
		t.Fatal("sessions must start read-only")
	}
	run := func(m shellModel, cmd string) shellModel {
		next, _ := m.runCommand(cmd)
		return next.(shellModel)
	}
	m = run(m, "/mode edit")
	if m.mode != tools.EditWithApproval || !strings.Contains(m.View(), "● EDIT") {
		t.Fatal("/mode edit not applied")
	}
	for cmd, want := range map[string]string{"/tools": "edit_file", "/permissions": "explicit approval for every change", "/status": "EDIT"} {
		if got := run(m, cmd).commandNotice; !strings.Contains(got, want) {
			t.Errorf("%s in edit mode: %q", cmd, got)
		}
	}
	m = run(m, "/mode read-only")
	if m.mode != tools.ReadOnly || strings.Contains(run(m, "/tools").commandNotice, "edit_file") || !strings.Contains(run(m, "/permissions").commandNotice, "Edit: denied") {
		t.Fatal("/mode read-only not applied")
	}
	if got := run(m, "/mode write"); got.mode != tools.ReadOnly || !strings.Contains(got.commandNotice, "Usage") {
		t.Fatal("unknown mode accepted")
	}
	busy := m
	busy.busy = true
	if got := run(busy, "/mode edit"); got.mode != tools.ReadOnly {
		t.Fatal("mode changed during a turn")
	}
	pending := m
	pending.approval = &approval.Request{ID: 1}
	if got := run(pending, "/mode edit"); got.mode != tools.ReadOnly {
		t.Fatal("mode changed during a pending approval")
	}
	if !strings.Contains(run(m, "/help").commandNotice, "/mode") {
		t.Fatal("/help does not list /mode")
	}
}

func TestTurnUsesModeSnapshot(t *testing.T) {
	m := newShellModel(".", "openai", "test", false)
	m.ctx = context.Background()
	var got tools.Mode
	m.turn = func(_ context.Context, _ []model.Message, _ string, mode tools.Mode, _ events.Sink) (string, error) {
		got = mode
		return "ok", nil
	}
	next, _ := m.runCommand("/mode edit")
	m = next.(shellModel)
	m.composer.SetValue("fix it")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	running := next.(shellModel)
	running.mode = tools.ReadOnly // even if the field changed mid-turn, the turn keeps its snapshot
	turnResult(t, cmd)
	if got != tools.EditWithApproval {
		t.Fatalf("turn ran with mode %q", got)
	}
}
