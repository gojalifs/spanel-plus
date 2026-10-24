package core

import (
	"errors"
	"fmt"
	"time"
)

// RegistryStore persists the registry. Load must report a missing
// store distinctly (ErrRegistryNotFound) so the service can seed
// defaults; a corrupt store is a loud error, never an empty registry.
type RegistryStore interface {
	Load() (Registry, error)
	Save(reg Registry) error
}

// ErrRegistryNotFound marks "no registry file yet".
var ErrRegistryNotFound = fmt.Errorf("registry not found")

// IsNotFound reports whether err wraps ErrRegistryNotFound.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrRegistryNotFound)
}

// RegistryService owns registry reads and mutations. Every mutation
// passes structure AND environment validation before it is saved —
// an invalid registry must never reach disk (Plan 10.1, jalur
// berbahaya).
type RegistryService struct {
	Store    RegistryStore
	Files    FileProbe
	Clock    Clock
	Defaults Settings // seeds a fresh registry (from panel.json)
}

// Load returns the stored registry, or a fresh default one when no
// registry exists yet.
func (s *RegistryService) Load() (Registry, error) {
	reg, err := s.Store.Load()
	if err != nil {
		if IsNotFound(err) {
			return Registry{Settings: s.Defaults}, nil
		}
		return Registry{}, err
	}
	if err := reg.ValidateStructure(); err != nil {
		return Registry{}, fmt.Errorf("stored registry is invalid: %w", err)
	}
	return reg, nil
}

// Save validates and persists a registry (no Apache apply — callers
// that change served config go through ApacheManager.Apply instead).
func (s *RegistryService) Save(reg Registry) error {
	if err := reg.ValidateStructure(); err != nil {
		return err
	}
	if err := reg.ValidateEnvironment(s.Files); err != nil {
		return err
	}
	return s.Store.Save(reg)
}

// AddPHPVersion registers a PHP build (registry-only change; nothing
// served changes until a project uses it).
func (s *RegistryService) AddPHPVersion(v PHPVersion) error {
	reg, err := s.Load()
	if err != nil {
		return err
	}
	if _, exists := reg.FindPHP(v.Version); exists {
		return fmt.Errorf("php version %q is already registered", v.Version)
	}
	reg.PHPVersions = append(reg.PHPVersions, v)
	return s.Save(reg)
}

// RemovePHPVersion unregisters a PHP build, refusing while any
// project still uses it (Plan F2).
func (s *RegistryService) RemovePHPVersion(version string) error {
	reg, err := s.Load()
	if err != nil {
		return err
	}
	for _, p := range reg.Projects {
		if p.PHPVersion == version {
			return fmt.Errorf("php version %q is still used by project %q", version, p.Name)
		}
	}
	out := reg.PHPVersions[:0]
	found := false
	for _, v := range reg.PHPVersions {
		if v.Version == version {
			found = true
			continue
		}
		out = append(out, v)
	}
	if !found {
		return fmt.Errorf("php version %q is not registered", version)
	}
	reg.PHPVersions = out
	return s.Save(reg)
}

// now is the service clock, isolated for readability.
func (s *RegistryService) now() time.Time { return s.Clock.Now() }
