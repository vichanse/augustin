package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vichanse/augustin/internal/events"
	"github.com/vichanse/augustin/internal/model"
)

func TestDashboardSharesShellLiveArea(t *testing.T) {
	shell := newShellModel("/workspace", "openai", "test", false)
	shell.busy, shell.submitted, shell.status = true, "Analyze discount.go", "Working…"
	var m tea.Model = dashboardModel{shell: shell}
	for _, msg := range []tea.Msg{
		liveEvent(events.Event{Kind: events.AgentStarted}),
		liveEvent(events.Event{Kind: events.ToolRequested, Tool: "read_file"}),
		liveToolCall(model.ToolCall{Name: "read_file", Arguments: []byte(`{"path":"discount.go"}`)}),
		liveEvent(events.Event{Kind: events.ToolCompleted, Tool: "read_file"}),
		liveEvent(events.Event{Kind: events.ModelCompleted, InputTokens: 10, OutputTokens: 5}),
		liveEvent(events.Event{Kind: events.AgentCompleted}),
	} {
		m, _ = m.Update(msg)
	}
	view := m.View()
	for _, want := range []string{"❯ Analyze discount.go", "✓ Read(discount.go)", "1 tools · 15 tokens", "✓ Task completed"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q in:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Enter send") {
		t.Fatal("run mode must not show the composer")
	}
	if !strings.Contains(m.(dashboardModel).shell.banner(), "AUGUSTIN") {
		t.Fatal("run mode banner missing")
	}
	failed, _ := dashboardModel{shell: shell}.Update(liveEvent(events.Event{Kind: events.AgentFailed, Reason: events.BudgetExceeded}))
	if !strings.Contains(failed.View(), "Task failed (budget_exceeded)") {
		t.Fatal("failure reason missing")
	}
}
