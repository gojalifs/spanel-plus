package platform

import (
	"context"
	"crypto/tls"
	"errors"
	"strings"
	"testing"

	ber "github.com/go-ldap/ldap/v3"
)

// fakeConn answers LDAP calls from canned data: Bind succeeds only
// for the configured password, Search returns the configured user
// entry, and the nested-group search returns nestedCount entries.
type fakeConn struct {
	password   string
	entry      *ber.Entry
	nestedHits int
	boundUser  string
}

func (f *fakeConn) Bind(username, password string) error {
	if password != f.password {
		return errors.New("invalid credentials")
	}
	f.boundUser = username
	return nil
}

func (f *fakeConn) Search(req *ber.SearchRequest) (*ber.SearchResult, error) {
	if strings.Contains(req.Filter, "1.2.840.113556.1.4.1941") {
		entries := []*ber.Entry{}
		for i := 0; i < f.nestedHits; i++ {
			entries = append(entries, &ber.Entry{DN: "CN=budi,DC=siws,DC=local"})
		}
		return &ber.SearchResult{Entries: entries}, nil
	}
	if f.entry == nil {
		return &ber.SearchResult{}, nil
	}
	return &ber.SearchResult{Entries: []*ber.Entry{f.entry}}, nil
}

func (f *fakeConn) StartTLS(*tls.Config) error { return nil }
func (f *fakeConn) Close() error               { return nil }

func ldapTestAuth(t *testing.T, entry *ber.Entry, nestedHits int) *LDAPAuthenticator {
	t.Helper()
	fake := &fakeConn{password: "budi-kuat-1", entry: entry, nestedHits: nestedHits}
	return NewLDAPAuthenticatorWithDial(LDAPConfig{
		Server:  "ldaps://dc.siws.local:636",
		Domain:  "siws.local",
		BaseDN:  "DC=siws,DC=local",
		GroupDN: "CN=IS,DC=siws,DC=local",
	}, func(context.Context, LDAPConfig, *tls.Config) (ldapConn, error) { return fake, nil })
}

func TestLDAPDirectMembership(t *testing.T) {
	entry := &ber.Entry{DN: "CN=budi,DC=siws,DC=local", Attributes: []*ber.EntryAttribute{
		{Name: "memberOf", Values: []string{"CN=IS,DC=siws,DC=local"}},
	}}
	auth := ldapTestAuth(t, entry, 0)
	if ok, err := auth.Authenticate(context.Background(), "budi", "budi-kuat-1"); !ok || err != nil {
		t.Errorf("direct member must pass, got ok=%v err=%v", ok, err)
	}
}

func TestLDAPNestedMembershipAndRejections(t *testing.T) {
	noGroup := &ber.Entry{DN: "CN=andi,DC=siws,DC=local", Attributes: []*ber.EntryAttribute{
		{Name: "memberOf", Values: []string{"CN=HRD,DC=siws,DC=local"}},
	}}
	if ok, _ := ldapTestAuth(t, noGroup, 1).Authenticate(context.Background(), "andi", "budi-kuat-1"); !ok {
		t.Error("nested member must pass via the matching rule")
	}
	if ok, _ := ldapTestAuth(t, noGroup, 0).Authenticate(context.Background(), "andi", "budi-kuat-1"); ok {
		t.Error("non-member must fail")
	}
	entry := &ber.Entry{DN: "CN=budi,DC=siws,DC=local", Attributes: []*ber.EntryAttribute{
		{Name: "memberOf", Values: []string{"CN=IS,DC=siws,DC=local"}},
	}}
	if ok, _ := ldapTestAuth(t, entry, 0).Authenticate(context.Background(), "budi", "salah"); ok {
		t.Error("wrong password must fail")
	}
}
