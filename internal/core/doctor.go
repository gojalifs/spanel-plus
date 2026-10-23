package core

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// CheckStatus is the outcome of one doctor check.
type CheckStatus string

const (
	StatusPass CheckStatus = "pass"
	StatusWarn CheckStatus = "warn"
	StatusFail CheckStatus = "fail"
)

// Check is one doctor result line. Detail must say what was found and,
// on failure, what to do about it — a check that fails silently or
// vaguely is a bug (fail-loud rule).
type Check struct {
	Name   string      `json:"name"`
	Status CheckStatus `json:"status"`
	Detail string      `json:"detail"`
}

// Report is the full doctor outcome.
type Report struct {
	Checks  []Check `json:"checks"`
	Healthy bool    `json:"healthy"`
}

// DoctorInput is everything Doctor needs, pre-resolved by the caller.
type DoctorInput struct {
	// ApacheHome from config; empty triggers discovery over Candidates.
	ApacheHome string
	// Candidates are probed in order when ApacheHome is empty.
	Candidates []string
	// PanelPort is checked for availability.
	PanelPort int
	// PanelPortSelf marks that the caller itself is the listener on
	// PanelPort (the /api/v1/doctor endpoint): occupation by self is
	// expected, reported as WARN instead of FAIL.
	PanelPortSelf bool
	// DataDir must be creatable and writable.
	DataDir string
}

// Doctor validates the environment the panel depends on. M1 scope
// (Plan Bagian 7): Apache path + `httpd -v`, extended with the two
// prerequisites M0 proved load-bearing — mod_fcgid presence — plus
// panel port and data dir sanity. Later milestones grow this list.
type Doctor struct {
	Runner CommandRunner
	Files  FileProbe
	Ports  PortProbe
}

// Run executes all checks in a stable order and aggregates the report.
// It never short-circuits: the operator sees every problem at once.
func (d Doctor) Run(ctx context.Context, in DoctorInput) Report {
	rep := Report{Healthy: true}
	add := func(c Check) {
		rep.Checks = append(rep.Checks, c)
		if c.Status == StatusFail {
			rep.Healthy = false
		}
	}

	home, homeCheck := d.resolveApacheHome(in)
	add(homeCheck)
	if home == "" {
		// Without an Apache home the dependent checks cannot run;
		// record them as failed instead of skipping silently.
		add(Check{"httpd-version", StatusFail, "skipped: no Apache home resolved"})
		add(Check{"mod-fcgid", StatusFail, "skipped: no Apache home resolved"})
	} else {
		add(d.checkHTTPdVersion(ctx, home))
		add(d.checkModFcgid(home))
	}
	add(d.checkPanelPort(in.PanelPort, in.PanelPortSelf))
	add(d.checkDataDir(in.DataDir))
	return rep
}

func (d Doctor) resolveApacheHome(in DoctorInput) (string, Check) {
	if in.ApacheHome != "" {
		if d.Files.IsDir(in.ApacheHome) {
			return in.ApacheHome, Check{"apache-home", StatusPass,
				fmt.Sprintf("%s (from config)", in.ApacheHome)}
		}
		return "", Check{"apache-home", StatusFail,
			fmt.Sprintf("configured apache_home %s does not exist — fix panel.json or install Apache there", in.ApacheHome)}
	}
	for _, cand := range in.Candidates {
		if d.Files.IsDir(cand) {
			return cand, Check{"apache-home", StatusPass,
				fmt.Sprintf("%s (auto-discovered)", cand)}
		}
	}
	return "", Check{"apache-home", StatusFail,
		fmt.Sprintf("no Apache installation found (looked in: %s) — install Apache or set apache_home in panel.json",
			strings.Join(in.Candidates, ", "))}
}

var apacheVersionRe = regexp.MustCompile(`Apache/(\d+\.\d+\.\d+)`)

func (d Doctor) checkHTTPdVersion(ctx context.Context, home string) Check {
	httpd := filepath.Join(home, "bin", "httpd.exe")
	if !d.Files.Exists(httpd) {
		return Check{"httpd-version", StatusFail,
			fmt.Sprintf("%s not found — %s does not look like an Apache installation", httpd, home)}
	}
	out, err := d.Runner.Run(ctx, httpd, "-v")
	if err != nil {
		return Check{"httpd-version", StatusFail,
			fmt.Sprintf("running httpd -v failed: %v (output: %s)", err, strings.TrimSpace(out))}
	}
	m := apacheVersionRe.FindStringSubmatch(out)
	if m == nil {
		return Check{"httpd-version", StatusFail,
			fmt.Sprintf("could not parse Apache version from httpd -v output: %q", strings.TrimSpace(out))}
	}
	return Check{"httpd-version", StatusPass, "Apache " + m[1]}
}

func (d Doctor) checkModFcgid(home string) Check {
	mod := filepath.Join(home, "modules", "mod_fcgid.so")
	if !d.Files.Exists(mod) {
		return Check{"mod-fcgid", StatusFail,
			fmt.Sprintf("%s not found — multi-PHP via mod_fcgid (Plan D4) cannot work; install the mod_fcgid build matching this Apache (see M0 findings)", mod)}
	}
	return Check{"mod-fcgid", StatusPass, mod}
}

func (d Doctor) checkPanelPort(port int, self bool) Check {
	if err := d.Ports.Free(port); err != nil {
		if self {
			return Check{"panel-port", StatusWarn,
				fmt.Sprintf("port %d is held by this panel itself (this report is served from it)", port)}
		}
		return Check{"panel-port", StatusFail,
			fmt.Sprintf("port %d is not available: %v — another program (or a running panel) is using it", port, err)}
	}
	return Check{"panel-port", StatusPass, fmt.Sprintf("port %d available", port)}
}

func (d Doctor) checkDataDir(dir string) Check {
	if err := d.Files.EnsureWritableDir(dir); err != nil {
		return Check{"data-dir", StatusFail,
			fmt.Sprintf("data dir %s is not writable: %v", dir, err)}
	}
	return Check{"data-dir", StatusPass, dir + " writable"}
}
