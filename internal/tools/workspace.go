package tools

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/vichanse/augustin/internal/model"
)

const maxFileBytes = 1 << 20

type Workspace struct {
	root *os.Root
}

func OpenWorkspace(path string) (*Workspace, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	return &Workspace{root: root}, nil
}

func (w *Workspace) Close() error {
	return w.root.Close()
}

func (w *Workspace) Handlers() map[string]Handler {
	return map[string]Handler{
		"read_file":  w.readFile,
		"list_files": w.listFiles,
	}
}

func pathArgument(call model.ToolCall) (string, error) {
	var args struct {
		Path string
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return "", err
	}
	if !filepath.IsLocal(args.Path) {
		return "", errors.New("path must be local")
	}
	for _, part := range strings.Split(filepath.ToSlash(args.Path), "/") {
		if part == ".." {
			return "", errors.New("parent traversal denied")
		}
	}
	return args.Path, nil
}

func (w *Workspace) readFile(ctx context.Context, call model.ToolCall) (string, error) {
	path, err := pathArgument(call)
	if err != nil {
		return "", err
	}
	info, err := w.root.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("regular files only")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f, err := w.root.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("regular files only")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxFileBytes {
		return "", errors.New("file exceeds size limit")
	}
	return string(data), ctx.Err()
}

func (w *Workspace) listFiles(ctx context.Context, call model.ToolCall) (string, error) {
	path, err := pathArgument(call)
	if err != nil {
		return "", err
	}
	info, err := w.root.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("directory required")
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
		return "", errors.New("directory exceeds entry limit")
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if entry.Type()&fs.ModeSymlink == 0 {
			names = append(names, entry.Name())
		}
	}
	data, err := json.Marshal(names)
	return string(data), err
}
