package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vichanse/augustin/internal/config"
	"github.com/vichanse/augustin/internal/model"
	"github.com/vichanse/augustin/internal/model/modeltest"
)

func TestReadOnlyRun(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"AUGUSTIN_PROVIDER":          "openai",
		"AUGUSTIN_MODEL":             "test",
		"AUGUSTIN_ALLOW_REMOTE_DATA": "true",
		"OPENAI_API_KEY":             "fake",
	}
	lookup := func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	}
	calls := 0
	factory := func(config.Lookup) (model.Provider, error) {
		calls++
		return modeltest.New(
			modeltest.Step{Response: model.Response{FinishReason: model.FinishToolCalls, Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "1", Name: "read_file", Arguments: json.RawMessage(`{"path":"main.go"}`)}}}}},
			modeltest.Step{Response: model.Response{FinishReason: model.FinishStop, Message: model.Message{Role: model.RoleAssistant, Content: "done\x1b\x07"}}},
		), nil
	}
	var out, stderr bytes.Buffer
	args := []string{"run", "--json", "--workspace", dir, "read fixture"}
	if code := runWith(context.Background(), args, &out, &stderr, lookup, factory); code != 0 || out.String() != "done\n" || !strings.Contains(stderr.String(), "tool.completed") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), stderr.String())
	}
	out.Reset()
	stderr.Reset()
	args = []string{"run", "--workspace", dir, "read fixture"}
	if code := runWith(context.Background(), args, &out, &stderr, lookup, factory); code != 0 || out.String() != "done\n" || !strings.Contains(stderr.String(), "tool.completed") {
		t.Fatalf("redirected output: code=%d stdout=%q stderr=%q", code, out.String(), stderr.String())
	}
	delete(env, "AUGUSTIN_ALLOW_REMOTE_DATA")
	if code := runWith(context.Background(), args, &out, &stderr, lookup, factory); code != 2 || calls != 2 {
		t.Fatalf("consent not enforced: code=%d factory calls=%d", code, calls)
	}
}
