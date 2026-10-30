package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupWritesStructuredLogToFile(t *testing.T) {
	dir := t.TempDir()
	logger, closer, err := Setup(dir, false)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	logger.Info("panel starting", "port", 8888)
	if err := closer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("log file must exist: %v", err)
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

func TestSetupRotatesOvergrownLog(t *testing.T) {
	old := MaxLogBytes
	MaxLogBytes = 100
	defer func() { MaxLogBytes = old }()

	dir := t.TempDir()
	big := make([]byte, 200)
	for i := range big {
		big[i] = 'x'
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), big, 0o644); err != nil {
		t.Fatal(err)
	}
	logger, closer, err := Setup(dir, false)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	logger.Info("fresh line")
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	rotated, err := os.ReadFile(filepath.Join(dir, FileName+".1"))
	if err != nil {
		t.Fatalf("overgrown log must rotate to panel.log.1: %v", err)
	}
	if len(rotated) != 200 {
		t.Errorf("rotated file must keep the old content, got %d bytes", len(rotated))
	}
	fresh, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fresh), "fresh line") {
		t.Errorf("new lines must go to a fresh panel.log, got: %s", fresh)
	}
}
