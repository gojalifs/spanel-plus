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
	var projectsSvc *core.ProjectService
	var phpMgr *core.PHPManager
	var apacheMgr *core.ApacheManager
	var supervisor *core.Supervisor
	var scheduler *core.Scheduler
	var authSvc *core.AuthService
	var auditLog core.AuditLogger
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
		projectsSvc = &core.ProjectService{Registry: registrySvc, Manager: manager, Files: fileStore}
		supervisor = &core.Supervisor{
			Registry: registrySvc,
			Starter:  platform.ExecProcessStarter{},
			OneShot:  platform.ExecOneShotRunner{},
			Clock:    clock,
			LogDir:   filepath.Join(wireCfg.DataDir, "projects"),
		}
		scheduler = &core.Scheduler{Supervisor: supervisor, Registry: registrySvc}
		phpMgr = &core.PHPManager{
			Registry:    registrySvc,
			Prober:      platform.ExecPHPProber{Runner: platform.ExecRunner{}},
			Files:       fileStore,
			RuntimeBase: filepath.Join(wireCfg.DataDir, "php-runtime"),
		}
		apacheMgr = manager
		projectOps = projectsSvc
		// M5: admin auth + audit live in the data dir, next to the
		// registry; the doctor's service check uses the named
		// Apache service controller (a WARN, not a FAIL, where
		// service control does not exist).
		authSvc = &core.AuthService{Files: fileStore, Path: filepath.Join(wireCfg.DataDir, "admin.json"), Clock: clock}
		auditLog = &core.FileAuditLogger{Files: fileStore, Path: filepath.Join(wireCfg.DataDir, "audit.log"), Clock: clock}
		doctor.ApacheStatus = platform.NewNamedServiceController().Status
	} else {
		broken := &brokenOps{err: cfgErr}
		projectOps = broken
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
		Stdin:     os.Stdin,
		NewAPI: func(cfg config.Config) cli.APIClient {
			return cli.NewHTTPAPIClient(
				fmt.Sprintf("http://127.0.0.1:%d", cfg.Port),
				cli.FileTokenStore{Path: filepath.Join(cfg.DataDir, ".cli-session")},
				nil,
			)
		},
		NewAuth: func(cfg config.Config) *core.AuthService {
			return &core.AuthService{Files: platform.OSFileStore{}, Path: filepath.Join(cfg.DataDir, "admin.json"), Clock: clock}
		},
		NewAudit: func(cfg config.Config) core.AuditLogger {
			return &core.FileAuditLogger{Files: platform.OSFileStore{}, Path: filepath.Join(cfg.DataDir, "audit.log"), Clock: clock}
		},
		Serve: func(cfg config.Config, logger *slog.Logger) error {
			application := app.New(cfg, logger, doctor, clock)
			if authSvc != nil {
				application.AttachAuth(authSvc, auditLog)
			}
			wireAD(application, cfg, logger)
			if projectsSvc != nil {
				application.AttachServices(projectsSvc, phpMgr, apacheMgr)
				supervisor.Log = logger
				scheduler.Log = logger
				application.AttachRuntime(app.Runtime{Supervisor: supervisor, Scheduler: scheduler}, supervisor, platform.OSFileStore{}, supervisor.LogDir)
			}
			handler := httpapi.NewHandler(application, logger)
			return platform.RunPanel(buildinfo.ServiceName, func(ctx context.Context) error {
				return application.Run(ctx, handler)
			})
		},
		OpenDesktop: func(cfg config.Config, logger *slog.Logger) error {
			application := app.New(cfg, logger, doctor, clock)
			if authSvc != nil {
				application.AttachAuth(authSvc, auditLog)
			}
			wireAD(application, cfg, logger)
			if projectsSvc != nil {
				application.AttachServices(projectsSvc, phpMgr, apacheMgr)
				supervisor.Log = logger
				scheduler.Log = logger
				application.AttachRuntime(app.Runtime{Supervisor: supervisor, Scheduler: scheduler}, supervisor, platform.OSFileStore{}, supervisor.LogDir)
			}
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
	return cli.New(deps).Run(cli.StripSelfArgs(os.Args[1:], os.Args[0]))
}

// wireAD attaches the directory authenticator from panel.json
// (M5-AD). Every failure mode here is loud in the log but never
// fatal: AD login answers 503 and the local break-glass account
// keeps working, because a bad AD config must not lock the operator
// out of their own panel.
func wireAD(application *app.Application, cfg config.Config, logger *slog.Logger) {
	adCfg := core.ADConfig{
		Enabled:       cfg.ADEnabled,
		Server:        cfg.ADServer,
		Domain:        cfg.ADDomain,
		BaseDN:        cfg.ADBaseDN,
		GroupDN:       cfg.ADGroupDN,
		CAFile:        cfg.ADCAFile,
		AllowInsecure: cfg.ADAllowInsecureLDAP,
		DummyUsers:    cfg.ADDummyUsers,
	}
	if !adCfg.Enabled {
		application.AttachAD(nil, adCfg, nil)
		return
	}
	if err := adCfg.Validate(); err != nil {
		logger.Warn("AD disabled: invalid directory configuration", "err", err)
		application.AttachAD(nil, adCfg, err)
		return
	}
	if adCfg.IsDummy() {
		logger.Warn("AD DUMMY MODE active — this is NOT real Active Directory authentication, never use it in production")
		application.AttachAD(core.DummyAuthenticator{Users: adCfg.DummyUsers, GroupCN: adCfg.GroupCN()}, adCfg, nil)
		return
	}
	ldapAuth, err := platform.NewLDAPAuthenticator(platform.ADConfigFrom(adCfg))
	if err != nil {
		logger.Warn("AD disabled: invalid directory configuration", "err", err)
		application.AttachAD(nil, adCfg, err)
		return
	}
	if err := ldapAuth.CheckTLS(); err != nil {
		logger.Warn("AD disabled: directory TLS configuration invalid", "err", err)
		application.AttachAD(nil, adCfg, err)
		return
	}
	if adCfg.AllowInsecure {
		logger.Warn("AD plain LDAP without TLS is enabled (ad_allow_insecure_ldap) — credentials cross the LAN in cleartext, test-only")
	}
	application.AttachAD(ldapAuth, adCfg, nil)
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
func (b *brokenOps) RemoveProject(context.Context, string) (core.ApplyResult, error) {
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
