package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/vichanse/augustin/internal/model"
)

// Recoverable edit errors: the model can read again or propose another edit.
var (
	ErrNoMatch        = errors.New("old_string not found")
	ErrAmbiguousMatch = errors.New("old_string occurs more than once")
	ErrNoOp           = errors.New("edit changes nothing")
	ErrNotText        = errors.New("file is not UTF-8 text")
	ErrDiffTooLarge   = errors.New("diff too large to review")
	ErrFileChanged    = errors.New("file changed before the edit was applied")
)

// maxDiffBytes keeps every proposed diff reviewable in one approval panel.
const maxDiffBytes = 4096

const diffContext = 3

// Preparers returns the workspace mutations. They are only reachable through
// a Registry whose policy permits Edit and asks for approval.
func (w *Workspace) Preparers() map[string]Preparer {
	return map[string]Preparer{"edit_file": w.prepareEdit}
}

type editArgs struct {
	path, old, new string
}

// parseEditArgs requires exactly path, old_string and new_string as strings,
// rejecting unknown, duplicate and null fields.
func parseEditArgs(raw []byte) (editArgs, error) {
	var a editArgs
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return a, ErrInvalidArguments
	}
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		key, ok := tok.(string)
		if err != nil || !ok || seen[key] {
			return a, ErrInvalidArguments
		}
		seen[key] = true
		tok, err = dec.Token()
		value, ok := tok.(string)
		if err != nil || !ok {
			return a, ErrInvalidArguments
		}
		switch key {
		case "path":
			a.path = value
		case "old_string":
			a.old = value
		case "new_string":
			a.new = value
		default:
			return a, ErrInvalidArguments
		}
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') || len(seen) != 3 {
		return a, ErrInvalidArguments
	}
	if _, err := dec.Token(); err != io.EOF {
		return a, ErrInvalidArguments // trailing data
	}
	return a, nil
}

func isText(data []byte) bool {
	return utf8.Valid(data) && bytes.IndexByte(data, 0) < 0
}

func (w *Workspace) prepareEdit(ctx context.Context, call model.ToolCall) (Prepared, error) {
	args, err := parseEditArgs(call.Arguments)
	if err != nil {
		return Prepared{}, err
	}
	path, err := localPath(args.path)
	if err != nil {
		return Prepared{}, err
	}
	if args.old == "" {
		return Prepared{}, ErrInvalidArguments
	}
	if args.old == args.new {
		return Prepared{}, ErrNoOp
	}
	data, info, err := w.readRegular(ctx, path)
	if err != nil {
		return Prepared{}, err
	}
	if !isText(data) {
		return Prepared{}, ErrNotText
	}
	original := string(data)
	at := strings.Index(original, args.old)
	switch {
	case at < 0:
		return Prepared{}, ErrNoMatch
	case strings.Contains(original[at+1:], args.old):
		return Prepared{}, ErrAmbiguousMatch // overlapping matches count too
	}
	updated := original[:at] + args.new + original[at+len(args.old):]
	if len(updated) > maxFileBytes {
		return Prepared{}, ErrFileTooLarge
	}
	if !isText([]byte(updated)) {
		return Prepared{}, ErrNotText
	}
	diff, removed, added := unifiedDiff(filepath.ToSlash(path), original, at, len(args.old), updated)
	if len(diff) > maxDiffBytes {
		return Prepared{}, ErrDiffTooLarge
	}
	sum := sha256.Sum256(data)
	return Prepared{Path: filepath.ToSlash(path), Diff: diff, Apply: func(ctx context.Context) (string, error) {
		return w.applyEdit(ctx, path, info, sum, updated, removed, added)
	}}, nil
}

// applyEdit writes exactly the prepared content if the file still holds the
// previewed bytes. The hash is the guarantee; the identity check is best effort
// because a filesystem may reuse the inode of a deleted file. Neither closes
// the window for a concurrent writer between the check and the rename.
func (w *Workspace) applyEdit(ctx context.Context, path string, original fs.FileInfo, sum [32]byte, content string, removed, added int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	current, info, err := w.readRegular(ctx, path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", ErrFileChanged
	}
	if err != nil {
		return "", err
	}
	if !os.SameFile(original, info) || sha256.Sum256(current) != sum {
		return "", ErrFileChanged
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	tmp := filepath.Join(filepath.Dir(path), ".augustin-edit-"+hex.EncodeToString(suffix[:])+".tmp")
	f, err := w.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	done := false
	defer func() {
		if !done {
			w.root.Remove(tmp) // never leave a partial replacement behind
		}
	}()
	_, err = f.WriteString(content)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = w.root.Chmod(tmp, original.Mode().Perm())
	}
	if err == nil {
		err = ctx.Err() // last cancellation point before the replacement
	}
	if err == nil {
		err = w.root.Rename(tmp, path)
	}
	if err != nil {
		return "", err
	}
	done = true
	result, err := json.Marshal(map[string]any{
		"status": "applied", "path": filepath.ToSlash(path), "removed_lines": removed, "added_lines": added,
		"note": "The file was modified as approved. No tests or commands were run.",
	})
	return string(result), err
}

// unifiedDiff renders the single hunk changed by replacing original[at:at+n].
// Prefix and suffix outside the touched lines are identical in both versions.
// It also returns the number of removed and added lines.
func unifiedDiff(path, original string, at, n int, updated string) (string, int, int) {
	start := strings.LastIndexByte(original[:at], '\n') + 1
	end := len(original)
	if i := strings.IndexByte(original[at+n-1:], '\n'); i >= 0 {
		end = at + n + i // just past the newline ending the last touched line
	}
	suffix := original[end:]
	before := splitLines(original[:start])
	after := splitLines(suffix)
	removed := splitLines(original[start:end])
	added := splitLines(updated[start : len(updated)-len(suffix)])
	ctxBefore := before[max(0, len(before)-diffContext):]
	ctxAfter := after[:min(len(after), diffContext)]

	oldCount := len(ctxBefore) + len(removed) + len(ctxAfter)
	newCount := len(ctxBefore) + len(added) + len(ctxAfter)
	oldStart := len(before) - len(ctxBefore) + 1
	newStart := oldStart
	if oldCount == 0 {
		oldStart--
	}
	if newCount == 0 {
		newStart--
	}
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n@@ -%d,%d +%d,%d @@\n", path, path, oldStart, oldCount, newStart, newCount)
	emit := func(prefix string, lines []line) {
		for _, l := range lines {
			b.WriteString(prefix + l.text + "\n")
			if !l.newline {
				b.WriteString("\\ No newline at end of file\n")
			}
		}
	}
	emit(" ", ctxBefore)
	emit("-", removed)
	emit("+", added)
	emit(" ", ctxAfter)
	return strings.TrimSuffix(b.String(), "\n"), len(removed), len(added)
}

type line struct {
	text    string
	newline bool
}

func splitLines(s string) []line {
	var lines []line
	for s != "" {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			return append(lines, line{text: s})
		}
		lines = append(lines, line{text: s[:i], newline: true})
		s = s[i+1:]
	}
	return lines
}
