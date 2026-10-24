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
	"strings"

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
	// Projects performs registry + apply operations (M2).
	Projects ProjectOps
	// Apache exposes managed-Apache diagnostics (M2).
	Apache ApacheOps
	// PHPManager registers PHP builds by probing the binaries (M3);
	// used by `php add` when no explicit --version is given.
	PHPManager PHPRegistrar
}

// PHPRegistrar is the CLI's view of core.PHPManager.
type PHPRegistrar interface {
	Register(ctx context.Context, path string) (core.PHPRegistration, error)
}

// ProjectOps is the CLI's view of core.ProjectService.
type ProjectOps interface {
	List() (core.Registry, error)
	AddPHPVersion(v core.PHPVersion) error
	RemovePHPVersion(version string) error
	AddProject(ctx context.Context, p core.Project) (core.ApplyResult, error)
	RemoveProject(ctx context.Context, name string) (core.ApplyResult, error)
	SetPHP(ctx context.Context, name, version string) (core.ApplyResult, error)
	SetEnabled(ctx context.Context, name string, enabled bool) (core.ApplyResult, error)
}

// ApacheOps is the CLI's view of the managed-Apache controls.
type ApacheOps interface {
	TestConfig(ctx context.Context) error
	Status() (string, error)
	Restart() error
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
  project list          Daftar proyek di registry
  project add --name N --url /path --path C:\folder --type laravel|php-native|static --php 8.2 [--public public] [--disabled]
  project set-php N V   Ganti versi PHP proyek lewat pipa apply (backup -> httpd -t -> restart -> health check -> rollback bila gagal)
  project enable N      Aktifkan proyek (apply)
  project disable N     Nonaktifkan proyek (apply)
  project remove N      Hapus proyek dari panel (apply; folder proyek tidak dihapus)
  php list              Daftar versi PHP terdaftar
  php add --path C:\panel\php\8.2 [--version 8.2]   Daftarkan PHP; tanpa --version, versi terdeteksi dari php.exe (build TS ditolak)
  php remove V          Hapus versi PHP (ditolak bila masih dipakai proyek)
  apache test           Jalankan httpd -t pada config aktif
  apache status         Status service Apache yang dikelola
  apache restart        Restart service Apache yang dikelola
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
	case "project":
		return c.runProject(args[1:])
	case "php":
		return c.runPHP(args[1:])
	case "apache":
		return c.runApache(args[1:])
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

// --- M2: project / php / apache commands -----------------------------

func (c *CLI) printApplyResult(res core.ApplyResult) {
	fmt.Fprintf(c.deps.Out, "Apply OK. Snapshot: %s\n", res.BackupDir)
	fmt.Fprintf(c.deps.Out, "Config ditulis: %d file (%s)\n", len(res.FilesWritten), strings.Join(res.FilesWritten, ", "))
	if len(res.HealthChecked) > 0 {
		fmt.Fprintf(c.deps.Out, "Health check lulus: %s\n", strings.Join(res.HealthChecked, ", "))
	}
}

func (c *CLI) applyError(err error) int {
	fmt.Fprintf(c.deps.ErrOut, "ERROR: %v\n", err)
	return 1
}

func (c *CLI) runProject(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(c.deps.ErrOut, "usage: panel project list|add|set-php|enable|disable|remove ...")
		return 2
	}
	ctx := context.Background()
	switch args[0] {
	case "list":
		reg, err := c.deps.Projects.List()
		if err != nil {
			return c.applyError(err)
		}
		if len(reg.Projects) == 0 {
			fmt.Fprintln(c.deps.Out, "Belum ada proyek. Tambah dengan 'panel project add ...'.")
			return 0
		}
		fmt.Fprintf(c.deps.Out, "%-16s %-18s %-10s %-6s %s\n", "NAME", "URL", "TYPE", "PHP", "ENABLED")
		for _, p := range reg.SortedProjects() {
			fmt.Fprintf(c.deps.Out, "%-16s %-18s %-10s %-6s %v\n", p.Name, p.URLPath, p.Type, p.PHPVersion, p.Enabled)
		}
		return 0
	case "add":
		_, flags, err := parseFlags(args[1:])
		if err != nil {
			fmt.Fprintf(c.deps.ErrOut, "ERROR: %v\n", err)
			return 2
		}
		p := core.Project{
			Name:         flags["name"],
			URLPath:      flags["url"],
			FSPath:       flags["path"],
			Type:         core.ProjectType(flags["type"]),
			PublicSubdir: flags["public"],
			PHPVersion:   flags["php"],
			Enabled:      flags["disabled"] != "true",
		}
		res, err := c.deps.Projects.AddProject(ctx, p)
		if err != nil {
			return c.applyError(err)
		}
		c.printApplyResult(res)
		return 0
	case "set-php":
		if len(args) != 3 {
			fmt.Fprintln(c.deps.ErrOut, "usage: panel project set-php <name> <version>")
			return 2
		}
		res, err := c.deps.Projects.SetPHP(ctx, args[1], args[2])
		if err != nil {
			return c.applyError(err)
		}
		c.printApplyResult(res)
		return 0
	case "enable", "disable":
		if len(args) != 2 {
			fmt.Fprintf(c.deps.ErrOut, "usage: panel project %s <name>\n", args[0])
			return 2
		}
		res, err := c.deps.Projects.SetEnabled(ctx, args[1], args[0] == "enable")
		if err != nil {
			return c.applyError(err)
		}
		c.printApplyResult(res)
		return 0
	case "remove":
		if len(args) != 2 {
			fmt.Fprintln(c.deps.ErrOut, "usage: panel project remove <name>")
			return 2
		}
		res, err := c.deps.Projects.RemoveProject(ctx, args[1])
		if err != nil {
			return c.applyError(err)
		}
		c.printApplyResult(res)
		return 0
	default:
		fmt.Fprintf(c.deps.ErrOut, "subcommand project tidak dikenal: %q\n", args[0])
		return 2
	}
}

func (c *CLI) runPHP(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(c.deps.ErrOut, "usage: panel php list|add|remove ...")
		return 2
	}
	switch args[0] {
	case "list":
		reg, err := c.deps.Projects.List()
		if err != nil {
			return c.applyError(err)
		}
		if len(reg.PHPVersions) == 0 {
			fmt.Fprintln(c.deps.Out, "Belum ada versi PHP terdaftar. Tambah dengan 'panel php add --version 8.2 --path ...'.")
			return 0
		}
		for _, v := range reg.PHPVersions {
			fmt.Fprintf(c.deps.Out, "%-6s %s\n", v.Version, v.Path)
		}
		return 0
	case "add":
		_, flags, err := parseFlags(args[1:])
		if err != nil {
			fmt.Fprintf(c.deps.ErrOut, "ERROR: %v\n", err)
			return 2
		}
		// Without an explicit --version, probe the binaries and
		// register what is actually installed (M3, Plan F2).
		if flags["version"] == "" && c.deps.PHPManager != nil {
			reg, err := c.deps.PHPManager.Register(context.Background(), flags["path"])
			if err != nil {
				return c.applyError(err)
			}
			fmt.Fprintf(c.deps.Out, "PHP %s terdaftar (%s) — terdeteksi %s %s %s, php.ini: %s.\n",
				reg.Version.Version, reg.Version.Path, reg.Full, map[bool]string{true: "NTS", false: "TS"}[reg.NTS], reg.Arch, reg.PHPIni)
			return 0
		}
		v := core.PHPVersion{Version: flags["version"], Path: flags["path"]}
		if err := c.deps.Projects.AddPHPVersion(v); err != nil {
			return c.applyError(err)
		}
		fmt.Fprintf(c.deps.Out, "PHP %s terdaftar (%s).\n", v.Version, v.Path)
		return 0
	case "remove":
		if len(args) != 2 {
			fmt.Fprintln(c.deps.ErrOut, "usage: panel php remove <version>")
			return 2
		}
		if err := c.deps.Projects.RemovePHPVersion(args[1]); err != nil {
			return c.applyError(err)
		}
		fmt.Fprintf(c.deps.Out, "PHP %s terhapus dari registry.\n", args[1])
		return 0
	default:
		fmt.Fprintf(c.deps.ErrOut, "subcommand php tidak dikenal: %q\n", args[0])
		return 2
	}
}

func (c *CLI) runApache(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(c.deps.ErrOut, "usage: panel apache test|status|restart")
		return 2
	}
	switch args[0] {
	case "test":
		if err := c.deps.Apache.TestConfig(context.Background()); err != nil {
			return c.applyError(err)
		}
		fmt.Fprintln(c.deps.Out, "httpd -t: Syntax OK")
		return 0
	case "status":
		status, err := c.deps.Apache.Status()
		if err != nil {
			return c.applyError(err)
		}
		fmt.Fprintf(c.deps.Out, "Apache service: %s\n", status)
		return 0
	case "restart":
		if err := c.deps.Apache.Restart(); err != nil {
			return c.applyError(err)
		}
		fmt.Fprintln(c.deps.Out, "Apache service direstart.")
		return 0
	default:
		fmt.Fprintf(c.deps.ErrOut, "subcommand apache tidak dikenal: %q\n", args[0])
		return 2
	}
}

// parseFlags parses `--key value` pairs plus the boolean `--disabled`.
// Anything else is returned as a positional argument.
func parseFlags(args []string) (pos []string, flags map[string]string, err error) {
	flags = map[string]string{}
	for i := 0; i < len(args); i++ {
		tok := args[i]
		if !strings.HasPrefix(tok, "--") {
			pos = append(pos, tok)
			continue
		}
		key := strings.TrimPrefix(tok, "--")
		if key == "disabled" {
			flags[key] = "true"
			continue
		}
		if i+1 >= len(args) {
			return nil, nil, fmt.Errorf("flag --%s butuh nilai", key)
		}
		flags[key] = args[i+1]
		i++
	}
	return pos, flags, nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
