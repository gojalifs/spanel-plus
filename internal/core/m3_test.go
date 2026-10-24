package core

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParsePHPVersionOutput(t *testing.T) {
	full, mm, nts, arch, err := ParsePHPVersionOutput("PHP 8.3.13 (cli) (built: Nov 26 2024 12:00:00) (NTS Visual C++ 2019 x64)\nCopyright (c) The PHP Group")
	if err != nil || full != "8.3.13" || mm != "8.3" || !nts || arch != "x64" {
		t.Errorf("NTS parse = %q %q nts=%v arch=%q err=%v", full, mm, nts, arch, err)
	}
	_, _, nts, _, err = ParsePHPVersionOutput("PHP 8.2.25 (cli) (built: Oct 22 2024 00:00:00) (TS Visual C++ 2019 x64)")
	if err != nil || nts {
		t.Errorf("TS build must parse with nts=false, got nts=%v err=%v", nts, err)
	}
	if _, _, _, _, err := ParsePHPVersionOutput("bukan php"); err == nil {
		t.Error("garbage output must be an error")
	}
}

func TestParsePHPModules(t *testing.T) {
	out := "[PHP Modules]\nbcmath\ncurl\nmbstring\n\n[Zend Modules]\nZend OPcache\n"
	got := ParsePHPModules(out)
	if len(got) != 3 || got[1] != "curl" {
		t.Errorf("modules = %v, want [bcmath curl mbstring] (Zend modules excluded)", got)
	}
}

func TestDiffLines(t *testing.T) {
	lines := DiffLines("a\nb\nc\n", "a\nx\nc\n")
	kinds := []DiffKind{}
	for _, l := range lines {
		kinds = append(kinds, l.Kind)
	}
	want := []DiffKind{DiffContext, DiffRemove, DiffAdd, DiffContext}
	if len(kinds) != len(want) {
		t.Fatalf("diff = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("diff = %v, want %v", kinds, want)
		}
	}
	if got := DiffLines("", "satu\n"); len(got) != 1 || got[0].Kind != DiffAdd {
		t.Errorf("empty->text diff = %v", got)
	}
	if got := DiffLines("sama\n", "sama\n"); len(got) != 1 || got[0].Kind != DiffContext {
		t.Errorf("identical diff = %v", got)
	}
}

func TestProjectWarnings(t *testing.T) {
	fs := newMemFS()
	laravel := Project{Name: "app", URLPath: "/app", FSPath: `C:\htdocs\app`, Type: ProjectLaravel, PublicSubdir: "public"}
	// APP_URL still carries /public -> mismatch warning.
	_ = fs.WriteFile(`C:\htdocs\app/.env`, []byte("APP_NAME=X\nAPP_URL=http://192.168.1.10/app/public\n"))
	// .htaccess with mod_php directives in the served (public) dir.
	_ = fs.WriteFile(`C:\htdocs\app/public/.htaccess`, []byte("php_value memory_limit 256M\nphp_flag display_errors on\nRewriteEngine on\n"))
	w := ProjectWarnings(fs, laravel)
	codes := map[string]bool{}
	for _, x := range w {
		codes[x.Code] = true
	}
	if !codes["app_url_mismatch"] {
		t.Errorf("expected app_url_mismatch, got %v", w)
	}
	if !codes["php_value_in_htaccess"] {
		t.Errorf("expected php_value_in_htaccess, got %v", w)
	}

	// Clean project: matching APP_URL, clean .htaccess -> no warnings.
	_ = fs.WriteFile(`C:\htdocs\app/.env`, []byte("APP_URL=http://192.168.1.10/app\n"))
	_ = fs.WriteFile(`C:\htdocs\app/public/.htaccess`, []byte("RewriteEngine on\n"))
	if w := ProjectWarnings(fs, laravel); len(w) != 0 {
		t.Errorf("clean project must have no warnings, got %v", w)
	}

	// Missing .env is reported, not silently OK.
	fs2 := newMemFS()
	w = ProjectWarnings(fs2, laravel)
	if len(w) != 1 || w[0].Code != "app_url_missing" {
		t.Errorf("missing .env must warn app_url_missing, got %v", w)
	}
}

// fakeProber returns a canned probe result.
type fakeProber struct {
	res PHPProbeResult
	err error
}

func (f fakeProber) Probe(context.Context, string) (PHPProbeResult, error) { return f.res, f.err }

func newPHPManagerRig(t *testing.T, probe PHPProbeResult) (*PHPManager, *memFS, *memStore) {
	t.Helper()
	fs := newMemFS()
	for _, d := range []string{`C:\php\8.3`, `C:\php\8.2`} {
		_ = fs.MkdirAll(d)
		_ = fs.WriteFile(filepath.Join(d, "php.exe"), nil)
		_ = fs.WriteFile(filepath.Join(d, "php-cgi.exe"), nil)
	}
	store := &memStore{loadErr: ErrRegistryNotFound}
	regSvc := &RegistryService{Store: store, Files: fs, Clock: &tickClock{t: time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)}}
	mgr := &PHPManager{Registry: regSvc, Prober: fakeProber{res: probe}, Files: fs, RuntimeBase: `C:\Panel\data\php-runtime`}
	return mgr, fs, store
}

func TestPHPManagerRegister(t *testing.T) {
	probe := PHPProbeResult{Version: "8.3", Full: "8.3.13", NTS: true, Arch: "x64", Modules: []string{"curl", "mbstring"}}
	mgr, fs, store := newPHPManagerRig(t, probe)
	reg, err := mgr.Register(context.Background(), `C:\php\8.3`)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if reg.Version.Version != "8.3" || reg.PHPIni != "generated" {
		t.Errorf("registration = %+v", reg)
	}
	ini, err := fs.ReadFile(`C:\php\8.3/php.ini`)
	if err != nil || !strings.Contains(string(ini), "opcache.enable = 1") || !strings.Contains(string(ini), "8.3") {
		t.Errorf("generated php.ini wrong: err=%v body=%s", err, ini)
	}
	if store.saved == nil || len(store.saved.PHPVersions) != 1 {
		t.Fatalf("registry must hold the new version, got %+v", store.saved)
	}

	// Second registration of the same folder: refused as duplicate,
	// and the existing php.ini stays untouched.
	if _, err := mgr.Register(context.Background(), `C:\php\8.3`); !errors.Is(err, ErrPHPVersionExists) {
		t.Errorf("duplicate register must wrap ErrPHPVersionExists, got %v", err)
	}
}

func TestPHPManagerRefusesTSBuild(t *testing.T) {
	probe := PHPProbeResult{Version: "8.2", Full: "8.2.25", NTS: false, Arch: "x64"}
	mgr, _, _ := newPHPManagerRig(t, probe)
	if _, err := mgr.Register(context.Background(), `C:\php\8.2`); err == nil || !strings.Contains(err.Error(), "NTS") {
		t.Fatalf("TS build must be refused with an NTS explanation, got %v", err)
	}
}

func TestPHPManagerKeepsExistingIni(t *testing.T) {
	probe := PHPProbeResult{Version: "8.3", Full: "8.3.13", NTS: true, Arch: "x64"}
	mgr, fs, _ := newPHPManagerRig(t, probe)
	_ = fs.WriteFile(`C:\php\8.3/php.ini`, []byte("memory_limit = 128M\n"))
	reg, err := mgr.Register(context.Background(), `C:\php\8.3`)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if reg.PHPIni != "existing" {
		t.Errorf("php_ini state = %q, want existing", reg.PHPIni)
	}
	ini, _ := fs.ReadFile(`C:\php\8.3/php.ini`)
	if string(ini) != "memory_limit = 128M\n" {
		t.Errorf("existing php.ini must be untouched, got %q", ini)
	}
}

func TestProjectRemoveAndUpdate(t *testing.T) {
	rig := newManagerRig(t)
	store := fsRegistryStore{fs: rig.fs, path: rig.regPath}
	regSvc := &RegistryService{Store: store, Files: fullEnvForManager(), Clock: &tickClock{t: time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)}}
	projects := &ProjectService{Registry: regSvc, Manager: rig.mgr, Files: rig.fs}

	// Update: move web-app to PHP 8.3 via full update.
	reg, _ := regSvc.Load()
	p, _ := reg.FindProject("web-app")
	p.PHPVersion = "8.3"
	origCreated := p.CreatedAt
	// A client sending its own created_at must be ignored: the stored
	// creation time is identity, not an editable field.
	p.CreatedAt = time.Date(2026, 1, 15, 8, 0, 0, 0, time.UTC)
	if _, err := projects.UpdateProject(context.Background(), "web-app", p); err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	conf, _ := rig.fs.ReadFile(filepath.Join(rig.genDir, "web-app.conf"))
	if !strings.Contains(string(conf), "8.3") {
		t.Errorf("config after update must use php 8.3, got:\n%s", conf)
	}
	after, _ := regSvc.Load()
	p2, _ := after.FindProject("web-app")
	if !p2.CreatedAt.Equal(origCreated) {
		t.Errorf("UpdateProject must preserve stored created_at %v, got %v", origCreated, p2.CreatedAt)
	}
	if p2.UpdatedAt.IsZero() {
		t.Error("UpdateProject must stamp updated_at")
	}

	// Preview the reverse change before applying it.
	diff, err := projects.PreviewSetPHP("web-app", "8.2")
	if err != nil {
		t.Fatalf("PreviewSetPHP: %v", err)
	}
	hasAdd, hasRemove := false, false
	for _, l := range diff.Lines {
		if l.Kind == DiffAdd && strings.Contains(l.Text, "8.2") {
			hasAdd = true
		}
		if l.Kind == DiffRemove && strings.Contains(l.Text, "8.3") {
			hasRemove = true
		}
	}
	if !hasAdd || !hasRemove {
		t.Errorf("preview diff must show 8.3 removed / 8.2 added, got %+v", diff.Lines)
	}
	if _, err := projects.PreviewSetPHP("web-app", "7.4"); !errors.Is(err, ErrPHPVersionNotRegistered) {
		t.Errorf("preview with unregistered version must wrap sentinel, got %v", err)
	}

	// Remove: config disappears, registry no longer lists it.
	if _, err := projects.RemoveProject(context.Background(), "web-app"); err != nil {
		t.Fatalf("RemoveProject: %v", err)
	}
	if rig.fs.Exists(filepath.Join(rig.genDir, "web-app.conf")) {
		t.Error("removed project's config must be deleted by apply")
	}
	after, _ = regSvc.Load()
	if _, ok := after.FindProject("web-app"); ok {
		t.Error("removed project must leave the registry")
	}
	if _, err := projects.RemoveProject(context.Background(), "web-app"); !errors.Is(err, ErrProjectNotFound) {
		t.Errorf("removing twice must wrap ErrProjectNotFound, got %v", err)
	}
}
