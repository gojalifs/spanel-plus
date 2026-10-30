package core

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// --- fakes (Plan 10.1: core logic is tested without touching the OS) ---

type fakeFiles struct {
	dirs     map[string]bool
	files    map[string]bool
	writeErr error
}

func (f fakeFiles) Exists(p string) bool { return f.files[p] || f.dirs[p] }
func (f fakeFiles) IsDir(p string) bool  { return f.dirs[p] }
func (f fakeFiles) EnsureWritableDir(string) error {
	return f.writeErr
}

type fakeRunner struct {
	out string
	err error
}

func (r fakeRunner) Run(_ context.Context, _ string, args ...string) (string, error) {
	// httpd -t answers differently from httpd -v; the fake mirrors
	// that so the M5 syntax check passes on a healthy fixture.
	if len(args) > 0 && args[len(args)-1] == "-t" {
		if r.err != nil {
			return r.out, r.err
		}
		return "Syntax OK\n", nil
	}
	return r.out, r.err
}

type fakePorts struct{ err error }

func (p fakePorts) Free(int) error { return p.err }

func healthyDoctor() Doctor {
	home := `C:\m0\Apache24`
	return Doctor{
		Runner: fakeRunner{out: "Server version: Apache/2.4.69 (Win64)\n"},
		Files: fakeFiles{
			dirs: map[string]bool{home: true},
			files: map[string]bool{
				filepath.Join(home, "bin", "httpd.exe"):        true,
				filepath.Join(home, "modules", "mod_fcgid.so"): true,
			},
		},
		Ports: fakePorts{},
	}
}

func TestDoctorAllPassWithDiscoveredHome(t *testing.T) {
	rep := healthyDoctor().Run(context.Background(), DoctorInput{
		Candidates: []string{`C:\xampp\apache`, `C:\m0\Apache24`},
		PanelPort:  8888,
		DataDir:    `C:\panel\data`,
	})
	if !rep.Healthy {
		t.Fatalf("expected healthy report, got %+v", rep.Checks)
	}
	// M5: httpd-syntax joins the M1 five (home, version, mod-fcgid,
	// panel-port, data-dir). Service/registry checks appear only
	// when wired/supplied.
	if len(rep.Checks) != 6 {
		t.Fatalf("expected 6 checks, got %d", len(rep.Checks))
	}
	for _, c := range rep.Checks {
		if c.Name == "httpd-syntax" && c.Status != StatusPass {
			t.Errorf("httpd-syntax status = %s, want pass", c.Status)
		}
	}
	if !strings.Contains(rep.Checks[1].Detail, "2.4.69") {
		t.Errorf("httpd-version detail should carry parsed version, got %q", rep.Checks[1].Detail)
	}
}

func TestDoctorConfiguredHomeMissingFailsLoud(t *testing.T) {
	d := healthyDoctor()
	rep := d.Run(context.Background(), DoctorInput{
		ApacheHome: `C:\nope\apache`,
		PanelPort:  8888,
		DataDir:    `C:\panel\data`,
	})
	if rep.Healthy {
		t.Fatal("missing configured Apache home must be unhealthy")
	}
	// Dependent checks are reported as failed, never silently skipped.
	for _, name := range []string{"httpd-version", "mod-fcgid"} {
		found := false
		for _, c := range rep.Checks {
			if c.Name == name {
				found = true
				if c.Status != StatusFail {
					t.Errorf("%s status = %s, want fail", name, c.Status)
				}
			}
		}
		if !found {
			t.Errorf("check %s missing from report", name)
		}
	}
}

func TestDoctorNoApacheAnywhereFails(t *testing.T) {
	d := Doctor{
		Runner: fakeRunner{},
		Files:  fakeFiles{dirs: map[string]bool{}, files: map[string]bool{}},
		Ports:  fakePorts{},
	}
	rep := d.Run(context.Background(), DoctorInput{
		Candidates: []string{`C:\xampp\apache`},
		PanelPort:  8888,
		DataDir:    `C:\panel\data`,
	})
	if rep.Healthy {
		t.Fatal("no Apache anywhere must be unhealthy")
	}
}

func TestDoctorModFcgidMissingFails(t *testing.T) {
	home := `C:\m0\Apache24`
	d := healthyDoctor()
	d.Files = fakeFiles{
		dirs:  map[string]bool{home: true},
		files: map[string]bool{filepath.Join(home, "bin", "httpd.exe"): true}, // no mod_fcgid.so
	}
	rep := d.Run(context.Background(), DoctorInput{
		ApacheHome: home, PanelPort: 8888, DataDir: `C:\panel\data`,
	})
	if rep.Healthy {
		t.Fatal("missing mod_fcgid must be unhealthy (Plan D4)")
	}
}

func TestDoctorHTTPdVersionUnparsableFails(t *testing.T) {
	d := healthyDoctor()
	d.Runner = fakeRunner{out: "something unexpected"}
	rep := d.Run(context.Background(), DoctorInput{
		ApacheHome: `C:\m0\Apache24`, PanelPort: 8888, DataDir: `C:\panel\data`,
	})
	if rep.Healthy {
		t.Fatal("unparsable httpd -v output must be unhealthy")
	}
}

func TestDoctorPortHeldBySelfIsWarnNotFail(t *testing.T) {
	// Regression: /api/v1/doctor runs while the panel itself holds the
	// port. That must not mark the report unhealthy.
	d := healthyDoctor()
	d.Ports = fakePorts{err: errors.New("address already in use")}
	rep := d.Run(context.Background(), DoctorInput{
		ApacheHome: `C:\m0\Apache24`, PanelPort: 8888, PanelPortSelf: true, DataDir: `C:\panel\data`,
	})
	if !rep.Healthy {
		t.Fatalf("port held by self must stay healthy, got %+v", rep.Checks)
	}
	for _, c := range rep.Checks {
		if c.Name == "panel-port" && c.Status != StatusWarn {
			t.Errorf("panel-port status = %s, want warn", c.Status)
		}
	}
}

func TestDoctorPortBusyAndDataDirUnwritableFail(t *testing.T) {
	d := healthyDoctor()
	d.Ports = fakePorts{err: errors.New("address already in use")}
	rep := d.Run(context.Background(), DoctorInput{
		ApacheHome: `C:\m0\Apache24`, PanelPort: 8888, DataDir: `C:\panel\data`,
	})
	if rep.Healthy {
		t.Fatal("busy panel port must be unhealthy")
	}

	d = healthyDoctor()
	d.Files = fakeFiles{
		dirs: map[string]bool{`C:\m0\Apache24`: true},
		files: map[string]bool{
			filepath.Join(`C:\m0\Apache24`, "bin", "httpd.exe"):        true,
			filepath.Join(`C:\m0\Apache24`, "modules", "mod_fcgid.so"): true,
		},
		writeErr: errors.New("access denied"),
	}
	rep = d.Run(context.Background(), DoctorInput{
		ApacheHome: `C:\m0\Apache24`, PanelPort: 8888, DataDir: `C:\panel\data`,
	})
	if rep.Healthy {
		t.Fatal("unwritable data dir must be unhealthy")
	}
}

func TestDoctorCI4ProjectChecks(t *testing.T) {
	proj := Project{Name: "ci", URLPath: "/ci", FSPath: `C:\htdocs\ci`, Type: ProjectCI4, PublicSubdir: "public"}
	reg := &Registry{Projects: []Project{proj}}

	// Folder + public present, but no writable/ -> warn.
	d := Doctor{Files: fakeFiles{dirs: map[string]bool{`C:\htdocs\ci`: true, `C:\htdocs\ci/public`: true}}}
	checks := d.checkProjects(reg)
	if len(checks) != 1 || checks[0].Status != StatusWarn || !strings.Contains(checks[0].Detail, "writable") {
		t.Errorf("missing writable must warn, got %v", checks)
	}

	// writable present, .env missing -> warn .env.
	d.Files = fakeFiles{dirs: map[string]bool{`C:\htdocs\ci`: true, `C:\htdocs\ci/public`: true, `C:\htdocs\ci/writable`: true}}
	checks = d.checkProjects(reg)
	if len(checks) != 1 || checks[0].Status != StatusWarn || !strings.Contains(checks[0].Detail, ".env") {
		t.Errorf("missing .env must warn, got %v", checks)
	}

	// All present -> pass.
	d.Files = fakeFiles{
		dirs:  map[string]bool{`C:\htdocs\ci`: true, `C:\htdocs\ci/public`: true, `C:\htdocs\ci/writable`: true},
		files: map[string]bool{`C:\htdocs\ci/.env`: true},
	}
	checks = d.checkProjects(reg)
	if len(checks) != 1 || checks[0].Status != StatusPass {
		t.Errorf("complete ci4 project must pass, got %v", checks)
	}
}
