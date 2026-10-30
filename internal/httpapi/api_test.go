package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gojalifs/spanel-plus/internal/app"
	"github.com/gojalifs/spanel-plus/internal/config"
	"github.com/gojalifs/spanel-plus/internal/core"
	"github.com/gojalifs/spanel-plus/internal/platform"
)

// --- fakes implementing the app service interfaces ---

type fakeProjects struct {
	reg       core.Registry
	setPHPErr error
	added     *core.Project
	removed   string
}

func (f *fakeProjects) List() (core.Registry, error) { return f.reg, nil }
func (f *fakeProjects) AddProject(_ context.Context, p core.Project) (core.ApplyResult, error) {
	f.added = &p
	return core.ApplyResult{BackupDir: "b1"}, nil
}
func (f *fakeProjects) UpdateProject(_ context.Context, _ string, p core.Project) (core.ApplyResult, error) {
	f.added = &p
	return core.ApplyResult{}, nil
}
func (f *fakeProjects) RemoveProject(_ context.Context, name string) (core.ApplyResult, error) {
	f.removed = name
	return core.ApplyResult{}, nil
}
func (f *fakeProjects) SetPHP(_ context.Context, _, _ string) (core.ApplyResult, error) {
	if f.setPHPErr != nil {
		return core.ApplyResult{}, f.setPHPErr
	}
	return core.ApplyResult{HealthChecked: []string{"/app"}}, nil
}
func (f *fakeProjects) SetEnabled(_ context.Context, _ string, _ bool) (core.ApplyResult, error) {
	return core.ApplyResult{}, nil
}
func (f *fakeProjects) PreviewSetPHP(_, version string) (core.ConfigDiff, error) {
	if version == "7.4" {
		return core.ConfigDiff{}, fmt.Errorf("php version %q is not registered: %w", version, core.ErrPHPVersionNotRegistered)
	}
	return core.ConfigDiff{Project: "app", Lines: []core.DiffLine{{Kind: core.DiffAdd, Text: "x"}}}, nil
}
func (f *fakeProjects) CurrentConfig(string) (string, error) { return "# conf", nil }
func (f *fakeProjects) Warnings(string) ([]core.Warning, error) {
	return []core.Warning{{Code: "app_url_mismatch", Message: "mismatch"}}, nil
}

type fakePHP struct{ removed string }

func (f *fakePHP) Register(context.Context, string) (core.PHPRegistration, error) {
	return core.PHPRegistration{Version: core.PHPVersion{Version: "8.3"}, PHPIni: "generated"}, nil
}
func (f *fakePHP) Remove(v string) error { f.removed = v; return nil }
func (f *fakePHP) Modules(context.Context, string) ([]string, error) {
	return []string{"curl"}, nil
}
func (f *fakePHP) Extensions(context.Context, string) (core.PHPExtensionInventory, error) {
	return core.PHPExtensionInventory{}, nil
}
func (f *fakePHP) SetExtensions(context.Context, string, map[string]bool) (core.SetExtensionsResult, error) {
	return core.SetExtensionsResult{}, nil
}

func (f *fakePHP) Scan(context.Context) (core.AutoRegisterResult, error) {
	return core.AutoRegisterResult{Root: "/logs/php"}, nil
}

type fakeApache struct{ restarted bool }

func (f *fakeApache) Status() (string, error)          { return "running", nil }
func (f *fakeApache) Restart() error                   { f.restarted = true; return nil }
func (f *fakeApache) TestConfig(context.Context) error { return nil }

func apiHandler(t *testing.T) (http.Handler, *fakeProjects) {
	t.Helper()
	cfg := config.Defaults(t.TempDir())
	cfg.DataDir = t.TempDir()
	doctor := core.Doctor{Runner: platform.ExecRunner{}, Files: platform.OSFileProbe{}, Ports: platform.OSPortProbe{}}
	application := app.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), doctor, platform.RealClock{})
	fp := &fakeProjects{reg: core.Registry{
		Settings:    core.Settings{ApacheService: "Apache2.4"},
		PHPVersions: []core.PHPVersion{{Version: "8.2", Path: `C:\php\8.2`}},
		Projects:    []core.Project{{Name: "app", URLPath: "/app", Type: core.ProjectLaravel, PHPVersion: "8.2", Enabled: true}},
	}}
	application.AttachServices(fp, &fakePHP{}, &fakeApache{})
	application.AttachRuntime(app.Runtime{}, &fakeProcesses{runOut: "migrated"}, &mapFiles{data: map[string][]byte{
		core.ProcessLogPath("/logs", "app", "queue"): []byte("log-baris-1\nlog-baris-2\n"),
	}}, "/logs")
	return NewHandler(application, slog.New(slog.NewTextHandler(io.Discard, nil))), fp
}

func call(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequestWithContext(context.Background(), method, path, rdr)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestOverviewAndList(t *testing.T) {
	h, _ := apiHandler(t)
	rec := call(t, h, http.MethodGet, "/api/v1/overview", "")
	if rec.Code != 200 {
		t.Fatalf("overview = %d (%s)", rec.Code, rec.Body.String())
	}
	var ov map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &ov)
	if ov["projects_total"] != float64(1) || ov["projects_active"] != float64(1) || ov["apache_status"] != "running" {
		t.Errorf("overview wrong: %v", ov)
	}
	rec = call(t, h, http.MethodGet, "/api/v1/projects", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"app"`) {
		t.Errorf("list = %d %s", rec.Code, rec.Body.String())
	}
}

func TestProjectMutations(t *testing.T) {
	h, fp := apiHandler(t)
	rec := call(t, h, http.MethodPost, "/api/v1/projects", `{"name":"baru","url_path":"/baru","fs_path":"C:/xampp/htdocs/baru","type":"php-native","php_version":"8.2","enabled":true}`)
	if rec.Code != http.StatusCreated || fp.added == nil || fp.added.Name != "baru" {
		t.Fatalf("add = %d %s added=%+v", rec.Code, rec.Body.String(), fp.added)
	}
	rec = call(t, h, http.MethodPost, "/api/v1/projects/app/set-php", `{"version":"8.3"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "health_checked") {
		t.Errorf("set-php = %d %s", rec.Code, rec.Body.String())
	}
	rec = call(t, h, http.MethodPost, "/api/v1/projects/app/enabled", `{"enabled":false}`)
	if rec.Code != 200 {
		t.Errorf("enabled = %d %s", rec.Code, rec.Body.String())
	}
	rec = call(t, h, http.MethodDelete, "/api/v1/projects/app", "")
	if rec.Code != 200 || fp.removed != "app" {
		t.Errorf("delete = %d removed=%q", rec.Code, fp.removed)
	}
	rec = call(t, h, http.MethodPost, "/api/v1/projects", `{rusak`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad JSON must be 400, got %d", rec.Code)
	}
}

func TestPreviewAndErrorMapping(t *testing.T) {
	h, fp := apiHandler(t)
	rec := call(t, h, http.MethodPost, "/api/v1/projects/app/preview-set-php", `{"version":"8.3"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"lines"`) {
		t.Errorf("preview = %d %s", rec.Code, rec.Body.String())
	}
	rec = call(t, h, http.MethodPost, "/api/v1/projects/app/preview-set-php", `{"version":"7.4"}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unregistered version preview must be 404, got %d (%s)", rec.Code, rec.Body.String())
	}
	fp.setPHPErr = fmt.Errorf("project %q not found: %w", "app", core.ErrProjectNotFound)
	rec = call(t, h, http.MethodPost, "/api/v1/projects/app/set-php", `{"version":"8.3"}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown project must be 404, got %d", rec.Code)
	}
	fp.setPHPErr = fmt.Errorf("apply failed at health check: rolled back")
	rec = call(t, h, http.MethodPost, "/api/v1/projects/app/set-php", `{"version":"8.3"}`)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("apply failure must be 502, got %d", rec.Code)
	}
}

func TestPHPAndApacheEndpoints(t *testing.T) {
	h, _ := apiHandler(t)
	rec := call(t, h, http.MethodPost, "/api/v1/php", `{"path":"C:/php/8.3"}`)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"8.3"`) {
		t.Errorf("register php = %d %s", rec.Code, rec.Body.String())
	}
	rec = call(t, h, http.MethodGet, "/api/v1/php/8.3/modules", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "curl") {
		t.Errorf("modules = %d %s", rec.Code, rec.Body.String())
	}
	rec = call(t, h, http.MethodPost, "/api/v1/apache/restart", "")
	if rec.Code != 200 {
		t.Errorf("apache restart = %d %s", rec.Code, rec.Body.String())
	}
	rec = call(t, h, http.MethodGet, "/api/v1/projects/app/warnings", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "app_url_mismatch") {
		t.Errorf("warnings = %d %s", rec.Code, rec.Body.String())
	}
	rec = call(t, h, http.MethodGet, "/api/v1/projects/app/config", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "# conf") {
		t.Errorf("config = %d %s", rec.Code, rec.Body.String())
	}
}

func TestStateEndpoints503WithoutServices(t *testing.T) {
	h := testHandler(t) // no AttachServices
	rec := call(t, h, http.MethodGet, "/api/v1/projects", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("without services, state API must be 503, got %d", rec.Code)
	}
	// Status/doctor keep working.
	if rec := call(t, h, http.MethodGet, "/api/v1/status", ""); rec.Code != 200 {
		t.Errorf("status must stay 200, got %d", rec.Code)
	}
}

// --- M4 fakes & tests ---

type fakeProcesses struct {
	started [2]string
	runOut  string
	err     error
}

func (f *fakeProcesses) Statuses() ([]core.ProcessStatus, error) {
	return []core.ProcessStatus{{Project: "app", Name: "queue", Command: "artisan queue:work", State: core.StateRunning, PID: 99}}, f.err
}
func (f *fakeProcesses) StartProcess(p, n string) error {
	f.started = [2]string{p, n}
	return f.err
}
func (f *fakeProcesses) StopProcess(_, _ string) error    { return f.err }
func (f *fakeProcesses) RestartProcess(_, _ string) error { return f.err }
func (f *fakeProcesses) RunOnce(_ context.Context, _ string, _ string) (string, int, error) {
	return f.runOut, 0, f.err
}
func (f *fakeProcesses) RunScheduleOnce(_ context.Context, _ string) (string, int, error) {
	return "sched-ok", 0, f.err
}

type mapFiles struct{ data map[string][]byte }

func (m *mapFiles) Exists(p string) bool           { _, ok := m.data[p]; return ok }
func (m *mapFiles) IsDir(string) bool              { return false }
func (m *mapFiles) EnsureWritableDir(string) error { return nil }
func (m *mapFiles) ReadFile(p string) ([]byte, error) {
	b, ok := m.data[p]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return b, nil
}
func (m *mapFiles) WriteFile(p string, b []byte) error       { m.data[p] = b; return nil }
func (m *mapFiles) WriteFileAtomic(p string, b []byte) error { m.data[p] = b; return nil }
func (m *mapFiles) MkdirAll(string) error                    { return nil }
func (m *mapFiles) Remove(p string) error                    { delete(m.data, p); return nil }
func (m *mapFiles) RemoveAll(string) error                   { return nil }
func (m *mapFiles) ListNames(string) ([]string, error)       { return nil, nil }

func TestProcessEndpoints(t *testing.T) {
	h, _ := apiHandler(t)
	rec := call(t, h, http.MethodGet, "/api/v1/processes", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"queue"`) {
		t.Fatalf("processes = %d %s", rec.Code, rec.Body.String())
	}
	rec = call(t, h, http.MethodPost, "/api/v1/projects/app/processes/queue/start", "")
	if rec.Code != 200 {
		t.Errorf("start = %d %s", rec.Code, rec.Body.String())
	}
	rec = call(t, h, http.MethodPost, "/api/v1/projects/app/run", `{"command":"artisan migrate:status"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "migrated") {
		t.Errorf("run = %d %s", rec.Code, rec.Body.String())
	}
	rec = call(t, h, http.MethodGet, "/api/v1/projects/app/processes/queue/log", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "log-baris") {
		t.Errorf("log = %d %s", rec.Code, rec.Body.String())
	}
	rec = call(t, h, http.MethodPost, "/api/v1/projects/app/schedule-run", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "sched-ok") {
		t.Errorf("schedule-run = %d %s", rec.Code, rec.Body.String())
	}
}

func TestPHPScanEndpoint(t *testing.T) {
	h, _ := apiHandler(t)
	rec := call(t, h, http.MethodPost, "/api/v1/php/scan", "")
	if rec.Code != 200 {
		t.Fatalf("scan = %d %s", rec.Code, rec.Body.String())
	}
	var res map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res["root"] != "/logs/php" {
		t.Errorf("scan root = %v, want /logs/php", res["root"])
	}
}
