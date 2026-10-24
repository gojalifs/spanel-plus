package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- M5: doctor comprehensive checks ---

func TestDoctorApacheServiceStoppedFails(t *testing.T) {
	d := healthyDoctor()
	d.ApacheStatus = func(string) (string, error) { return "stopped", nil }
	rep := d.Run(context.Background(), DoctorInput{
		ApacheHome: `C:\m0\Apache24`, PanelPort: 8888, DataDir: `C:\panel\data`,
		ApacheService: "Apache2.4",
	})
	if rep.Healthy {
		t.Fatal("stopped Apache service must be unhealthy")
	}
	found := false
	for _, c := range rep.Checks {
		if c.Name == "apache-service" && c.Status == StatusFail && strings.Contains(c.Detail, "panel apache restart") {
			found = true
		}
	}
	if !found {
		t.Errorf("apache-service FAIL with recovery hint missing: %+v", rep.Checks)
	}
}

func TestDoctorApacheServiceRunningPasses(t *testing.T) {
	d := healthyDoctor()
	d.ApacheStatus = func(string) (string, error) { return "running", nil }
	rep := d.Run(context.Background(), DoctorInput{
		ApacheHome: `C:\m0\Apache24`, PanelPort: 8888, DataDir: `C:\panel\data`,
		ApacheService: "Apache2.4",
	})
	if !rep.Healthy {
		t.Fatalf("running Apache service must stay healthy, got %+v", rep.Checks)
	}
}

func TestDoctorRegistryChecks(t *testing.T) {
	home := `C:\m0\Apache24`
	phpDir := `C:\php\8.2`
	projDir := `C:\sites\app`
	d := healthyDoctor()
	d.Files = fakeFiles{
		dirs: map[string]bool{
			home: true, phpDir: true, projDir: true,
			projDir + "/public": true, projDir + "/storage": true,
		},
		files: map[string]bool{
			filepath.Join(home, "bin", "httpd.exe"):        true,
			filepath.Join(home, "modules", "mod_fcgid.so"): true,
			phpDir + "/php.exe":                            true,
			phpDir + "/php-cgi.exe":                        true,
			projDir + "/.env":                              true,
		},
	}
	reg := &Registry{
		PHPVersions: []PHPVersion{{Version: "8.2", Path: phpDir}},
		Projects: []Project{{Name: "app", URLPath: "/app", FSPath: projDir,
			Type: ProjectLaravel, PublicSubdir: "public", PHPVersion: "8.2", Enabled: true}},
	}
	rep := d.Run(context.Background(), DoctorInput{
		ApacheHome: home, PanelPort: 8888, DataDir: `C:\panel\data`, Registry: reg,
	})
	if !rep.Healthy {
		t.Fatalf("complete registry must stay healthy, got %+v", rep.Checks)
	}

	// Missing project folder fails loudly.
	reg2 := reg.Clone()
	reg2.Projects[0].FSPath = `C:\sites\hilang`
	rep = d.Run(context.Background(), DoctorInput{
		ApacheHome: home, PanelPort: 8888, DataDir: `C:\panel\data`, Registry: &reg2,
	})
	if rep.Healthy {
		t.Fatal("missing project folder must be unhealthy")
	}
	// Missing PHP binary fails loudly.
	reg3 := reg.Clone()
	reg3.PHPVersions[0].Path = `C:\php\kosong`
	rep = d.Run(context.Background(), DoctorInput{
		ApacheHome: home, PanelPort: 8888, DataDir: `C:\panel\data`, Registry: &reg3,
	})
	if rep.Healthy {
		t.Fatal("missing PHP folder must be unhealthy")
	}
}

// --- M5: auth ---

type m5MemStore struct {
	files map[string][]byte
}

func newMemStore() *m5MemStore                       { return &m5MemStore{files: map[string][]byte{}} }
func (m *m5MemStore) Exists(p string) bool           { _, ok := m.files[p]; return ok }
func (m *m5MemStore) IsDir(string) bool              { return false }
func (m *m5MemStore) EnsureWritableDir(string) error { return nil }
func (m *m5MemStore) ReadFile(p string) ([]byte, error) {
	b, ok := m.files[p]
	if !ok {
		return nil, errNotFound
	}
	return b, nil
}
func (m *m5MemStore) WriteFile(p string, data []byte) error {
	cp := append([]byte(nil), data...)
	m.files[p] = cp
	return nil
}
func (m *m5MemStore) WriteFileAtomic(p string, data []byte) error { return m.WriteFile(p, data) }
func (m *m5MemStore) MkdirAll(string) error                       { return nil }
func (m *m5MemStore) Remove(p string) error                       { delete(m.files, p); return nil }
func (m *m5MemStore) RemoveAll(p string) error                    { delete(m.files, p); return nil }
func (m *m5MemStore) ListNames(string) ([]string, error)          { return nil, nil }

type notFoundError struct{}

func (notFoundError) Error() string { return "not found" }

var errNotFound = notFoundError{}

type m5FixedClock struct{ t time.Time }

func (c m5FixedClock) Now() time.Time { return c.t }

func TestAuthLifecycle(t *testing.T) {
	auth := &AuthService{Files: newMemStore(), Path: "admin.json", Clock: m5FixedClock{t: time.Unix(1700000000, 0)}}
	if auth.Configured() {
		t.Fatal("fresh auth must be unconfigured")
	}
	if _, err := auth.Authenticate("whatever"); err != ErrAdminNotConfigured {
		t.Fatalf("login before setup = %v, want ErrAdminNotConfigured", err)
	}
	if err := auth.SetPassword("pendek"); err == nil {
		t.Fatal("short password must be refused")
	}
	if err := auth.SetPassword("rahasia-kuat-1"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if !auth.Configured() {
		t.Fatal("auth must be configured after SetPassword")
	}
	if _, err := auth.Authenticate("salah"); err != ErrBadCredentials {
		t.Fatalf("wrong password = %v, want ErrBadCredentials", err)
	}
	token, err := auth.Authenticate("rahasia-kuat-1")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if token == "" || !auth.ValidateSession(token) {
		t.Fatal("fresh session must validate")
	}
	if auth.ValidateSession("bukan-token") {
		t.Fatal("unknown token must not validate")
	}
	auth.Logout(token)
	if auth.ValidateSession(token) {
		t.Fatal("logged-out session must not validate")
	}
}

func TestAuthSessionExpires(t *testing.T) {
	clock := &mutableClock{t: time.Unix(1700000000, 0)}
	auth := &AuthService{Files: newMemStore(), Path: "admin.json", Clock: clock}
	if err := auth.SetPassword("rahasia-kuat-1"); err != nil {
		t.Fatal(err)
	}
	token, err := auth.Authenticate("rahasia-kuat-1")
	if err != nil {
		t.Fatal(err)
	}
	clock.t = clock.t.Add(SessionTTL + time.Minute)
	if auth.ValidateSession(token) {
		t.Fatal("expired session must not validate")
	}
}

type mutableClock struct{ t time.Time }

func (c *mutableClock) Now() time.Time { return c.t }

// --- M5: audit ---

func TestFileAuditLoggerAppendsLines(t *testing.T) {
	store := newMemStore()
	log := &FileAuditLogger{Files: store, Path: "audit.log", Clock: m5FixedClock{t: time.Unix(1700000000, 0)}}
	if err := log.Append(AuditEntry{Actor: "admin", Action: "POST /api/v1/apache/restart", Detail: "200"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := log.Append(AuditEntry{Actor: "admin", Action: "POST /api/v1/auth/login", Detail: "401"}); err != nil {
		t.Fatalf("Append 2: %v", err)
	}
	raw, _ := store.ReadFile("audit.log")
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("audit.log must hold 2 lines, got %d: %q", len(lines), string(raw))
	}
	if !strings.Contains(lines[0], "apache/restart") || !strings.Contains(lines[1], "auth/login") {
		t.Errorf("audit lines wrong: %q", string(raw))
	}
}
