package core

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ApacheServiceControl restarts and reports a Windows service by
// name — the managed Apache, in this package.
type ApacheServiceControl interface {
	Restart(name string) error
	Status(name string) (string, error)
}

// HealthResult is one probe's outcome. Status is the HTTP status when
// the server answered (0 otherwise); Err is the transport error when
// the server did not answer at all. Either one explains a failed check
// in the activity log — the old bool-only result hid exactly that
// (refused? 404? 500? timeout?) from the operator debugging a failed
// apply.
// HealthChecker probes one URL and reports the outcome in detail.
type HealthResult struct {
	OK     bool
	Status int
	Err    error
}

// Describe renders the failure detail for logs and error messages:
// "status 404", the transport error, or "no response".
func (r HealthResult) Describe() string {
	if r.Status != 0 {
		return fmt.Sprintf("status %d", r.Status)
	}
	if r.Err != nil {
		return r.Err.Error()
	}
	return "no response"
}

type HealthChecker interface {
	Check(url string) HealthResult
}

// ApplyConfig locates everything the apply pipeline touches.
type ApplyConfig struct {
	ApacheHome     string // folder with bin/httpd.exe
	ApacheService  string // Windows service name of the managed Apache
	RegistryPath   string // registry.json the pipeline commits at the end
	GeneratedDir   string // folder with the generated *.conf files
	BackupsDir     string // snapshot root (one subfolder per apply)
	HealthBaseURL  string // e.g. http://127.0.0.1 (Apache port is the site port)
	Retention      int    // snapshots kept (Plan F9: 20)
	HealthAttempts int
	HealthInterval time.Duration
}

// ApplyResult reports a committed apply.
type ApplyResult struct {
	BackupDir     string   `json:"backup_dir"`
	FilesWritten  []string `json:"files_written"`
	HealthChecked []string `json:"health_checked"`
}

// ApacheManager executes the one safe path for changing what Apache
// serves (Plan Bagian 6 & F9):
//
//	snapshot → write generated config → httpd -t → restart →
//	health check → commit registry
//
// Any failure after the snapshot restores the snapshot exactly and
// restarts Apache back onto it. A failed apply therefore changes
// nothing observable — and says so, loudly, in its error.
type ApacheManager struct {
	Runner  CommandRunner
	Files   FileStore
	Service ApacheServiceControl
	Health  HealthChecker
	Clock   Clock
	Sleep   func(time.Duration)
	Cfg     ApplyConfig
	Log     *slog.Logger // nil = no activity logging (direct CLI use)

	mu sync.Mutex // one apply at a time; config files are global state
}

// logInfo/logError are nil-safe: the manager logs only when a logger
// was wired in (serve/desktop do; direct CLI use does not).
func (m *ApacheManager) logInfo(msg string, args ...any) {
	if m.Log != nil {
		m.Log.Info(msg, args...)
	}
}

func (m *ApacheManager) logError(msg string, args ...any) {
	if m.Log != nil {
		m.Log.Error(msg, args...)
	}
}

// TestConfig runs httpd -t against the currently written config.
func (m *ApacheManager) TestConfig(ctx context.Context) error {
	httpd := filepath.Join(m.Cfg.ApacheHome, "bin", "httpd.exe")
	out, err := m.Runner.Run(ctx, httpd, "-t")
	if err != nil {
		return fmt.Errorf("httpd -t failed: %v (output: %s)", err, strings.TrimSpace(out))
	}
	return nil
}

// Status reports the managed Apache service state.
func (m *ApacheManager) Status() (string, error) {
	return m.Service.Status(m.Cfg.ApacheService)
}

// Restart restarts the managed Apache service (outside an apply).
func (m *ApacheManager) Restart() error {
	if err := m.Service.Restart(m.Cfg.ApacheService); err != nil {
		m.logError("apache restart failed", "service", m.Cfg.ApacheService, "err", err)
		return err
	}
	m.logInfo("apache restarted", "service", m.Cfg.ApacheService)
	return nil
}

// Apply moves Apache + registry to the candidate state, or leaves
// everything exactly as it was. healthPaths are URL paths (registry
// url_path values) that must answer after the restart.
func (m *ApacheManager) Apply(ctx context.Context, candidate Registry, healthPaths []string) (ApplyResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	names := make([]string, 0, len(candidate.Projects))
	for _, p := range candidate.Projects {
		names = append(names, p.Name)
	}
	m.logInfo("apply start", "projects", names, "health_paths", healthPaths)

	if err := candidate.ValidateStructure(); err != nil {
		return ApplyResult{}, err
	}
	generated, err := Generator{}.GenerateAll(candidate)
	if err != nil {
		return ApplyResult{}, err
	}
	files := make([]string, 0, len(generated))
	for name := range generated {
		files = append(files, name)
	}
	sort.Strings(files)

	snapshot, err := m.takeSnapshot()
	if err != nil {
		return ApplyResult{}, fmt.Errorf("apply aborted before any change (snapshot): %w", err)
	}
	m.logInfo("apply snapshot taken", "dir", snapshot.dir)

	fail := func(stage string, cause error) (ApplyResult, error) {
		rbErr := m.restoreSnapshot(snapshot)
		if rbErr != nil {
			m.logError("apply failed, rollback failed", "stage", stage, "err", cause, "rollback_err", rbErr, "snapshot", snapshot.dir)
			return ApplyResult{}, fmt.Errorf("apply failed at %s: %v — AND ROLLBACK FAILED: %v (snapshot kept at %s, restore manually)", stage, cause, rbErr, snapshot.dir)
		}
		if rErr := m.Service.Restart(m.Cfg.ApacheService); rErr != nil {
			m.logError("apply failed, apache restart after rollback failed", "stage", stage, "err", cause, "restart_err", rErr)
			return ApplyResult{}, fmt.Errorf("apply failed at %s: %v — config rolled back, but restarting Apache onto the old config failed: %v", stage, cause, rErr)
		}
		m.logError("apply failed, rolled back", "stage", stage, "err", cause)
		return ApplyResult{}, fmt.Errorf("apply failed at %s: %v — rolled back, previous config is live again", stage, cause)
	}

	if err := m.writeGenerated(generated); err != nil {
		return fail("write generated config", err)
	}
	m.logInfo("apply generated config written", "files", files)
	if err := m.TestConfig(ctx); err != nil {
		return fail("httpd -t", err)
	}
	m.logInfo("apply httpd -t ok")
	if err := m.Service.Restart(m.Cfg.ApacheService); err != nil {
		return fail("restart apache", err)
	}
	m.logInfo("apply apache restarted", "service", m.Cfg.ApacheService)
	for _, path := range healthPaths {
		if err := m.healthCheck(path); err != nil {
			return fail("health check "+path, err)
		}
	}

	// Commit point: only now does the registry move to the candidate.
	raw, err := candidate.Marshal()
	if err != nil {
		return fail("marshal registry", err)
	}
	if err := m.Files.WriteFileAtomic(m.Cfg.RegistryPath, raw); err != nil {
		return fail("commit registry", err)
	}
	m.pruneBackups()

	m.logInfo("apply committed", "files", files, "backup", snapshot.dir)
	return ApplyResult{BackupDir: snapshot.dir, FilesWritten: files, HealthChecked: healthPaths}, nil
}

// healthCheck polls one path with the configured attempt budget.
// Every attempt is logged with what the probe actually saw, so a
// failed apply leaves a trail that distinguishes "Apache belum
// dengar" (refused) from "config salah target" (404) and "aplikasi
// error" (500) — the distinction the old bool-only result erased.
func (m *ApacheManager) healthCheck(path string) error {
	url := strings.TrimSuffix(m.Cfg.HealthBaseURL, "/") + path
	var last HealthResult
	for i := 0; i < m.Cfg.HealthAttempts; i++ {
		last = m.Health.Check(url)
		if last.OK {
			m.logInfo("health check ok", "url", url, "attempt", i+1, "of", m.Cfg.HealthAttempts)
			return nil
		}
		m.logInfo("health check attempt failed", "url", url, "attempt", i+1, "of", m.Cfg.HealthAttempts, "detail", last.Describe())
		if i+1 < m.Cfg.HealthAttempts {
			m.Sleep(m.Cfg.HealthInterval)
		}
	}
	return fmt.Errorf("%s did not answer acceptably after %d attempts (last: %s)", url, m.Cfg.HealthAttempts, last.Describe())
}

// writeGenerated makes GeneratedDir match the generated set exactly:
// new/changed files written, files of removed/disabled projects
// deleted.
func (m *ApacheManager) writeGenerated(generated map[string]string) error {
	if err := m.Files.MkdirAll(m.Cfg.GeneratedDir); err != nil {
		return err
	}
	existing, err := m.Files.ListNames(m.Cfg.GeneratedDir)
	if err != nil {
		return err
	}
	for _, name := range existing {
		if _, keep := generated[name]; !keep && strings.HasSuffix(name, ".conf") {
			if err := m.Files.Remove(filepath.Join(m.Cfg.GeneratedDir, name)); err != nil {
				return err
			}
		}
	}
	names := make([]string, 0, len(generated))
	for name := range generated {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := m.Files.WriteFileAtomic(filepath.Join(m.Cfg.GeneratedDir, name), []byte(generated[name])); err != nil {
			return err
		}
	}
	return nil
}

// --- snapshots -------------------------------------------------------

type snapshot struct {
	dir           string
	registryBytes []byte // nil when no registry file existed
	registryHad   bool
	files         map[string][]byte // generated dir contents by name
}

// takeSnapshot copies the current registry + generated dir into
// BackupsDir/<timestamp>/ (and keeps an in-memory copy for rollback).
func (m *ApacheManager) takeSnapshot() (snapshot, error) {
	stamp := m.Clock.Now().Format("20060102-150405")
	dir := filepath.Join(m.Cfg.BackupsDir, stamp)
	// Two applies in the same second share a stamp; suffix until free.
	for i := 1; m.Files.Exists(dir); i++ {
		dir = filepath.Join(m.Cfg.BackupsDir, fmt.Sprintf("%s-%d", stamp, i))
	}
	snap := snapshot{dir: dir, files: map[string][]byte{}}

	if m.Files.Exists(m.Cfg.RegistryPath) {
		raw, err := m.Files.ReadFile(m.Cfg.RegistryPath)
		if err != nil {
			return snapshot{}, err
		}
		snap.registryBytes, snap.registryHad = raw, true
		if err := m.Files.WriteFile(filepath.Join(dir, "registry.json"), raw); err != nil {
			return snapshot{}, err
		}
	}
	names, err := m.Files.ListNames(m.Cfg.GeneratedDir)
	if err != nil {
		return snapshot{}, err
	}
	for _, name := range names {
		raw, err := m.Files.ReadFile(filepath.Join(m.Cfg.GeneratedDir, name))
		if err != nil {
			return snapshot{}, err
		}
		snap.files[name] = raw
		if err := m.Files.WriteFile(filepath.Join(dir, "generated", name), raw); err != nil {
			return snapshot{}, err
		}
	}
	return snap, nil
}

// restoreSnapshot puts registry + generated dir back to the snapshot.
func (m *ApacheManager) restoreSnapshot(snap snapshot) error {
	if snap.registryHad {
		if err := m.Files.WriteFileAtomic(m.Cfg.RegistryPath, snap.registryBytes); err != nil {
			return err
		}
	} else {
		if err := m.Files.Remove(m.Cfg.RegistryPath); err != nil {
			return err
		}
	}
	existing, err := m.Files.ListNames(m.Cfg.GeneratedDir)
	if err != nil {
		return err
	}
	for _, name := range existing {
		if _, keep := snap.files[name]; !keep {
			if err := m.Files.Remove(filepath.Join(m.Cfg.GeneratedDir, name)); err != nil {
				return err
			}
		}
	}
	for name, raw := range snap.files {
		if err := m.Files.WriteFileAtomic(filepath.Join(m.Cfg.GeneratedDir, name), raw); err != nil {
			return err
		}
	}
	return nil
}

// pruneBackups keeps the newest Cfg.Retention snapshot folders.
func (m *ApacheManager) pruneBackups() {
	if m.Cfg.Retention <= 0 {
		return
	}
	names, err := m.Files.ListNames(m.Cfg.BackupsDir)
	if err != nil {
		return
	}
	sort.Strings(names) // timestamp names sort chronologically
	for len(names) > m.Cfg.Retention {
		_ = m.Files.RemoveAll(filepath.Join(m.Cfg.BackupsDir, names[0]))
		names = names[1:]
	}
}
