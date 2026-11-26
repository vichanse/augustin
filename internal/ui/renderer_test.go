package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vichanse/augustin/internal/events"
)

func TestRenderer(t *testing.T) {
	var out bytes.Buffer
	r := New(&out)
	r.Emit(events.Event{Kind: events.AgentStarted})
	r.Emit(events.Event{Kind: events.ModelCompleted, InputTokens: 10, OutputTokens: 5})
	r.Emit(events.Event{Kind: events.ToolRequested, Tool: "read_file"})
	r.Emit(events.Event{Kind: events.ToolCompleted, Tool: "read_file"})
	r.Emit(events.Event{Kind: events.AgentCompleted, Reason: events.Success, Iteration: 2})
	for _, want := range []string{"A U G U S T I N", "[OK] Reading source code  (read_file)", "2 iterations", "1 tools", "10 input / 5 output tokens"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in %q", want, out.String())
		}
	}
	if r.Err() != nil {
		t.Fatal(r.Err())
	}
}
