package core

import (
	"context"
	"testing"
)

// M5-AD dummy directory: domain siws.local, IT group "IS".
func dummyTestUsers(t *testing.T) []DummyUser {
	t.Helper()
	budi, err := HashPassword("budi-kuat-1")
	if err != nil {
		t.Fatal(err)
	}
	andi, err := HashPassword("andi-kuat-1")
	if err != nil {
		t.Fatal(err)
	}
	return []DummyUser{
		{Username: "budi", PasswordHash: budi, Groups: []string{"IS"}},
		{Username: "andi", PasswordHash: andi, Groups: []string{"HRD"}},
	}
}

func TestDummyAuthenticator(t *testing.T) {
	auth := DummyAuthenticator{Users: dummyTestUsers(t), GroupCN: "IS"}
	ctx := context.Background()
	if ok, _ := auth.Authenticate(ctx, "budi", "budi-kuat-1"); !ok {
		t.Error("IS member with the right password must pass")
	}
	if ok, _ := auth.Authenticate(ctx, "budi", "salah"); ok {
		t.Error("wrong password must fail")
	}
	if ok, _ := auth.Authenticate(ctx, "andi", "andi-kuat-1"); ok {
		t.Error("right password but not an IS member must fail")
	}
	if ok, _ := auth.Authenticate(ctx, "tidak-ada", "apa-pun"); ok {
		t.Error("unknown user must fail")
	}
	if ok, _ := auth.Authenticate(ctx, "BUDI", "budi-kuat-1"); !ok {
		t.Error("username comparison must be case-insensitive")
	}
}

func TestADConfigValidate(t *testing.T) {
	if err := (ADConfig{Enabled: true, Server: "dummy"}).Validate(); err == nil {
		t.Error("dummy mode without users must fail validation")
	}
	if err := (ADConfig{Enabled: true, Server: "dummy", DummyUsers: dummyTestUsers(t)}).Validate(); err != nil {
		t.Errorf("dummy mode with users must validate: %v", err)
	}
	if err := (ADConfig{Enabled: true, Server: "ldaps://dc.siws.local:636", Domain: "siws.local"}).Validate(); err == nil {
		t.Error("real config missing base DN and group DN must fail validation")
	}
	if err := (ADConfig{Enabled: false}).Validate(); err != nil {
		t.Errorf("disabled AD must always validate: %v", err)
	}
}

func TestADConfigUPNAndGroupCN(t *testing.T) {
	cfg := ADConfig{Domain: "siws.local", GroupDN: "CN=IS,DC=siws,DC=local"}
	if got := cfg.UserPrincipalName("budi"); got != "budi@siws.local" {
		t.Errorf("UPN = %q, want budi@siws.local", got)
	}
	if got := cfg.UserPrincipalName("budi@siws.local"); got != "budi@siws.local" {
		t.Errorf("full UPN must pass through, got %q", got)
	}
	if got := cfg.GroupCN(); got != "IS" {
		t.Errorf("GroupCN = %q, want IS", got)
	}
}
