package core

import (
	"context"
	"fmt"
)

// ProjectService is the orchestration the CLI and (later) the UI call
// for project changes: load registry → build candidate → validate →
// run the apply pipeline. Registry-only reads/writes delegate to
// RegistryService.
type ProjectService struct {
	Registry *RegistryService
	Manager  *ApacheManager
}

// List returns the current registry (projects + PHP versions).
func (s *ProjectService) List() (Registry, error) {
	return s.Registry.Load()
}

// AddPHPVersion registers a PHP build (no Apache change).
func (s *ProjectService) AddPHPVersion(v PHPVersion) error {
	return s.Registry.AddPHPVersion(v)
}

// RemovePHPVersion unregisters a PHP build (no Apache change).
func (s *ProjectService) RemovePHPVersion(version string) error {
	return s.Registry.RemovePHPVersion(version)
}

// AddProject registers a new project and applies it end-to-end.
func (s *ProjectService) AddProject(ctx context.Context, p Project) (ApplyResult, error) {
	reg, err := s.Registry.Load()
	if err != nil {
		return ApplyResult{}, err
	}
	if _, exists := reg.FindProject(p.Name); exists {
		return ApplyResult{}, fmt.Errorf("project %q already exists", p.Name)
	}
	now := s.Registry.now()
	p.CreatedAt, p.UpdatedAt = now, now
	if p.Type == ProjectLaravel && p.PublicSubdir == "" {
		p.PublicSubdir = "public"
	}
	candidate := reg.Clone()
	candidate.Projects = append(candidate.Projects, p)
	return s.apply(ctx, candidate, healthPathsFor(candidate, p))
}

// SetPHP changes one project's PHP version through the full apply
// pipeline — the M2 acceptance scenario (Plan Bagian 7).
func (s *ProjectService) SetPHP(ctx context.Context, name, version string) (ApplyResult, error) {
	reg, err := s.Registry.Load()
	if err != nil {
		return ApplyResult{}, err
	}
	candidate := reg.Clone()
	idx := projectIndex(candidate, name)
	if idx < 0 {
		return ApplyResult{}, fmt.Errorf("project %q not found", name)
	}
	if _, ok := candidate.FindPHP(version); !ok {
		return ApplyResult{}, fmt.Errorf("php version %q is not registered — add it first with 'panel php add'", version)
	}
	candidate.Projects[idx].PHPVersion = version
	candidate.Projects[idx].UpdatedAt = s.Registry.now()
	target, _ := candidate.FindProject(name)
	return s.apply(ctx, candidate, healthPathsFor(candidate, target))
}

// SetEnabled enables or disables a project through the pipeline.
// Enabling health-checks the project; disabling generates no config
// for it, so there is nothing to health-check.
func (s *ProjectService) SetEnabled(ctx context.Context, name string, enabled bool) (ApplyResult, error) {
	reg, err := s.Registry.Load()
	if err != nil {
		return ApplyResult{}, err
	}
	candidate := reg.Clone()
	idx := projectIndex(candidate, name)
	if idx < 0 {
		return ApplyResult{}, fmt.Errorf("project %q not found", name)
	}
	candidate.Projects[idx].Enabled = enabled
	candidate.Projects[idx].UpdatedAt = s.Registry.now()
	var paths []string
	if enabled {
		paths = []string{candidate.Projects[idx].URLPath}
	}
	return s.apply(ctx, candidate, paths)
}

// apply validates the candidate against the machine, then runs the
// pipeline (which validates structure again and owns rollback).
func (s *ProjectService) apply(ctx context.Context, candidate Registry, healthPaths []string) (ApplyResult, error) {
	if err := candidate.ValidateStructure(); err != nil {
		return ApplyResult{}, err
	}
	if err := candidate.ValidateEnvironment(s.Registry.Files); err != nil {
		return ApplyResult{}, err
	}
	return s.Manager.Apply(ctx, candidate, healthPaths)
}

func projectIndex(reg Registry, name string) int {
	for i, p := range reg.Projects {
		if p.Name == name {
			return i
		}
	}
	return -1
}

// healthPathsFor returns the health-check targets for a change: a
// disabled project serves nothing, so it needs no check.
func healthPathsFor(_ Registry, p Project) []string {
	if !p.Enabled {
		return nil
	}
	return []string{p.URLPath}
}
