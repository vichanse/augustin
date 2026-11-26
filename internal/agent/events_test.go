package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/vichanse/augustin/internal/events"
	"github.com/vichanse/augustin/internal/model"
	"github.com/vichanse/augustin/internal/model/modeltest"
)

func TestExecutionEvents(t *testing.T) {
	var buf bytes.Buffer
	sink := events.NewJSONL(&buf)
	p := modeltest.New(
		modeltest.Step{Response: toolResponse("read")},
		modeltest.Step{Response: model.Response{
			FinishReason: model.FinishStop,
			Message:      model.Message{Role: model.RoleAssistant, Content: "secret-value"},
		}},
	)
	l := Loop{Provider: p, MaxIterations: 2, Events: sink, Executor: executorFunc(func(context.Context, model.ToolCall) (string, error) {
		return "secret-value", nil
	})}
	_, err := l.Run(context.Background(), model.Request{Tools: []model.ToolDefinition{{Name: "read"}}})
	if err != nil || sink.Err() != nil {
		t.Fatalf("run=%v sink=%v", err, sink.Err())
	}
	if strings.Contains(buf.String(), "secret-value") {
		t.Fatal("payload leaked into telemetry")
	}
	want := []events.Kind{events.AgentStarted, events.ModelRequested, events.ModelCompleted, events.ToolRequested, events.ToolCompleted, events.ModelRequested, events.ModelCompleted, events.AgentCompleted}
	dec := json.NewDecoder(&buf)
	for i, kind := range want {
		var e events.Event
		if err := dec.Decode(&e); err != nil || e.Kind != kind || e.Sequence != i+1 {
			t.Fatalf("event %d: %+v %v", i, e, err)
		}
	}
	if dec.More() {
		t.Fatal("unexpected extra event")
	}
}

func TestFailureEvents(t *testing.T) {
	var buf bytes.Buffer
	sink := events.NewJSONL(&buf)
	l := Loop{Provider: modeltest.New(modeltest.Step{Err: errors.New("secret-error")}), MaxIterations: 1, Events: sink}
	if _, err := l.Run(context.Background(), model.Request{}); err == nil {
		t.Fatal("expected failure")
	}
	if strings.Contains(buf.String(), "secret-error") {
		t.Fatal("raw error leaked")
	}
	if !strings.Contains(buf.String(), `"kind":"model.failed"`) || !strings.Contains(buf.String(), `"reason":"execution_error"`) {
		t.Fatalf("missing failure metadata: %s", buf.String())
	}
}
