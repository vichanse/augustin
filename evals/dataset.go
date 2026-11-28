// Package evals measures whether Augustin actually fixes bugs, independently
// of the model's claims and of the harness's own verification verdict.
//
// Cases are small Go modules stored as .txt files so that neither the buggy
// code nor the hidden evaluator tests are part of this repository's build.
package evals

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DatasetVersion changes whenever a case's fixture, tests or prompt change in
// a way that makes results incomparable. DatasetHash identifies the exact bytes.
const DatasetVersion = "1"

//go:embed cases fixtures
var dataset embed.FS

// Case is one evaluation task. Paths in Fixture and Hidden are relative to
// the evals directory.
type Case struct {
	ID              string   `json:"id"`
	Description     string   `json:"description"`
	Prompt          string   `json:"prompt"`
	Mode            string   `json:"mode"` // "execute" (bug fixing) or "read-only" (smoke)
	Fixture         string   `json:"fixture"`
	Hidden          string   `json:"hidden,omitempty"`
	AllowedPaths    []string `json:"allowed_paths"`
	BaselineFailure string   `json:"baseline_failure,omitempty"`
	TimeoutSeconds  int      `json:"timeout_seconds"`
}

func (c Case) ReadOnly() bool { return c.Mode == "read-only" }

func (c Case) Timeout() time.Duration { return time.Duration(c.TimeoutSeconds) * time.Second }

// Cases returns the validated dataset, sorted by ID.
func Cases() ([]Case, error) { return loadCases(dataset) }

func loadCases(fsys fs.FS) ([]Case, error) {
	paths, err := fs.Glob(fsys, "cases/*/case.json")
	if err != nil {
		return nil, err
	}
	var cases []Case
	for _, p := range paths {
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil, err
		}
		var c Case
		dec := json.NewDecoder(strings.NewReader(string(data)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if err := validate(fsys, c, path.Base(path.Dir(p))); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		cases = append(cases, c)
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	return cases, nil
}

func validate(fsys fs.FS, c Case, dir string) error {
	switch {
	case c.ID == "" || c.ID != dir:
		return fmt.Errorf("id %q must match its directory %q", c.ID, dir)
	case c.Prompt == "" || c.Description == "" || c.TimeoutSeconds <= 0:
		return fmt.Errorf("prompt, description and timeout_seconds are required")
	case c.Mode != "execute" && c.Mode != "read-only":
		return fmt.Errorf("unknown mode %q", c.Mode)
	case c.Mode == "execute" && (c.Hidden == "" || c.BaselineFailure == "" || len(c.AllowedPaths) == 0):
		return fmt.Errorf("bug-fixing cases need hidden tests, a baseline failure and allowed paths")
	case c.Mode == "read-only" && len(c.AllowedPaths) != 0:
		return fmt.Errorf("read-only cases cannot allow modifications")
	}
	files, err := fixtureFiles(fsys, c.Fixture)
	if err != nil || len(files) == 0 {
		return fmt.Errorf("fixture %q is empty or missing", c.Fixture)
	}
	for _, p := range c.AllowedPaths {
		if path.Clean(p) != p || strings.HasSuffix(p, "_test.go") || p == "go.mod" || !contains(files, p) {
			return fmt.Errorf("allowed path %q must be an existing non-test source file", p)
		}
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// fixtureFiles lists the workspace paths a fixture directory produces.
func fixtureFiles(fsys fs.FS, dir string) ([]string, error) {
	var out []string
	err := fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(p, ".txt") {
			return fmt.Errorf("fixture file %q must end in .txt", p)
		}
		out = append(out, strings.TrimSuffix(strings.TrimPrefix(p, dir+"/"), ".txt"))
		return nil
	})
	return out, err
}

// materialize writes a fixture directory into dst, dropping the .txt suffix.
// It refuses to overwrite existing files, so hidden tests cannot replace
// workspace files.
func materialize(fsys fs.FS, src, dst string) error {
	files, err := fixtureFiles(fsys, src)
	if err != nil {
		return err
	}
	for _, rel := range files {
		data, err := fs.ReadFile(fsys, src+"/"+rel+".txt")
		if err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// DatasetHash is a SHA-256 over every embedded case and fixture file.
func DatasetHash() string { return hashFS(dataset) }

func hashFS(fsys fs.FS) string {
	h := sha256.New()
	fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			data, _ := fs.ReadFile(fsys, p)
			fmt.Fprintf(h, "%s\x00%d\x00", p, len(data))
			h.Write(data)
		}
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))
}
