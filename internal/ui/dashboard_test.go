package ui

import (
	"strings"
	"testing"

	"github.com/vichanse/augustin/internal/events"
)

func TestDashboardView(t *testing.T) {
	m := dashboardModel{width: 80, provider: "openai", model: "test", prompt: "Analyze discount.go"}
	for _, e := range []events.Event{
		{Kind: events.AgentStarted},
		{Kind: events.ToolRequested, Tool: "read_file"},
		{Kind: events.ToolCompleted, Tool: "read_file"},
		{Kind: events.ModelCompleted, InputTokens: 10, OutputTokens: 5},
		{Kind: events.AgentCompleted},
	} {
		next, _ := m.Update(e)
		m = next.(dashboardModel)
	}
	for _, want := range []string{"A U G U S T I N", "READ ONLY", "Analyze discount.go", "Reading source code", "Task completed", "Tool calls", "15"} {
		if !strings.Contains(m.View(), want) {
			t.Errorf("missing %q", want)
		}
	}
}
