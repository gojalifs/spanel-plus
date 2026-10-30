package core

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fsRegistryStore is a RegistryStore over the same memFS the manager
// commits to, so service-level tests see one consistent registry.
type fsRegistryStore struct {
	fs   *memFS
	path string
}

func (s fsRegistryStore) Load() (Registry, error) {
	raw, err := s.fs.ReadFile(s.path)
	if err != nil {
		return Registry{}, ErrRegistryNotFound
	}
	var reg Registry
	if err := json.Unmarshal(raw, &reg); err != nil {
		return Registry{}, err
	}
	return reg, nil
}

func (s fsRegistryStore) Save(reg Registry) error {
	raw, err := reg.Marshal()
	if err != nil {
		return err
	}
	return s.fs.WriteFileAtomic(s.path, raw)
}

// --- in-memory FileStore fake ---

type memFS struct {
	files map[string][]byte
	dirs  map[string]bool
}

func newMemFS() *memFS {
	return &memFS{files: map[string][]byte{}, dirs: map[string]bool{}}
}

// normPath collapses both separators to "/": production code joins
// paths with joinPath (always "/"), while test fixtures are built
// with filepath.Join (OS separator). A real filesystem accepts both
// on Windows; this fake must too, or tests that mix the two
// conventions fail on Windows only (proven 2026-10-26, CI run #4:
// TestPHPManagerRegister and TestPHPManagerKeepsExistingIni failed
// with "missing php-cgi.exe" because the rig wrote
// `C:\php\8.3\php-cgi.exe` while ValidateEnvironment looked up
// `C:\php\8.3/php-cgi.exe`).
func normPath(p string) string { return strings.ReplaceAll(p, "\\", "/") }

func (f *memFS) Exists(p string) bool {
	p = normPath(p)
	_, ok := f.files[p]
	return ok || f.dirs[p]
}
func (f *memFS) IsDir(p string) bool { return f.dirs[normPath(p)] }
func (f *memFS) EnsureWritableDir(dir string) error {
	return f.MkdirAll(dir)
}
func (f *memFS) ReadFile(p string) ([]byte, error) {
	data, ok := f.files[normPath(p)]
	if !ok {
		return nil, errors.New("not exist: " + p)
	}
	return append([]byte(nil), data...), nil
}
func (f *memFS) WriteFile(p string, data []byte) error {
	p = normPath(p)
	_ = f.MkdirAll(filepath.Dir(p))
	f.files[p] = append([]byte(nil), data...)
	return nil
}
func (f *memFS) WriteFileAtomic(p string, data []byte) error { return f.WriteFile(p, data) }
func (f *memFS) MkdirAll(dir string) error {
	for d := normPath(dir); ; {
		f.dirs[d] = true
		parent := normPath(filepath.Dir(d))
		if parent == d {
			return nil
		}
		d = parent
	}
}
func (f *memFS) Remove(p string) error { delete(f.files, normPath(p)); return nil }
func (f *memFS) RemoveAll(p string) error {
	p = normPath(p)
	delete(f.files, p)
	delete(f.dirs, p)
	prefix := p + "/"
	for k := range f.files {
		if strings.HasPrefix(k, prefix) {
			delete(f.files, k)
		}
	}
	for k := range f.dirs {
		if strings.HasPrefix(k, prefix) {
			delete(f.dirs, k)
		}
	}
	return nil
}
func (f *memFS) ListNames(dir string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	prefix := normPath(dir) + "/"
	collect := func(path string) {
		if !strings.HasPrefix(path, prefix) {
			return
		}
		rest := strings.TrimPrefix(path, prefix)
		name := strings.SplitN(rest, "/", 2)[0]
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	for p := range f.files {
		collect(p)
	}
	for d := range f.dirs {
		collect(d)
	}
	return out, nil
}

func TestMemFSSeparatorTolerance(t *testing.T) {
	fs := newMemFS()
	// Production code joins with "/" (joinPath); fixtures are built
	// with filepath.Join ("\" on Windows). The fake must resolve both
	// spellings to the same entry, like a real Windows filesystem.
	if err := fs.WriteFile(`C:\php\8.3\php-cgi.exe`, []byte("x")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if !fs.Exists(`C:/php/8.3/php-cgi.exe`) {
		t.Error("slash-joined path must find backslash-written file")
	}
	if !fs.IsDir(`C:/php/8.3`) {
		t.Error("slash-joined dir must resolve")
	}
	if _, err := fs.ReadFile(`C:/php/8.3/php-cgi.exe`); err != nil {
		t.Errorf("ReadFile slash path: %v", err)
	}
	names, err := fs.ListNames(`C:\php\8.3`)
	if err != nil || len(names) != 1 || names[0] != "php-cgi.exe" {
		t.Errorf("ListNames = %v, %v", names, err)
	}
}

// --- other fakes ---

type fakeServiceCtl struct {
	restarts  int
	failFirst int // number of initial Restart calls that fail
}

func (f *fakeServiceCtl) Restart(string) error {
	f.restarts++
	if f.restarts <= f.failFirst {
		return errors.New("service control failed")
	}
	return nil
}
func (f *fakeServiceCtl) Status(string) (string, error) { return "running", nil }

type fakeHealth struct {
	ok    bool
	calls []string
}

func (f *fakeHealth) Check(url string) bool {
	f.calls = append(f.calls, url)
	return f.ok
}

type tickClock struct{ t time.Time }

func (c *tickClock) Now() time.Time {
	c.t = c.t.Add(time.Second)
	return c.t
}

func baseRegistry(php string) Registry {
	return Registry{
		Settings:    Settings{ApacheService: "Apache2.4", DocumentRoot: `C:\htdocs`, PanelPort: 8888},
		PHPVersions: []PHPVersion{{Version: "8.2", Path: `C:\php\8.2`}, {Version: "8.3", Path: `C:\php\8.3`}},
		Projects: []Project{
			{Name: "web-app", URLPath: "/web-app", FSPath: `C:\htdocs\web-app`, Type: ProjectLaravel, PublicSubdir: "public", PHPVersion: php, Enabled: true},
		},
	}
}

type managerRig struct {
	mgr     *ApacheManager
	fs      *memFS
	svc     *fakeServiceCtl
	health  *fakeHealth
	runner  *fakeRunner
	oldReg  Registry
	regPath string
	genDir  string
}

func newManagerRig(t *testing.T) *managerRig {
	t.Helper()
	fs := newMemFS()
	old := baseRegistry("8.2")
	raw, err := old.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	regPath := filepath.Join("data", "registry.json")
	genDir := filepath.Join("data", "generated", "apache")
	if err := fs.WriteFile(regPath, raw); err != nil {
		t.Fatal(err)
	}
	oldGen, err := Generator{}.GenerateAll(old)
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range oldGen {
		if err := fs.WriteFile(filepath.Join(genDir, name), []byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	svc := &fakeServiceCtl{}
	health := &fakeHealth{ok: true}
	runner := &fakeRunner{out: "Syntax OK"}
	mgr := &ApacheManager{
		Runner: runner, Files: fs, Service: svc, Health: health,
		Clock: &tickClock{t: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)},
		Sleep: func(time.Duration) {},
		Cfg: ApplyConfig{
			ApacheHome: `C:\apache`, ApacheService: "Apache2.4",
			RegistryPath: regPath, GeneratedDir: genDir,
			BackupsDir:    filepath.Join("data", "backups"),
			HealthBaseURL: "http://127.0.0.1", Retention: 20,
			HealthAttempts: 2, HealthInterval: time.Millisecond,
		},
	}
	return &managerRig{mgr: mgr, fs: fs, svc: svc, health: health, runner: runner, oldReg: old, regPath: regPath, genDir: genDir}
}

func TestApplySuccessCommitsEverything(t *testing.T) {
	rig := newManagerRig(t)
	candidate := baseRegistry("8.3")
	res, err := rig.mgr.Apply(context.Background(), candidate, []string{"/web-app"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	conf, err := rig.fs.ReadFile(filepath.Join(rig.genDir, "web-app.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(conf), `C:/php/8.3/php-cgi.exe`) {
		t.Errorf("generated conf must pin php 8.3 after set-php, got:\n%s", conf)
	}
	raw, _ := rig.fs.ReadFile(rig.regPath)
	if !strings.Contains(string(raw), `"php_version": "8.3"`) {
		t.Errorf("registry must be committed with 8.3, got:\n%s", raw)
	}
	if res.BackupDir == "" {
		t.Error("result must report the snapshot dir")
	}
	snapReg, err := rig.fs.ReadFile(filepath.Join(res.BackupDir, "registry.json"))
	if err != nil || !strings.Contains(string(snapReg), `"php_version": "8.2"`) {
		t.Errorf("snapshot must hold the previous registry (8.2), err=%v body=%s", err, snapReg)
	}
	if rig.svc.restarts != 1 {
		t.Errorf("success path restarts Apache exactly once, got %d", rig.svc.restarts)
	}
	if len(rig.health.calls) != 1 || rig.health.calls[0] != "http://127.0.0.1/web-app" {
		t.Errorf("health must check the changed project once, got %v", rig.health.calls)
	}
}

func TestApplyHTTPdTestFailureRollsBack(t *testing.T) {
	rig := newManagerRig(t)
	rig.runner.err = errors.New("exit status 1")
	rig.runner.out = "Syntax error on line 1"
	before, _ := rig.fs.ReadFile(filepath.Join(rig.genDir, "web-app.conf"))
	_, err := rig.mgr.Apply(context.Background(), baseRegistry("8.3"), []string{"/web-app"})
	if err == nil || !strings.Contains(err.Error(), "httpd -t") || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("expected loud rolled-back httpd -t error, got %v", err)
	}
	after, _ := rig.fs.ReadFile(filepath.Join(rig.genDir, "web-app.conf"))
	if string(after) != string(before) {
		t.Error("generated config must be restored byte-for-byte after rollback")
	}
	raw, _ := rig.fs.ReadFile(rig.regPath)
	if !strings.Contains(string(raw), `"php_version": "8.2"`) {
		t.Error("registry must stay on 8.2 after rollback")
	}
	if rig.svc.restarts != 1 {
		t.Errorf("rollback restarts Apache once onto the old config, got %d restarts", rig.svc.restarts)
	}
}

func TestApplyHealthFailureRollsBack(t *testing.T) {
	rig := newManagerRig(t)
	rig.health.ok = false
	_, err := rig.mgr.Apply(context.Background(), baseRegistry("8.3"), []string{"/web-app"})
	if err == nil || !strings.Contains(err.Error(), "health check") {
		t.Fatalf("expected health check failure, got %v", err)
	}
	raw, _ := rig.fs.ReadFile(rig.regPath)
	if !strings.Contains(string(raw), `"php_version": "8.2"`) {
		t.Error("registry must stay on 8.2 after health-check rollback")
	}
	if rig.svc.restarts != 2 {
		t.Errorf("expected apply restart + rollback restart, got %d", rig.svc.restarts)
	}
}

func TestApplyRestartFailureRollsBack(t *testing.T) {
	rig := newManagerRig(t)
	rig.svc.failFirst = 1 // apply's restart fails; rollback's succeeds
	_, err := rig.mgr.Apply(context.Background(), baseRegistry("8.3"), []string{"/web-app"})
	if err == nil || !strings.Contains(err.Error(), "restart apache") {
		t.Fatalf("expected restart failure, got %v", err)
	}
	conf, _ := rig.fs.ReadFile(filepath.Join(rig.genDir, "web-app.conf"))
	if !strings.Contains(string(conf), `C:/php/8.2/php-cgi.exe`) {
		t.Error("config must be rolled back to php 8.2")
	}
}

func TestApplyPrunesOldSnapshots(t *testing.T) {
	rig := newManagerRig(t)
	rig.mgr.Cfg.Retention = 2
	for _, stamp := range []string{"20260101-000000", "20260102-000000", "20260103-000000"} {
		if err := rig.fs.WriteFile(filepath.Join(rig.mgr.Cfg.BackupsDir, stamp, "registry.json"), []byte("{}")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := rig.mgr.Apply(context.Background(), baseRegistry("8.3"), nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	names, err := rig.fs.ListNames(rig.mgr.Cfg.BackupsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 {
		t.Errorf("retention 2 must keep exactly 2 snapshots, got %v", names)
	}
}

func TestSetPHPEndToEnd(t *testing.T) {
	rig := newManagerRig(t)
	store := fsRegistryStore{fs: rig.fs, path: rig.regPath}
	regSvc := &RegistryService{Store: store, Files: fullEnvForManager(), Clock: &tickClock{t: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}}
	projects := &ProjectService{Registry: regSvc, Manager: rig.mgr}

	// Unregistered target version is refused before anything changes.
	if _, err := projects.SetPHP(context.Background(), "web-app", "7.4"); err == nil {
		t.Fatal("SetPHP to unregistered version must be refused")
	}
	res, err := projects.SetPHP(context.Background(), "web-app", "8.3")
	if err != nil {
		t.Fatalf("SetPHP: %v", err)
	}
	if res.BackupDir == "" {
		t.Error("SetPHP must return the apply result")
	}
	raw, _ := rig.fs.ReadFile(rig.regPath)
	if !strings.Contains(string(raw), `"php_version": "8.3"`) {
		t.Error("committed registry must show php 8.3")
	}
	if _, err := projects.SetPHP(context.Background(), "hantu", "8.3"); err == nil {
		t.Fatal("SetPHP on unknown project must be refused")
	}
}

func TestManagerStatusAndRestartDelegate(t *testing.T) {
	rig := newManagerRig(t)
	if st, err := rig.mgr.Status(); err != nil || st != "running" {
		t.Errorf("Status = %q, %v — want running from the service control", st, err)
	}
	if err := rig.mgr.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if rig.svc.restarts != 1 {
		t.Errorf("Restart must reach the service control once, got %d", rig.svc.restarts)
	}
}

func TestApplyRemovesStaleConfigs(t *testing.T) {
	rig := newManagerRig(t)
	stale := filepath.Join(rig.genDir, "hantu.conf")
	if err := rig.fs.WriteFile(stale, []byte("old")); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.mgr.Apply(context.Background(), baseRegistry("8.3"), nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if rig.fs.Exists(stale) {
		t.Error("config files of removed/disabled projects must be deleted by apply")
	}
}

// armedFS fails WriteFileAtomic for generated-dir paths once armed,
// to prove the rollback-failure branch reports loudly (never silently).
type armedFS struct {
	*memFS
	armed bool
}

func (f *armedFS) WriteFileAtomic(p string, data []byte) error {
	if f.armed && strings.Contains(p, "generated") {
		return errors.New("disk full (simulated)")
	}
	return f.memFS.WriteFileAtomic(p, data)
}

func TestApplyRollbackFailureIsLoud(t *testing.T) {
	rig := newManagerRig(t)
	afs := &armedFS{memFS: rig.fs, armed: true}
	rig.mgr.Files = afs
	// Seeding is done; from here generated writes fail, so apply fails
	// at write stage and the rollback write fails too.
	_, err := rig.mgr.Apply(context.Background(), baseRegistry("8.3"), nil)
	if err == nil || !strings.Contains(err.Error(), "ROLLBACK FAILED") {
		t.Fatalf("expected a loud ROLLBACK FAILED error, got %v", err)
	}
}

func TestAddProjectAndSetEnabled(t *testing.T) {
	rig := newManagerRig(t)
	store := fsRegistryStore{fs: rig.fs, path: rig.regPath}
	regSvc := &RegistryService{Store: store, Files: fullEnvForManager(), Clock: &tickClock{t: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}}
	projects := &ProjectService{Registry: regSvc, Manager: rig.mgr}

	newProj := Project{Name: "alat", URLPath: "/alat", FSPath: `C:\htdocs\web-app`, Type: ProjectLaravel, PHPVersion: "8.2", Enabled: true}
	if _, err := projects.AddProject(context.Background(), newProj); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	conf, err := rig.fs.ReadFile(filepath.Join(rig.genDir, "alat.conf"))
	if err != nil {
		t.Fatalf("new project config must be generated: %v", err)
	}
	if !strings.Contains(string(conf), "public") {
		t.Errorf("laravel project without explicit public_subdir must default to public, got:\n%s", conf)
	}
	if _, err := projects.AddProject(context.Background(), newProj); err == nil {
		t.Fatal("duplicate AddProject must be refused")
	}

	// Disabling removes its config and needs no health check.
	rig.health.calls = nil
	if _, err := projects.SetEnabled(context.Background(), "alat", false); err != nil {
		t.Fatalf("SetEnabled(false): %v", err)
	}
	if rig.fs.Exists(filepath.Join(rig.genDir, "alat.conf")) {
		t.Error("disabled project's config must be removed")
	}
	if len(rig.health.calls) != 0 {
		t.Errorf("disabling must not health-check, got %v", rig.health.calls)
	}
	if _, err := projects.SetEnabled(context.Background(), "hantu", true); err == nil {
		t.Fatal("SetEnabled on unknown project must be refused")
	}
	if _, err := projects.List(); err != nil {
		t.Fatalf("List: %v", err)
	}
}

func TestAddProjectCI4DefaultsPublic(t *testing.T) {
	rig := newManagerRig(t)
	store := fsRegistryStore{fs: rig.fs, path: rig.regPath}
	regSvc := &RegistryService{Store: store, Files: fullEnvForManager(), Clock: &tickClock{t: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}}
	projects := &ProjectService{Registry: regSvc, Manager: rig.mgr}

	newProj := Project{Name: "ci4app", URLPath: "/ci4app", FSPath: `C:\htdocs\web-app`, Type: ProjectCI4, PHPVersion: "8.2", Enabled: true}
	if _, err := projects.AddProject(context.Background(), newProj); err != nil {
		t.Fatalf("AddProject ci4: %v", err)
	}
	conf, err := rig.fs.ReadFile(filepath.Join(rig.genDir, "ci4app.conf"))
	if err != nil {
		t.Fatalf("ci4 project config must be generated: %v", err)
	}
	if !strings.Contains(string(conf), `Alias "/ci4app"`) || !strings.Contains(string(conf), "public") {
		t.Errorf("ci4 project must Alias to public, got:\n%s", conf)
	}
}

func TestProjectServicePHPPassthrough(t *testing.T) {
	rig := newManagerRig(t)
	regSvc := &RegistryService{Store: fsRegistryStore{fs: rig.fs, path: rig.regPath}, Files: fullEnvForManager(), Clock: &tickClock{}}
	projects := &ProjectService{Registry: regSvc, Manager: rig.mgr}
	if err := projects.AddPHPVersion(PHPVersion{Version: "8.2", Path: `C:\php\8.2`}); err == nil {
		t.Error("duplicate AddPHPVersion via ProjectService must be refused")
	}
	if err := projects.RemovePHPVersion("8.3"); err != nil {
		t.Errorf("RemovePHPVersion unused via ProjectService: %v", err)
	}
	if err := projects.RemovePHPVersion("8.2"); err == nil {
		t.Error("RemovePHPVersion in use via ProjectService must be refused")
	}
}

// fullEnvForManager matches baseRegistry's paths.
func fullEnvForManager() envFiles {
	dirs := map[string]bool{}
	files := map[string]bool{}
	for _, v := range []string{`C:\php\8.2`, `C:\php\8.3`} {
		dirs[v] = true
		files[v+"/php-cgi.exe"] = true
		files[v+"/php.exe"] = true
	}
	// baseRegistry uses the golden fixture paths for variety; align here:
	for _, v := range []string{`C:\panel\php\8.2`, `C:\panel\php\8.3`} {
		dirs[v] = true
		files[v+"/php-cgi.exe"] = true
		files[v+"/php.exe"] = true
	}
	dirs[`C:\htdocs\web-app`] = true
	dirs[`C:\htdocs\web-app/public`] = true
	return envFiles{dirs: dirs, files: files}
}
