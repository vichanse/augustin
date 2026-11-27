package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/vichanse/augustin/internal/events"
	"github.com/vichanse/augustin/internal/model"
	"github.com/vichanse/augustin/internal/tools"
)

// runTool feeds one tool's events, plus optional registry evidence, the way
// the loop and the registry send them, and returns the activity line.
func runTool(m shellModel, name, args string, evidence *tools.Evidence, end events.Event) string {
	m.busy = true
	next := m
	steps := []any{
		liveEvent(events.Event{Kind: events.ToolRequested, Tool: name}),
		liveToolCall(model.ToolCall{Name: name, Arguments: []byte(args)}),
	}
	if evidence != nil {
		steps = append(steps, liveEvidence(*evidence))
	}
	steps = append(steps, liveEvent(end))
	for _, msg := range steps {
		updated, _ := next.Update(msg)
		next = updated.(shellModel)
	}
	return next.activity[len(next.activity)-1]
}

func goTestEvidence(status string, exit int) *tools.Evidence {
	return &tools.Evidence{Kind: tools.EvidenceApplied, Capability: tools.Execute, Request: []string{"go", "test", "./..."}, Status: status, ExitCode: exit}
}

func TestActivityMarksFollowRealOutcomes(t *testing.T) {
	completed := func(tool string) events.Event { return events.Event{Kind: events.ToolCompleted, Tool: tool} }
	failed := func(tool string, reason events.Reason) events.Event {
		return events.Event{Kind: events.ToolFailed, Tool: tool, Reason: reason}
	}
	run := `{"command":"go","args":["test","./..."]}`
	m := newShellModel("/workspace", "openai", "test", false)
	for name, tc := range map[string]struct {
		got, want string
	}{
		"read ok":          {runTool(m, "read_file", `{"path":"a.go"}`, nil, completed("read_file")), "✓ Read(a.go)"},
		"tests pass":       {runTool(m, "run_command", run, goTestEvidence("exited", 0), completed("run_command")), "✓ Run(go test ./...)"},
		"tests fail":       {runTool(m, "run_command", run, goTestEvidence("exited", 1), completed("run_command")), "✗ Run(go test ./...) (exit 1)"},
		"tests time out":   {runTool(m, "run_command", run, goTestEvidence("timeout", -1), completed("run_command")), "! Run(go test ./...) (timed out)"},
		"execution error":  {runTool(m, "run_command", run, goTestEvidence("error", 0), completed("run_command")), "! Run(go test ./...) (error)"},
		"rejected":         {runTool(m, "run_command", run, nil, failed("run_command", "user_rejected")), "! Run(go test ./...) (user_rejected)"},
		"canceled":         {runTool(m, "run_command", run, nil, failed("run_command", events.Canceled)), "! Run(go test ./...) (canceled)"},
		"tool failure":     {runTool(m, "edit_file", `{"path":"a.go"}`, nil, failed("edit_file", "old_string_not_found")), "✗ Edit(a.go) (old_string_not_found)"},
		"permission error": {runTool(m, "read_file", `{"path":".env"}`, nil, failed("read_file", "permission_denied")), "✗ Read(.env) (permission_denied)"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s: got %q, want %q", name, tc.got, tc.want)
		}
	}
}

func TestActivityMarksAreColoredButLabelsAreNot(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	m := newShellModel("/workspace", "openai", "test", true)
	run := `{"command":"go","args":["test","./..."]}`
	for name, tc := range map[string]struct {
		line  string
		color string
		glyph string
	}{
		"green pass":   {runTool(m, "run_command", run, goTestEvidence("exited", 0), events.Event{Kind: events.ToolCompleted, Tool: "run_command"}), "78", "✓"},
		"red failure":  {runTool(m, "run_command", run, goTestEvidence("exited", 2), events.Event{Kind: events.ToolCompleted, Tool: "run_command"}), "203", "✗"},
		"yellow abort": {runTool(m, "run_command", run, nil, events.Event{Kind: events.ToolFailed, Tool: "run_command", Reason: "user_rejected"}), "214", "!"},
	} {
		want := m.paint(tc.glyph, tc.color) + " Run(go test ./...)"
		if !strings.HasPrefix(tc.line, want) || !strings.Contains(tc.line, "\x1b[") {
			t.Errorf("%s: %q does not start with the colored glyph and a plain label", name, tc.line)
		}
	}
	m.busy = true
	running, _ := m.Update(liveEvent(events.Event{Kind: events.ToolRequested, Tool: "read_file"}))
	if line := running.(shellModel).activity[0]; line != m.paint("◌", "245")+" read_file" {
		t.Errorf("running line not gray: %q", line)
	}
}

func TestActivityMarksRespectNoColor(t *testing.T) {
	m := newShellModel("/workspace", "openai", "test", false) // what ColorEnabled returns under NO_COLOR
	line := runTool(m, "run_command", `{"command":"go","args":["test","./..."]}`, goTestEvidence("exited", 1), events.Event{Kind: events.ToolCompleted, Tool: "run_command"})
	if strings.Contains(line, "\x1b") || line != "✗ Run(go test ./...) (exit 1)" {
		t.Fatalf("plain mode line: %q", line)
	}
}
