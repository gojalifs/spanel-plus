package core

import (
	"context"
	"fmt"
)

// ProjectService is the orchestration the CLI and the UI call for
// project changes: load registry → build candidate → validate →
// run the apply pipeline. Registry-only reads/writes delegate to
// RegistryService.
type ProjectService struct {
	Registry *RegistryService
	Manager  *ApacheManager
	// Files backs the warning scanners (APP_URL, php_value) and the
	// config preview; a FileStore (it extends the probe Registry uses).
	Files FileStore
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
		return ApplyResult{}, fmt.Errorf("project %q already exists: %w", p.Name, ErrProjectExists)
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
		return ApplyResult{}, fmt.Errorf("project %q not found: %w", name, ErrProjectNotFound)
	}
	if _, ok := candidate.FindPHP(version); !ok {
		return ApplyResult{}, fmt.Errorf("php version %q is not registered — add it first with 'panel php add': %w", version, ErrPHPVersionNotRegistered)
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
		return ApplyResult{}, fmt.Errorf("project %q not found: %w", name, ErrProjectNotFound)
	}
	candidate.Projects[idx].Enabled = enabled
	candidate.Projects[idx].UpdatedAt = s.Registry.now()
	var paths []string
	if enabled {
		paths = []string{candidate.Projects[idx].URLPath}
	}
	return s.apply(ctx, candidate, paths)
}

// RemoveProject unregisters a project and applies: its generated
// config disappears with the next Apache restart inside the pipeline.
// Nothing on disk inside the project folder is touched (Plan D9).
func (s *ProjectService) RemoveProject(ctx context.Context, name string) (ApplyResult, error) {
	reg, err := s.Registry.Load()
	if err != nil {
		return ApplyResult{}, err
	}
	candidate := reg.Clone()
	idx := projectIndex(candidate, name)
	if idx < 0 {
		return ApplyResult{}, fmt.Errorf("project %q not found: %w", name, ErrProjectNotFound)
	}
	candidate.Projects = append(candidate.Projects[:idx], candidate.Projects[idx+1:]...)
	return s.apply(ctx, candidate, nil)
}

// UpdateProject replaces a project's editable fields (URL path,
// folder, type, public subdir, PHP version, enabled) and applies.
// Identity (name), creation time and — unless explicitly provided —
// CLI process definitions are preserved.
func (s *ProjectService) UpdateProject(ctx context.Context, name string, upd Project) (ApplyResult, error) {
	reg, err := s.Registry.Load()
	if err != nil {
		return ApplyResult{}, err
	}
	candidate := reg.Clone()
	idx := projectIndex(candidate, name)
	if idx < 0 {
		return ApplyResult{}, fmt.Errorf("project %q not found: %w", name, ErrProjectNotFound)
	}
	old := candidate.Projects[idx]
	upd.Name = old.Name
	upd.CreatedAt = old.CreatedAt
	upd.UpdatedAt = s.Registry.now()
	if upd.CLIProcesses == nil {
		upd.CLIProcesses = old.CLIProcesses
	}
	if upd.Type == ProjectLaravel && upd.PublicSubdir == "" {
		upd.PublicSubdir = "public"
	}
	candidate.Projects[idx] = upd
	return s.apply(ctx, candidate, healthPathsFor(candidate, upd))
}

// ConfigDiff is a before/after pair of one project's generated config
// plus the rendered line diff, for the preview step of the UI flow
// (Plan F3/F8: edit → preview diff → apply).
type ConfigDiff struct {
	Project   string     `json:"project"`
	Current   string     `json:"current"`
	Candidate string     `json:"candidate"`
	Lines     []DiffLine `json:"lines"`
}

// PreviewSetPHP renders what a project's config would become under a
// different PHP version, without changing anything.
func (s *ProjectService) PreviewSetPHP(name, version string) (ConfigDiff, error) {
	reg, err := s.Registry.Load()
	if err != nil {
		return ConfigDiff{}, err
	}
	p, ok := reg.FindProject(name)
	if !ok {
		return ConfigDiff{}, fmt.Errorf("project %q not found: %w", name, ErrProjectNotFound)
	}
	candidate := reg.Clone()
	idx := projectIndex(candidate, name)
	if _, ok := candidate.FindPHP(version); !ok && candidate.Projects[idx].Type != ProjectStatic {
		return ConfigDiff{}, fmt.Errorf("php version %q is not registered: %w", version, ErrPHPVersionNotRegistered)
	}
	candidate.Projects[idx].PHPVersion = version
	return s.preview(reg, candidate, p.Name)
}

// CurrentConfig returns the project's config as ConfigGen renders it
// for the committed registry — identical to the file the last apply
// wrote into the generated dir.
func (s *ProjectService) CurrentConfig(name string) (string, error) {
	reg, err := s.Registry.Load()
	if err != nil {
		return "", err
	}
	p, ok := reg.FindProject(name)
	if !ok {
		return "", fmt.Errorf("project %q not found: %w", name, ErrProjectNotFound)
	}
	files, err := (Generator{}).GenerateAll(reg)
	if err != nil {
		return "", err
	}
	return files[p.Name+".conf"], nil
}

// preview generates both sides of a config diff for one project.
func (s *ProjectService) preview(current, candidate Registry, name string) (ConfigDiff, error) {
	curFiles, err := (Generator{}).GenerateAll(current)
	if err != nil {
		return ConfigDiff{}, err
	}
	candFiles, err := (Generator{}).GenerateAll(candidate)
	if err != nil {
		return ConfigDiff{}, err
	}
	cur := curFiles[name+".conf"]
	cand := candFiles[name+".conf"]
	return ConfigDiff{
		Project:   name,
		Current:   cur,
		Candidate: cand,
		Lines:     DiffLines(cur, cand),
	}, nil
}

// Warnings returns the migration warnings for one project (APP_URL
// mismatch, php_value/php_flag in .htaccess — Plan F1/F4).
func (s *ProjectService) Warnings(name string) ([]Warning, error) {
	if s.Files == nil {
		return nil, fmt.Errorf("warning scanner is not wired")
	}
	reg, err := s.Registry.Load()
	if err != nil {
		return nil, err
	}
	p, ok := reg.FindProject(name)
	if !ok {
		return nil, fmt.Errorf("project %q not found: %w", name, ErrProjectNotFound)
	}
	return ProjectWarnings(s.Files, p), nil
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
