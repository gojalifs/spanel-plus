// Package cli is the panel's command-line face. Like the HTTP API it
// is a thin translator: parse args, call core/app services, print the
// result, return an exit code. No business logic lives here (Plan 10.1).
//
// Exit codes: 0 success, 1 runtime failure, 2 usage error.
package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/gojalifs/spanel-plus/internal/app"
	"github.com/gojalifs/spanel-plus/internal/buildinfo"
	"github.com/gojalifs/spanel-plus/internal/config"
	"github.com/gojalifs/spanel-plus/internal/core"
)

// Deps are the CLI's collaborators, injected by main so commands can
// be unit-tested with fakes.
type Deps struct {
	Out    io.Writer
	ErrOut io.Writer

	// LoadConfig resolves the panel configuration.
	LoadConfig func() (config.Config, error)
	// NewLogger builds the panel logger for serve (logDir, console).
	NewLogger func(logDir string, console bool) (*slog.Logger, io.Closer, error)
	// Doctor performs the environment checks.
	Doctor core.Doctor
	// Service controls the panel's own Windows Service.
	Service core.ServiceController
	// Clock is the app clock.
	Clock core.Clock
	// Serve runs the panel until stopped; main wires it to the
	// platform layer (console or Windows Service).
	Serve func(cfg config.Config, logger *slog.Logger) error
	// OpenDesktop runs the panel as a desktop app (native window);
	// main wires it to internal/desktop.
	OpenDesktop func(cfg config.Config, logger *slog.Logger) error
}

// CLI is the command dispatcher.
type CLI struct{ deps Deps }

// New builds a CLI from its dependencies.
func New(deps Deps) *CLI { return &CLI{deps: deps} }

const usage = `SPanel+ — admin panel Windows untuk multi-PHP per path (internal)

Usage:
  panel [command]

Commands:
  app                   Buka panel sebagai aplikasi desktop (jendela WebView2). Ini juga
                        yang jalan bila panel.exe dibuka tanpa perintah (double-click).
  serve                 Jalankan server panel tanpa jendela (console, atau sebagai
                        Windows Service bila dimulai SCM)
  doctor                Periksa lingkungan: path Apache, httpd -v, mod_fcgid, port panel, data dir
  service install       Daftarkan panel sebagai Windows Service (start otomatis saat boot) [Administrator]
  service uninstall     Hapus Windows Service panel [Administrator]
  service start         Start Windows Service panel [Administrator]
  service stop          Stop Windows Service panel [Administrator]
  service status        Tampilkan status Windows Service panel
  version               Tampilkan versi panel
  help                  Tampilkan bantuan ini

Konfigurasi: file panel.json di folder yang sama dengan panel.exe
(port, data_dir, log_dir, apache_home, apache_service).
`

// Run executes one command line and returns the process exit code.
func (c *CLI) Run(args []string) int {
	if len(args) == 0 {
		// Double-click (atau `panel` polos) membuka aplikasi desktop —
		// keputusan max 2026-10-24: panel diakses lewat exe, bukan browser.
		return c.runApp()
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(c.deps.Out, usage)
		return 0
	case "app":
		return c.runApp()
	case "version", "-v", "--version":
		fmt.Fprintf(c.deps.Out, "%s %s (%s)\n", buildinfo.Name, buildinfo.Version, buildinfo.Commit)
		return 0
	case "doctor":
		return c.runDoctor()
	case "serve":
		return c.runServe()
	case "service":
		return c.runService(args[1:])
	default:
		fmt.Fprintf(c.deps.ErrOut, "perintah tidak dikenal: %q\n\n%s", args[0], usage)
		return 2
	}
}

func (c *CLI) loadConfig() (config.Config, int) {
	cfg, err := c.deps.LoadConfig()
	if err != nil {
		fmt.Fprintf(c.deps.ErrOut, "ERROR: %v\n", err)
		return config.Config{}, 1
	}
	return cfg, 0
}

func (c *CLI) runDoctor() int {
	cfg, code := c.loadConfig()
	if code != 0 {
		return code
	}
	application := app.New(cfg, discardLogger(), c.deps.Doctor, c.deps.Clock)
	report := c.deps.Doctor.Run(context.Background(), application.DoctorInput())
	for _, check := range report.Checks {
		fmt.Fprintf(c.deps.Out, "%-5s %-14s %s\n", check.Status, check.Name, check.Detail)
	}
	if !report.Healthy {
		fmt.Fprintln(c.deps.Out, "\nDoctor: TIDAK SEHAT — perbaiki baris FAIL di atas.")
		return 1
	}
	fmt.Fprintln(c.deps.Out, "\nDoctor: SEHAT — semua pemeriksaan lulus.")
	return 0
}

func (c *CLI) runApp() int {
	cfg, code := c.loadConfig()
	if code != 0 {
		return code
	}
	logger, closer, err := c.deps.NewLogger(cfg.LogDir, true)
	if err != nil {
		fmt.Fprintf(c.deps.ErrOut, "ERROR: %v\n", err)
		return 1
	}
	defer closer.Close()
	logger.Info("panel desktop starting", "version", buildinfo.Version, "port", cfg.Port)
	if err := c.deps.OpenDesktop(cfg, logger); err != nil {
		logger.Error("panel desktop exited with error", "err", err)
		fmt.Fprintf(c.deps.ErrOut, "ERROR: %v\n", err)
		return 1
	}
	logger.Info("panel desktop closed")
	return 0
}

func (c *CLI) runServe() int {
	cfg, code := c.loadConfig()
	if code != 0 {
		return code
	}
	// If the data dir cannot be created, logger setup fails loudly here.
	logger, closer, err := c.deps.NewLogger(cfg.LogDir, true)
	if err != nil {
		fmt.Fprintf(c.deps.ErrOut, "ERROR: %v\n", err)
		return 1
	}
	defer closer.Close()
	logger.Info("panel starting",
		"version", buildinfo.Version, "port", cfg.Port,
		"data_dir", cfg.DataDir, "apache_service", cfg.ApacheService)
	if err := c.deps.Serve(cfg, logger); err != nil {
		logger.Error("panel exited with error", "err", err)
		fmt.Fprintf(c.deps.ErrOut, "ERROR: %v\n", err)
		return 1
	}
	logger.Info("panel stopped cleanly")
	return 0
}

func (c *CLI) runService(args []string) int {
	if len(args) != 1 {
		fmt.Fprintf(c.deps.ErrOut, "usage: panel service install|uninstall|start|stop|status\n")
		return 2
	}
	var err error
	switch args[0] {
	case "install":
		err = c.deps.Service.Install()
		if err == nil {
			fmt.Fprintln(c.deps.Out, "Service terpasang: start otomatis saat boot. Jalankan 'panel service start' untuk menyalakan sekarang.")
		}
	case "uninstall":
		err = c.deps.Service.Uninstall()
		if err == nil {
			fmt.Fprintln(c.deps.Out, "Service terhapus.")
		}
	case "start":
		err = c.deps.Service.Start()
		if err == nil {
			fmt.Fprintln(c.deps.Out, "Service start.")
		}
	case "stop":
		err = c.deps.Service.Stop()
		if err == nil {
			fmt.Fprintln(c.deps.Out, "Service stop.")
		}
	case "status":
		var status string
		status, err = c.deps.Service.Status()
		if err == nil {
			fmt.Fprintf(c.deps.Out, "Service %s: %s\n", buildinfo.ServiceName, status)
		}
	default:
		fmt.Fprintf(c.deps.ErrOut, "subcommand service tidak dikenal: %q\nusage: panel service install|uninstall|start|stop|status\n", args[0])
		return 2
	}
	if err != nil {
		fmt.Fprintf(c.deps.ErrOut, "ERROR: %v\n", err)
		return 1
	}
	return 0
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
