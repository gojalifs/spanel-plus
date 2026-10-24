package core

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Registry is the panel's single source of truth for projects and PHP
// versions, stored as JSON in the panel data dir (Plan D9 & Bagian
// 4.3). Panel-level settings (panel.json) seed the defaults; the
// registry settings below are the managed-Apache settings.
type Registry struct {
	Settings    Settings     `json:"settings"`
	PHPVersions []PHPVersion `json:"php_versions"`
	Projects    []Project    `json:"projects"`
}

// Settings are the registry-level managed-Apache settings.
type Settings struct {
	ApacheHome    string `json:"apache_home"`
	ApacheService string `json:"apache_service"`
	DocumentRoot  string `json:"document_root"`
	PanelPort     int    `json:"panel_port"`
}

// PHPVersion is one installed PHP build the panel can assign.
type PHPVersion struct {
	Version string `json:"version"` // e.g. "8.2"
	Path    string `json:"path"`    // folder holding php.exe / php-cgi.exe
}

// ProjectType enumerates the project kinds ConfigGen understands.
type ProjectType string

const (
	ProjectLaravel   ProjectType = "laravel"
	ProjectPHPNative ProjectType = "php-native"
	ProjectStatic    ProjectType = "static"
)

// CLIProcess is a per-project long-running CLI definition (queue,
// scheduler, artisan commands). Stored from M2; executed by the
// Supervisor and Scheduler since M4 (Plan F5).
type CLIProcess struct {
	Name      string `json:"name"`
	Command   string `json:"command"`
	Autostart bool   `json:"autostart"`
	// Kind distinguishes a long-running process ("", default) from
	// the scheduler hook ("schedule:run"): the latter is not a
	// process at all — the panel's Scheduler runs
	// `php artisan schedule:run` every minute for it.
	Kind string `json:"kind,omitempty"`
	// Restart is the supervisor policy for long-running processes:
	// "" or "on-failure" (default), "always", "never".
	Restart string `json:"restart,omitempty"`
}

// Process kinds for CLIProcess.Kind.
const (
	ProcessKindLongRunning = ""
	ProcessKindScheduleRun = "schedule:run"
)

// Project is one managed application, routed by URL path (Plan D3).
type Project struct {
	Name         string       `json:"name"`
	URLPath      string       `json:"url_path"`
	FSPath       string       `json:"fs_path"`
	Type         ProjectType  `json:"type"`
	PublicSubdir string       `json:"public_subdir"` // laravel: "public"; native: ""; static: e.g. "dist"
	PHPVersion   string       `json:"php_version"`   // "" allowed for static only
	Enabled      bool         `json:"enabled"`
	CLIProcesses []CLIProcess `json:"cli_processes,omitempty"`

	// Slot Fase 2 (git deploy) — stored, not yet acted on (Plan D10).
	RepoURL    string   `json:"repo_url,omitempty"`
	Branch     string   `json:"branch,omitempty"`
	PostDeploy []string `json:"post_deploy,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TargetDir is the folder Apache serves for this project: the public
// subdir when one is set, else the project root.
func (p Project) TargetDir() string {
	if p.PublicSubdir == "" {
		return p.FSPath
	}
	return strings.TrimRight(p.FSPath, `/\`) + "/" + p.PublicSubdir
}

var (
	projectNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	phpVersionRe  = regexp.MustCompile(`^\d+\.\d+$`)
	urlPathRe     = regexp.MustCompile(`^/[a-z0-9._/-]*$`)
)

// FindProject returns the project with the given name, if any.
func (r Registry) FindProject(name string) (Project, bool) {
	for _, p := range r.Projects {
		if p.Name == name {
			return p, true
		}
	}
	return Project{}, false
}

// FindPHP returns the registered PHP version, if any.
func (r Registry) FindPHP(version string) (PHPVersion, bool) {
	for _, v := range r.PHPVersions {
		if v.Version == version {
			return v, true
		}
	}
	return PHPVersion{}, false
}

// SortedProjects returns the projects in deterministic (name) order —
// ConfigGen and listings must never depend on insertion order.
func (r Registry) SortedProjects() []Project {
	out := make([]Project, len(r.Projects))
	copy(out, r.Projects)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Clone deep-copies the registry (via JSON round-trip) so a candidate
// can be mutated and validated without touching the loaded original.
func (r Registry) Clone() Registry {
	raw, err := json.Marshal(r)
	if err != nil {
		panic(err) // Registry is always marshalable by construction
	}
	var out Registry
	if err := json.Unmarshal(raw, &out); err != nil {
		panic(err)
	}
	return out
}

// Marshal renders the registry exactly as it is stored on disk.
func (r Registry) Marshal() ([]byte, error) {
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// ValidateStructure checks every rule that does not need the machine:
// uniqueness, formats, cross-references (Plan 4.3 aturan validasi).
// These rules guard the dangerous paths, so they are exhaustively
// unit-tested (Plan 10.1: jalur berbahaya wajib 100%).
func (r Registry) ValidateStructure() error {
	if r.Settings.PanelPort != 0 && (r.Settings.PanelPort < 1 || r.Settings.PanelPort > 65535) {
		return fmt.Errorf("registry: settings.panel_port %d out of range", r.Settings.PanelPort)
	}
	phpSeen := map[string]bool{}
	for _, v := range r.PHPVersions {
		if !phpVersionRe.MatchString(v.Version) {
			return fmt.Errorf("registry: php version %q must look like \"8.2\"", v.Version)
		}
		if v.Path == "" {
			return fmt.Errorf("registry: php %s has an empty path", v.Version)
		}
		if phpSeen[v.Version] {
			return fmt.Errorf("registry: duplicate php version %q", v.Version)
		}
		phpSeen[v.Version] = true
	}

	nameSeen := map[string]bool{}
	urlSeen := map[string]bool{}
	for _, p := range r.Projects {
		if err := validateProjectStructure(p, phpSeen); err != nil {
			return err
		}
		if nameSeen[p.Name] {
			return fmt.Errorf("registry: duplicate project name %q", p.Name)
		}
		nameSeen[p.Name] = true
		if urlSeen[p.URLPath] {
			return fmt.Errorf("registry: duplicate url_path %q", p.URLPath)
		}
		urlSeen[p.URLPath] = true
	}
	return nil
}

func validateProjectStructure(p Project, phpSeen map[string]bool) error {
	if !projectNameRe.MatchString(p.Name) {
		return fmt.Errorf("registry: project name %q must match [a-z][a-z0-9-]* (it becomes a config file name)", p.Name)
	}
	if p.URLPath == "/" || !urlPathRe.MatchString(p.URLPath) || strings.HasSuffix(p.URLPath, "/") {
		return fmt.Errorf("registry: project %s url_path %q must start with /, have no trailing slash, and use only [a-z0-9._/-]", p.Name, p.URLPath)
	}
	if p.FSPath == "" {
		return fmt.Errorf("registry: project %s has an empty fs_path", p.Name)
	}
	switch p.Type {
	case ProjectLaravel:
		if p.PublicSubdir == "" {
			return fmt.Errorf("registry: laravel project %s must set public_subdir (usually \"public\")", p.Name)
		}
	case ProjectPHPNative:
		if p.PublicSubdir != "" {
			return fmt.Errorf("registry: php-native project %s must not set public_subdir", p.Name)
		}
	case ProjectStatic:
		// public_subdir is allowed (e.g. "dist") and optional.
	default:
		return fmt.Errorf("registry: project %s has unknown type %q", p.Name, p.Type)
	}
	if p.Type == ProjectStatic {
		if p.PHPVersion != "" && !phpSeen[p.PHPVersion] {
			return fmt.Errorf("registry: project %s references unregistered php version %q", p.Name, p.PHPVersion)
		}
	} else if !phpSeen[p.PHPVersion] {
		return fmt.Errorf("registry: project %s references unregistered php version %q", p.Name, p.PHPVersion)
	}
	procSeen := map[string]bool{}
	for _, proc := range p.CLIProcesses {
		if proc.Name == "" {
			return fmt.Errorf("registry: project %s has a cli process with empty name", p.Name)
		}
		switch proc.Kind {
		case ProcessKindLongRunning:
			if proc.Command == "" {
				return fmt.Errorf("registry: project %s cli process %q has an empty command", p.Name, proc.Name)
			}
		case ProcessKindScheduleRun:
			// Command is implied (artisan schedule:run); a stored
			// command is ignored for this kind.
		default:
			return fmt.Errorf("registry: project %s cli process %q has unknown kind %q", p.Name, proc.Name, proc.Kind)
		}
		switch proc.Restart {
		case "", "on-failure", "always", "never":
		default:
			return fmt.Errorf("registry: project %s cli process %q has unknown restart policy %q", p.Name, proc.Name, proc.Restart)
		}
		if procSeen[proc.Name] {
			return fmt.Errorf("registry: project %s has duplicate cli process %q", p.Name, proc.Name)
		}
		procSeen[proc.Name] = true
	}
	return nil
}

// ValidateEnvironment checks the registry against the real machine
// through a FileProbe: folders and PHP binaries must exist before a
// config pointing at them may be applied. Failing here is what keeps
// a typo'd path from ever reaching httpd -t.
func (r Registry) ValidateEnvironment(files FileProbe) error {
	for _, v := range r.PHPVersions {
		if !files.IsDir(v.Path) {
			return fmt.Errorf("registry: php %s path %s is not a directory", v.Version, v.Path)
		}
		for _, bin := range []string{"php-cgi.exe", "php.exe"} {
			if !files.Exists(joinPath(v.Path, bin)) {
				return fmt.Errorf("registry: php %s at %s is missing %s — not a usable PHP build folder", v.Version, v.Path, bin)
			}
		}
	}
	for _, p := range r.Projects {
		if !files.IsDir(p.FSPath) {
			return fmt.Errorf("registry: project %s fs_path %s is not a directory", p.Name, p.FSPath)
		}
		if p.Type == ProjectLaravel && !files.IsDir(p.TargetDir()) {
			return fmt.Errorf("registry: laravel project %s public dir %s does not exist", p.Name, p.TargetDir())
		}
	}
	return nil
}

// joinPath joins with forward slashes; Go's filepath would use the
// host OS separator, and panel paths must stay valid on Windows no
// matter where a test runs.
func joinPath(dir, name string) string {
	return strings.TrimRight(dir, `/\`) + "/" + name
}
