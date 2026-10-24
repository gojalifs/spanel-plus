package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/gojalifs/spanel-plus/internal/core"
)

// ExecProcessStarter implements core.ProcessStarter with os/exec.
// The child's stdout/stderr append to the spec's log file (created
// with its parent dirs), alongside timestamped panel markers, so one
// file tells the whole story of a process (Plan F5: log per proses).
type ExecProcessStarter struct{}

func (ExecProcessStarter) Start(spec core.ProcessSpec) (core.ProcessHandle, error) {
	logFile, err := openProcessLog(spec.LogPath)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(logFile, "\n--- panel start %s (%s) ---\n", spec.Name, spec.Exe)
	cmd := exec.Command(spec.Exe, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("start %s: %w", spec.Exe, err)
	}
	return &execHandle{cmd: cmd, log: logFile}, nil
}

type execHandle struct {
	cmd  *exec.Cmd
	log  *os.File
	once sync.Once
	code int
}

func (h *execHandle) PID() int {
	if h.cmd.Process == nil {
		return 0
	}
	return h.cmd.Process.Pid
}

func (h *execHandle) Wait() int {
	h.once.Do(func() {
		err := h.cmd.Wait()
		h.code = exitCodeOf(err)
		fmt.Fprintf(h.log, "--- panel exit code=%d ---\n", h.code)
		_ = h.log.Close()
	})
	return h.code
}

func (h *execHandle) Kill() error {
	if h.cmd.Process == nil {
		return nil
	}
	return h.cmd.Process.Kill()
}

// ExecOneShotRunner implements core.OneShotRunner: runs to
// completion, captures combined output, and appends it to the spec's
// log file when one is set.
type ExecOneShotRunner struct{}

func (ExecOneShotRunner) Run(spec core.ProcessSpec) (string, int, error) {
	cmd := exec.Command(spec.Exe, spec.Args...)
	cmd.Dir = spec.Dir
	out, err := cmd.CombinedOutput()
	code := exitCodeOf(err)
	if spec.LogPath != "" {
		if lf, lerr := openProcessLog(spec.LogPath); lerr == nil {
			fmt.Fprintf(lf, "\n--- panel run %s (%s) exit=%d ---\n%s", spec.Name, spec.Exe, code, out)
			_ = lf.Close()
		}
	}
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return string(out), code, nil // a non-zero exit is a result, not a runner failure
		}
		return string(out), code, fmt.Errorf("run %s: %w", spec.Exe, err)
	}
	return string(out), code, nil
}

func openProcessLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create log dir for %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log %s: %w", path, err)
	}
	return f, nil
}

func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode() // -1 when killed by a signal
	}
	return -1
}
