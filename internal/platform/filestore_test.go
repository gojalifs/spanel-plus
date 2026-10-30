package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOSFileStoreReadTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	content := []byte("0123456789ABCDEF") // 16 bytes
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	var s OSFileStore

	// Tail shorter than the file: only the last bytes, not the head.
	got, err := s.ReadTail(path, 4)
	if err != nil || string(got) != "CDEF" {
		t.Errorf("ReadTail(4) = %q, %v", got, err)
	}
	// Tail larger than the file: the whole file.
	got, err = s.ReadTail(path, 100)
	if err != nil || string(got) != string(content) {
		t.Errorf("ReadTail(100) = %q, %v", got, err)
	}
	// Non-positive limit: the whole file, same as ReadFile.
	got, err = s.ReadTail(path, 0)
	if err != nil || string(got) != string(content) {
		t.Errorf("ReadTail(0) = %q, %v", got, err)
	}
	// Empty file: empty, no error.
	empty := filepath.Join(dir, "empty.log")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = s.ReadTail(empty, 10)
	if err != nil || len(got) != 0 {
		t.Errorf("ReadTail(empty) = %q, %v", got, err)
	}
	// Missing file: error (TailLog turns this into an empty result).
	if _, err := s.ReadTail(filepath.Join(dir, "nope.log"), 10); err == nil {
		t.Errorf("ReadTail(missing) must error")
	}
}
