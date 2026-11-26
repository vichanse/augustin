package trust

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
)

type Store struct {
	File string
}

func Default() (Store, error) {
	dir, err := os.UserConfigDir()
	return Store{File: filepath.Join(dir, "augustin", "trusted-workspaces.json")}, err
}

func Canonical(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func PersistentAllowed(path string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	home, err = Canonical(home)
	if err != nil {
		return false
	}
	return path != home && filepath.Dir(path) != path
}

func (s Store) List() ([]string, error) {
	data, err := os.ReadFile(s.File)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var paths []string
	err = json.Unmarshal(data, &paths)
	return paths, err
}

func (s Store) Contains(path string) (bool, error) {
	paths, err := s.List()
	for _, p := range paths {
		if p == path && PersistentAllowed(path) {
			return true, err
		}
	}
	return false, err
}

func (s Store) Add(path string) error {
	if !PersistentAllowed(path) {
		return errors.New("persistent trust not permitted")
	}
	paths, err := s.List()
	if err != nil {
		return err
	}
	for _, p := range paths {
		if p == path {
			return nil
		}
	}
	return s.save(append(paths, path))
}

func (s Store) Revoke(path string) error {
	paths, err := s.List()
	if err != nil {
		return err
	}
	var kept []string
	for _, p := range paths {
		if p != path {
			kept = append(kept, p)
		}
	}
	return s.save(kept)
}

func (s Store) Clear() error { return s.save(nil) }

func (s Store) save(paths []string) error {
	sort.Strings(paths)
	data, err := json.MarshalIndent(paths, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.File)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".trust-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), s.File)
}
