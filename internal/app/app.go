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
}

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
	}
}

// Doctor runs the environment checks against this app's config.
// Because this is called while the panel itself serves the request,
// the panel-port check is told the listener is us (WARN, not FAIL).
func (a *Application) Doctor(ctx context.Context) core.Report {
	in := a.DoctorInput()
	in.PanelPortSelf = true
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
	}
}

// Run serves the HTTP API/UI until ctx is cancelled, then shuts down
// gracefully. A bind failure returns immediately (fail loud) instead of
// announcing a panel that is not actually listening.
func (a *Application) Run(ctx context.Context, handler http.Handler) error {
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
