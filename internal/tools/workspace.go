package tools

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vichanse/augustin/internal/model"
)

var ErrDirectoryRequired = errors.New("directory required")
var ErrRegularFileRequired = errors.New("regular file required")
var ErrFileTooLarge = errors.New("file exceeds size limit")
var ErrDirectoryEntryLimit = errors.New("directory exceeds entry limit")

const maxFileBytes = 1 << 20

type Workspace struct {
	root *os.Root
	dir  string // canonical absolute path: command working directory
}

func OpenWorkspace(path string) (*Workspace, error) {
	dir, err := filepath.Abs(path)
	if err == nil {
		dir, err = filepath.EvalSymlinks(dir)
	}
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &Workspace{root: root, dir: dir}, nil
}

func (w *Workspace) Close() error {
	return w.root.Close()
}

func (w *Workspace) Handlers() map[string]Handler {
	return map[string]Handler{
		"read_file":          w.readFile,
		"list_files":         w.listFiles,
		"workspace_overview": w.overview,
	}
}

func pathArgument(call model.ToolCall) (string, error) {
	var args map[string]json.RawMessage
	if err := json.Unmarshal(call.Arguments, &args); err != nil || args == nil {
		return "", ErrInvalidArguments
	}
	var path string
	for key, value := range args {
		if key == "detailed" && call.Name == "list_files" {
			var detailed bool
			if string(value) == "null" || json.Unmarshal(value, &detailed) != nil {
				return "", ErrInvalidArguments
			}
			continue
		}
		if key != "path" || string(value) == "null" || json.Unmarshal(value, &path) != nil {
			return "", ErrInvalidArguments
		}
	}
	if path == "" && call.Name == "list_files" {
		path = "."
	}
	return localPath(path)
}

// localPath accepts workspace-relative paths only: no absolute paths or traversal.
func localPath(path string) (string, error) {
	if !filepath.IsLocal(path) {
		return "", ErrInvalidArguments
	}
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return "", ErrDenied
		}
	}
	return path, nil
}

func (w *Workspace) readFile(ctx context.Context, call model.ToolCall) (string, error) {
	path, err := pathArgument(call)
	if err != nil {
		return "", err
	}
	data, _, err := w.readRegular(ctx, path)
	if err != nil {
		return "", err
	}
	return string(data), ctx.Err()
}

// readRegular reads a permitted regular file: no sensitive names, no symlink
// components, at most maxFileBytes. The FileInfo comes from the open file.
func (w *Workspace) readRegular(ctx context.Context, path string) ([]byte, fs.FileInfo, error) {
	info, err := w.checkedStat(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, ErrRegularFileRequired
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	f, err := w.root.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, ErrRegularFileRequired
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if len(data) > maxFileBytes {
		return nil, nil, ErrFileTooLarge
	}
	return data, info, nil
}

func (w *Workspace) listFiles(ctx context.Context, call model.ToolCall) (string, error) {
	path, err := pathArgument(call)
	if err != nil {
		return "", err
	}
	info, err := w.checkedStat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", ErrDirectoryRequired
	}
	f, err := w.root.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	entries, err := f.ReadDir(1001)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if len(entries) > 1000 {
		return "", ErrDirectoryEntryLimit
	}
	var options struct {
		Detailed bool
	}
	if err := json.Unmarshal(call.Arguments, &options); err != nil {
		return "", ErrInvalidArguments
	}
	type listingEntry struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	details := make([]listingEntry, 0, len(entries))
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if entry.Type()&fs.ModeSymlink == 0 && !sensitiveName(entry.Name()) {
			names = append(names, entry.Name())
			kind := "other"
			if entry.IsDir() {
				kind = "directory"
			} else if entry.Type().IsRegular() {
				kind = "file"
			}
			details = append(details, listingEntry{Name: entry.Name(), Type: kind})
		}
	}
	if options.Detailed {
		sort.Slice(details, func(i, j int) bool { return details[i].Name < details[j].Name })
		data, err := json.Marshal(details)
		return string(data), err
	}
	sort.Strings(names)
	data, err := json.Marshal(names)
	return string(data), err
}
