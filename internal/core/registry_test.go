package core

import (
	"strings"
	"testing"
	"time"
)

func validRegistry() Registry {
	return Registry{
		Settings: Settings{ApacheService: "Apache2.4", PanelPort: 8888},
		PHPVersions: []PHPVersion{
			{Version: "8.2", Path: `C:\panel\php\8.2`},
			{Version: "8.3", Path: `C:\panel\php\8.3`},
		},
		Projects: []Project{
			{Name: "web-app", URLPath: "/web-app", FSPath: `C:\htdocs\web-app`, Type: ProjectLaravel, PublicSubdir: "public", PHPVersion: "8.2", Enabled: true},
		},
	}
}

func TestRegistryValidateStructureHappy(t *testing.T) {
	if err := validRegistry().ValidateStructure(); err != nil {
		t.Fatalf("valid registry rejected: %v", err)
	}
	// Static without PHP is legal.
	reg := validRegistry()
	reg.Projects = append(reg.Projects, Project{
		Name: "statis", URLPath: "/statis", FSPath: `C:\htdocs\statis`, Type: ProjectStatic, Enabled: true,
	})
	if err := reg.ValidateStructure(); err != nil {
		t.Fatalf("static project without php rejected: %v", err)
	}
}

func TestRegistryValidateStructureRejections(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Registry)
		wantIn string
	}{
		{"duplicate project name", func(r *Registry) {
			r.Projects = append(r.Projects, r.Projects[0])
		}, "duplicate project name"},
		{"duplicate url", func(r *Registry) {
			p := r.Projects[0]
			p.Name = "lain"
			r.Projects = append(r.Projects, p)
		}, "duplicate url_path"},
		{"bad project name", func(r *Registry) { r.Projects[0].Name = "Web App!" }, "must match"},
		{"url without slash", func(r *Registry) { r.Projects[0].URLPath = "web-app" }, "url_path"},
		{"url root", func(r *Registry) { r.Projects[0].URLPath = "/" }, "url_path"},
		{"url trailing slash", func(r *Registry) { r.Projects[0].URLPath = "/web-app/" }, "url_path"},
		{"empty fs path", func(r *Registry) { r.Projects[0].FSPath = "" }, "fs_path"},
		{"unknown type", func(r *Registry) { r.Projects[0].Type = "wordpress" }, "unknown type"},
		{"laravel without public", func(r *Registry) { r.Projects[0].PublicSubdir = "" }, "public_subdir"},
		{"ci4 without public", func(r *Registry) {
			r.Projects[0].Type = ProjectCI4
			r.Projects[0].PublicSubdir = ""
		}, "public_subdir"},
		{"native with public", func(r *Registry) {
			r.Projects[0].Type = ProjectPHPNative
		}, "must not set public_subdir"},
		{"unregistered php", func(r *Registry) { r.Projects[0].PHPVersion = "7.4" }, "unregistered php"},
		{"duplicate php", func(r *Registry) {
			r.PHPVersions = append(r.PHPVersions, r.PHPVersions[0])
		}, "duplicate php version"},
		{"bad php version format", func(r *Registry) { r.PHPVersions[0].Version = "8" }, "must look like"},
		{"php empty path", func(r *Registry) { r.PHPVersions[1].Path = "" }, "empty path"},
		{"duplicate cli process", func(r *Registry) {
			r.Projects[0].CLIProcesses = []CLIProcess{{Name: "q", Command: "a"}, {Name: "q", Command: "b"}}
		}, "duplicate cli process"},
		{"bad panel port", func(r *Registry) { r.Settings.PanelPort = 99999 }, "panel_port"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := validRegistry()
			tc.mutate(&reg)
			err := reg.ValidateStructure()
			if err == nil {
				t.Fatalf("expected rejection (%s)", tc.wantIn)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error %q should mention %q", err, tc.wantIn)
			}
		})
	}
}

// envFiles fakes the machine: only the paths in dirs/files exist.
type envFiles struct {
	dirs  map[string]bool
	files map[string]bool
}

func (f envFiles) Exists(p string) bool { return f.files[p] || f.dirs[p] }
func (f envFiles) IsDir(p string) bool  { return f.dirs[p] }
func (f envFiles) EnsureWritableDir(string) error {
	return nil
}

func fullEnv() envFiles {
	dirs := map[string]bool{}
	files := map[string]bool{}
	for _, v := range []string{`C:\panel\php\8.2`, `C:\panel\php\8.3`} {
		dirs[v] = true
		files[v+"/php-cgi.exe"] = true
		files[v+"/php.exe"] = true
	}
	dirs[`C:\htdocs\web-app`] = true
	dirs[`C:\htdocs\web-app/public`] = true
	return envFiles{dirs: dirs, files: files}
}

func TestRegistryValidateEnvironment(t *testing.T) {
	reg := validRegistry()
	if err := reg.ValidateEnvironment(fullEnv()); err != nil {
		t.Fatalf("full env rejected: %v", err)
	}

	env := fullEnv()
	delete(env.files, `C:\panel\php\8.2/php-cgi.exe`)
	if err := reg.ValidateEnvironment(env); err == nil {
		t.Error("missing php-cgi.exe must be rejected")
	}

	env = fullEnv()
	delete(env.dirs, `C:\htdocs\web-app/public`)
	if err := reg.ValidateEnvironment(env); err == nil {
		t.Error("missing laravel public dir must be rejected")
	}

	env = fullEnv()
	delete(env.dirs, `C:\panel\php\8.3`)
	if err := reg.ValidateEnvironment(env); err == nil {
		t.Error("missing php folder must be rejected (registered versions are validated too)")
	}
}

type memStore struct {
	reg     Registry
	hasReg  bool
	loadErr error
	saved   *Registry
}

func (m *memStore) Load() (Registry, error) {
	if m.hasReg {
		return m.reg, nil
	}
	if m.loadErr != nil {
		return Registry{}, m.loadErr
	}
	return m.reg, nil
}

func (m *memStore) Save(reg Registry) error {
	cp := reg.Clone()
	m.saved = &cp
	m.reg = cp // subsequent Loads see what was saved
	m.hasReg = true
	return nil
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

func TestRegistryServiceDefaultsAndPHPOps(t *testing.T) {
	store := &memStore{loadErr: ErrRegistryNotFound}
	svc := &RegistryService{
		Store: store, Files: fullEnv(), Clock: fixedClock{},
		Defaults: Settings{ApacheService: "Apache2.4", PanelPort: 8888},
	}
	reg, err := svc.Load()
	if err != nil {
		t.Fatalf("Load with no registry file: %v", err)
	}
	if reg.Settings.ApacheService != "Apache2.4" {
		t.Errorf("fresh registry must seed defaults, got %+v", reg.Settings)
	}

	if err := svc.AddPHPVersion(PHPVersion{Version: "8.2", Path: `C:\panel\php\8.2`}); err != nil {
		t.Fatalf("AddPHPVersion: %v", err)
	}
	if store.saved == nil {
		t.Fatal("AddPHPVersion must save")
	}
	if err := svc.AddPHPVersion(PHPVersion{Version: "8.2", Path: `C:\panel\php\8.2`}); err == nil {
		t.Fatal("duplicate AddPHPVersion must be refused")
	}

	// Removal blocked while a project uses the version.
	store.reg = validRegistry()
	if err := svc.RemovePHPVersion("8.2"); err == nil {
		t.Fatal("RemovePHPVersion in use must be refused")
	}
	if err := svc.RemovePHPVersion("8.3"); err != nil {
		t.Fatalf("RemovePHPVersion unused: %v", err)
	}
	if err := svc.RemovePHPVersion("9.9"); err == nil {
		t.Fatal("RemovePHPVersion unknown must be refused")
	}
}

func TestRegistryServiceCorruptStoreIsLoud(t *testing.T) {
	svc := &RegistryService{
		Store: &memStore{loadErr: errBoom}, Files: fullEnv(), Clock: fixedClock{},
	}
	if _, err := svc.Load(); err == nil {
		t.Fatal("a store read failure must surface, not default")
	}
}

var errBoom = &boomError{}

type boomError struct{}

func (*boomError) Error() string { return "boom" }
