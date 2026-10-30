package app

import (
	"context"

	"github.com/gojalifs/spanel-plus/internal/core"
)

// ProjectOps is the state-changing project surface the HTTP API (and
// only it, besides the CLI's own copy) consumes. Defined here, at the
// composition root, so handlers depend on behavior — not on the
// concrete service — and stay unit-testable with fakes.
type ProjectOps interface {
	List() (core.Registry, error)
	AddProject(ctx context.Context, p core.Project) (core.ApplyResult, error)
	UpdateProject(ctx context.Context, name string, upd core.Project) (core.ApplyResult, error)
	RemoveProject(ctx context.Context, name string) (core.ApplyResult, error)
	SetPHP(ctx context.Context, name, version string) (core.ApplyResult, error)
	SetEnabled(ctx context.Context, name string, enabled bool) (core.ApplyResult, error)
	PreviewSetPHP(name, version string) (core.ConfigDiff, error)
	CurrentConfig(name string) (string, error)
	Warnings(name string) ([]core.Warning, error)
}

// PHPOps is the PHP version manager surface for the API.
type PHPOps interface {
	Register(ctx context.Context, path string) (core.PHPRegistration, error)
	Remove(version string) error
	Modules(ctx context.Context, version string) ([]string, error)
	Extensions(ctx context.Context, version string) (core.PHPExtensionInventory, error)
	SetExtensions(ctx context.Context, version string, desired map[string]bool) (core.SetExtensionsResult, error)
	// Scan auto-registers PHP builds found in the manager's scan root.
	Scan(ctx context.Context) (core.AutoRegisterResult, error)
}

// ApacheOps is the managed-Apache surface for the API.
type ApacheOps interface {
	Status() (string, error)
	Restart() error
	TestConfig(ctx context.Context) error
}

// ProcessOps is the supervisor surface for the API (Plan F5).
type ProcessOps interface {
	Statuses() ([]core.ProcessStatus, error)
	StartProcess(project, name string) error
	StopProcess(project, name string) error
	RestartProcess(project, name string) error
	RunOnce(ctx context.Context, project, command string) (string, int, error)
	RunScheduleOnce(ctx context.Context, project string) (string, int, error)
}

// Runtime bundles the long-running background workers (supervisor +
// scheduler) the application starts and stops with its own lifecycle.
type Runtime struct {
	Supervisor *core.Supervisor
	Scheduler  *core.Scheduler
}
