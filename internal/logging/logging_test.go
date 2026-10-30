package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSetupWritesStructuredLogToTodayFile(t *testing.T) {
	dir := t.TempDir()
	logger, closer, err := Setup(dir, false)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	logger.Info("panel starting", "port", 8888)
	if err := closer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, DailyFileName(time.Now())))
	if err != nil {
		t.Fatalf("today's log file must exist: %v", err)
	}
	if !strings.Contains(string(raw), `"msg":"panel starting"`) {
		t.Errorf("expected structured JSON log line, got: %s", raw)
	}
	if !strings.Contains(string(raw), `"port":8888`) {
		t.Errorf("expected port attribute in log line, got: %s", raw)
	}
}

func TestSetupFailsLoudWhenLogDirImpossible(t *testing.T) {
	// A path whose parent is a regular file can never become a dir.
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Setup(filepath.Join(blocker, "logs"), false); err == nil {
		t.Fatal("Setup under a file path must fail loudly")
	}
}

func TestDailyWriterRollsOverAtMidnight(t *testing.T) {
	dir := t.TempDir()
	day := time.Date(2026, 10, 30, 23, 59, 0, 0, time.Local)
	w := &dailyWriter{dir: dir, now: func() time.Time { return day }, keepDays: 90}
	if _, err := w.Write([]byte("before\n")); err != nil {
		t.Fatal(err)
	}
	day = day.Add(2 * time.Minute) // 2026-10-31 00:01 local
	if _, err := w.Write([]byte("after\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "panel-2026-10-30.log"))
	if err != nil || string(before) != "before\n" {
		t.Errorf("day 1 file = %q, err %v", before, err)
	}
	after, err := os.ReadFile(filepath.Join(dir, "panel-2026-10-31.log"))
	if err != nil || string(after) != "after\n" {
		t.Errorf("day 2 file = %q, err %v", after, err)
	}
}

func TestPruneKeepsRecentAndIgnoresLegacy(t *testing.T) {
	dir := t.TempDir()
	today := time.Now()
	old := today.AddDate(0, 0, -100).Format("2006-01-02")
	recent := today.AddDate(0, 0, -10).Format("2006-01-02")
	for _, name := range []string{
		"panel-" + old + ".log",
		"panel-" + recent + ".log",
		"panel.log",       // legacy single file: never pruned
		"panel.log.1",     // legacy backup: never pruned
		"panel-notes.log", // non-dated: never pruned
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	Prune(dir, 90)
	if _, err := os.Stat(filepath.Join(dir, "panel-"+old+".log")); !os.IsNotExist(err) {
		t.Errorf("100-day-old file must be pruned")
	}
	for _, name := range []string{"panel-" + recent + ".log", "panel.log", "panel.log.1", "panel-notes.log"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s must survive pruning: %v", name, err)
		}
	}
}

func TestSetupPrunesAtStartup(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().AddDate(0, 0, -100).Format("2006-01-02")
	if err := os.WriteFile(filepath.Join(dir, "panel-"+old+".log"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, closer, err := Setup(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	closer.Close()
	if _, err := os.Stat(filepath.Join(dir, "panel-"+old+".log")); !os.IsNotExist(err) {
		t.Errorf("stale file must be pruned at startup")
	}
}
