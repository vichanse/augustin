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

func TestWorkspace(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	w, err := OpenWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	r := New(w.Handlers(), []string{"read_file", "list_files"})
	call := func(name, path string) (string, error) {
		args, _ := json.Marshal(map[string]string{"path": path})
		return r.Execute(context.Background(), model.ToolCall{Name: name, Arguments: args})
	}
	if got, err := call("read_file", "hello.txt"); err != nil || got != "hello" {
		t.Fatalf("read: %q %v", got, err)
	}
	if got, err := call("list_files", "."); err != nil || got != `["hello.txt"]` {
		t.Fatalf("list: %q %v", got, err)
	}
	for _, path := range []string{"../hello.txt", "sub/../hello.txt", filepath.Join(dir, "hello.txt")} {
		if _, err := call("read_file", path); err == nil {
			t.Fatalf("accepted path %q", path)
		}
	}
	if _, err := call("run_command", "."); !errors.Is(err, ErrDenied) {
		t.Fatalf("command: %v", err)
	}
	if _, err := New(w.Handlers(), nil).Execute(context.Background(), model.ToolCall{Name: "read_file"}); !errors.Is(err, ErrDenied) {
		t.Fatalf("default policy: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := call("read_file", "escape"); err == nil {
		t.Fatal("external symlink accepted")
	}
}

func TestListFilesRootArguments(t *testing.T) {
	w, err := OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	r := New(w.Handlers(), []string{"read_file", "list_files"})
	for _, args := range []string{`{}`, `{"path":""}`, `{"path":"."}`} {
		got, err := r.Execute(context.Background(), model.ToolCall{Name: "list_files", Arguments: json.RawMessage(args)})
		if err != nil || got != "[]" {
			t.Fatalf("list %s: %q %v", args, got, err)
		}
	}
	for _, args := range []string{`null`, `{"path":null}`, `{"path":"/"}`, `{"path":".."}`, `{"unknown":true}`} {
		if _, err := r.Execute(context.Background(), model.ToolCall{Name: "list_files", Arguments: json.RawMessage(args)}); err == nil {
			t.Fatalf("accepted invalid arguments %s", args)
		}
	}
	if _, err := r.Execute(context.Background(), model.ToolCall{Name: "read_file", Arguments: json.RawMessage(`{}`)}); !errors.Is(err, ErrInvalidArguments) {
		t.Fatalf("read without path: %v", err)
	}
}
