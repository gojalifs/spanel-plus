package core

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// AuditEntry is one recorded state-changing action (Plan F6: siapa,
// kapan, apa). The panel never audits read-only requests — the log
// must stay a short list of "who changed what", not a traffic dump.
type AuditEntry struct {
	Time   time.Time `json:"time"`
	Actor  string    `json:"actor"`  // "admin" for API/CLI sessions; "console" for local commands
	Action string    `json:"action"` // e.g. "POST /api/v1/projects/app/set-php"
	Detail string    `json:"detail"` // outcome: HTTP status, or a short result
}

// AuditLogger records audit entries. Implementations must be safe
// for concurrent use.
type AuditLogger interface {
	Append(entry AuditEntry) error
}

// FileAuditLogger appends JSON lines to one file in the panel data
// dir (audit.log). It reads-modifies-writes under a mutex: audit
// volume is one line per state change, so a full-file rewrite per
// entry is cheap and keeps the logger on the same FileStore
// abstraction as everything else (no new append primitive needed).
// A failed append must never fail the action it records — callers
// log it and move on.
type FileAuditLogger struct {
	Files FileStore
	Path  string
	Clock Clock // nil = real time

	mu sync.Mutex
}

// Append adds one entry to the audit log.
func (l *FileAuditLogger) Append(entry AuditEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if entry.Time.IsZero() {
		if l.Clock != nil {
			entry.Time = l.Clock.Now()
		} else {
			entry.Time = time.Now()
		}
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	var existing []byte
	if raw, err := l.Files.ReadFile(l.Path); err == nil {
		existing = raw
	}
	existing = append(existing, append(line, '\n')...)
	if err := l.Files.WriteFile(l.Path, existing); err != nil {
		return fmt.Errorf("write audit log %s: %w", l.Path, err)
	}
	return nil
}

// DiscardAuditLogger drops entries; used where no audit sink is
// wired (tests, degraded mode).
type DiscardAuditLogger struct{}

func (DiscardAuditLogger) Append(AuditEntry) error { return nil }
