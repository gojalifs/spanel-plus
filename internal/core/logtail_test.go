package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProcessLogPathIsDated(t *testing.T) {
	got := ProcessLogPathFor("logs", "emc", "queue", time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC))
	want := "logs/emc/queue-2026-10-30.log"
	if got != want {
		t.Errorf("ProcessLogPathFor = %q, want %q", got, want)
	}
}

func TestLatestProcessLogPath(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "emc")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// Nothing yet: today's dated path.
	if got := LatestProcessLogPath(dir, "emc", "queue"); got != ProcessLogPath(dir, "emc", "queue") {
		t.Errorf("empty dir: got %q, want today's path", got)
	}
	// A run from an earlier day is the live file until a newer one appears.
	old := filepath.Join(proj, "queue-2026-10-28.log")
	if err := os.WriteFile(old, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LatestProcessLogPath(dir, "emc", "queue"); got != old {
		t.Errorf("earlier-day file must win, got %q", got)
	}
	// A newer day supersedes it.
	today := filepath.Join(proj, "queue-"+time.Now().Format("2006-01-02")+".log")
	if err := os.WriteFile(today, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LatestProcessLogPath(dir, "emc", "queue"); got != today {
		t.Errorf("newest file must win, got %q", got)
	}
}

func TestLatestProcessLogPathFallsBackToLegacy(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "emc")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(proj, "queue.log")
	if err := os.WriteFile(legacy, []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LatestProcessLogPath(dir, "emc", "queue"); got != legacy {
		t.Errorf("legacy undated file must be the fallback, got %q", got)
	}
}

func TestPruneProcessLogs(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "emc")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	today := time.Now()
	old := today.AddDate(0, 0, -100).Format("2006-01-02")
	recent := today.AddDate(0, 0, -10).Format("2006-01-02")
	for _, name := range []string{
		"queue-" + old + ".log",
		"queue-" + recent + ".log",
		"queue.log",                 // legacy: never pruned
		"scheduler-" + old + ".log", // other process: untouched
	} {
		if err := os.WriteFile(filepath.Join(proj, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	PruneProcessLogs(dir, "emc", "queue", 90)
	if _, err := os.Stat(filepath.Join(proj, "queue-"+old+".log")); !os.IsNotExist(err) {
		t.Errorf("100-day-old queue log must be pruned")
	}
	for _, name := range []string{"queue-" + recent + ".log", "queue.log", "scheduler-" + old + ".log"} {
		if _, err := os.Stat(filepath.Join(proj, name)); err != nil {
			t.Errorf("%s must survive pruning: %v", name, err)
		}
	}
}
