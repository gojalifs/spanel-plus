package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gojalifs/spanel-plus/internal/config"
	"github.com/gojalifs/spanel-plus/internal/core"
)

// M5: state commands are API clients. These tests run the real
// httpAPIClient against a stub panel server, so the CLI's request
// shapes and error mapping are proven, not just its flag parsing.

type memTokens struct{ token string }

func (m *memTokens) Load() (string, error) { return m.token, nil }
func (m *memTokens) Save(t string) error   { m.token = t; return nil }
func (m *memTokens) Clear() error          { m.token = ""; return nil }

// stubPanel records requests and answers canned panel JSON. When
// requireAuth is set, requests without the session cookie get a 401.
type stubPanel struct {
	lastMethod, lastPath string
	lastBody             string
	status               int
	body                 string
	requireAuth          bool
}

func (s *stubPanel) handler(w http.ResponseWriter, r *http.Request) {
	s.lastMethod, s.lastPath = r.Method, r.URL.Path
	buf := make([]byte, 1<<20)
	n, _ := r.Body.Read(buf)
	s.lastBody = string(buf[:n])
	if s.requireAuth {
		ck, err := r.Cookie(core.SessionCookieName)
		if err != nil || ck.Value == "" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"belum login","auth_required":true}`))
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(s.status)
	_, _ = w.Write([]byte(s.body))
}

func apiTestDeps(t *testing.T, stub *stubPanel) (Deps, *httptest.Server, *memTokens) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(stub.handler))
	tokens := &memTokens{token: "tok"}
	deps, _, _ := testDeps()
	deps.NewAPI = func(config.Config) APIClient {
		return NewHTTPAPIClient(srv.URL, tokens, nil)
	}
	return deps, srv, tokens
}

func TestProjectListViaAPI(t *testing.T) {
	stub := &stubPanel{status: http.StatusOK,
		body: `{"settings":{},"php_versions":[],"projects":[{"name":"web-app","url_path":"/web-app","type":"laravel","php_version":"8.2","enabled":true}]}`}
	deps, srv, _ := apiTestDeps(t, stub)
	defer srv.Close()
	deps2, out, _ := testDeps()
	deps2.NewAPI = deps.NewAPI
	if code := New(deps2).Run([]string{"project", "list"}); code != 0 {
		t.Fatalf("project list exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "web-app") || !strings.Contains(out.String(), "/web-app") {
		t.Errorf("project list must show projects, got: %q", out.String())
	}
	if stub.lastPath != "/api/v1/projects" || stub.lastMethod != http.MethodGet {
		t.Errorf("project list must GET /api/v1/projects, got %s %s", stub.lastMethod, stub.lastPath)
	}
}

func TestProjectAddParsesFlagsViaAPI(t *testing.T) {
	stub := &stubPanel{status: http.StatusCreated, body: `{"apply":{"backup_dir":"bk","files_written":["x.conf"]}}`}
	deps, srv, _ := apiTestDeps(t, stub)
	defer srv.Close()
	code := New(deps).Run([]string{"project", "add",
		"--name", "alat", "--url", "/alat", "--path", `D:\apps\alat`,
		"--type", "php-native", "--php", "8.3"})
	if code != 0 {
		t.Fatalf("project add exit = %d, want 0", code)
	}
	if stub.lastPath != "/api/v1/projects" {
		t.Errorf("project add must POST /api/v1/projects, got %s", stub.lastPath)
	}
	for _, want := range []string{`"name":"alat"`, `"url_path":"/alat"`, `"type":"php-native"`, `"php_version":"8.3"`} {
		if !strings.Contains(stub.lastBody, want) {
			t.Errorf("project add body must contain %s, got %s", want, stub.lastBody)
		}
	}

	// --disabled round-trips as enabled:false.
	stub.body = `{"apply":{"backup_dir":"bk","files_written":[]}}`
	code = New(deps).Run([]string{"project", "add",
		"--name", "x", "--url", "/x", "--path", `C:\x`, "--type", "static", "--disabled"})
	if code != 0 {
		t.Fatalf("project add --disabled exit = %d, want 0", code)
	}
	if !strings.Contains(stub.lastBody, `"enabled":false`) {
		t.Errorf("--disabled must send enabled:false, got %s", stub.lastBody)
	}
}

func TestProjectCommandsUsageAndErrors(t *testing.T) {
	stub := &stubPanel{status: http.StatusOK, body: `{"apply":{"backup_dir":"bk","files_written":[]}}`}
	deps, srv, _ := apiTestDeps(t, stub)
	defer srv.Close()
	if code := New(deps).Run([]string{"project", "set-php", "satu"}); code != 2 {
		t.Errorf("set-php with missing args exit = %d, want 2", code)
	}
	if code := New(deps).Run([]string{"project", "ngawur"}); code != 2 {
		t.Errorf("unknown project subcommand exit = %d, want 2", code)
	}
	// A failed apply from the panel surfaces its message and exits 1.
	stub.status = http.StatusBadGateway
	stub.body = `{"error":"apply failed at httpd -t: bad — rolled back, previous config is live again"}`
	deps2, _, errOut := testDeps()
	deps2.NewAPI = deps.NewAPI
	if code := New(deps2).Run([]string{"project", "set-php", "web-app", "8.3"}); code != 1 {
		t.Errorf("failing apply exit = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "rolled back") {
		t.Errorf("failing apply must surface the panel message, got: %q", errOut.String())
	}
}

func TestPHPAndApacheCommandsViaAPI(t *testing.T) {
	stub := &stubPanel{status: http.StatusOK,
		body: `{"settings":{},"php_versions":[{"version":"8.2","path":"C:\\php\\8.2"}],"projects":[]}`}
	deps, srv, _ := apiTestDeps(t, stub)
	defer srv.Close()
	deps2, out, _ := testDeps()
	deps2.NewAPI = deps.NewAPI
	if code := New(deps2).Run([]string{"php", "list"}); code != 0 {
		t.Fatalf("php list exit = %d", code)
	}
	if !strings.Contains(out.String(), "8.2") {
		t.Errorf("php list must show versions, got: %q", out.String())
	}

	// php add probes through the API and reports the detected build.
	stub.body = `{"version":{"version":"8.3","path":"C:\\php\\8.3"},"full_version":"8.3.13","nts":true,"arch":"x64","php_ini":"generated"}`
	out.Reset()
	if code := New(deps2).Run([]string{"php", "add", "--path", `C:\php\8.3`}); code != 0 {
		t.Fatalf("php add exit = %d", code)
	}
	if !strings.Contains(out.String(), "8.3.13") || !strings.Contains(out.String(), "NTS") {
		t.Errorf("php add must report the detected build, got: %q", out.String())
	}

	// apache test/status/restart hit their API routes.
	stub.body = `{"status":"running"}`
	if code := New(deps2).Run([]string{"apache", "status"}); code != 0 {
		t.Errorf("apache status exit = %d, want 0", code)
	}
	stub.body = `{"restarted":"ok"}`
	if code := New(deps2).Run([]string{"apache", "restart"}); code != 0 {
		t.Errorf("apache restart exit = %d, want 0", code)
	}
	if stub.lastPath != "/api/v1/apache/restart" {
		t.Errorf("apache restart must POST /api/v1/apache/restart, got %s", stub.lastPath)
	}
}

func TestProcessCommandsViaAPI(t *testing.T) {
	stub := &stubPanel{status: http.StatusOK,
		body: `{"processes":[{"project":"app","name":"queue","state":"running","pid":123,"restarts":2,"command":"artisan queue:work"}]}`}
	deps, srv, _ := apiTestDeps(t, stub)
	defer srv.Close()
	deps2, out, _ := testDeps()
	deps2.NewAPI = deps.NewAPI
	if code := New(deps2).Run([]string{"process", "list"}); code != 0 {
		t.Fatalf("process list exit = %d", code)
	}
	if !strings.Contains(out.String(), "queue") || !strings.Contains(out.String(), "123") {
		t.Errorf("process list must show supervisor statuses, got: %q", out.String())
	}
	stub.body = `{"ok":"true"}`
	if code := New(deps2).Run([]string{"process", "stop", "app", "queue"}); code != 0 {
		t.Errorf("process stop exit = %d, want 0", code)
	}
	if stub.lastPath != "/api/v1/projects/app/processes/queue/stop" {
		t.Errorf("process stop must hit the API route, got %s", stub.lastPath)
	}
}

func TestAPIUnauthenticatedHintsLogin(t *testing.T) {
	stub := &stubPanel{status: http.StatusOK, body: `{}`, requireAuth: true}
	deps, srv, tokens := apiTestDeps(t, stub)
	defer srv.Close()
	tokens.token = "" // no session cookie sent
	deps2, _, errOut := testDeps()
	deps2.NewAPI = deps.NewAPI
	if code := New(deps2).Run([]string{"project", "list"}); code != 1 {
		t.Fatalf("unauthenticated project list exit = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "panel login") {
		t.Errorf("401 must point at 'panel login', got: %q", errOut.String())
	}
}

func TestAPIPanelDownFailsLoud(t *testing.T) {
	stub := &stubPanel{status: http.StatusOK, body: `{}`}
	deps, srv, _ := apiTestDeps(t, stub)
	srv.Close() // panel gone before the command runs
	deps2, _, errOut := testDeps()
	deps2.NewAPI = deps.NewAPI
	if code := New(deps2).Run([]string{"project", "list"}); code != 1 {
		t.Fatalf("panel-down project list exit = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "panel tidak menjawab") {
		t.Errorf("panel-down must say so and how to start it, got: %q", errOut.String())
	}
}

func TestAdminSetPassword(t *testing.T) {
	deps, _, _ := testDeps()
	deps.Stdin = strings.NewReader("rahasia-kuat-1\nrahasia-kuat-1\n")
	auth := &core.AuthService{Files: newCLIMemStore(), Path: "admin.json"}
	deps.NewAuth = func(config.Config) *core.AuthService { return auth }
	deps.NewAudit = func(config.Config) core.AuditLogger { return core.DiscardAuditLogger{} }
	if code := New(deps).Run([]string{"admin", "set-password"}); code != 0 {
		t.Fatalf("admin set-password exit = %d, want 0", code)
	}
	if !auth.Configured() {
		t.Fatal("admin must be configured after set-password")
	}
	if _, err := auth.Authenticate("rahasia-kuat-1"); err != nil {
		t.Fatalf("new password must authenticate: %v", err)
	}

	// Mismatched confirmation refuses without touching the account.
	deps.Stdin = strings.NewReader("satu\ndua\n")
	if code := New(deps).Run([]string{"admin", "set-password"}); code != 1 {
		t.Errorf("mismatched passwords exit = %d, want 1", code)
	}
	if _, err := auth.Authenticate("rahasia-kuat-1"); err != nil {
		t.Error("mismatched set-password must not replace the stored password")
	}
}

func TestLoginStoresSessionAndLogoutClears(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/login" {
			http.SetCookie(w, &http.Cookie{Name: core.SessionCookieName, Value: "tok123", Path: "/"})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"authenticated":true}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":"true"}`))
	}))
	defer srv.Close()
	tokens := &memTokens{}
	deps, out, _ := testDeps()
	deps.NewAPI = func(config.Config) APIClient { return NewHTTPAPIClient(srv.URL, tokens, nil) }
	deps.Stdin = strings.NewReader("rahasia-kuat-1\n")
	if code := New(deps).Run([]string{"login"}); code != 0 {
		t.Fatalf("login exit = %d, want 0", code)
	}
	if tokens.token != "tok123" {
		t.Errorf("login must store the session token, got %q", tokens.token)
	}
	if !strings.Contains(out.String(), "Login berhasil") {
		t.Errorf("login output must confirm, got: %q", out.String())
	}
	if code := New(deps).Run([]string{"logout"}); code != 0 {
		t.Fatalf("logout exit = %d, want 0", code)
	}
	if tokens.token != "" {
		t.Errorf("logout must clear the session token, got %q", tokens.token)
	}
}

func TestProjectUpdateMergesFlagsOverStored(t *testing.T) {
	var putBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"settings":{},"php_versions":[],"projects":[{"name":"app","url_path":"/app","fs_path":"C:\\sites\\app","type":"laravel","public_subdir":"public","php_version":"8.2","enabled":true}]}`))
			return
		}
		buf := make([]byte, 1<<20)
		n, _ := r.Body.Read(buf)
		putBody = string(buf[:n])
		_, _ = w.Write([]byte(`{"apply":{"backup_dir":"bk","files_written":[]}}`))
	}))
	defer srv.Close()
	deps, _, _ := testDeps()
	deps.NewAPI = func(config.Config) APIClient { return NewHTTPAPIClient(srv.URL, &memTokens{token: "tok"}, nil) }
	if code := New(deps).Run([]string{"project", "update", "app", "--php", "8.3"}); code != 0 {
		t.Fatalf("project update exit = %d, want 0", code)
	}
	// Only --php changed; url/path/type/public survive from storage.
	for _, want := range []string{`"php_version":"8.3"`, `"url_path":"/app"`, `"type":"laravel"`, `"public_subdir":"public"`} {
		if !strings.Contains(putBody, want) {
			t.Errorf("update body must contain %s, got %s", want, putBody)
		}
	}
}

// newCLIMemStore is a tiny in-memory FileStore for the admin test.
type cliMemStore struct{ files map[string][]byte }

func newCLIMemStore() *cliMemStore                    { return &cliMemStore{files: map[string][]byte{}} }
func (m *cliMemStore) Exists(p string) bool           { _, ok := m.files[p]; return ok }
func (m *cliMemStore) IsDir(string) bool              { return false }
func (m *cliMemStore) EnsureWritableDir(string) error { return nil }
func (m *cliMemStore) ReadFile(p string) ([]byte, error) {
	b, ok := m.files[p]
	if !ok {
		return nil, errCLIFileMissing
	}
	return b, nil
}
func (m *cliMemStore) WriteFile(p string, data []byte) error {
	m.files[p] = append([]byte(nil), data...)
	return nil
}
func (m *cliMemStore) WriteFileAtomic(p string, data []byte) error { return m.WriteFile(p, data) }
func (m *cliMemStore) MkdirAll(string) error                       { return nil }
func (m *cliMemStore) Remove(p string) error                       { delete(m.files, p); return nil }
func (m *cliMemStore) RemoveAll(p string) error                    { delete(m.files, p); return nil }
func (m *cliMemStore) ListNames(string) ([]string, error)          { return nil, nil }

type cliFileMissingError struct{}

func (cliFileMissingError) Error() string { return "not found" }

var errCLIFileMissing = cliFileMissingError{}
