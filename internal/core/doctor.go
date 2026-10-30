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
	// Registry, when supplied, adds the M5 checks: every registered
	// PHP build must still be a usable folder, and every project
	// folder (plus Laravel storage/.env) must be present. Nil skips
	// those checks (e.g. registry unreadable) without failing the
	// checks that can still run.
	Registry *Registry
	// ApacheService is the managed Apache's service name for the
	// service-state check; empty skips it.
	ApacheService string
	// AD fields feed the M5-AD directory-config check; ADEnabled
	// false skips it entirely (a panel without AD is not unhealthy).
	ADEnabled bool
	ADServer  string
	ADDomain  string
	ADBaseDN  string
	ADGroupDN string
}

// Doctor validates the environment the panel depends on. M1 scope
// (Plan Bagian 7): Apache path + `httpd -v`, extended with the two
// prerequisites M0 proved load-bearing — mod_fcgid presence — plus
// panel port and data dir sanity. M5 (Plan F7) completes it: httpd -t
// syntax, the managed Apache service state, registered PHP builds,
// and project folders including Laravel storage/.env.
type Doctor struct {
	Runner CommandRunner
	Files  FileProbe
	Ports  PortProbe
	// ApacheStatus, when set, reports a named Windows service's
	// state (platform named controller in production). Nil skips the
	// service check; an "unsupported on this OS" result is a WARN,
	// never a FAIL, so doctor stays meaningful on non-Windows hosts.
	ApacheStatus func(name string) (string, error)
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
		add(d.checkHTTPdSyntax(ctx, home))
	}
	add(d.checkPanelPort(in.PanelPort, in.PanelPortSelf))
	add(d.checkDataDir(in.DataDir))
	if c, ok := d.apacheServiceCheck(in.ApacheService); ok {
		add(c)
	}
	if in.Registry != nil {
		for _, c := range d.checkPHPVersions(in.Registry) {
			add(c)
		}
		for _, c := range d.checkProjects(in.Registry) {
			add(c)
		}
	}
	if in.ADEnabled {
		add(d.adConfigCheck(in))
	}
	return rep
}

// adConfigCheck (M5-AD) validates the directory configuration only
// — it never binds, because doctor has no credentials to bind with.
// Dummy mode is a WARN, not a PASS: it is a development stand-in
// and must never be mistaken for production AD.
func (d Doctor) adConfigCheck(in DoctorInput) Check {
	if in.ADServer == "dummy" {
		return Check{"ad-config", StatusWarn,
			"AD dummy mode aktif — bukan autentikasi AD sungguhan, jangan dipakai produksi"}
	}
	missing := []string{}
	if in.ADServer == "" {
		missing = append(missing, "ad_server")
	}
	if in.ADDomain == "" {
		missing = append(missing, "ad_domain")
	}
	if in.ADBaseDN == "" {
		missing = append(missing, "ad_base_dn")
	}
	if in.ADGroupDN == "" {
		missing = append(missing, "ad_group_dn")
	}
	if len(missing) > 0 {
		return Check{"ad-config", StatusFail,
			"konfigurasi AD belum lengkap, kurang: " + strings.Join(missing, ", ")}
	}
	return Check{"ad-config", StatusPass,
		"AD terkonfigurasi (server " + in.ADServer + ", grup " + in.ADGroupDN + ")"}
}

// ResolveApacheHome determines the managed Apache installation:
// the configured home when set, else the first existing discovery
// candidate. Shared by Doctor and by callers that need the concrete
// home (e.g. wiring the apply pipeline in main).
func ResolveApacheHome(files FileProbe, configured string, candidates []string) (string, Check) {
	if configured != "" {
		if files.IsDir(configured) {
			return configured, Check{"apache-home", StatusPass,
				fmt.Sprintf("%s (from config)", configured)}
		}
		return "", Check{"apache-home", StatusFail,
			fmt.Sprintf("configured apache_home %s does not exist — fix panel.json or install Apache there", configured)}
	}
	for _, cand := range candidates {
		if files.IsDir(cand) {
			return cand, Check{"apache-home", StatusPass,
				fmt.Sprintf("%s (auto-discovered)", cand)}
		}
	}
	return "", Check{"apache-home", StatusFail,
		fmt.Sprintf("no Apache installation found (looked in: %s) — install Apache or set apache_home in panel.json",
			strings.Join(candidates, ", "))}
}

func (d Doctor) resolveApacheHome(in DoctorInput) (string, Check) {
	return ResolveApacheHome(d.Files, in.ApacheHome, in.Candidates)
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

func (d Doctor) checkHTTPdSyntax(ctx context.Context, home string) Check {
	httpd := filepath.Join(home, "bin", "httpd.exe")
	if !d.Files.Exists(httpd) {
		return Check{"httpd-syntax", StatusFail, httpd + " not found — cannot run httpd -t"}
	}
	out, err := d.Runner.Run(ctx, httpd, "-t")
	if err != nil {
		return Check{"httpd-syntax", StatusFail,
			fmt.Sprintf("httpd -t failed: %v (output: %s)", err, strings.TrimSpace(out))}
	}
	if !strings.Contains(out, "Syntax OK") {
		return Check{"httpd-syntax", StatusFail,
			fmt.Sprintf("httpd -t did not report Syntax OK (output: %s)", strings.TrimSpace(out))}
	}
	return Check{"httpd-syntax", StatusPass, "httpd -t: Syntax OK"}
}

// apacheServiceCheck returns false when no checker is wired: a
// missing checker must not turn a healthy panel red (notably on
// non-Windows hosts, where service control does not exist).
func (d Doctor) apacheServiceCheck(name string) (Check, bool) {
	if d.ApacheStatus == nil || name == "" {
		return Check{}, false
	}
	status, err := d.ApacheStatus(name)
	if err != nil {
		return Check{"apache-service", StatusWarn,
			fmt.Sprintf("service %s state could not be checked: %v", name, err)}, true
	}
	if status != "running" {
		return Check{"apache-service", StatusFail,
			fmt.Sprintf("Apache service %s is %s — sites are down; restore with: panel apache restart", name, status)}, true
	}
	return Check{"apache-service", StatusPass, fmt.Sprintf("service %s running", name)}, true
}

func (d Doctor) checkPHPVersions(reg *Registry) []Check {
	if len(reg.PHPVersions) == 0 {
		return []Check{{"php-versions", StatusWarn, "no PHP version registered yet — register one with: panel php add --path <folder>"}}
	}
	checks := make([]Check, 0, len(reg.PHPVersions))
	for _, v := range reg.PHPVersions {
		name := "php-" + v.Version
		if !d.Files.IsDir(v.Path) {
			checks = append(checks, Check{name, StatusFail,
				fmt.Sprintf("php %s folder %s is not a directory — projects on this version cannot run", v.Version, v.Path)})
			continue
		}
		missing := ""
		for _, bin := range []string{"php.exe", "php-cgi.exe"} {
			if !d.Files.Exists(joinPath(v.Path, bin)) {
				missing += bin + " "
			}
		}
		if missing != "" {
			checks = append(checks, Check{name, StatusFail,
				fmt.Sprintf("php %s at %s is missing %s— not a usable PHP build folder", v.Version, v.Path, missing)})
			continue
		}
		checks = append(checks, Check{name, StatusPass, fmt.Sprintf("php %s usable at %s", v.Version, v.Path)})
	}
	return checks
}

func (d Doctor) checkProjects(reg *Registry) []Check {
	if len(reg.Projects) == 0 {
		return []Check{{"projects", StatusWarn, "no project registered yet"}}
	}
	checks := make([]Check, 0, len(reg.Projects))
	for _, p := range reg.Projects {
		name := "project-" + p.Name
		if !d.Files.IsDir(p.FSPath) {
			checks = append(checks, Check{name, StatusFail,
				fmt.Sprintf("project %s folder %s is not a directory", p.Name, p.FSPath)})
			continue
		}
		if p.Type == ProjectLaravel || p.Type == ProjectCI4 {
			if !d.Files.IsDir(p.TargetDir()) {
				checks = append(checks, Check{name, StatusFail,
					fmt.Sprintf("%s project %s public dir %s does not exist", p.Type, p.Name, p.TargetDir())})
				continue
			}
			if p.Type == ProjectLaravel {
				if !d.Files.IsDir(joinPath(p.FSPath, "storage")) {
					checks = append(checks, Check{name, StatusWarn,
						fmt.Sprintf("laravel project %s has no storage/ folder — Apache's service account may fail to write cache/logs", p.Name)})
					continue
				}
				if !d.Files.Exists(joinPath(p.FSPath, ".env")) {
					checks = append(checks, Check{name, StatusWarn,
						fmt.Sprintf("laravel project %s has no .env file — the app may boot with wrong settings", p.Name)})
					continue
				}
			} else {
				// CodeIgniter 4 keeps its runtime files under
				// writable/ (logs, cache, sessions, uploads) and
				// its settings in .env, mirroring Laravel's
				// storage/ + .env checks above.
				if !d.Files.IsDir(joinPath(p.FSPath, "writable")) {
					checks = append(checks, Check{name, StatusWarn,
						fmt.Sprintf("ci4 project %s has no writable/ folder — CodeIgniter needs it for logs, cache and sessions", p.Name)})
					continue
				}
				if !d.Files.Exists(joinPath(p.FSPath, ".env")) {
					checks = append(checks, Check{name, StatusWarn,
						fmt.Sprintf("ci4 project %s has no .env file — the app may boot with wrong settings", p.Name)})
					continue
				}
			}
		}
		checks = append(checks, Check{name, StatusPass, fmt.Sprintf("project %s folder present at %s", p.Name, p.FSPath)})
	}
	return checks
}

func (d Doctor) checkDataDir(dir string) Check {
	if err := d.Files.EnsureWritableDir(dir); err != nil {
		return Check{"data-dir", StatusFail,
			fmt.Sprintf("data dir %s is not writable: %v", dir, err)}
	}
	return Check{"data-dir", StatusPass, dir + " writable"}
}
