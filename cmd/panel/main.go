// Command panel is the single SPanel+ binary (Plan D6): the same
// executable runs as a console program, as a Windows Service
// (`panel serve` started by the SCM), and as the CLI tool.
// main only wires dependencies together; all logic lives in
// internal/core, internal/app, internal/cli and internal/httpapi.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/gojalifs/spanel-plus/internal/app"
	"github.com/gojalifs/spanel-plus/internal/buildinfo"
	"github.com/gojalifs/spanel-plus/internal/cli"
	"github.com/gojalifs/spanel-plus/internal/config"
	"github.com/gojalifs/spanel-plus/internal/core"
	"github.com/gojalifs/spanel-plus/internal/desktop"
	"github.com/gojalifs/spanel-plus/internal/httpapi"
	"github.com/gojalifs/spanel-plus/internal/logging"
	"github.com/gojalifs/spanel-plus/internal/platform"
)

func main() {
	os.Exit(run())
}

func run() int {
	// All panel files live next to the binary (Plan D9): config,
	// data, logs. The binary's folder is the one stable anchor a
	// Windows Service has — its working directory is System32.
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: locate own executable: %v\n", err)
		return 1
	}
	baseDir := filepath.Dir(exe)

	doctor := core.Doctor{
		Runner: platform.ExecRunner{},
		Files:  platform.OSFileProbe{},
		Ports:  platform.OSPortProbe{},
	}
	clock := platform.RealClock{}

	// M2 wiring: registry + apply pipeline. Built from the same
	// config the commands load; when the config is broken, commands
	// get the config error itself instead of a half-wired pipeline.
	var projectOps cli.ProjectOps
	var apacheOps cli.ApacheOps
	if wireCfg, cfgErr := config.Load(baseDir); cfgErr == nil {
		apacheHome, _ := core.ResolveApacheHome(platform.OSFileProbe{}, wireCfg.ApacheHome, config.DefaultApacheCandidates())
		fileStore := platform.OSFileStore{}
		registrySvc := &core.RegistryService{
			Store: platform.NewJSONRegistryStore(filepath.Join(wireCfg.DataDir, "registry.json")),
			Files: platform.OSFileProbe{},
			Clock: clock,
			Defaults: core.Settings{
				ApacheHome:    apacheHome,
				ApacheService: wireCfg.ApacheService,
				DocumentRoot:  wireCfg.DocumentRoot,
				PanelPort:     wireCfg.Port,
			},
		}
		manager := &core.ApacheManager{
			Runner:  platform.ExecRunner{},
			Files:   fileStore,
			Service: platform.NewNamedServiceController(),
			Health:  platform.HTTPHealthChecker{},
			Clock:   clock,
			Sleep:   time.Sleep,
			Cfg: core.ApplyConfig{
				ApacheHome:     apacheHome,
				ApacheService:  wireCfg.ApacheService,
				RegistryPath:   filepath.Join(wireCfg.DataDir, "registry.json"),
				GeneratedDir:   filepath.Join(wireCfg.DataDir, "generated", "apache"),
				BackupsDir:     filepath.Join(wireCfg.DataDir, "backups"),
				HealthBaseURL:  "http://127.0.0.1",
				Retention:      20,
				HealthAttempts: 10,
				HealthInterval: 500 * time.Millisecond,
			},
		}
		projectOps = &core.ProjectService{Registry: registrySvc, Manager: manager}
		apacheOps = manager
	} else {
		broken := &brokenOps{err: cfgErr}
		projectOps, apacheOps = broken, broken
	}

	deps := cli.Deps{
		Out:    os.Stdout,
		ErrOut: os.Stderr,
		LoadConfig: func() (config.Config, error) {
			return config.Load(baseDir)
		},
		NewLogger: logging.Setup,
		Doctor:    doctor,
		Service:   platform.NewServiceController(),
		Clock:     clock,
		Projects:  projectOps,
		Apache:    apacheOps,
		Serve: func(cfg config.Config, logger *slog.Logger) error {
			application := app.New(cfg, logger, doctor, clock)
			handler := httpapi.NewHandler(application, logger)
			return platform.RunPanel(buildinfo.ServiceName, func(ctx context.Context) error {
				return application.Run(ctx, handler)
			})
		},
		OpenDesktop: func(cfg config.Config, logger *slog.Logger) error {
			application := app.New(cfg, logger, doctor, clock)
			handler := httpapi.NewHandler(application, logger)
			base := fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)
			var runErr error
			finished := make(chan struct{})
			launcher := desktop.Launcher{
				HealthCheck: func() bool { return desktop.HealthCheckNow(base) },
				StartServer: func() (func(), error) {
					ctx, cancel := context.WithCancel(context.Background())
					go func() {
						runErr = application.Run(ctx, handler)
						close(finished)
					}()
					return func() { cancel(); <-finished }, nil
				},
				WaitReady: func() error {
					// Surface a fast server failure (e.g. bind error)
					// directly instead of polling until the budget ends.
					select {
					case <-finished:
						return runErr
					default:
					}
					return desktop.WaitReady(base, 50, 100*time.Millisecond)
				},
				OpenWindow: func() error {
					platform.DetachConsole()
					return desktop.OpenWindow(base)
				},
			}
			return launcher.Launch()
		},
	}
	return cli.New(deps).Run(os.Args[1:])
}

// brokenOps stands in for the M2 pipeline when panel.json cannot be
// loaded: every operation returns the config error verbatim.
type brokenOps struct{ err error }

func (b *brokenOps) List() (core.Registry, error) { return core.Registry{}, b.err }
func (b *brokenOps) AddPHPVersion(core.PHPVersion) error {
	return b.err
}
func (b *brokenOps) RemovePHPVersion(string) error { return b.err }
func (b *brokenOps) AddProject(context.Context, core.Project) (core.ApplyResult, error) {
	return core.ApplyResult{}, b.err
}
func (b *brokenOps) SetPHP(context.Context, string, string) (core.ApplyResult, error) {
	return core.ApplyResult{}, b.err
}
func (b *brokenOps) SetEnabled(context.Context, string, bool) (core.ApplyResult, error) {
	return core.ApplyResult{}, b.err
}
func (b *brokenOps) TestConfig(context.Context) error { return b.err }
func (b *brokenOps) Status() (string, error)          { return "", b.err }
func (b *brokenOps) Restart() error                   { return b.err }
