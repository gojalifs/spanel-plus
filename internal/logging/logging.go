// Package logging sets up the panel's structured logger (stdlib slog).
// Logs always go to a file under the panel's own log dir; console mode
// additionally mirrors them to stderr so an operator running
// `panel serve` in a terminal sees what the service is doing.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// FileName is the panel log file inside the configured log dir.
const FileName = "panel.log"

// Setup creates logDir when needed, opens the log file for appending,
// and returns a logger plus a closer for the file. Any failure is a
// loud error: a panel that cannot log must not start silently blind.
func Setup(logDir string, alsoConsole bool) (*slog.Logger, io.Closer, error) {
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("logging: create log dir %s: %w", logDir, err)
	}
	path := filepath.Join(logDir, FileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("logging: open %s: %w", path, err)
	}
	var w io.Writer = f
	if alsoConsole {
		w = io.MultiWriter(f, os.Stderr)
	}
	logger := slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
	return logger, f, nil
}
