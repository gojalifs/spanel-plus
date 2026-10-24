package platform

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gojalifs/spanel-plus/internal/core"
)

// OSFileStore is the production core.FileStore (real filesystem).
type OSFileStore struct{}

// Exists reports whether path exists.
func (OSFileStore) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// IsDir reports whether path exists and is a directory.
func (OSFileStore) IsDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// EnsureWritableDir creates dir if needed and write-tests it.
func (OSFileStore) EnsureWritableDir(dir string) error {
	return OSFileProbe{}.EnsureWritableDir(dir)
}

// ReadFile reads a whole file.
func (OSFileStore) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// WriteFile writes a whole file, creating parent directories.
func (OSFileStore) WriteFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// WriteFileAtomic writes via a temp file in the same folder + rename:
// readers never observe a half-written file, and a crash leaves
// either the old or the new contents, never a mix.
func (OSFileStore) WriteFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmpName, path, err)
	}
	return nil
}

// MkdirAll creates a directory tree.
func (OSFileStore) MkdirAll(dir string) error { return os.MkdirAll(dir, 0o755) }

// Remove deletes one file; a missing file is not an error.
func (OSFileStore) Remove(path string) error {
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// RemoveAll deletes a file or directory tree.
func (OSFileStore) RemoveAll(path string) error { return os.RemoveAll(path) }

// ListNames returns entry names inside dir; a missing dir reads empty.
func (OSFileStore) ListNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names, nil
}

var _ core.FileStore = OSFileStore{}
