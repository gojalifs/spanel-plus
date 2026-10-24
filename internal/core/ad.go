package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// Directory authentication (Plan F6, M5-AD): the panel verifies AD
// credentials against a directory instead of storing them. The
// directory itself sits behind this interface (Plan 10.1) — the real
// LDAP client lives in internal/platform, the dummy directory below
// lives here, and tests use either without touching a network.

// Authenticator verifies one directory account. It returns true
// only when the password is right AND the account is a member of the
// configured IT group. A false result must not distinguish "wrong
// password" from "not a group member" — login errors stay generic.
type Authenticator interface {
	Authenticate(ctx context.Context, username, password string) (bool, error)
}

// ADConfig is the directory configuration, read from panel.json
// (F6: JSON-only, no service account, no separate file).
type ADConfig struct {
	Enabled       bool
	Server        string // ldaps://dc.siws.local:636, ldap://... (StartTLS), or "dummy"
	Domain        string // UPN suffix, e.g. siws.local
	BaseDN        string // e.g. DC=siws,DC=local
	GroupDN       string // e.g. CN=IS,DC=siws,DC=local (placeholder until the real DN is confirmed)
	CAFile        string // optional PEM for a DC cert from an internal CA
	AllowInsecure bool   // plain LDAP without TLS; test-only, logs a loud warning
	DummyUsers    []DummyUser
}

// IsDummy reports whether this config selects the development
// directory instead of a real one. Dummy mode exists so the AD login
// flow (toggle, session, audit actor) can be proven end-to-end
// without a domain controller; it is NEVER production AD, and the
// panel says so loudly at startup and in doctor.
func (c ADConfig) IsDummy() bool { return c.Enabled && c.Server == "dummy" }

// UserPrincipalName builds the bind name for a login: a bare
// sAMAccountName gets the configured domain suffix; a value that
// already looks like a UPN is used as-is.
func (c ADConfig) UserPrincipalName(username string) string {
	username = strings.TrimSpace(username)
	if strings.Contains(username, "@") {
		return username
	}
	return username + "@" + c.Domain
}

// GroupCN extracts the CN from the configured group DN, for the
// dummy directory's group comparison (the real LDAP path compares
// full DNs instead).
func (c ADConfig) GroupCN() string {
	for _, part := range strings.Split(c.GroupDN, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 && strings.EqualFold(kv[0], "CN") {
			return kv[1]
		}
	}
	return ""
}

// Validate checks the directory configuration. A dummy config needs
// dummy users; a real config needs server, domain, base DN and group
// DN. Validation failure disables AD (the local break-glass account
// keeps working) — it must never take the whole panel down.
func (c ADConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.IsDummy() {
		if len(c.DummyUsers) == 0 {
			return errors.New("ad: dummy mode is enabled but ad_dummy_users is empty — add test users or disable ad")
		}
		for _, u := range c.DummyUsers {
			if u.Username == "" || u.PasswordHash == "" {
				return errors.New("ad: dummy user needs a username and a bcrypt password_hash (generate one with: panel admin hash-password)")
			}
		}
		return nil
	}
	if c.Server == "" || c.Domain == "" || c.BaseDN == "" || c.GroupDN == "" {
		return errors.New("ad: ad_server, ad_domain, ad_base_dn and ad_group_dn are all required when ad is enabled")
	}
	return nil
}

// DummyUser is one account in the development directory. Passwords
// are bcrypt hashes, exactly like the local admin account — never
// plaintext, even for test accounts.
type DummyUser struct {
	Username     string   `json:"username"`
	PasswordHash string   `json:"password_hash"`
	Groups       []string `json:"groups"` // group CNs, e.g. ["IS"]
}

// DummyAuthenticator is the development directory: it checks a
// password hash and a group CN from config, nothing more. It exists
// so the AD login flow can be exercised without a DC; production
// must use the LDAP authenticator instead.
type DummyAuthenticator struct {
	Users   []DummyUser
	GroupCN string
}

// Authenticate implements Authenticator for the dummy directory.
func (d DummyAuthenticator) Authenticate(_ context.Context, username, password string) (bool, error) {
	for _, u := range d.Users {
		if !strings.EqualFold(u.Username, username) {
			continue
		}
		if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
			return false, nil
		}
		for _, g := range u.Groups {
			if strings.EqualFold(g, d.GroupCN) {
				return true, nil
			}
		}
		return false, nil // right password, not a group member
	}
	return false, nil
}

// HashPassword renders a bcrypt hash for pasting into config (used
// by `panel admin hash-password` for dummy users).
func HashPassword(password string) (string, error) {
	if len(password) < MinAdminPasswordLength {
		return "", fmt.Errorf("password must be at least %d characters", MinAdminPasswordLength)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}
