// Package logging sets up the panel's structured logger (stdlib slog).
// Logs always go to files under the panel's own log dir; console mode
// additionally mirrors them to stderr so an operator running
// `panel serve` in a terminal sees what the service is doing.
//
// The panel log is split per day: panel-YYYY-MM-DD.log in local server
// time, rolling over at midnight while the panel runs. Files older than
// RetentionDays (90 days, about 3 months) are pruned at startup and on
// every midnight rollover. This replaces the old single panel.log with
// a 10 MB startup rotation: a day's file is bounded by that day's
// activity, and total disk use is bounded by the retention window.
// Files that do not match the panel-YYYY-MM-DD.log pattern are never
// pruned — including the legacy panel.log / panel.log.1 from before
// daily rotation, which the operator may delete by hand once checked.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DailyFileName returns the panel log file name for a date (local time).
func DailyFileName(t time.Time) string {
	return "panel-" + t.Format("2006-01-02") + ".log"
}

// RetentionDays bounds panel log history: dated files older than this
// many days are pruned. 90 days is the standing "maksimal 3 bulan"
// policy; tests may lower it.
var RetentionDays = 90

// Prune deletes panel-YYYY-MM-DD.log files in dir older than keepDays
// days (by filename date, compared as ISO dates in local time).
// Best effort by design: an unreadable directory or an unremovable
// file is skipped silently — pruning must never break logging or
// startup. Non-matching files are left alone.
func Prune(dir string, keepDays int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -keepDays).Format("2006-01-02")
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "panel-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		day := strings.TrimSuffix(strings.TrimPrefix(name, "panel-"), ".log")
		if _, err := time.Parse("2006-01-02", day); err != nil {
			continue
		}
		if day < cutoff {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

// dailyWriter is the panel log sink: it appends to today's dated file
// and switches files at midnight without restarting the panel. slog
// may write concurrently, so every write takes the mutex. The writer
// is the panel's single log writer (service or console, never both:
// the desktop reuses a running service instead of starting a second
// one), so no cross-process locking is attempted.
type dailyWriter struct {
	mu       sync.Mutex
	dir      string
	now      func() time.Time
	keepDays int

	curDate string
	f       *os.File
}

// rotateLocked closes the current file (if any) and opens the file for
// day, pruning old files as a side effect. Caller holds w.mu.
func (w *dailyWriter) rotateLocked(day string) error {
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}
	f, err := os.OpenFile(filepath.Join(w.dir, "panel-"+day+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		w.curDate = ""
		return err
	}
	w.f = f
	w.curDate = day
	Prune(w.dir, w.keepDays)
	return nil
}

// openLocked opens today's file when none is open. Caller holds w.mu.
func (w *dailyWriter) openLocked() error {
	if w.f != nil {
		return nil
	}
	return w.rotateLocked(w.now().Format("2006-01-02"))
}

func (w *dailyWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	day := w.now().Format("2006-01-02")
	if w.f == nil || day != w.curDate {
		if err := w.rotateLocked(day); err != nil {
			return 0, err
		}
	}
	return w.f.Write(p)
}

// Close releases the current file handle. Logging after Close reopens
// today's file on the next write; Setup's closer is only used at
// shutdown, so this path is not expected in normal operation.
func (w *dailyWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	w.curDate = ""
	return err
}

// Setup creates logDir when needed, prunes log files older than the
// retention window, opens today's dated log file, and returns a
// logger plus a closer for the file. Any failure is a loud error: a
// panel that cannot log must not start silently blind. A prune failure
// never blocks startup (see Prune).
func Setup(logDir string, alsoConsole bool) (*slog.Logger, io.Closer, error) {
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("logging: create log dir %s: %w", logDir, err)
	}
	Prune(logDir, RetentionDays)
	w := &dailyWriter{dir: logDir, now: time.Now, keepDays: RetentionDays}
	w.mu.Lock()
	err := w.openLocked()
	w.mu.Unlock()
	if err != nil {
		return nil, nil, fmt.Errorf("logging: open today's log: %w", err)
	}
	var out io.Writer = w
	if alsoConsole {
		out = io.MultiWriter(w, os.Stderr)
	}
	logger := slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: slog.LevelInfo}))
	return logger, w, nil
}
