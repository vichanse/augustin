package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vichanse/augustin/internal/model"
)

func TestWorkspaceOverview(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "cmd"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cmd/main.go", ".env"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("private content"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	w, err := OpenWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	out, err := w.overview(context.Background(), model.ToolCall{Name: "workspace_overview", Arguments: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "main.go") || strings.Contains(out, ".env") || strings.Contains(out, "private content") {
		t.Fatalf("unexpected overview: %s", out)
	}
}
