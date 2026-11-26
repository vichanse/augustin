package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/vichanse/augustin/internal/model"
)

func TestSensitiveWorkspacePaths(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{".env", ".ENV.local", "private.pem", "main.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".env", filepath.Join(dir, "alias")); err != nil {
		t.Fatal(err)
	}
	w, err := OpenWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	call := func(name, path string) (string, error) {
		args, err := json.Marshal(map[string]string{"path": path})
		if err != nil {
			t.Fatal(err)
		}
		return w.Handlers()[name](context.Background(), model.ToolCall{Name: name, Arguments: args})
	}
	for _, path := range []string{".env", ".ENV.local", "private.pem", ".git/config", "alias"} {
		if _, err := call("read_file", path); !errors.Is(err, errSensitivePath) {
			t.Errorf("path %q: expected policy denial, got %v", path, err)
		}
	}
	if _, err := call("list_files", ".git"); !errors.Is(err, errSensitivePath) {
		t.Fatalf("expected directory denial, got %v", err)
	}
	if out, err := call("list_files", "."); err != nil || out != `["main.go"]` {
		t.Fatalf("listing=%q error=%v", out, err)
	}
	if out, err := call("read_file", "main.go"); err != nil || out != "data" {
		t.Fatalf("read=%q error=%v", out, err)
	}
}
