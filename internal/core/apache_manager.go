package core

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// ApacheServiceControl restarts and reports a Windows service by
// name — the managed Apache, in this package.
type ApacheServiceControl interface {
	Restart(name string) error
	Status(name string) (string, error)
}

// ApplyConfig locates everything the apply pipeline touches.
type ApplyConfig struct {
	ApacheHome    string // folder with bin/httpd.exe
	ApacheService string // Windows service name of the managed Apache
	RegistryPath  string // registry.json the pipeline commits at the end
	GeneratedDir  string // folder with the generated *.conf files
	BackupsDir    string // snapshot root (one subfolder per apply)
	Retention     int    // snapshots kept (Plan F9: 20)
	// ApacheLogDir is where Apache writes access + error logs
	// (<DataDir>/logs/apache). Empty disables the log directives
	// and the log view — Apache then keeps its own default logs.
	ApacheLogDir string
	// TLS feeds the :443 VirtualHost. TLSDir is the resolved material
	// dir; TLSApache false means no vhost even when material exists
	// (dashboard HTTPS only); TLSApachePort zero means 443.
	TLSDir        string
	TLSApache     bool
	TLSApachePort int
}

// TLSVHostForApply resolves the generator's TLS vhost for one apply:
// enabled only when Apache TLS is on AND valid material exists.
// Present-but-broken material fails the apply BEFORE anything is
// written — a vhost pointing at broken certs would fail httpd -t
// after the restart anyway, but the early error names TLS directly.
func (m *ApacheManager) TLSVHostForApply() (TLSVHost, error) {
	if !m.Cfg.TLSApache {
		return TLSVHost{}, nil
	}
	dir := m.Cfg.TLSDir
	if dir == "" || !HasTLSMaterial(dir) {
		return TLSVHost{}, nil
	}
	if err := ValidateMaterial(dir); err != nil {
		return TLSVHost{}, fmt.Errorf("apply dibatalkan: material TLS rusak: %w", err)
	}
	_, certPath, keyPath, _ := TLSPaths(dir)
	port := m.Cfg.TLSApachePort
	if port == 0 {
		port = 443
	}
	return TLSVHost{Enabled: true, Port: port, CertFile: certPath, KeyFile: keyPath}, nil
}

// ApplyResult reports a committed apply.
type ApplyResult struct {
	BackupDir    string   `json:"backup_dir"`
	FilesWritten []string `json:"files_written"`
}

// ApacheManager executes the one safe path for changing what Apache
// serves (Plan Bagian 6 & F9):
//
//	snapshot → write generated config → httpd -t → restart →
//	commit registry
//
// Any failure after the snapshot restores the snapshot exactly and
// restarts Apache back onto it. A failed apply therefore changes
// nothing observable — and says so, loudly, in its error.
//
// NOTE (2026-11-02, operator decision): the pipeline used to end with
// a health-check gate — the new config had to answer HTTP < 400 before
// the registry committed. That gate kept failing on application-side
// refusals that are not config errors (a browser-version gate answering
// 426, a team's auth filter answering 403), rolling back a correct
// config and deleting the very files needed to diagnose it. The gate
// is gone: what guards an apply now is httpd -t plus a successful
// restart. Whether the application itself answers is the operator's
// check in the browser, not the panel's. The prober survives as a
// standalone utility in internal/platform/health.go for future manual
// diagnostics; nothing in the pipeline calls it anymore.
type ApacheManager struct {
	Runner  CommandRunner
	Files   FileStore
	Service ApacheServiceControl
	Clock   Clock
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
// everything exactly as it was.
func (m *ApacheManager) Apply(ctx context.Context, candidate Registry) (ApplyResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	names := make([]string, 0, len(candidate.Projects))
	for _, p := range candidate.Projects {
		names = append(names, p.Name)
	}
	m.logInfo("apply start", "projects", names)

	if err := candidate.ValidateStructure(); err != nil {
		return ApplyResult{}, err
	}
	vhost, err := m.TLSVHostForApply()
	if err != nil {
		return ApplyResult{}, err
	}
	logCfg := m.ApacheLogConfigForApply()
	generated, err := Generator{TLS: vhost, ApacheLogs: logCfg}.GenerateAll(candidate)
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

	// rotatelogs does not create its target dir: make sure it
	// exists before httpd -t / restart, or Apache fails to open
	// the log and the apply rolls back for a logging reason.
	if logCfg.Dir != "" {
		if err := m.Files.MkdirAll(m.Cfg.ApacheLogDir); err != nil {
			return ApplyResult{}, fmt.Errorf("apply aborted before any change (create apache log dir): %w", err)
		}
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

	// Commit point: only now does the registry move to the candidate.
	raw, err := candidate.Marshal()
	if err != nil {
		return fail("marshal registry", err)
	}
	if err := m.Files.WriteFileAtomic(m.Cfg.RegistryPath, raw); err != nil {
		return fail("commit registry", err)
	}
	m.pruneBackups()
	// Best effort: old dated Apache logs (90+ days) go now, while
	// the apply already succeeded — pruning never fails an apply.
	PruneApacheLogs(m.Files, m.Cfg.ApacheLogDir, ApacheLogRetentionDays)

	m.logInfo("apply committed", "files", files, "backup", snapshot.dir)
	return ApplyResult{BackupDir: snapshot.dir, FilesWritten: files}, nil
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
