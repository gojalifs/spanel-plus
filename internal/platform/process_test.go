package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gojalifs/spanel-plus/internal/core"
)

func shSpec(t *testing.T, script string) core.ProcessSpec {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("test uses /bin/sh")
	}
	return core.ProcessSpec{
		Project: "demo", Name: "proc",
		Exe: "/bin/sh", Args: []string{"-c", script},
		Dir:     t.TempDir(),
		LogPath: filepath.Join(t.TempDir(), "demo", "proc.log"),
	}
}

func TestExecProcessStarterRunsAndLogs(t *testing.T) {
	spec := shSpec(t, "echo halo-dunia; exit 3")
	h, err := (ExecProcessStarter{}).Start(spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if h.PID() <= 0 {
		t.Errorf("PID must be positive, got %d", h.PID())
	}
	if code := h.Wait(); code != 3 {
		t.Errorf("Wait code = %d, want 3", code)
	}
	raw, err := os.ReadFile(spec.LogPath)
	if err != nil || !strings.Contains(string(raw), "halo-dunia") || !strings.Contains(string(raw), "exit code=3") {
		t.Errorf("log must capture output and the exit marker: err=%v body=%q", err, raw)
	}
}

func TestExecProcessKill(t *testing.T) {
	spec := shSpec(t, "sleep 30")
	h, err := (ExecProcessStarter{}).Start(spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done := make(chan int, 1)
	go func() { done <- h.Wait() }()
	if err := h.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	select {
	case code := <-done:
		if code == 0 {
			t.Error("a killed process must not report exit 0")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return after Kill")
	}
}

func TestExecOneShotRunner(t *testing.T) {
	spec := shSpec(t, "echo sekali-jalan; exit 2")
	out, code, err := (ExecOneShotRunner{}).Run(spec)
	if err != nil {
		t.Fatalf("Run: %v (a non-zero exit is a result, not an error)", err)
	}
	if code != 2 || !strings.Contains(out, "sekali-jalan") {
		t.Errorf("Run = %q, %d", out, code)
	}
	raw, _ := os.ReadFile(spec.LogPath)
	if !strings.Contains(string(raw), "sekali-jalan") {
		t.Errorf("one-shot output must also land in the log, got %q", raw)
	}
}
