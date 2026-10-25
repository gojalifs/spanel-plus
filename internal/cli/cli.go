// Package cli is the panel's command-line face. Like the HTTP API it
// is a thin translator: parse args, call core/app services, print the
// result, return an exit code. No business logic lives here (Plan 10.1).
//
// Exit codes: 0 success, 1 runtime failure, 2 usage error.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
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
	// Projects reads the registry directly, for `doctor` only:
	// diagnostics must work when the panel is down. All state
	// commands go through NewAPI instead (M5).
	Projects ProjectOps
	// NewAPI builds the HTTP client for the running panel from the
	// loaded config (address + CLI session token location).
	NewAPI func(cfg config.Config) APIClient
	// NewAuth builds the local admin auth service (data dir from
	// the loaded config) for `admin set-password`.
	NewAuth func(cfg config.Config) *core.AuthService
	// NewAudit builds the audit sink for local console actions.
	NewAudit func(cfg config.Config) core.AuditLogger
	// Stdin is where passwords are read from (os.Stdin in
	// production; a buffer in tests).
	Stdin io.Reader
}

// ProjectOps is the CLI's view of the registry, used only by
// `doctor` (local diagnostics). State commands use APIClient.
type ProjectOps interface {
	List() (core.Registry, error)
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
  login                 Login ke panel yang sedang jalan (sesi 10 menit idle, tersimpan lokal)
  login --ad            Login AD (username AD + password; butuh ad_enabled di panel.json)
  logout                Logout (hapus sesi lokal)
  admin set-password    Buat/ganti password admin — perintah lokal, pertama kali sebelum login
  admin hash-password   Cetak hash bcrypt untuk ditempel ke ad_dummy_users di panel.json
  project list          Daftar proyek di registry
  project add --name N --url /path --path C:\folder --type laravel|php-native|static --php 8.2 [--public public] [--disabled]
  project update N [flags seperti add]   Ubah proyek (flag yang tidak disebut memakai nilai lama)
  project set-php N V   Ganti versi PHP proyek lewat pipa apply (backup -> httpd -t -> restart -> health check -> rollback bila gagal)
  project enable N      Aktifkan proyek (apply)
  project disable N     Nonaktifkan proyek (apply)
  project remove N      Hapus proyek dari panel (apply; folder proyek tidak dihapus)
  project config N      Tampilkan config Apache yang sedang berlaku untuk proyek
  project warnings N    Tampilkan peringatan proyek (APP_URL, php_value di .htaccess, …)
  project preview N V   Preview diff config bila PHP proyek diganti ke V (tanpa apply)
  php list              Daftar versi PHP terdaftar
  php add --path C:\panel\php\8.2   Daftarkan PHP; versi terdeteksi dari php.exe (build TS ditolak)
  php remove V          Hapus versi PHP (ditolak bila masih dipakai proyek)
  php modules V         Daftar extension PHP versi V
  process list          Status runtime proses CLI per proyek (dari supervisor panel)
  process start P N     Start proses N milik proyek P
  process stop P N      Stop proses N (final — tidak di-restart supervisor)
  process restart P N   Restart proses N
  process log P N       Tampilkan log proses N
  process run P <perintah>   Jalankan perintah sekali jalan di proyek P (artisan/*.php via php.exe proyek)
  process schedule-run P    Jalankan artisan schedule:run proyek P sekali
  apache test           Jalankan httpd -t pada config aktif
  apache status         Status service Apache yang dikelola
  apache restart        Restart service Apache yang dikelola
  version               Tampilkan versi panel
  help                  Tampilkan bantuan ini

Perintah project/php/apache/process berbicara ke panel yang sedang
jalan lewat HTTP API (port di panel.json) dan butuh sesi login.
Bila panel tidak jalan, mereka gagal keras dengan petunjuk
menyalakannya dulu — bukan jalan pintas lokal, supaya CLI dan UI
tidak pernah beraksi atas state yang berbeda.
Konfigurasi: file panel.json di folder yang sama dengan panel.exe
(port, data_dir, log_dir, apache_home, apache_service).
`

// StripSelfArgs drops leading arguments that are just the panel
// binary itself. Some launch environments (notably Android's linker
// under Termux) deliver the executable path as the first argument,
// so `panel admin set-password` arrives as `panel <path> admin
// set-password` and dispatch would otherwise report the path as an
// unknown command. No real panel command is ever a path, so dropping
// such an argument cannot swallow a user command.
func StripSelfArgs(args []string, exePath string) []string {
	exeBase := filepath.Base(exePath)
	for len(args) > 0 {
		a := args[0]
		if a == exePath || (strings.Contains(a, "/") && filepath.Base(a) == exeBase) {
			args = args[1:]
			continue
		}
		break
	}
	return args
}

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
	case "login":
		return c.runLogin(args[1:])
	case "logout":
		return c.runLogout()
	case "admin":
		return c.runAdmin(args[1:])
	case "service":
		return c.runService(args[1:])
	case "project":
		return c.runProject(args[1:])
	case "php":
		return c.runPHP(args[1:])
	case "apache":
		return c.runApache(args[1:])
	case "process":
		return c.runProcess(args[1:])
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

// apiClient loads the config and builds the panel client for one
// state command. A missing client factory is a wiring bug, not a
// user error — it fails loud.
func (c *CLI) apiClient() (APIClient, int) {
	cfg, code := c.loadConfig()
	if code != 0 {
		return nil, code
	}
	if c.deps.NewAPI == nil {
		fmt.Fprintln(c.deps.ErrOut, "ERROR: klien API panel tidak terpasang (kesalahan wiring)")
		return nil, 1
	}
	return c.deps.NewAPI(cfg), 0
}

// apiErr prints an API failure in the operator's terms: a missing
// session names the fix, a dead panel names how to start it.
func (c *CLI) apiErr(err error) int {
	switch {
	case errors.Is(err, ErrNeedLogin):
		fmt.Fprintln(c.deps.ErrOut, "ERROR: belum login / sesi habis — jalankan 'panel login' dulu (pertama kali: 'panel admin set-password').")
	case errors.Is(err, ErrPanelDown):
		fmt.Fprintf(c.deps.ErrOut, "ERROR: %v\n", err)
	default:
		fmt.Fprintf(c.deps.ErrOut, "ERROR: %v\n", err)
	}
	return 1
}

func (c *CLI) runDoctor() int {
	cfg, code := c.loadConfig()
	if code != 0 {
		return code
	}
	application := app.New(cfg, discardLogger(), c.deps.Doctor, c.deps.Clock)
	in := application.DoctorInput()
	in.ApacheService = cfg.ApacheService
	// Doctor reads the registry file directly (local diagnostics):
	// an unreadable registry only skips the registry checks.
	if c.deps.Projects != nil {
		if reg, err := c.deps.Projects.List(); err == nil {
			in.Registry = &reg
		}
	}
	report := c.deps.Doctor.Run(context.Background(), in)
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

func (c *CLI) runProject(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(c.deps.ErrOut, "usage: panel project list|add|update|set-php|enable|disable|remove|config|warnings|preview ...")
		return 2
	}
	client, code := c.apiClient()
	if code != 0 {
		return code
	}
	switch args[0] {
	case "list":
		reg, err := client.ListProjects()
		if err != nil {
			return c.apiErr(err)
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
		res, err := client.AddProject(p)
		if err != nil {
			return c.apiErr(err)
		}
		c.printApplyResult(res)
		return 0
	case "update":
		// Merge flags over the stored project: an omitted flag keeps
		// the old value (the dashboard edit form behaves the same
		// way — it is always prefilled).
		if len(args) < 2 {
			fmt.Fprintln(c.deps.ErrOut, "usage: panel project update <name> [--url ...] [--path ...] [--type ...] [--public ...] [--php ...] [--disabled]")
			return 2
		}
		_, flags, err := parseFlags(args[2:])
		if err != nil {
			fmt.Fprintf(c.deps.ErrOut, "ERROR: %v\n", err)
			return 2
		}
		reg, err := client.ListProjects()
		if err != nil {
			return c.apiErr(err)
		}
		p, ok := reg.FindProject(args[1])
		if !ok {
			fmt.Fprintf(c.deps.ErrOut, "ERROR: project %q tidak ditemukan\n", args[1])
			return 1
		}
		if v, ok := flags["url"]; ok {
			p.URLPath = v
		}
		if v, ok := flags["path"]; ok {
			p.FSPath = v
		}
		if v, ok := flags["type"]; ok {
			p.Type = core.ProjectType(v)
		}
		if v, ok := flags["public"]; ok {
			p.PublicSubdir = v
		}
		if v, ok := flags["php"]; ok {
			p.PHPVersion = v
		}
		if flags["disabled"] == "true" {
			p.Enabled = false
		}
		res, err := client.UpdateProject(args[1], p)
		if err != nil {
			return c.apiErr(err)
		}
		c.printApplyResult(res)
		return 0
	case "set-php":
		if len(args) != 3 {
			fmt.Fprintln(c.deps.ErrOut, "usage: panel project set-php <name> <version>")
			return 2
		}
		res, err := client.SetPHP(args[1], args[2])
		if err != nil {
			return c.apiErr(err)
		}
		c.printApplyResult(res)
		return 0
	case "enable", "disable":
		if len(args) != 2 {
			fmt.Fprintf(c.deps.ErrOut, "usage: panel project %s <name>\n", args[0])
			return 2
		}
		res, err := client.SetEnabled(args[1], args[0] == "enable")
		if err != nil {
			return c.apiErr(err)
		}
		c.printApplyResult(res)
		return 0
	case "remove":
		if len(args) != 2 {
			fmt.Fprintln(c.deps.ErrOut, "usage: panel project remove <name>")
			return 2
		}
		res, err := client.RemoveProject(args[1])
		if err != nil {
			return c.apiErr(err)
		}
		c.printApplyResult(res)
		return 0
	case "config":
		if len(args) != 2 {
			fmt.Fprintln(c.deps.ErrOut, "usage: panel project config <name>")
			return 2
		}
		content, err := client.ProjectConfig(args[1])
		if err != nil {
			return c.apiErr(err)
		}
		fmt.Fprintln(c.deps.Out, content)
		return 0
	case "warnings":
		if len(args) != 2 {
			fmt.Fprintln(c.deps.ErrOut, "usage: panel project warnings <name>")
			return 2
		}
		warnings, err := client.ProjectWarnings(args[1])
		if err != nil {
			return c.apiErr(err)
		}
		if len(warnings) == 0 {
			fmt.Fprintln(c.deps.Out, "Tidak ada peringatan.")
			return 0
		}
		for _, w := range warnings {
			fmt.Fprintf(c.deps.Out, "%-24s %s\n", w.Code, w.Message)
		}
		return 0
	case "preview":
		if len(args) != 3 {
			fmt.Fprintln(c.deps.ErrOut, "usage: panel project preview <name> <version>")
			return 2
		}
		diff, err := client.PreviewSetPHP(args[1], args[2])
		if err != nil {
			return c.apiErr(err)
		}
		for _, line := range diff.Lines {
			fmt.Fprintf(c.deps.Out, "%s %s\n", line.Kind, line.Text)
		}
		return 0
	default:
		fmt.Fprintf(c.deps.ErrOut, "subcommand project tidak dikenal: %q\n", args[0])
		return 2
	}
}

func (c *CLI) runPHP(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(c.deps.ErrOut, "usage: panel php list|add|remove|modules ...")
		return 2
	}
	client, code := c.apiClient()
	if code != 0 {
		return code
	}
	switch args[0] {
	case "list":
		reg, err := client.ListProjects()
		if err != nil {
			return c.apiErr(err)
		}
		if len(reg.PHPVersions) == 0 {
			fmt.Fprintln(c.deps.Out, "Belum ada versi PHP terdaftar. Tambah dengan 'panel php add --path ...'.")
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
		// The version is always probed from the binaries (M3, Plan
		// F2): a typed version number could lie about the binary.
		reg, err := client.RegisterPHP(flags["path"])
		if err != nil {
			return c.apiErr(err)
		}
		fmt.Fprintf(c.deps.Out, "PHP %s terdaftar (%s) — terdeteksi %s %s %s, php.ini: %s.\n",
			reg.Version.Version, reg.Version.Path, reg.Full, map[bool]string{true: "NTS", false: "TS"}[reg.NTS], reg.Arch, reg.PHPIni)
		for _, w := range reg.Warnings {
			fmt.Fprintf(c.deps.Out, "PERINGATAN: %s\n", w)
		}
		return 0
	case "remove":
		if len(args) != 2 {
			fmt.Fprintln(c.deps.ErrOut, "usage: panel php remove <version>")
			return 2
		}
		if err := client.RemovePHP(args[1]); err != nil {
			return c.apiErr(err)
		}
		fmt.Fprintf(c.deps.Out, "PHP %s terhapus dari registry.\n", args[1])
		return 0
	case "modules":
		if len(args) != 2 {
			fmt.Fprintln(c.deps.ErrOut, "usage: panel php modules <version>")
			return 2
		}
		mods, err := client.PHPModules(args[1])
		if err != nil {
			return c.apiErr(err)
		}
		fmt.Fprintln(c.deps.Out, strings.Join(mods, ", "))
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
	client, code := c.apiClient()
	if code != 0 {
		return code
	}
	switch args[0] {
	case "test":
		if err := client.ApacheTest(); err != nil {
			return c.apiErr(err)
		}
		fmt.Fprintln(c.deps.Out, "httpd -t: Syntax OK")
		return 0
	case "status":
		status, err := client.ApacheStatus()
		if err != nil {
			return c.apiErr(err)
		}
		fmt.Fprintf(c.deps.Out, "Apache service: %s\n", status)
		return 0
	case "restart":
		if err := client.ApacheRestart(); err != nil {
			return c.apiErr(err)
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

// runProcess implements the M5 CLI→API process surface (Plan F7):
// the supervisor lives in the running panel, so the CLI drives it
// through the API — the M4 limitation (list-only) is gone.
func (c *CLI) runProcess(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(c.deps.ErrOut, "usage: panel process list|start|stop|restart|log|run|schedule-run ...")
		return 2
	}
	client, code := c.apiClient()
	if code != 0 {
		return code
	}
	switch args[0] {
	case "list":
		sts, err := client.ProcessStatuses()
		if err != nil {
			return c.apiErr(err)
		}
		if len(sts) == 0 {
			fmt.Fprintln(c.deps.Out, "Belum ada proses. Tambahkan dari dashboard (Edit proyek → Proses CLI) — template Laravel: queue & scheduler.")
			return 0
		}
		fmt.Fprintf(c.deps.Out, "%-16s %-12s %-10s %-6s %-8s %s\n", "PROJECT", "NAME", "STATUS", "PID", "RESTARTS", "COMMAND")
		for _, s := range sts {
			fmt.Fprintf(c.deps.Out, "%-16s %-12s %-10s %-6d %-8d %s\n", s.Project, s.Name, s.State, s.PID, s.Restarts, s.Command)
		}
		return 0
	case "start", "stop", "restart":
		if len(args) != 3 {
			fmt.Fprintf(c.deps.ErrOut, "usage: panel process %s <project> <name>\n", args[0])
			return 2
		}
		var err error
		switch args[0] {
		case "start":
			err = client.ProcessStart(args[1], args[2])
		case "stop":
			err = client.ProcessStop(args[1], args[2])
		case "restart":
			err = client.ProcessRestart(args[1], args[2])
		}
		if err != nil {
			return c.apiErr(err)
		}
		fmt.Fprintf(c.deps.Out, "Proses %s/%s: %s OK.\n", args[1], args[2], args[0])
		return 0
	case "log":
		if len(args) != 3 {
			fmt.Fprintln(c.deps.ErrOut, "usage: panel process log <project> <name>")
			return 2
		}
		text, err := client.ProcessLog(args[1], args[2])
		if err != nil {
			return c.apiErr(err)
		}
		fmt.Fprint(c.deps.Out, text)
		if !strings.HasSuffix(text, "\n") {
			fmt.Fprintln(c.deps.Out)
		}
		return 0
	case "run":
		if len(args) < 3 {
			fmt.Fprintln(c.deps.ErrOut, "usage: panel process run <project> <perintah...>")
			return 2
		}
		out, code, err := client.RunOnce(args[1], strings.Join(args[2:], " "))
		if err != nil {
			return c.apiErr(err)
		}
		fmt.Fprintf(c.deps.Out, "[exit %d]\n%s", code, out)
		if !strings.HasSuffix(out, "\n") {
			fmt.Fprintln(c.deps.Out)
		}
		return 0
	case "schedule-run":
		if len(args) != 2 {
			fmt.Fprintln(c.deps.ErrOut, "usage: panel process schedule-run <project>")
			return 2
		}
		out, code, err := client.ScheduleRun(args[1])
		if err != nil {
			return c.apiErr(err)
		}
		fmt.Fprintf(c.deps.Out, "[exit %d]\n%s", code, out)
		if !strings.HasSuffix(out, "\n") {
			fmt.Fprintln(c.deps.Out)
		}
		return 0
	default:
		fmt.Fprintf(c.deps.ErrOut, "subcommand process tidak dikenal: %q\n", args[0])
		return 2
	}
}

// --- M5: login / logout / admin ------------------------------------

// readPasswordLine reads one line from Stdin (prompt on ErrOut).
// It reads byte-by-byte on purpose: a fresh buffered reader per
// prompt would swallow the following line (the confirmation prompt
// would then read EOF). The password echoes on the terminal — the
// panel is a local console tool, and suppressing echo portably needs
// a terminal library the project deliberately does not carry; the
// password itself is never printed, logged, or stored anywhere
// except its bcrypt hash.
func (c *CLI) readPasswordLine(prompt string) (string, error) {
	fmt.Fprint(c.deps.ErrOut, prompt)
	in := c.deps.Stdin
	if in == nil {
		return "", fmt.Errorf("tidak ada input (stdin)")
	}
	var line []byte
	buf := make([]byte, 1)
	for {
		n, err := in.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				break
			}
			line = append(line, buf[0])
		}
		if err != nil {
			if len(line) == 0 {
				return "", fmt.Errorf("baca password: %w", err)
			}
			break
		}
	}
	return strings.TrimRight(string(line), "\r"), nil
}

func (c *CLI) runLogin(args []string) int {
	ad := false
	username := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--ad":
			ad = true
		case "--username":
			if i+1 >= len(args) {
				fmt.Fprintln(c.deps.ErrOut, "usage: panel login [--ad] [--username <user>]")
				return 2
			}
			i++
			username = args[i]
		default:
			fmt.Fprintln(c.deps.ErrOut, "usage: panel login [--ad] [--username <user>]")
			return 2
		}
	}
	client, code := c.apiClient()
	if code != 0 {
		return code
	}
	if ad {
		if username == "" {
			line, err := c.readPasswordLine("Username AD: ")
			if err != nil {
				fmt.Fprintf(c.deps.ErrOut, "ERROR: %v\n", err)
				return 1
			}
			username = line
		}
		password, err := c.readPasswordLine("Password AD: ")
		if err != nil {
			fmt.Fprintf(c.deps.ErrOut, "ERROR: %v\n", err)
			return 1
		}
		if err := client.LoginAD(username, password); err != nil {
			return c.apiErr(err)
		}
		fmt.Fprintln(c.deps.Out, "Login AD berhasil — sesi tersimpan lokal (10 menit idle).")
		return 0
	}
	password, err := c.readPasswordLine("Password admin: ")
	if err != nil {
		fmt.Fprintf(c.deps.ErrOut, "ERROR: %v\n", err)
		return 1
	}
	if err := client.Login(password); err != nil {
		return c.apiErr(err)
	}
	fmt.Fprintln(c.deps.Out, "Login berhasil — sesi tersimpan lokal (10 menit idle).")
	return 0
}

func (c *CLI) runLogout() int {
	client, code := c.apiClient()
	if code != 0 {
		return code
	}
	if err := client.Logout(); err != nil {
		return c.apiErr(err)
	}
	fmt.Fprintln(c.deps.Out, "Logout — sesi lokal dihapus.")
	return 0
}

func (c *CLI) runAdmin(args []string) int {
	if len(args) == 1 && args[0] == "hash-password" {
		// Prints a bcrypt hash for pasting into ad_dummy_users.
		// The hash is not a credential: it is exactly what belongs
		// in config, and the password itself is never stored.
		password, err := c.readPasswordLine("Password untuk di-hash: ")
		if err != nil {
			fmt.Fprintf(c.deps.ErrOut, "ERROR: %v\n", err)
			return 1
		}
		hash, err := core.HashPassword(password)
		if err != nil {
			fmt.Fprintf(c.deps.ErrOut, "ERROR: %v\n", err)
			return 1
		}
		fmt.Fprintln(c.deps.Out, hash)
		return 0
	}
	if len(args) != 1 || args[0] != "set-password" {
		fmt.Fprintln(c.deps.ErrOut, "usage: panel admin set-password | panel admin hash-password")
		return 2
	}
	cfg, code := c.loadConfig()
	if code != 0 {
		return code
	}
	if c.deps.NewAuth == nil {
		fmt.Fprintln(c.deps.ErrOut, "ERROR: layanan auth tidak terpasang (kesalahan wiring)")
		return 1
	}
	first, err := c.readPasswordLine("Password admin baru: ")
	if err != nil {
		fmt.Fprintf(c.deps.ErrOut, "ERROR: %v\n", err)
		return 1
	}
	second, err := c.readPasswordLine("Ulangi password admin: ")
	if err != nil {
		fmt.Fprintf(c.deps.ErrOut, "ERROR: %v\n", err)
		return 1
	}
	if first != second {
		fmt.Fprintln(c.deps.ErrOut, "ERROR: password tidak sama — ulangi dari awal.")
		return 1
	}
	auth := c.deps.NewAuth(cfg)
	if err := auth.SetPassword(first); err != nil {
		fmt.Fprintf(c.deps.ErrOut, "ERROR: %v\n", err)
		return 1
	}
	if c.deps.NewAudit != nil {
		_ = c.deps.NewAudit(cfg).Append(core.AuditEntry{Actor: "console", Action: "panel admin set-password", Detail: "ok"})
	}
	fmt.Fprintln(c.deps.Out, "Password admin disimpan (hash bcrypt). Sesi lama ikut dicabut — login ulang bila perlu.")
	return 0
}
