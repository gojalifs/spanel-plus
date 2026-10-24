package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/gojalifs/spanel-plus/internal/config"
	"github.com/gojalifs/spanel-plus/internal/core"
)

// --- fakes ---

type fakeService struct{ err error }

func (f fakeService) Install() error   { return f.err }
func (f fakeService) Uninstall() error { return f.err }
func (f fakeService) Start() error     { return f.err }
func (f fakeService) Stop() error      { return f.err }
func (f fakeService) Status() (string, error) {
	return "running", f.err
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Unix(1700000000, 0) }

type okFiles struct{}

func (okFiles) Exists(string) bool             { return true }
func (okFiles) IsDir(string) bool              { return true }
func (okFiles) EnsureWritableDir(string) error { return nil }

type okRunner struct{}

func (okRunner) Run(context.Context, string, ...string) (string, error) {
	return "Server version: Apache/2.4.69 (Win64)", nil
}

type okPorts struct{}

func (okPorts) Free(int) error { return nil }

func testDeps() (Deps, *bytes.Buffer, *bytes.Buffer) {
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cfg := config.Config{
		Port:          8888,
		DataDir:       `C:\panel\data`,
		LogDir:        `C:\panel\logs`,
		ApacheHome:    `C:\m0\Apache24`,
		ApacheService: "Apache2.4",
	}
	deps := Deps{
		Out:        out,
		ErrOut:     errOut,
		LoadConfig: func() (config.Config, error) { return cfg, nil },
		NewLogger: func(string, bool) (*slog.Logger, io.Closer, error) {
			return slog.New(slog.NewTextHandler(io.Discard, nil)), io.NopCloser(nil), nil
		},
		Doctor:      core.Doctor{Runner: okRunner{}, Files: okFiles{}, Ports: okPorts{}},
		Service:     fakeService{},
		Clock:       fixedClock{},
		Serve:       func(config.Config, *slog.Logger) error { return nil },
		OpenDesktop: func(config.Config, *slog.Logger) error { return nil },
	}
	return deps, out, errOut
}

func TestHelpAndVersion(t *testing.T) {
	deps, out, _ := testDeps()
	if code := New(deps).Run([]string{"help"}); code != 0 {
		t.Errorf("help exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "doctor") {
		t.Errorf("usage must list commands, got: %.200s", out.String())
	}

	deps, out, _ = testDeps()
	if code := New(deps).Run([]string{"version"}); code != 0 {
		t.Errorf("version exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "SPanel+") {
		t.Errorf("version output must name the panel, got: %q", out.String())
	}
}

func TestAppCommandAndNoArgsOpenDesktop(t *testing.T) {
	// Keputusan max 2026-10-24: panel diakses via exe sebagai aplikasi
	// desktop — double-click (tanpa argumen) dan `panel app` sama-sama
	// membuka jendela desktop.
	for _, args := range [][]string{{"app"}, {}} {
		deps, _, _ := testDeps()
		opened := false
		deps.OpenDesktop = func(config.Config, *slog.Logger) error { opened = true; return nil }
		if code := New(deps).Run(args); code != 0 {
			t.Errorf("Run(%v) exit = %d, want 0", args, code)
		}
		if !opened {
			t.Errorf("Run(%v) must invoke OpenDesktop", args)
		}
	}

	deps, _, errOut := testDeps()
	deps.OpenDesktop = func(config.Config, *slog.Logger) error { return errors.New("no webview2") }
	if code := New(deps).Run([]string{"app"}); code != 1 {
		t.Errorf("failing app exit = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "no webview2") {
		t.Errorf("app failure must surface the error, got: %q", errOut.String())
	}
}

func TestUnknownCommandIsUsageError(t *testing.T) {
	deps, _, errOut := testDeps()
	if code := New(deps).Run([]string{"ngawur"}); code != 2 {
		t.Errorf("unknown command exit = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "tidak dikenal") {
		t.Errorf("error output should explain, got: %q", errOut.String())
	}
}

func TestDoctorHealthyExitsZero(t *testing.T) {
	deps, out, _ := testDeps()
	if code := New(deps).Run([]string{"doctor"}); code != 0 {
		t.Fatalf("healthy doctor exit = %d, want 0; output: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "SEHAT") {
		t.Errorf("doctor output must summarise health, got: %s", out.String())
	}
}

func TestDoctorUnhealthyExitsOne(t *testing.T) {
	deps, _, _ := testDeps()
	deps.Doctor.Ports = failingPorts{}
	if code := New(deps).Run([]string{"doctor"}); code != 1 {
		t.Errorf("unhealthy doctor exit = %d, want 1 (fail loud for scripts)", code)
	}
}

type failingPorts struct{}

func (failingPorts) Free(int) error { return errors.New("in use") }

func TestServiceCommandsDelegate(t *testing.T) {
	deps, out, _ := testDeps()
	if code := New(deps).Run([]string{"service", "status"}); code != 0 {
		t.Fatalf("service status exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "running") {
		t.Errorf("status output must show service state, got: %q", out.String())
	}

	deps, _, errOut := testDeps()
	deps.Service = fakeService{err: errors.New("access denied")}
	if code := New(deps).Run([]string{"service", "install"}); code != 1 {
		t.Errorf("failing install exit = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "access denied") {
		t.Errorf("service failure must surface the error, got: %q", errOut.String())
	}
}

func TestServeSuccessExitsZero(t *testing.T) {
	deps, _, _ := testDeps()
	served := false
	deps.Serve = func(config.Config, *slog.Logger) error { served = true; return nil }
	if code := New(deps).Run([]string{"serve"}); code != 0 {
		t.Errorf("successful serve exit = %d, want 0", code)
	}
	if !served {
		t.Error("serve command must invoke the Serve dependency")
	}
}

func TestServiceInstallSuccess(t *testing.T) {
	deps, out, _ := testDeps()
	if code := New(deps).Run([]string{"service", "install"}); code != 0 {
		t.Fatalf("service install exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "terpasang") {
		t.Errorf("install output must confirm, got: %q", out.String())
	}
}

func TestServeFailureExitsOne(t *testing.T) {
	deps, _, _ := testDeps()
	deps.Serve = func(config.Config, *slog.Logger) error { return errors.New("port busy") }
	if code := New(deps).Run([]string{"serve"}); code != 1 {
		t.Errorf("failing serve exit = %d, want 1", code)
	}
}

func TestConfigFailureExitsOne(t *testing.T) {
	deps, _, errOut := testDeps()
	deps.LoadConfig = func() (config.Config, error) { return config.Config{}, errors.New("bad json") }
	if code := New(deps).Run([]string{"doctor"}); code != 1 {
		t.Errorf("config failure exit = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "bad json") {
		t.Errorf("config error must be surfaced, got: %q", errOut.String())
	}
}
