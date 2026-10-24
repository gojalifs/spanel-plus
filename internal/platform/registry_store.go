package platform

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/gojalifs/spanel-plus/internal/core"
)

// JSONRegistryStore is the production core.RegistryStore: one JSON
// file, written atomically (temp + rename via OSFileStore).
type JSONRegistryStore struct {
	Path  string
	Files OSFileStore
}

// NewJSONRegistryStore builds a store for the given registry path.
func NewJSONRegistryStore(path string) JSONRegistryStore {
	return JSONRegistryStore{Path: path, Files: OSFileStore{}}
}

// Load reads and parses the registry. A missing file yields
// core.ErrRegistryNotFound; invalid JSON is a loud error.
func (s JSONRegistryStore) Load() (core.Registry, error) {
	raw, err := os.ReadFile(s.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return core.Registry{}, core.ErrRegistryNotFound
		}
		return core.Registry{}, fmt.Errorf("read registry %s: %w", s.Path, err)
	}
	var reg core.Registry
	if err := json.Unmarshal(raw, &reg); err != nil {
		return core.Registry{}, fmt.Errorf("registry file %s is corrupt: %w", s.Path, err)
	}
	return reg, nil
}

// Save validates nothing itself (the service layer owns validation)
// and writes the registry atomically.
func (s JSONRegistryStore) Save(reg core.Registry) error {
	raw, err := reg.Marshal()
	if err != nil {
		return err
	}
	return s.Files.WriteFileAtomic(s.Path, raw)
}

var _ core.RegistryStore = JSONRegistryStore{}
