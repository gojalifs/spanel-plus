package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseApacheVersionOutput(t *testing.T) {
	ver, err := ParseApacheVersionOutput("Server version: Apache/2.4.69 (Win64)\nServer built:   Aug  8 2025 12:00:00\n")
	if err != nil || ver != "2.4.69" {
		t.Errorf("parse = %q, %v, want 2.4.69", ver, err)
	}
	if _, err := ParseApacheVersionOutput("bukan output httpd"); err == nil {
		t.Error("garbage output must be an error")
	}
}

func TestApacheManagerApacheInfo(t *testing.T) {
	rig := newManagerRig(t)
	// The rig's ApacheHome is C:\apache; the probe needs a real
	// httpd.exe file there plus the module it reports on.
	_ = rig.fs.WriteFile(filepath.Join(`C:\apache`, "bin", "httpd.exe"), nil)
	_ = rig.fs.WriteFile(filepath.Join(`C:\apache`, "modules", "mod_fcgid.so"), nil)
	rig.runner.out = "Server version: Apache/2.4.69 (Win64)\nServer built:   Aug  8 2025 12:00:00\n"
	info, err := rig.mgr.ApacheInfo(context.Background())
	if err != nil {
		t.Fatalf("ApacheInfo: %v", err)
	}
	if info.Version != "2.4.69" || info.Home != `C:\apache` {
		t.Errorf("ApacheInfo = %+v, want version 2.4.69 home C:\\apache", info)
	}
	if !info.ModFcgid {
		t.Error("mod_fcgid.so exists in the fixture but ModFcgid is false")
	}
	if info.RotateLogs {
		t.Error("rotatelogs.exe is absent in the fixture but RotateLogs is true")
	}

	// Unparseable httpd -v output is an error, not a guessed version.
	rig.runner.out = "bukan output httpd"
	if _, err := rig.mgr.ApacheInfo(context.Background()); err == nil || !strings.Contains(err.Error(), "could not parse") {
		t.Errorf("garbage httpd -v must error with a parse message, got %v", err)
	}
}
