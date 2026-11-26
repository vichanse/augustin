package tools

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
)

var errSensitivePath = errors.New("workspace path denied by policy")

func sensitiveName(name string) bool {
	name = strings.ToLower(name)
	switch name {
	case ".git", ".ssh", ".aws", ".gnupg", ".kube", ".netrc", ".npmrc", ".pypirc", ".docker", "credentials", "credentials.json", "secrets", "secrets.json", "id_rsa", "id_ed25519", "id_ecdsa", "id_dsa":
		return true
	}
	return name == ".env" || strings.HasPrefix(name, ".env.") ||
		strings.HasSuffix(name, ".pem") || strings.HasSuffix(name, ".key") ||
		strings.HasSuffix(name, ".p12") || strings.HasSuffix(name, ".pfx")
}

// checkedStat rejects symlink components in trusted, non-concurrently
// modified workspaces. It is not a race-free sandbox.
func (w *Workspace) checkedStat(path string) (fs.FileInfo, error) {
	current := "."
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == "." || part == "" {
			continue
		}
		if sensitiveName(part) {
			return nil, errSensitivePath
		}
		current = filepath.Join(current, part)
		info, err := w.root.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return nil, errSensitivePath
		}
	}
	return w.root.Lstat(current)
}
