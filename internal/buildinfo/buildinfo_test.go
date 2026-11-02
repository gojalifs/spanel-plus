package buildinfo

import (
	"strings"
	"testing"
)

func TestVersionedServiceDisplayName(t *testing.T) {
	got := VersionedServiceDisplayName()
	if got != ServiceDisplayName+" "+Version {
		t.Errorf("VersionedServiceDisplayName = %q, want %q", got, ServiceDisplayName+" "+Version)
	}
	if !strings.Contains(got, Version) {
		t.Errorf("display name %q must carry the binary version", got)
	}
}
