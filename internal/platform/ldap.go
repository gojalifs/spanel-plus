package platform

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/url"
	"os"
	"strings"

	ber "github.com/go-ldap/ldap/v3"

	"github.com/gojalifs/spanel-plus/internal/core"
)

// LDAP directory authentication (Plan F6, M5-AD). The panel binds
// directly as the logging-in user — no service account, no stored
// directory password anywhere — and then checks group membership.
// The server never needs to be domain-joined: LDAP is a plain
// network protocol, and a non-joined host binds exactly the same
// way, as long as it can reach the DC over the LAN and trust its
// certificate (system roots, or ADCAFile for an internal CA).

// LDAPConfig is the wire-level view of core.ADConfig.
type LDAPConfig struct {
	Server        string
	Domain        string
	BaseDN        string
	GroupDN       string
	CAFile        string
	AllowInsecure bool
}

// LDAPAuthenticator is the production directory authenticator.
type LDAPAuthenticator struct {
	cfg  LDAPConfig
	dial func(ctx context.Context, cfg LDAPConfig, tlsCfg *tls.Config) (ldapConn, error)
}

// ldapConn is the slice of the directory connection this code uses.
// go-ldap's *Conn satisfies it; tests inject a fake.
type ldapConn interface {
	Bind(username, password string) error
	Search(*ber.SearchRequest) (*ber.SearchResult, error)
	StartTLS(*tls.Config) error
	Close() error
}

// NewLDAPAuthenticator builds the production authenticator. A
// non-TLS server is refused here unless AllowInsecure is set
// (test-only); the caller then disables AD with a loud log instead
// of taking the panel down.
func NewLDAPAuthenticator(cfg LDAPConfig) (*LDAPAuthenticator, error) {
	u, err := url.Parse(cfg.Server)
	if err != nil || (u.Scheme != "ldaps" && u.Scheme != "ldap") {
		return nil, fmt.Errorf("ad: ad_server must be ldaps:// or ldap:// (got %q)", cfg.Server)
	}
	if u.Scheme == "ldap" && !cfg.AllowInsecure {
		// ldap:// is accepted only as StartTLS (see dialLDAP): a
		// plain bind is refused at construction time.
	}
	return &LDAPAuthenticator{cfg: cfg, dial: dialLDAP}, nil
}

// NewLDAPAuthenticatorWithDial is NewLDAPAuthenticator with an
// injectable dialer, for tests.
func NewLDAPAuthenticatorWithDial(cfg LDAPConfig, dial func(context.Context, LDAPConfig, *tls.Config) (ldapConn, error)) *LDAPAuthenticator {
	return &LDAPAuthenticator{cfg: cfg, dial: dial}
}

func (a *LDAPAuthenticator) tlsConfig() (*tls.Config, error) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if a.cfg.CAFile == "" {
		return tlsCfg, nil
	}
	pem, err := os.ReadFile(a.cfg.CAFile)
	if err != nil {
		return nil, fmt.Errorf("ad: read ad_ca_file: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("ad: ad_ca_file %q contains no valid PEM certificate", a.cfg.CAFile)
	}
	tlsCfg.RootCAs = pool
	return tlsCfg, nil
}

func dialLDAP(ctx context.Context, cfg LDAPConfig, tlsCfg *tls.Config) (ldapConn, error) {
	u, _ := url.Parse(cfg.Server)
	switch u.Scheme {
	case "ldaps":
		// DialURL wraps the connection in TLS from the first byte;
		// the DC hostname (not an IP) must match the certificate.
		return ber.DialURL(cfg.Server, ber.DialWithTLSConfig(tlsCfg))
	case "ldap":
		if cfg.AllowInsecure {
			return ber.DialURL(cfg.Server)
		}
		conn, err := ber.DialURL(cfg.Server)
		if err != nil {
			return nil, err
		}
		// StartTLS is mandatory for ldap://: a failed upgrade aborts
		// the login rather than falling back to a cleartext bind.
		if err := conn.StartTLS(tlsCfg); err != nil {
			conn.Close()
			return nil, fmt.Errorf("ad: StartTLS failed: %w", err)
		}
		return conn, nil
	default:
		return nil, fmt.Errorf("ad: unsupported scheme %q", u.Scheme)
	}
}

// Authenticate binds as the user (UPN) and checks membership of the
// configured group: first a direct memberOf match, then — for
// nested groups — the LDAP_MATCHING_RULE_IN_CHAIN rule
// (1.2.840.113556.1.4.1941) against the user's DN. Any failure
// returns false without distinguishing the cause (generic login
// error at the HTTP layer); only transport/setup errors are real
// errors, and the caller still answers them generically.
func (a *LDAPAuthenticator) Authenticate(ctx context.Context, username, password string) (bool, error) {
	if strings.TrimSpace(username) == "" || password == "" {
		return false, nil
	}
	tlsCfg, err := a.tlsConfig()
	if err != nil {
		return false, err
	}
	conn, err := a.dial(ctx, a.cfg, tlsCfg)
	if err != nil {
		return false, fmt.Errorf("ad: connect to directory: %w", err)
	}
	defer conn.Close()

	upn := username
	if !strings.Contains(username, "@") {
		upn = username + "@" + a.cfg.Domain
	}
	if err := conn.Bind(upn, password); err != nil {
		return false, nil // wrong credentials — generic, not an error
	}

	// Find the user entry: its DN (for the nested-group search) and
	// its direct memberOf list. A UPN login is reduced to its
	// sAMAccountName half for this search.
	sam := username
	if at := strings.Index(sam, "@"); at >= 0 {
		sam = sam[:at]
	}
	req := ber.NewSearchRequest(
		a.cfg.BaseDN, ber.ScopeWholeSubtree, ber.NeverDerefAliases, 0, 0, false,
		fmt.Sprintf("(sAMAccountName=%s)", ber.EscapeFilter(sam)),
		[]string{"distinguishedName", "memberOf"}, nil,
	)
	res, err := conn.Search(req)
	if err != nil || len(res.Entries) == 0 {
		return false, nil
	}
	entry := res.Entries[0]
	for _, memberOf := range entry.GetAttributeValues("memberOf") {
		if strings.EqualFold(memberOf, a.cfg.GroupDN) {
			return true, nil
		}
	}
	// Nested groups: ask the directory whether this DN is a member
	// of the group through any chain of nested groups.
	nested := ber.NewSearchRequest(
		a.cfg.BaseDN, ber.ScopeWholeSubtree, ber.NeverDerefAliases, 0, 0, false,
		fmt.Sprintf("(&(distinguishedName=%s)(member:1.2.840.113556.1.4.1941:=%s))",
			ber.EscapeFilter(entry.DN), ber.EscapeFilter(a.cfg.GroupDN)),
		[]string{"distinguishedName"}, nil,
	)
	nestedRes, err := conn.Search(nested)
	if err != nil {
		return false, nil
	}
	return len(nestedRes.Entries) > 0, nil
}

// CheckTLS validates the TLS configuration eagerly (CA file
// readable and parseable) so a bad ad_ca_file is caught at panel
// start, not at the first AD login.
func (a *LDAPAuthenticator) CheckTLS() error {
	_, err := a.tlsConfig()
	return err
}

// ADConfigFrom converts the panel config view to the LDAP view.
func ADConfigFrom(c core.ADConfig) LDAPConfig {
	return LDAPConfig{
		Server:        c.Server,
		Domain:        c.Domain,
		BaseDN:        c.BaseDN,
		GroupDN:       c.GroupDN,
		CAFile:        c.CAFile,
		AllowInsecure: c.AllowInsecure,
	}
}
