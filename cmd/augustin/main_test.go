package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/vichanse/augustin/internal/approval"
	"github.com/vichanse/augustin/internal/model"
	"github.com/vichanse/augustin/internal/tools"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"--help"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "Augustin") {
		t.Fatalf("help: code=%d output=%q", code, out.String())
	}
	if code := run([]string{"unknown"}, &out, &errOut); code != 2 || errOut.Len() == 0 {
		t.Fatalf("invalid arguments: code=%d stderr=%q", code, errOut.String())
	}
}

func TestEditOnlyInInteractiveEditMode(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "discount.go"), []byte("a + b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := tools.OpenWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	broker := approval.New()
	broker.Attach(func(u approval.Update) {
		if u.Open {
			go broker.Resolve(u.Request.ID, true) // would approve anything it is asked
		}
	})
	defer broker.Close()
	call := model.ToolCall{Name: "edit_file", Arguments: json.RawMessage(`{"path":"discount.go","old_string":"+","new_string":"-"}`)}
	for name, exec := range map[string]*tools.Registry{
		"headless (no broker)":     executorFor(w, tools.EditWithApproval, nil),
		"interactive read-only":    executorFor(w, tools.ReadOnly, broker),
		"headless read-only (run)": executorFor(w, tools.ReadOnly, nil),
	} {
		if _, err := exec.Execute(context.Background(), call); !errors.Is(err, tools.ErrDenied) {
			t.Errorf("%s: edit not denied: %v", name, err)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "discount.go")); string(data) != "a + b\n" {
		t.Fatal("file modified outside edit mode")
	}
	if _, err := executorFor(w, tools.EditWithApproval, broker).Execute(context.Background(), call); err != nil {
		t.Fatalf("approved edit in edit mode: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "discount.go")); string(data) != "a - b\n" {
		t.Fatalf("edit mode did not apply the approved edit: %q", data)
	}
}

func TestToolsAndPromptFollowMode(t *testing.T) {
	names := func(req model.Request) string {
		var out []string
		for _, tool := range req.Tools {
			out = append(out, tool.Name)
		}
		return strings.Join(out, ",")
	}
	if got := names(codingRequest(nil, tools.ReadOnly)); strings.Contains(got, "edit_file") {
		t.Fatalf("read-only request exposes %s", got)
	}
	if got := names(codingRequest(nil, tools.EditWithApproval)); !strings.HasSuffix(got, ",edit_file") {
		t.Fatalf("edit request tools: %s", got)
	}
	readOnly, edit := systemMessage(tools.ReadOnly).Content, systemMessage(tools.EditWithApproval).Content
	if !strings.Contains(readOnly, "Do not claim to modify files") || strings.Contains(readOnly, "edit_file") {
		t.Fatal("read-only prompt mentions editing")
	}
	if !strings.Contains(edit, "edit_file") || !strings.Contains(edit, "tests were not run") {
		t.Fatal("edit prompt must explain approval and forbid claiming tests ran")
	}
}

func TestRunCommandOnlyInInteractiveExecuteMode(t *testing.T) {
	dir := t.TempDir()
	w, err := tools.OpenWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	broker := approval.New()
	asked := 0
	broker.Attach(func(u approval.Update) {
		if u.Open {
			asked++
			go broker.Resolve(u.Request.ID, false) // never actually run anything here
		}
	})
	defer broker.Close()
	call := model.ToolCall{Name: "run_command", Arguments: json.RawMessage(`{"command":"git","args":["status"]}`)}
	for name, exec := range map[string]*tools.Registry{
		"headless execute (no broker)": executorFor(w, tools.ExecuteWithApproval, nil),
		"headless read-only (run)":     executorFor(w, tools.ReadOnly, nil),
		"interactive read-only":        executorFor(w, tools.ReadOnly, broker),
		"interactive edit":             executorFor(w, tools.EditWithApproval, broker),
	} {
		if _, err := exec.Execute(context.Background(), call); !errors.Is(err, tools.ErrDenied) {
			t.Errorf("%s: run_command not denied: %v", name, err)
		}
	}
	if asked != 0 {
		t.Fatal("a denied mode asked for approval")
	}
	_, err = executorFor(w, tools.ExecuteWithApproval, broker).Execute(context.Background(), call)
	if !errors.Is(err, tools.ErrRejected) && !errors.Is(err, tools.ErrExecutableNotFound) {
		t.Fatalf("execute mode must reach approval (or report a missing git): %v", err)
	}
}

func TestRunCommandExposureFollowsMode(t *testing.T) {
	names := func(mode tools.Mode) string {
		var out []string
		for _, tool := range codingRequest(nil, mode).Tools {
			out = append(out, tool.Name)
		}
		return strings.Join(out, ",")
	}
	if strings.Contains(names(tools.ReadOnly), "run_command") || strings.Contains(names(tools.EditWithApproval), "run_command") {
		t.Fatal("run_command exposed outside execute mode")
	}
	if got := names(tools.ExecuteWithApproval); !strings.HasSuffix(got, ",edit_file,run_command") {
		t.Fatalf("execute tools: %s", got)
	}
	execute := systemMessage(tools.ExecuteWithApproval).Content
	if strings.Contains(execute, "You cannot run commands") || !strings.Contains(execute, "go test ./...") || !strings.Contains(execute, "exit_code 0") {
		t.Fatalf("execute prompt: %q", execute)
	}
	if strings.Contains(systemMessage(tools.EditWithApproval).Content, "run_command") {
		t.Fatal("edit prompt mentions run_command")
	}
}
