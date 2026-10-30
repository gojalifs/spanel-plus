package core

import (
	"os"
	"strings"
	"time"
)

// LogRetentionDays bounds process log history: dated files older than
// this many days are pruned whenever a process (re)starts. 90 days is
// the standing "maksimal 3 bulan" policy, matching logging.RetentionDays
// for the panel log; tests may lower it.
var LogRetentionDays = 90

// ProcessLogPath is where a process definition's log lives for a run
// starting today: <logDir>/<project>/<name>-YYYY-MM-DD.log (local
// server time). A process is spawned with its start day's file, and a
// long-running process keeps writing that file across midnight — the
// child holds the file handle directly, so a strict midnight split
// would need output piped through the panel (a larger change, not
// done here). The Log view resolves the live file via
// LatestProcessLogPath instead of assuming today's file.
func ProcessLogPath(logDir, project, name string) string {
	return ProcessLogPathFor(logDir, project, name, time.Now())
}

// ProcessLogPathFor is ProcessLogPath for an explicit date (tests and
// backfills); production always passes time.Now via ProcessLogPath.
func ProcessLogPathFor(logDir, project, name string, t time.Time) string {
	return joinPath(joinPath(logDir, project), name+"-"+t.Format("2006-01-02")+".log")
}

// LatestProcessLogPath resolves the log file the Log view should show
// for a process: the newest dated <name>-YYYY-MM-DD.log in the
// project's log dir — which is the live file of a process running
// since an earlier day, or the most recent day it ran. It falls back
// to the legacy undated <name>.log (written before daily rotation),
// and to today's dated path when nothing exists yet.
func LatestProcessLogPath(logDir, project, name string) string {
	dir := joinPath(logDir, project)
	entries, err := os.ReadDir(dir)
	if err == nil {
		latest := ""
		prefix := name + "-"
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			n := e.Name()
			if !strings.HasPrefix(n, prefix) || !strings.HasSuffix(n, ".log") {
				continue
			}
			day := strings.TrimSuffix(strings.TrimPrefix(n, prefix), ".log")
			if _, err := time.Parse("2006-01-02", day); err != nil {
				continue
			}
			if day > latest {
				latest = day
			}
		}
		if latest != "" {
			return joinPath(dir, name+"-"+latest+".log")
		}
		if _, err := os.Stat(joinPath(dir, name+".log")); err == nil {
			return joinPath(dir, name+".log")
		}
	}
	return ProcessLogPath(logDir, project, name)
}

// PruneProcessLogs deletes a process's dated log files older than
// keepDays days (by filename date, compared as ISO dates). Best
// effort: unreadable directories and unremovable files are skipped,
// and the legacy undated <name>.log is never deleted. Callers ignore
// the result — pruning must never fail a process start.
func PruneProcessLogs(logDir, project, name string, keepDays int) {
	dir := joinPath(logDir, project)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -keepDays).Format("2006-01-02")
	prefix := name + "-"
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !strings.HasPrefix(n, prefix) || !strings.HasSuffix(n, ".log") {
			continue
		}
		day := strings.TrimSuffix(strings.TrimPrefix(n, prefix), ".log")
		if _, err := time.Parse("2006-01-02", day); err != nil {
			continue
		}
		if day < cutoff {
			_ = os.Remove(joinPath(dir, n))
		}
	}
}

// TailLog returns the last maxLines lines of a log file (reading at
// most the last maxBytes so a runaway log cannot exhaust memory).
// A missing log is an empty result, not an error — the process may
// simply not have run yet.
func TailLog(files FileStore, path string, maxLines, maxBytes int) (string, error) {
	raw, err := files.ReadFile(path)
	if err != nil {
		return "", nil
	}
	if maxBytes > 0 && len(raw) > maxBytes {
		raw = raw[len(raw)-maxBytes:]
		// Drop the partial first line of the window.
		if i := strings.IndexByte(string(raw), '\n'); i >= 0 {
			raw = raw[i+1:]
		}
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n"), nil
}
