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
}

// ApacheOps is the managed-Apache surface for the API.
type ApacheOps interface {
	Status() (string, error)
	Restart() error
	TestConfig(ctx context.Context) error
}
