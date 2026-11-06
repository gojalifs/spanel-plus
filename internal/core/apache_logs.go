package core

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Apache logs (access + error) in the panel.
//
// Apache itself writes no dated files: a plain ErrorLog/CustomLog
// grows one file forever. The panel therefore points Apache at
// rotatelogs.exe (shipped with every standard Apache 2.4 Windows
// build) via the generated global config, so each day gets its own
// access-YYYY-MM-DD.log / error-YYYY-MM-DD.log under
// <DataDir>/logs/apache — the same daily-file shape the panel log
// and the process logs already use. Rotation is Apache-native: no
// panel restart is needed at midnight.
//
// Retention follows the standing "maksimal 3 bulan" policy shared
// with the panel log (logging.RetentionDays) and the process logs
// (LogRetentionDays): dated files older than 90 days are pruned,
// best effort, on every successful apply and whenever the log list
// is opened. Files that do not match the dated pattern — the plain
// access.log/error.log fallback and anything foreign — are never
// pruned.
//
// When rotatelogs.exe is missing the apply does NOT fail: logging
// must never block serving. The generator falls back to plain
// access.log/error.log in the same dir and the manager logs a
// warning; doctor surfaces the same as a WARN check
// (apache-rotatelogs). A plain file is unbounded, so the fallback
// is honest about that in the generated comment.

// ApacheLogRetentionDays bounds Apache log history: dated files
// older than this many days are pruned. 90 days is the standing
// "maksimal 3 bulan" policy.
const ApacheLogRetentionDays = 90

// Apache log kinds, as used by the API and the dashboard.
const (
	ApacheLogAccess = "access"
	ApacheLogError  = "error"
)

// ApacheLogConfig is the log half of the generator input: where the
// logs live (slash path, as written into Apache config) and whether
// daily rotation is available. Zero value = no log directives at
// all, so Generator{} callers (diffs, golden files) are unaffected
// and Apache keeps its own default logs.
type ApacheLogConfig struct {
	Dir    string // slash path of the log dir; "" disables directives
	Rotate bool   // rotatelogs.exe found under ApacheHome/bin
}

// ApacheLogFile is one log file the dashboard can open.
type ApacheLogFile struct {
	Kind string `json:"kind"` // "access" | "error"
	Date string `json:"date"` // "YYYY-MM-DD", or "" for the plain fallback file
	Path string `json:"path"`
}

// HasRotateLogs reports whether rotatelogs.exe ships with this
// Apache install (bin/rotatelogs.exe under ApacheHome).
func HasRotateLogs(files FileProbe, apacheHome string) bool {
	if files == nil || apacheHome == "" {
		return false
	}
	return files.Exists(filepath.Join(apacheHome, "bin", "rotatelogs.exe"))
}

// ApacheLogConfigForApply resolves the generator's log config for
// one apply from the manager's own paths. An empty ApacheLogDir
// disables the directives; a missing rotatelogs.exe falls back to
// plain files with a warning in the panel log (never a failed
// apply).
func (m *ApacheManager) ApacheLogConfigForApply() ApacheLogConfig {
	dir := m.Cfg.ApacheLogDir
	if dir == "" {
		return ApacheLogConfig{}
	}
	rotate := HasRotateLogs(m.Files, m.Cfg.ApacheHome)
	if !rotate {
		m.logInfo("apache rotatelogs tidak ditemukan, log apache ditulis ke file tunggal tanpa rotasi harian", "apache_home", m.Cfg.ApacheHome, "log_dir", dir)
	}
	return ApacheLogConfig{Dir: slashPath(dir), Rotate: rotate}
}

// apacheLogFileName is the dated file name for a kind and a day.
func apacheLogFileName(kind string, day string) string {
	return kind + "-" + day + ".log"
}

// parseApacheLogName splits a log file name into (kind, date, ok):
// access-YYYY-MM-DD.log / error-YYYY-MM-DD.log, or the plain
// access.log / error.log fallback (date "").
func parseApacheLogName(name string) (kind, date string, ok bool) {
	for _, k := range []string{ApacheLogAccess, ApacheLogError} {
		if name == k+".log" {
			return k, "", true
		}
		if strings.HasPrefix(name, k+"-") && strings.HasSuffix(name, ".log") {
			day := strings.TrimSuffix(strings.TrimPrefix(name, k+"-"), ".log")
			if _, err := time.Parse("2006-01-02", day); err == nil {
				return k, day, true
			}
		}
	}
	return "", "", false
}

// ListApacheLogs returns the log files in dir, newest first within
// each kind (access before error). A missing dir reads empty, not
// an error — a fresh panel has no Apache logs yet.
func ListApacheLogs(files FileStore, dir string) ([]ApacheLogFile, error) {
	if dir == "" {
		return nil, nil
	}
	names, err := files.ListNames(dir)
	if err != nil {
		return nil, err
	}
	out := []ApacheLogFile{}
	for _, name := range names {
		kind, date, ok := parseApacheLogName(name)
		if !ok {
			continue
		}
		out = append(out, ApacheLogFile{Kind: kind, Date: date, Path: joinPath(dir, name)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind // "access" < "error"
		}
		// Plain fallback files (date "") sort after dated ones.
		if out[i].Date == "" {
			return false
		}
		if out[j].Date == "" {
			return true
		}
		return out[i].Date > out[j].Date
	})
	return out, nil
}

// PruneApacheLogs deletes dated Apache log files in dir older than
// keepDays days (by filename date, compared as ISO dates). Best
// effort: unreadable directories and unremovable files are skipped,
// and the plain fallback files are never deleted. Callers ignore
// the result — pruning must never fail an apply or a log view.
func PruneApacheLogs(files FileStore, dir string, keepDays int) {
	if dir == "" {
		return
	}
	names, err := files.ListNames(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -keepDays).Format("2006-01-02")
	for _, name := range names {
		_, date, ok := parseApacheLogName(name)
		if !ok || date == "" || date >= cutoff {
			continue
		}
		_ = files.Remove(joinPath(dir, name))
	}
}

// ListApacheLogs prunes files older than the retention window, then
// lists what remains. The manager owns the log dir, so the prune
// lives here rather than in the HTTP layer.
func (m *ApacheManager) ListApacheLogs() ([]ApacheLogFile, error) {
	dir := m.Cfg.ApacheLogDir
	if dir == "" {
		return nil, fmt.Errorf("log apache belum dikonfigurasi (ApacheLogDir kosong)")
	}
	PruneApacheLogs(m.Files, dir, ApacheLogRetentionDays)
	return ListApacheLogs(m.Files, dir)
}

// ReadApacheLog returns the tail of one Apache log: the dated file
// for date ("" = today), falling back to the plain access.log /
// error.log when no dated file exists (rotatelogs fallback mode).
// A missing log is an empty result, not an error — Apache may
// simply not have written yet.
func (m *ApacheManager) ReadApacheLog(kind, date string) (string, error) {
	if kind != ApacheLogAccess && kind != ApacheLogError {
		return "", fmt.Errorf("jenis log %q tidak dikenal (access/error)", kind)
	}
	if date == "" {
		date = time.Now().Format("2006-01-02")
	} else if _, err := time.Parse("2006-01-02", date); err != nil {
		return "", fmt.Errorf("format tanggal harus YYYY-MM-DD")
	}
	dir := m.Cfg.ApacheLogDir
	if dir == "" {
		return "", fmt.Errorf("log apache belum dikonfigurasi (ApacheLogDir kosong)")
	}
	dated := joinPath(dir, apacheLogFileName(kind, date))
	if m.Files.Exists(dated) {
		return TailLog(m.Files, dated, apacheLogMaxLines, apacheLogMaxBytes)
	}
	plain := joinPath(dir, kind+".log")
	if m.Files.Exists(plain) {
		return TailLog(m.Files, plain, apacheLogMaxLines, apacheLogMaxBytes)
	}
	return "", nil
}

// Tail window for one Apache log view: a busy access log grows
// fast, so the read is capped both in lines and in bytes.
const (
	apacheLogMaxLines = 300
	apacheLogMaxBytes = 256 << 10 // 256 KB
)
