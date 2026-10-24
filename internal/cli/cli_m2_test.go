package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gojalifs/spanel-plus/internal/core"
)

// --- fakes for M2 ops ---

type fakeProjects struct {
	reg       core.Registry
	applyErr  error
	lastAdd   core.Project
	lastPHP   [2]string
	lastToggl [2]any
}

func (f *fakeProjects) List() (core.Registry, error) { return f.reg, nil }
func (f *fakeProjects) AddPHPVersion(v core.PHPVersion) error {
	f.lastPHP = [2]string{v.Version, v.Path}
	return f.applyErr
}
func (f *fakeProjects) RemovePHPVersion(string) error { return f.applyErr }
func (f *fakeProjects) AddProject(_ context.Context, p core.Project) (core.ApplyResult, error) {
	f.lastAdd = p
	return core.ApplyResult{BackupDir: "bk", FilesWritten: []string{"x.conf"}}, f.applyErr
}
func (f *fakeProjects) SetPHP(_ context.Context, name, version string) (core.ApplyResult, error) {
	f.lastPHP = [2]string{name, version}
	return core.ApplyResult{BackupDir: "bk", FilesWritten: []string{"x.conf"}, HealthChecked: []string{"/x"}}, f.applyErr
}
func (f *fakeProjects) SetEnabled(_ context.Context, name string, enabled bool) (core.ApplyResult, error) {
	f.lastToggl = [2]any{name, enabled}
	return core.ApplyResult{BackupDir: "bk"}, f.applyErr
}

type fakeApache struct{ err error }

func (f fakeApache) TestConfig(context.Context) error { return f.err }
func (f fakeApache) Status() (string, error)          { return "running", f.err }
func (f fakeApache) Restart() error                   { return f.err }

func TestProjectList(t *testing.T) {
	projects := &fakeProjects{reg: core.Registry{
		Projects: []core.Project{
			{Name: "web-app", URLPath: "/web-app", Type: core.ProjectLaravel, PHPVersion: "8.2", Enabled: true},
		},
	}}
	deps, out, _ := testDeps()
	deps.Projects = projects
	deps.Apache = fakeApache{}
	if code := New(deps).Run([]string{"project", "list"}); code != 0 {
		t.Fatalf("project list exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "web-app") || !strings.Contains(out.String(), "/web-app") {
		t.Errorf("project list must show projects, got: %q", out.String())
	}
}

func TestProjectSetPHPCommand(t *testing.T) {
	projects := &fakeProjects{}
	deps, out, _ := testDeps()
	deps.Projects = projects
	deps.Apache = fakeApache{}
	if code := New(deps).Run([]string{"project", "set-php", "web-app", "8.3"}); code != 0 {
		t.Fatalf("set-php exit = %d, want 0", code)
	}
	if projects.lastPHP != [2]string{"web-app", "8.3"} {
		t.Errorf("SetPHP called with %v, want [web-app 8.3]", projects.lastPHP)
	}
	if !strings.Contains(out.String(), "Apply OK") {
		t.Errorf("output must report the apply, got: %q", out.String())
	}
}

func TestProjectAddParsesFlags(t *testing.T) {
	projects := &fakeProjects{}
	deps, _, _ := testDeps()
	deps.Projects = projects
	deps.Apache = fakeApache{}
	code := New(deps).Run([]string{"project", "add",
		"--name", "alat", "--url", "/alat", "--path", `D:\apps\alat`,
		"--type", "php-native", "--php", "8.3"})
	if code != 0 {
		t.Fatalf("project add exit = %d, want 0", code)
	}
	p := projects.lastAdd
	if p.Name != "alat" || p.URLPath != "/alat" || p.FSPath != `D:\apps\alat` ||
		p.Type != core.ProjectPHPNative || p.PHPVersion != "8.3" || !p.Enabled {
		t.Errorf("parsed project wrong: %+v", p)
	}

	code = New(deps).Run([]string{"project", "add",
		"--name", "x", "--url", "/x", "--path", `C:\x`, "--type", "static", "--disabled"})
	if code != 0 {
		t.Fatalf("project add --disabled exit = %d, want 0", code)
	}
	if projects.lastAdd.Enabled {
		t.Error("--disabled must create a disabled project")
	}
}

func TestProjectCommandsUsageAndErrors(t *testing.T) {
	projects := &fakeProjects{}
	deps, _, _ := testDeps()
	deps.Projects = projects
	deps.Apache = fakeApache{}
	if code := New(deps).Run([]string{"project", "set-php", "satu"}); code != 2 {
		t.Errorf("set-php with missing args exit = %d, want 2", code)
	}
	projects.applyErr = errors.New("rolled back")
	if code := New(deps).Run([]string{"project", "set-php", "web-app", "8.3"}); code != 1 {
		t.Errorf("failing apply exit = %d, want 1", code)
	}
	if code := New(deps).Run([]string{"project", "ngawur"}); code != 2 {
		t.Errorf("unknown project subcommand exit = %d, want 2", code)
	}
}

func TestPHPAndApacheCommands(t *testing.T) {
	projects := &fakeProjects{reg: core.Registry{
		PHPVersions: []core.PHPVersion{{Version: "8.2", Path: `C:\php\8.2`}},
	}}
	deps, out, _ := testDeps()
	deps.Projects = projects
	deps.Apache = fakeApache{}
	if code := New(deps).Run([]string{"php", "list"}); code != 0 {
		t.Fatalf("php list exit = %d", code)
	}
	if !strings.Contains(out.String(), "8.2") {
		t.Errorf("php list must show versions, got: %q", out.String())
	}
	if code := New(deps).Run([]string{"php", "add", "--version", "8.3", "--path", `C:\php\8.3`}); code != 0 {
		t.Fatalf("php add exit = %d", code)
	}
	if projects.lastPHP != [2]string{"8.3", `C:\php\8.3`} {
		t.Errorf("AddPHPVersion called with %v", projects.lastPHP)
	}
	if code := New(deps).Run([]string{"apache", "test"}); code != 0 {
		t.Errorf("apache test exit = %d, want 0", code)
	}
	if code := New(deps).Run([]string{"apache", "status"}); code != 0 {
		t.Errorf("apache status exit = %d, want 0", code)
	}
}
