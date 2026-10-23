// Package platform holds the real, OS-backed implementations of the
// core ports (Plan 10.1): everything in here touches the machine, and
// everything in core stays testable without it.
package platform

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/gojalifs/spanel-plus/internal/core"
)

// ExecRunner is the production core.CommandRunner (os/exec).
type ExecRunner struct{}

// Run executes name with args and returns combined stdout+stderr.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// OSFileProbe is the production core.FileProbe (os filesystem).
type OSFileProbe struct{}

// Exists reports whether path exists.
func (OSFileProbe) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// IsDir reports whether path exists and is a directory.
func (OSFileProbe) IsDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// EnsureWritableDir creates dir if needed and write-tests it.
func (OSFileProbe) EnsureWritableDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	probe := filepath.Join(dir, ".write-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		return err
	}
	if err := os.Remove(probe); err != nil {
		return fmt.Errorf("remove write probe in %s: %w", dir, err)
	}
	return nil
}

// OSPortProbe is the production core.PortProbe (real TCP bind attempt).
type OSPortProbe struct{}

// Free binds the port briefly; success means nobody is listening.
func (OSPortProbe) Free(port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}
	return ln.Close()
}

// RealClock is the production core.Clock.
type RealClock struct{}

// Now returns the current time.
func (RealClock) Now() time.Time { return time.Now() }

// Compile-time proof the implementations satisfy the core ports.
var (
	_ core.CommandRunner = ExecRunner{}
	_ core.FileProbe     = OSFileProbe{}
	_ core.PortProbe     = OSPortProbe{}
	_ core.Clock         = RealClock{}
)
