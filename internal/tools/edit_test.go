package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vichanse/augustin/internal/model"
)

const discountSource = "package pricing\n\nfunc CalculateDiscount(price, discount float64) float64 {\n\treturn price + discount\n}\n"

func editWorkspace(t *testing.T, files map[string]string) (string, *Workspace) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	w, err := OpenWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return dir, w
}

// approver records every proposal and answers with decide.
type approver struct {
	asked  []Proposal
	decide func(Proposal) (bool, error)
}

func (a *approver) approve(_ context.Context, p Proposal) (bool, error) {
	a.asked = append(a.asked, p)
	if a.decide == nil {
		return true, nil
	}
	return a.decide(p)
}

func editRegistry(w *Workspace, a *approver) *Registry {
	policy := EditPolicy()
	policy.Approve = a.approve
	return NewWithPreparers(w.Handlers(), w.Preparers(), EditTools, policy)
}

func editCall(path, old, new string) model.ToolCall {
	args, _ := json.Marshal(map[string]string{"path": path, "old_string": old, "new_string": new})
	return model.ToolCall{Name: "edit_file", Arguments: args}
}

func readBack(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func noTempFiles(t *testing.T, dir string) {
	t.Helper()
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(d.Name(), ".augustin-edit-") {
			t.Errorf("temporary file left behind: %s", path)
		}
		return nil
	})
}

func TestEditAppliesExactReplacementAfterApproval(t *testing.T) {
	dir, w := editWorkspace(t, map[string]string{"discount.go": discountSource})
	a := &approver{}
	out, err := editRegistry(w, a).Execute(context.Background(), editCall("discount.go", "return price + discount", "return price - discount"))
	if err != nil {
		t.Fatal(err)
	}
	if got := readBack(t, dir, "discount.go"); got != strings.Replace(discountSource, "+ discount", "- discount", 1) {
		t.Fatalf("unexpected content:\n%s", got)
	}
	if len(a.asked) != 1 || a.asked[0].Capability != Edit || a.asked[0].Path != "discount.go" {
		t.Fatalf("approval not asked once for the edit: %+v", a.asked)
	}
	wantDiff := "--- a/discount.go\n+++ b/discount.go\n@@ -1,5 +1,5 @@\n package pricing\n \n func CalculateDiscount(price, discount float64) float64 {\n-\treturn price + discount\n+\treturn price - discount\n }"
	if a.asked[0].Diff != wantDiff {
		t.Fatalf("approved diff:\n%s\nwant:\n%s", a.asked[0].Diff, wantDiff)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil || result["status"] != "applied" || result["removed_lines"] != 1.0 || result["added_lines"] != 1.0 {
		t.Fatalf("result %q: %v", out, err)
	}
	if !strings.Contains(out, "No tests or commands were run") {
		t.Fatal("result must not imply verification")
	}
	info, _ := os.Stat(filepath.Join(dir, "discount.go"))
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("permissions not preserved: %v", info.Mode().Perm())
	}
	noTempFiles(t, dir)
}

func TestEditRejectionLeavesFileUnchanged(t *testing.T) {
	dir, w := editWorkspace(t, map[string]string{"discount.go": discountSource})
	a := &approver{decide: func(Proposal) (bool, error) { return false, nil }}
	_, err := editRegistry(w, a).Execute(context.Background(), editCall("discount.go", "+ discount", "- discount"))
	if !errors.Is(err, ErrRejected) || errors.Is(err, ErrDenied) {
		t.Fatalf("rejection: %v", err)
	}
	if readBack(t, dir, "discount.go") != discountSource {
		t.Fatal("rejected edit modified the file")
	}
	noTempFiles(t, dir)
}

func TestEditPermittedBeforePreparation(t *testing.T) {
	prepared := 0
	counting := map[string]Preparer{"edit_file": func(context.Context, model.ToolCall) (Prepared, error) {
		prepared++
		return Prepared{Apply: func(context.Context) (string, error) { t.Fatal("applied"); return "", nil }}, nil
	}}
	a := &approver{}
	read := ReadPolicy()
	read.Approve = a.approve
	misconfigured := EditPolicy()
	misconfigured.Capabilities["edit_file"] = Read // a mutation declared as read must not skip approval
	misconfigured.Approve = a.approve
	for name, r := range map[string]*Registry{
		"read-only mode":        NewWithPreparers(nil, counting, EditTools, read),
		"no approver":           NewWithPreparers(nil, counting, EditTools, EditPolicy()),
		"not allowlisted":       NewWithPreparers(nil, counting, ReadTools, EditPolicy()),
		"no policy":             New(nil, EditTools),
		"mutation marked read":  NewWithPreparers(nil, counting, EditTools, misconfigured),
		"unknown capability":    NewWithPreparers(nil, counting, EditTools, Policy{Mode: EditWithApproval, Capabilities: map[string]Capability{"edit_file": "write"}, Approve: a.approve}),
		"edit mode, plan stage": NewWithPreparers(nil, counting, EditTools, Policy{Mode: Plan, Capabilities: map[string]Capability{"edit_file": Edit}, Approve: a.approve}),
	} {
		if _, err := r.Execute(context.Background(), editCall("discount.go", "a", "b")); !errors.Is(err, ErrDenied) {
			t.Errorf("%s: %v", name, err)
		}
	}
	r := New(nil, EditTools)
	r.preparers["edit_file"] = counting["edit_file"]
	if _, err := r.Execute(context.Background(), editCall("discount.go", "a", "b")); !errors.Is(err, ErrDenied) {
		t.Errorf("preparer without policy: %v", err)
	}
	if prepared != 0 || len(a.asked) != 0 {
		t.Fatalf("denied edits were prepared %d times and asked %d times", prepared, len(a.asked))
	}
}

func TestEditInvalidReplacements(t *testing.T) {
	src := "aaa\nx := 1\nx := 1\n"
	for _, tc := range []struct {
		name     string
		old, new string
		want     error
	}{
		{"missing", "y := 2", "y := 3", ErrNoMatch},
		{"ambiguous", "x := 1", "x := 2", ErrAmbiguousMatch},
		{"overlapping", "aa", "b", ErrAmbiguousMatch},
		{"no-op", "aaa", "aaa", ErrNoOp},
		{"empty old", "", "z", ErrInvalidArguments},
		{"NUL in result", "aaa", "a\x00a", ErrNotText},
	} {
		dir, w := editWorkspace(t, map[string]string{"f.go": src})
		a := &approver{}
		if _, err := editRegistry(w, a).Execute(context.Background(), editCall("f.go", tc.old, tc.new)); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
		if len(a.asked) != 0 || readBack(t, dir, "f.go") != src {
			t.Errorf("%s: invalid edit was proposed or applied", tc.name)
		}
	}
}

func TestEditEmptyReplacementDeletes(t *testing.T) {
	dir, w := editWorkspace(t, map[string]string{"f.go": "keep\ndrop me\nkeep too\n"})
	a := &approver{}
	if _, err := editRegistry(w, a).Execute(context.Background(), editCall("f.go", "drop me\n", "")); err != nil {
		t.Fatal(err)
	}
	if got := readBack(t, dir, "f.go"); got != "keep\nkeep too\n" {
		t.Fatalf("deletion: %q", got)
	}
	if want := "--- a/f.go\n+++ b/f.go\n@@ -1,3 +1,2 @@\n keep\n-drop me\n keep too"; a.asked[0].Diff != want {
		t.Fatalf("deletion diff:\n%s", a.asked[0].Diff)
	}
}

func TestEditStrictArguments(t *testing.T) {
	_, w := editWorkspace(t, map[string]string{"f.go": "a\n"})
	for _, args := range []string{
		`{"path":"f.go","old_string":"a"}`,
		`{"path":"f.go","old_string":"a","new_string":"b","extra":1}`,
		`{"path":"f.go","old_string":"a","new_string":"b","path":"g.go"}`,
		`{"path":"f.go","old_string":"a","new_string":null}`,
		`{"path":"f.go","old_string":"a","new_string":1}`,
		`{"path":"f.go","old_string":["a"],"new_string":"b"}`,
		`{"path":"f.go","old_string":"a","new_string":"b"} {}`,
		`["f.go","a","b"]`,
		`{"Path":"f.go","old_string":"a","new_string":"b"}`,
	} {
		a := &approver{}
		_, err := editRegistry(w, a).Execute(context.Background(), model.ToolCall{Name: "edit_file", Arguments: json.RawMessage(args)})
		if !errors.Is(err, ErrInvalidArguments) || len(a.asked) != 0 {
			t.Errorf("%s: %v", args, err)
		}
	}
}

func TestEditPathProtections(t *testing.T) {
	dir, w := editWorkspace(t, map[string]string{
		"real/f.go": "a\n", ".env": "a\n", ".git/config": "a\n", "bin.dat": "a\x00\n", "latin1.txt": "a\xe9\n",
	})
	if err := os.Symlink("real/f.go", filepath.Join(dir, "link.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(dir, "linkdir")); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]error{
		"../outside.go":     ErrInvalidArguments,
		"real/../../x.go":   ErrInvalidArguments,
		"real/../real/f.go": ErrDenied, // lexically local, but traversal is refused anyway
		"/etc/hosts":        ErrInvalidArguments,
		"link.go":           ErrSensitivePath, // symlink target
		"linkdir/f.go":      ErrSensitivePath, // symlink component
		".env":              ErrSensitivePath,
		".git/config":       ErrSensitivePath,
		"real":              ErrRegularFileRequired,
		"bin.dat":           ErrNotText,
		"latin1.txt":        ErrNotText,
		"missing.go":        os.ErrNotExist,
	} {
		a := &approver{}
		if _, err := editRegistry(w, a).Execute(context.Background(), editCall(path, "a", "b")); !errors.Is(err, want) || len(a.asked) != 0 {
			t.Errorf("%s: got %v, want %v (asked %d)", path, err, want, len(a.asked))
		}
	}
	if readBack(t, dir, "real/f.go") != "a\n" {
		t.Fatal("protected target modified")
	}
}

func TestEditRejectsFileChangedWhileWaiting(t *testing.T) {
	for name, change := range map[string]func(dir string) error{
		"content changed": func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "discount.go"), []byte(discountSource+"// concurrent\n"), 0o640)
		},
		"replaced by a new file with identical content": func(dir string) error {
			path := filepath.Join(dir, "discount.go")
			if err := os.Remove(path); err != nil {
				return err
			}
			return os.WriteFile(path, []byte(discountSource), 0o640)
		},
		"deleted": func(dir string) error { return os.Remove(filepath.Join(dir, "discount.go")) },
	} {
		dir, w := editWorkspace(t, map[string]string{"discount.go": discountSource})
		var before string
		a := &approver{decide: func(Proposal) (bool, error) {
			if err := change(dir); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(filepath.Join(dir, "discount.go")) // empty when deleted
			before = string(data)
			return true, nil
		}}
		_, err := editRegistry(w, a).Execute(context.Background(), editCall("discount.go", "+ discount", "- discount"))
		if !errors.Is(err, ErrFileChanged) {
			t.Errorf("%s: %v", name, err)
		}
		if after, _ := os.ReadFile(filepath.Join(dir, "discount.go")); string(after) != before {
			t.Errorf("%s: concurrent change overwritten", name)
		}
		noTempFiles(t, dir)
	}
}

func TestEditCanceledBeforeApplication(t *testing.T) {
	dir, w := editWorkspace(t, map[string]string{"discount.go": discountSource})
	ctx, cancel := context.WithCancel(context.Background())
	a := &approver{decide: func(Proposal) (bool, error) {
		cancel() // e.g. Ctrl+C right as the user approves
		return true, nil
	}}
	if _, err := editRegistry(w, a).Execute(ctx, editCall("discount.go", "+ discount", "- discount")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if readBack(t, dir, "discount.go") != discountSource {
		t.Fatal("canceled edit modified the file")
	}
}

func TestEditPreservesUnrelatedContent(t *testing.T) {
	src := "line 1\r\n\tindented\u00a0é 🚀\n" + strings.Repeat("filler\n", 50) + "target here\n" + strings.Repeat("tail\n", 50) + "no newline"
	dir, w := editWorkspace(t, map[string]string{"f.txt": src})
	if err := os.Chmod(filepath.Join(dir, "f.txt"), 0o751); err != nil {
		t.Fatal(err)
	}
	a := &approver{}
	if _, err := editRegistry(w, a).Execute(context.Background(), editCall("f.txt", "target", "TARGET")); err != nil {
		t.Fatal(err)
	}
	if got := readBack(t, dir, "f.txt"); got != strings.Replace(src, "target here", "TARGET here", 1) {
		t.Fatal("content outside the replacement changed")
	}
	if info, _ := os.Stat(filepath.Join(dir, "f.txt")); info.Mode().Perm() != 0o751 {
		t.Fatalf("permissions: %v", info.Mode().Perm())
	}
}

func TestEditDiffEdgeCases(t *testing.T) {
	for _, tc := range []struct {
		name, src, old, new, want string
	}{
		{"no trailing newline", "a\nb", "b", "c", "--- a/f\n+++ b/f\n@@ -1,2 +1,2 @@\n a\n-b\n\\ No newline at end of file\n+c\n\\ No newline at end of file"},
		{"add trailing newline", "a", "a", "a\n", "--- a/f\n+++ b/f\n@@ -1,1 +1,1 @@\n-a\n\\ No newline at end of file\n+a"},
		{"insert line", "a\nb\n", "a\n", "a\nnew\n", "--- a/f\n+++ b/f\n@@ -1,2 +1,3 @@\n-a\n+a\n+new\n b"},
		{"delete only line", "x\n", "x\n", "", "--- a/f\n+++ b/f\n@@ -1,1 +0,0 @@\n-x"},
		{"context is bounded", "1\n2\n3\n4\n5\nX\n6\n7\n8\n9\n", "X", "Y", "--- a/f\n+++ b/f\n@@ -3,7 +3,7 @@\n 3\n 4\n 5\n-X\n+Y\n 6\n 7\n 8"},
		{"multi-line replacement", "a\nb\nc\nd\n", "b\nc", "B", "--- a/f\n+++ b/f\n@@ -1,4 +1,3 @@\n a\n-b\n-c\n+B\n d"},
	} {
		at := strings.Index(tc.src, tc.old)
		updated := tc.src[:at] + tc.new + tc.src[at+len(tc.old):]
		if got, _, _ := unifiedDiff("f", tc.src, at, len(tc.old), updated); got != tc.want {
			t.Errorf("%s:\n%s\nwant:\n%s", tc.name, got, tc.want)
		}
	}
}

func TestEditLargeDiffRejectedBeforeApproval(t *testing.T) {
	src := strings.Repeat("x", 3000) + "\n"
	dir, w := editWorkspace(t, map[string]string{"f.txt": src})
	a := &approver{}
	if _, err := editRegistry(w, a).Execute(context.Background(), editCall("f.txt", strings.Repeat("x", 3000), strings.Repeat("y", 3000))); !errors.Is(err, ErrDiffTooLarge) {
		t.Fatalf("large diff: %v", err)
	}
	if len(a.asked) != 0 || readBack(t, dir, "f.txt") != src {
		t.Fatal("unreviewable diff was proposed or applied")
	}
}

func TestEditFailedReplacementKeepsOriginal(t *testing.T) {
	dir, w := editWorkspace(t, map[string]string{"locked/f.go": "a\n"})
	locked := filepath.Join(dir, "locked")
	if err := os.Chmod(locked, 0o500); err != nil { // no temp file can be created next to the target
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o700) })
	if _, err := editRegistry(w, &approver{}).Execute(context.Background(), editCall("locked/f.go", "a", "b")); err == nil {
		t.Fatal("write into a read-only directory succeeded")
	}
	if readBack(t, dir, "locked/f.go") != "a\n" {
		t.Fatal("failed replacement damaged the original")
	}
	noTempFiles(t, dir)
}
