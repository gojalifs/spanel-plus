package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gojalifs/spanel-plus/internal/core"
)

func TestJSONRegistryStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "registry.json")
	store := NewJSONRegistryStore(path)

	if _, err := store.Load(); !core.IsNotFound(err) {
		t.Fatalf("missing registry must report ErrRegistryNotFound, got %v", err)
	}
	reg := core.Registry{
		Settings:    core.Settings{ApacheService: "Apache2.4", PanelPort: 8888},
		PHPVersions: []core.PHPVersion{{Version: "8.2", Path: `C:\php\8.2`}},
		Projects: []core.Project{
			{Name: "web-app", URLPath: "/web-app", FSPath: `C:\htdocs\web-app`, Type: core.ProjectLaravel, PublicSubdir: "public", PHPVersion: "8.2", Enabled: true},
		},
	}
	if err := store.Save(reg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Projects) != 1 || loaded.Projects[0].Name != "web-app" {
		t.Errorf("round trip lost projects: %+v", loaded)
	}
	// Atomic save leaves no temp files behind.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("atomic write left a temp file: %s", e.Name())
		}
	}
}

func TestJSONRegistryStoreCorruptIsLoud(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(path, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewJSONRegistryStore(path).Load(); err == nil || core.IsNotFound(err) {
		t.Fatalf("corrupt registry must be a loud parse error, got %v", err)
	}
}

func TestOSFileStoreWriteAtomicAndList(t *testing.T) {
	dir := t.TempDir()
	fs := OSFileStore{}
	target := filepath.Join(dir, "sub", "a.conf")
	if err := fs.WriteFileAtomic(target, []byte("one")); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	if err := fs.WriteFileAtomic(target, []byte("two")); err != nil {
		t.Fatalf("WriteFileAtomic overwrite: %v", err)
	}
	raw, err := fs.ReadFile(target)
	if err != nil || string(raw) != "two" {
		t.Fatalf("ReadFile = %q, %v — want the second write", raw, err)
	}
	names, err := fs.ListNames(filepath.Join(dir, "sub"))
	if err != nil || len(names) != 1 || names[0] != "a.conf" {
		t.Fatalf("ListNames = %v, %v", names, err)
	}
	if names, err := fs.ListNames(filepath.Join(dir, "tidak-ada")); err != nil || len(names) != 0 {
		t.Fatalf("ListNames on missing dir = %v, %v — want empty, no error", names, err)
	}
}
