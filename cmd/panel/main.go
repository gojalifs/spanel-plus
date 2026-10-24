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
