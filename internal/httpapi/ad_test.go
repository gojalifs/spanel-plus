package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gojalifs/spanel-plus/internal/app"
	"github.com/gojalifs/spanel-plus/internal/config"
	"github.com/gojalifs/spanel-plus/internal/core"
	"github.com/gojalifs/spanel-plus/internal/platform"
)

// M5-AD end-to-end over HTTP with the dummy directory (siws.local /
// group IS): budi (IS) logs in and the audit records ad:budi; andi
// (HRD) is refused with the generic message; status advertises
// ad_enabled.
func adHandler(t *testing.T) (http.Handler, string) {
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
	budi, err := core.HashPassword("budi-kuat-1")
	if err != nil {
		t.Fatal(err)
	}
	andi, err := core.HashPassword("andi-kuat-1")
	if err != nil {
		t.Fatal(err)
	}
	adCfg := core.ADConfig{
		Enabled: true, Server: "dummy", Domain: "siws.local",
		BaseDN: "DC=siws,DC=local", GroupDN: "CN=IS,DC=siws,DC=local",
		DummyUsers: []core.DummyUser{
			{Username: "budi", PasswordHash: budi, Groups: []string{"IS"}},
			{Username: "andi", PasswordHash: andi, Groups: []string{"HRD"}},
		},
	}
	application.AttachAD(core.DummyAuthenticator{Users: adCfg.DummyUsers, GroupCN: adCfg.GroupCN()}, adCfg, nil)
	application.AttachServices(&fakeProjects{}, &fakePHP{}, &fakeApache{})
	return NewHandler(application, slog.New(slog.NewTextHandler(io.Discard, nil))), dir
}

func TestADLoginFlow(t *testing.T) {
	h, dir := adHandler(t)

	rec := call(t, h, http.MethodGet, "/api/v1/auth/status", "")
	if !strings.Contains(rec.Body.String(), `"ad_enabled":true`) {
		t.Fatalf("status must advertise ad_enabled, got: %s", rec.Body.String())
	}

	// Non-member: generic 401, no session cookie.
	rec = call(t, h, http.MethodPost, "/api/v1/auth/login", `{"method":"ad","username":"andi","password":"andi-kuat-1"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("non-member AD login = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "bukan anggota grup IT") {
		t.Errorf("AD refusal must stay generic: %s", rec.Body.String())
	}

	// Member: 200 + cookie, and the cookie opens protected endpoints.
	rec = call(t, h, http.MethodPost, "/api/v1/auth/login", `{"method":"ad","username":"budi","password":"budi-kuat-1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("AD member login = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	cookie := ""
	for _, c := range rec.Result().Cookies() {
		if c.Name == core.SessionCookieName {
			cookie = c.Value
			if c.MaxAge != int(core.SessionIdleTimeout.Seconds()) {
				t.Errorf("session cookie MaxAge = %d, want %d (10-minute sliding)", c.MaxAge, int(core.SessionIdleTimeout.Seconds()))
			}
		}
	}
	if cookie == "" {
		t.Fatal("AD login must set the session cookie")
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	req.AddCookie(&http.Cookie{Name: core.SessionCookieName, Value: cookie})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("overview with AD session = %d, want 200", rec.Code)
	}

	// The audit trail names the directory identity, not "admin".
	raw, err := os.ReadFile(filepath.Join(dir, "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "ad:budi") {
		t.Errorf("audit.log must record ad:budi, got: %s", string(raw))
	}
}
