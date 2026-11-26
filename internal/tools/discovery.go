package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/vichanse/augustin/internal/model"
)

type discoveryEntry struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// overview reuses the authorized directory listing implementation.
// It returns metadata only, never file contents.
func (w *Workspace) overview(ctx context.Context, call model.ToolCall) (string, error) {
	var args map[string]json.RawMessage
	if err := json.Unmarshal(call.Arguments, &args); err != nil || args == nil || len(args) != 0 {
		return "", ErrInvalidArguments
	}
	type directory struct {
		Path    string           `json:"path"`
		Entries []discoveryEntry `json:"entries"`
	}
	result := struct {
		Directories []directory `json:"directories"`
		Truncated   bool        `json:"truncated"`
	}{Directories: []directory{}}
	queue := []string{"."}
	for len(queue) > 0 && len(result.Directories) < 12 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		path := queue[0]
		queue = queue[1:]
		raw, err := json.Marshal(map[string]any{"path": path, "detailed": true})
		if err != nil {
			return "", err
		}
		output, err := w.listFiles(ctx, model.ToolCall{Name: "list_files", Arguments: raw})
		if err != nil {
			if path == "." {
				return "", err
			}
			result.Truncated = true
			continue
		}
		var entries []discoveryEntry
		if err := json.Unmarshal([]byte(output), &entries); err != nil {
			return "", err
		}
		if len(entries) > 80 {
			entries = entries[:80]
			result.Truncated = true
		}
		result.Directories = append(result.Directories, directory{Path: path, Entries: entries})
		for _, entry := range entries {
			if entry.Type != "directory" {
				continue
			}
			switch entry.Name {
			case "vendor", "node_modules", "dist", "build", "target":
				result.Truncated = true
				continue
			}
			if strings.HasPrefix(entry.Name, ".") || path != "." && filepath.Dir(path) != "." {
				result.Truncated = true
				continue
			}
			if len(queue) < 24 {
				queue = append(queue, filepath.Join(path, entry.Name))
			} else {
				result.Truncated = true
			}
		}
	}
	result.Truncated = result.Truncated || len(queue) > 0
	data, err := json.Marshal(result)
	return string(data), err
}
