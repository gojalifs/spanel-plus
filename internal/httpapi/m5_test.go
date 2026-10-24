package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gojalifs/spanel-plus/internal/app"
	"github.com/gojalifs/spanel-plus/internal/config"
	"github.com/gojalifs/spanel-plus/internal/core"
	"github.com/gojalifs/spanel-plus/internal/platform"
)

func authHandler(t *testing.T) (http.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Defaults(dir)
	cfg.DataDir = dir
	doctor := core.Doctor{Runner: platform.ExecRunner{}, Files: platform.OSFileProbe{}, Ports: platform.OSPortProbe{}}
	application := app.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), doctor, platform.RealClock{})
	auth := &core.AuthService{Files: platform.OSFileStore{}, Path: filepath.Join(dir, "admin.json")}
	if err := auth.SetPassword("rahasia-kuat-1"); err != nil {
		t.Fatal(err)
	}
	audit := &core.FileAuditLogger{Files: platform.OSFileStore{}, Path: filepath.Join(dir, "audit.log")}
	application.AttachAuth(auth, audit)
	application.AttachServices(&fakeProjects{}, &fakePHP{}, &fakeApache{})
	return NewHandler(application, slog.New(slog.NewTextHandler(io.Discard, nil))), dir
}

func TestAuthGateBlocksAndLoginOpens(t *testing.T) {
	h, dir := authHandler(t)

	// Protected endpoint without a session: 401 with a hint.
	rec := call(t, h, http.MethodGet, "/api/v1/overview", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("overview without login = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "auth_required") {
		t.Errorf("401 body must flag auth_required: %s", rec.Body.String())
	}

	// Wrong password stays out.
	rec = call(t, h, http.MethodPost, "/api/v1/auth/login", `{"password":"salah"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password login = %d, want 401", rec.Code)
	}

	// Right password sets the session cookie.
	rec = call(t, h, http.MethodPost, "/api/v1/auth/login", `{"password":"rahasia-kuat-1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	cookie := ""
	for _, c := range rec.Result().Cookies() {
		if c.Name == core.SessionCookieName {
			cookie = c.Value
		}
	}
	if cookie == "" {
		t.Fatal("login must set the session cookie")
	}

	// The same cookie opens the protected endpoint.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	req.AddCookie(&http.Cookie{Name: core.SessionCookieName, Value: cookie})
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("overview with session = %d (%s), want 200", rec2.Code, rec2.Body.String())
	}

	// A state change is audited.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/apache/restart", nil)
	req.AddCookie(&http.Cookie{Name: core.SessionCookieName, Value: cookie})
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req)
	if rec3.Code != http.StatusOK {
		t.Fatalf("apache restart with session = %d, want 200", rec3.Code)
	}
	raw, err := platform.OSFileStore{}.ReadFile(filepath.Join(dir, "audit.log"))
	if err != nil {
		t.Fatalf("read audit.log: %v", err)
	}
	if !strings.Contains(string(raw), "apache/restart") || !strings.Contains(string(raw), "auth/login") {
		t.Errorf("audit.log must record login + restart, got: %s", string(raw))
	}

	// Logout closes the session again.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: core.SessionCookieName, Value: cookie})
	rec4 := httptest.NewRecorder()
	h.ServeHTTP(rec4, req)
	if rec4.Code != http.StatusOK {
		t.Fatalf("logout = %d, want 200", rec4.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	req.AddCookie(&http.Cookie{Name: core.SessionCookieName, Value: cookie})
	rec5 := httptest.NewRecorder()
	h.ServeHTTP(rec5, req)
	if rec5.Code != http.StatusUnauthorized {
		t.Fatalf("overview after logout = %d, want 401", rec5.Code)
	}
}

func TestAuthStatusReportsSetupState(t *testing.T) {
	h, _ := authHandler(t)
	rec := call(t, h, http.MethodGet, "/api/v1/auth/status", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"configured":true`) {
		t.Fatalf("auth status = %d (%s), want configured:true", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"authenticated":false`) {
		t.Errorf("auth status without cookie must say authenticated:false: %s", rec.Body.String())
	}
}
