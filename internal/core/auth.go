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
	// SessionTTL is how long one login stays valid (fixed, not
	// sliding: a stolen token stops working on its own).
	SessionTTL = 12 * time.Hour
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

// Authenticate checks the password and, on success, mints a session
// token. The returned token is the only value the cookie ever
// carries; the service stores only its hash.
func (a *AuthService) Authenticate(password string) (string, error) {
	acc, err := a.load()
	if err != nil || acc.PasswordHash == "" {
		return "", ErrAdminNotConfigured
	}
	if err := bcrypt.CompareHashAndPassword([]byte(acc.PasswordHash), []byte(password)); err != nil {
		return "", ErrBadCredentials
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("mint session token: %w", err)
	}
	token := hex.EncodeToString(raw[:])
	a.mu.Lock()
	if a.sessions == nil {
		a.sessions = map[string]session{}
	}
	a.sessions[sessionKey(token)] = session{expiresAt: a.now().Add(SessionTTL)}
	a.mu.Unlock()
	return token, nil
}

// ValidateSession reports whether the token is a live session.
// Expired sessions are pruned on sight.
func (a *AuthService) ValidateSession(token string) bool {
	if token == "" {
		return false
	}
	key := sessionKey(token)
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[key]
	if !ok {
		return false
	}
	if a.now().After(s.expiresAt) {
		delete(a.sessions, key)
		return false
	}
	return true
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
