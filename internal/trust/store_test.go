package trust

import (
	"path/filepath"
	"testing"
)

func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	path, err := Canonical(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := Store{File: filepath.Join(dir, "trust.json")}
	if err := s.Add(path); err != nil {
		t.Fatal(err)
	}
	reopened := Store{File: s.File}
	if ok, err := reopened.Contains(path); !ok || err != nil {
		t.Fatalf("trust not persisted: %v", err)
	}
	if err := reopened.Revoke(path); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Contains(path); ok || err != nil {
		t.Fatalf("trust not revoked: %v", err)
	}
}
