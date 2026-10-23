// Package core holds the panel's business logic. It knows nothing about
// HTTP, the CLI, the Windows Service Control Manager, or the real clock:
// every side effect sits behind one of the interfaces in this file
// (Plan Bagian 10.1 — aturan testability, mengikat sejak M1).
package core

import (
	"context"
	"time"
)

// CommandRunner executes an external program and returns its combined
// output. The production implementation wraps os/exec; tests use a fake.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

// FileProbe abstracts the filesystem questions the core needs to ask, so
// doctor/config logic can be unit-tested with an in-memory fake.
type FileProbe interface {
	// Exists reports whether path exists (file or directory).
	Exists(path string) bool
	// IsDir reports whether path exists and is a directory.
	IsDir(path string) bool
	// EnsureWritableDir creates dir when missing and proves it is
	// writable by writing and removing a probe file inside it.
	EnsureWritableDir(dir string) error
}

// PortProbe checks whether a TCP port can be listened on.
type PortProbe interface {
	// Free returns nil when the port can be bound, or the bind error.
	Free(port int) error
}

// Clock abstracts time so uptime/backoff logic stays deterministic in tests.
type Clock interface {
	Now() time.Time
}

// ServiceController abstracts the operating system's service manager
// (Windows SCM in production). It is only used by the `service`
// subcommands; running the panel itself goes through the platform layer.
type ServiceController interface {
	Install() error
	Uninstall() error
	Start() error
	Stop() error
	// Status returns a short human-readable service state.
	Status() (string, error)
}
