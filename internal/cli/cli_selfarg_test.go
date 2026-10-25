package cli

import (
	"testing"
)

func TestStripSelfArgs(t *testing.T) {
	exe := "/data/data/com.termux/files/home/spanel-test/panel-v0.6.1-m5a-android-arm64"
	dup := []string{"/data/data/com.termux/files/home/spanel-test/panel-v0.6.1-m5a-android-arm64", "admin", "set-password"}
	got := StripSelfArgs(dup, exe)
	if len(got) != 2 || got[0] != "admin" {
		t.Fatalf("duplicated exe path must be stripped, got %v", got)
	}
	rel := []string{"./panel", "version"}
	if got := StripSelfArgs(rel, "./panel"); len(got) != 1 || got[0] != "version" {
		t.Fatalf("relative exe path must be stripped, got %v", got)
	}
	normal := []string{"admin", "set-password"}
	if got := StripSelfArgs(normal, exe); len(got) != 2 {
		t.Fatalf("normal args must pass through, got %v", got)
	}
}
