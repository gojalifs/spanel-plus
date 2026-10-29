// Package app wires the core services, configuration and HTTP API
// together and owns the panel's run lifecycle. It is the single
// composition root used by both `panel serve` (console and Windows
// Service) and the API handlers — one brain, two faces (Plan 4.1).
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/gojalifs/spanel-plus/internal/buildinfo"
	"github.com/gojalifs/spanel-plus/internal/config"
	"github.com/gojalifs/spanel-plus/internal/core"
)

// Application is the running panel.
type Application struct {
	cfg     config.Config
	logger  *slog.Logger
	doctor  core.Doctor
	clock   core.Clock
	started time.Time

	projects   ProjectOps
	php        PHPOps
	apache     ApacheOps
	apachePort int // site port detected from httpd.conf (0 = undetected)
	processes  ProcessOps
	runtime    Runtime
	logFiles   core.FileStore
	logDir     string
	auth       *core.AuthService
	audit      core.AuditLogger
	ad         core.Authenticator // nil = directory login unavailable
	adConfig   core.ADConfig
	adErr      error // construction/validation error: AD login answers 503, local stays up
}

// AttachAD wires the directory authenticator (M5-AD). When the
// authenticator failed to build (bad config, unreadable CA file),
// adErr is kept so the AD login endpoint can explain itself — the
// local account is never taken down by an AD misconfiguration.
func (a *Application) AttachAD(auth core.Authenticator, cfg core.ADConfig, err error) {
	a.ad, a.adConfig, a.adErr = auth, cfg, err
}

// AD returns the directory authenticator (or nil), its config, and
// any build error.
func (a *Application) AD() (core.Authenticator, core.ADConfig, error) {
	return a.ad, a.adConfig, a.adErr
}

// AttachAuth wires the M5 admin auth service and audit sink. When no
// auth is attached (tests, degraded mode), the API stays open —
// production wiring in main always attaches both.
func (a *Application) AttachAuth(auth *core.AuthService, audit core.AuditLogger) {
	a.auth, a.audit = auth, audit
}

// Auth returns the attached auth service, or nil.
func (a *Application) Auth() *core.AuthService { return a.auth }

// Audit returns the attached audit sink, or a discarding one.
func (a *Application) Audit() core.AuditLogger {
	if a.audit == nil {
		return core.DiscardAuditLogger{}
	}
	return a.audit
}

// AttachRuntime wires the supervisor/scheduler runtime and the
// process service the API controls (M4). The runtime starts and
// stops with Application.Run.
func (a *Application) AttachRuntime(rt Runtime, processes ProcessOps, logFiles core.FileStore, logDir string) {
	a.runtime, a.processes, a.logFiles, a.logDir = rt, processes, logFiles, logDir
}

// Processes returns the attached process service, or nil.
func (a *Application) Processes() ProcessOps { return a.processes }

// LogFiles returns the store used to tail process logs, or nil.
func (a *Application) LogFiles() core.FileStore { return a.logFiles }

// ProcessLogDir returns the panel's per-project log root.
func (a *Application) ProcessLogDir() string { return a.logDir }

// AttachServices wires the state-changing services (registry, PHP
// manager, Apache manager) into the application so the HTTP API can
// reach them. Without it, the state endpoints answer 503 — the panel
// still serves status/doctor (e.g. when panel.json is broken).
func (a *Application) AttachServices(projects ProjectOps, php PHPOps, apache ApacheOps) {
	a.projects, a.php, a.apache = projects, php, apache
}

// Projects returns the attached project service, or nil.
// AttachApachePort records the Apache site port detected from
// httpd.conf at startup (see core.DetectApachePort). Status
// exposes it so the dashboard builds project links with the right
// port; zero means undetected and Status reports 80 then, matching
// the historic default.
func (a *Application) AttachApachePort(port int) {
	a.apachePort = port
}

func (a *Application) Projects() ProjectOps { return a.projects }

// PHP returns the attached PHP manager, or nil.
func (a *Application) PHP() PHPOps { return a.php }

// Apache returns the attached Apache manager, or nil.
func (a *Application) Apache() ApacheOps { return a.apache }

// New builds an Application from fully-resolved dependencies.
func New(cfg config.Config, logger *slog.Logger, doctor core.Doctor, clock core.Clock) *Application {
	return &Application{
		cfg:     cfg,
		logger:  logger,
		doctor:  doctor,
		clock:   clock,
		started: clock.Now(),
	}
}

// Status is the JSON shape of GET /api/v1/status.
type Status struct {
	Name          string `json:"name"`
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	PanelPort     int    `json:"panel_port"`
	UptimeSeconds int64  `json:"uptime_seconds"`
	ApacheHome    string `json:"apache_home"` // "" = auto-discover (see doctor)
	ApacheService string `json:"apache_service"`
	ApachePort    int    `json:"apache_port"` // site port from httpd.conf Listen
}

// Status reports the panel's current state.
func (a *Application) Status() Status {
	apacheHome := a.cfg.ApacheHome
	if apacheHome == "" {
		apacheHome = "(auto-discover)"
	}
	return Status{
		Name:          buildinfo.Name,
		Version:       buildinfo.Version,
		Commit:        buildinfo.Commit,
		PanelPort:     a.cfg.Port,
		UptimeSeconds: int64(a.clock.Now().Sub(a.started).Seconds()),
		ApacheHome:    apacheHome,
		ApacheService: a.cfg.ApacheService,
		ApachePort:    a.sitePort(),
	}
}

// sitePort is the Apache site port for links and status: the
// detected port, or 80 when nothing was attached (tests, old
// wiring) — the historic default.
func (a *Application) sitePort() int {
	if a.apachePort == 0 {
		return core.DefaultApachePort
	}
	return a.apachePort
}

// Doctor runs the environment checks against this app's config.
// Because this is called while the panel itself serves the request,
// the panel-port check is told the listener is us (WARN, not FAIL).
// The registry (when loadable) feeds the M5 PHP/project checks; a
// load failure only skips those checks and is logged, never hidden
// behind a healthy report.
func (a *Application) Doctor(ctx context.Context) core.Report {
	in := a.DoctorInput()
	in.PanelPortSelf = true
	if a.projects != nil {
		if reg, err := a.projects.List(); err == nil {
			in.Registry = &reg
		} else {
			a.logger.Warn("doctor: registry unreadable, skipping registry checks", "err", err)
		}
	}
	return a.doctor.Run(ctx, in)
}

// DoctorInput exposes the doctor input derived from the config, so the
// CLI `doctor` command checks exactly what `serve` would check.
func (a *Application) DoctorInput() core.DoctorInput {
	return core.DoctorInput{
		ApacheHome: a.cfg.ApacheHome,
		Candidates: config.DefaultApacheCandidates(),
		PanelPort:  a.cfg.Port,
		DataDir:    a.cfg.DataDir,
		ADEnabled:  a.cfg.ADEnabled,
		ADServer:   a.cfg.ADServer,
		ADDomain:   a.cfg.ADDomain,
		ADBaseDN:   a.cfg.ADBaseDN,
		ADGroupDN:  a.cfg.ADGroupDN,
	}
}

// Run serves the HTTP API/UI until ctx is cancelled, then shuts down
// gracefully. A bind failure returns immediately (fail loud) instead of
// announcing a panel that is not actually listening. The M4 runtime
// (supervisor + scheduler) runs alongside and is stopped first, so no
// managed process outlives the panel.
func (a *Application) Run(ctx context.Context, handler http.Handler) error {
	if a.runtime.Supervisor != nil {
		if err := a.runtime.Supervisor.Start(ctx); err != nil {
			// Not fatal: the panel must stay reachable to fix the
			// registry; the failure is loud in the log.
			a.logger.Error("supervisor start failed", "err", err)
		}
		defer a.runtime.Supervisor.StopAll()
	}
	if a.runtime.Scheduler != nil {
		go a.runtime.Scheduler.Run(ctx)
	}
	addr := fmt.Sprintf(":%d", a.cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	srv := &http.Server{Handler: handler}

	errCh := make(chan error, 1)
	go func() {
		a.logger.Info("panel listening", "addr", addr, "version", buildinfo.Version)
		errCh <- srv.Serve(ln)
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		a.logger.Info("panel stopping", "reason", ctx.Err().Error())
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		return nil
	}
}
