package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vichanse/augustin/internal/model"
)

func TestCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("unexpected HTTP request")
		}
		var body struct {
			Model    string    `json:"model"`
			Messages []message `json:"messages"`
			Tools    []tool    `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if body.Model != "test-model" || len(body.Messages) != 3 || len(body.Tools) != 1 {
			t.Error("unexpected request payload")
		} else if body.Messages[1].ToolCalls[0].Function.Arguments != `{"path":"main.go"}` || body.Messages[2].ToolCallID != "previous" {
			t.Error("invalid tool round trip")
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"next","type":"function","function":{"name":"read_file","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":12,"completion_tokens":5}}`)
	}))
	defer server.Close()
	p, err := New("test-key", "test-model")
	if err != nil {
		t.Fatal(err)
	}
	p.url = server.URL
	r, err := p.Complete(context.Background(), model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "read"},
			{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "previous", Name: "read_file", Arguments: json.RawMessage(`{"path":"main.go"}`)}}},
			{Role: model.RoleTool, ToolCallID: "previous", Content: "data"},
		},
		Tools: []model.ToolDefinition{{Name: "read_file", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil || r.FinishReason != model.FinishToolCalls || len(r.Message.ToolCalls) != 1 || r.Usage.InputTokens != 12 || r.Usage.OutputTokens != 5 {
		t.Fatalf("response=%+v error=%v", r, err)
	}
}

func TestHTTPErrorRedacted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, "secret payload")
	}))
	defer server.Close()
	p, err := New("test-key", "test-model")
	if err != nil {
		t.Fatal(err)
	}
	p.url = server.URL
	_, err = p.Complete(context.Background(), model.Request{})
	if err == nil || !strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unexpected error: %v", err)
	}
}
