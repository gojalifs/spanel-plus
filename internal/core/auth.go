package core

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Auth for the panel (Plan F6): one admin account, password stored
// only as a bcrypt hash, sessions as random bearer tokens in a
// HttpOnly cookie. The account file lives in the panel data dir
// (admin.json, next to registry.json) — never inside a project
// folder, never in the repo, and never logged.
//
// Trust boundary, stated plainly: this protects the panel over the
// network (LAN). A local console operator on the server itself can
// already read the data dir, so `panel admin set-password` (a local
// command) is allowed to set the password without the old one — the
// same trust that lets that operator run `panel serve` at all. The
// CLI state commands go through the HTTP API with the session token
// precisely so a network caller cannot skip this gate.

const (
	// SessionCookieName is the session cookie the API and CLI share.
	SessionCookieName = "spanel_session"
	// SessionIdleTimeout is how long a session survives without a
	// request (M5-AD, replacing M5's fixed 12h TTL): every
	// authenticated request slides the deadline forward by this
	// much, so active use never logs out mid-click, while an idle
	// session dies on its own. There is deliberately no absolute
	// session age in M5-AD.
	SessionIdleTimeout = 10 * time.Minute
	// LocalActor is the audit identity of the local break-glass
	// account; AD sessions use "ad:<username>" instead (see ad.go).
	LocalActor = "local:admin"
	// MinAdminPasswordLength keeps the single admin account from
	// being protected by a trivial password.
	MinAdminPasswordLength = 8
)

// ErrAdminNotConfigured marks a login attempt before any admin
// password has been set (first-run state).
var ErrAdminNotConfigured = errors.New("admin account is not configured yet")

// ErrBadCredentials marks a wrong admin password. Login handlers
// return the same message for "no such account" and "wrong password"
// so the two cases cannot be told apart.
var ErrBadCredentials = errors.New("invalid admin credentials")

// adminAccount is the on-disk shape of admin.json.
type adminAccount struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
}

type session struct {
	actor     string // audit identity: LocalActor or "ad:<username>"
	expiresAt time.Time
}

// AuthService owns the admin account and its sessions. Sessions live
// in memory only: restarting the panel logs everyone out, which is
// the safe default for a panel that runs as a Windows Service.
type AuthService struct {
	Files FileStore
	Path  string // admin.json path
	Clock Clock  // nil = real time

	mu       sync.Mutex
	sessions map[string]session // keyed by sha256(token), never the token itself
}

func (a *AuthService) now() time.Time {
	if a.Clock != nil {
		return a.Clock.Now()
	}
	return time.Now()
}

func (a *AuthService) load() (adminAccount, error) {
	raw, err := a.Files.ReadFile(a.Path)
	if err != nil {
		return adminAccount{}, err
	}
	var acc adminAccount
	if err := json.Unmarshal(raw, &acc); err != nil {
		return adminAccount{}, fmt.Errorf("admin file %s is corrupt: %w", a.Path, err)
	}
	return acc, nil
}

// Configured reports whether an admin password has been set.
func (a *AuthService) Configured() bool {
	acc, err := a.load()
	return err == nil && acc.PasswordHash != ""
}

// SetPassword replaces the admin password (first setup and later
// resets alike — both are local-console operations). Existing
// sessions are dropped: a password change must not leave old logins
// alive.
func (a *AuthService) SetPassword(password string) error {
	if len(password) < MinAdminPasswordLength {
		return fmt.Errorf("admin password must be at least %d characters", MinAdminPasswordLength)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash admin password: %w", err)
	}
	raw, err := json.MarshalIndent(adminAccount{Username: "admin", PasswordHash: string(hash)}, "", "  ")
	if err != nil {
		return err
	}
	if err := a.Files.WriteFileAtomic(a.Path, append(raw, '\n')); err != nil {
		return err
	}
	a.mu.Lock()
	a.sessions = map[string]session{}
	a.mu.Unlock()
	return nil
}

// Authenticate checks the local admin password and, on success,
// mints a session token. The returned token is the only value the
// cookie ever carries; the service stores only its hash.
func (a *AuthService) Authenticate(password string) (string, error) {
	acc, err := a.load()
	if err != nil || acc.PasswordHash == "" {
		return "", ErrAdminNotConfigured
	}
	if err := bcrypt.CompareHashAndPassword([]byte(acc.PasswordHash), []byte(password)); err != nil {
		return "", ErrBadCredentials
	}
	return a.CreateSession(LocalActor)
}

// CreateSession mints a session for an already-verified identity
// (used by the AD login path, after the directory said yes).
func (a *AuthService) CreateSession(actor string) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("mint session token: %w", err)
	}
	token := hex.EncodeToString(raw[:])
	a.mu.Lock()
	if a.sessions == nil {
		a.sessions = map[string]session{}
	}
	a.sessions[sessionKey(token)] = session{actor: actor, expiresAt: a.now().Add(SessionIdleTimeout)}
	a.mu.Unlock()
	return token, nil
}

// ValidateSession reports whether the token is a live session and,
// when it is, who it belongs to (the audit actor). Validation IS
// activity: a live session's idle deadline slides forward by
// SessionIdleTimeout on every call, so the 10-minute window always
// runs from the last request, never from login. Expired sessions
// are pruned on sight.
func (a *AuthService) ValidateSession(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	key := sessionKey(token)
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[key]
	if !ok {
		return "", false
	}
	if a.now().After(s.expiresAt) {
		delete(a.sessions, key)
		return "", false
	}
	s.expiresAt = a.now().Add(SessionIdleTimeout)
	a.sessions[key] = s
	return s.actor, true
}

// Logout drops one session; dropping an unknown token is not an error.
func (a *AuthService) Logout(token string) {
	a.mu.Lock()
	delete(a.sessions, sessionKey(token))
	a.mu.Unlock()
}

func sessionKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
