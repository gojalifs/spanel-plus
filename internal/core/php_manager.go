package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// PHPRegistration reports what registering one PHP folder produced.
type PHPRegistration struct {
	Version  PHPVersion `json:"version"`
	Full     string     `json:"full_version"`
	NTS      bool       `json:"nts"`
	Arch     string     `json:"arch"`
	Modules  []string   `json:"modules"`
	PHPIni   string     `json:"php_ini"` // "generated" | "existing"
	IniPath  string     `json:"ini_path"`
	Warnings []string   `json:"warnings,omitempty"`
}

// PHPManager is the F2 PHP version manager: it registers PHP builds
// by probing the real binaries (never trusting a typed version
// number), refuses thread-safe builds (mod_fcgid needs NTS), and
// seeds a production php.ini when the folder has none.
type PHPManager struct {
	Registry *RegistryService
	Prober   PHPProber
	Files    FileStore
	// RuntimeBase is the panel-owned folder for per-version runtime
	// dirs (session & temp paths written into generated php.ini).
	RuntimeBase string
	// ScanRoot is the folder whose immediate subdirectories are
	// auto-registered by Scan (M0 layout: <root>/php). Empty means
	// no auto-register; main derives it from the Apache home.
	ScanRoot string
}

// Register probes the folder at path, validates the build, registers
// the DETECTED version, and ensures a php.ini exists.
func (m *PHPManager) Register(ctx context.Context, path string) (PHPRegistration, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return PHPRegistration{}, fmt.Errorf("php folder path is empty")
	}
	probe, err := m.Prober.Probe(ctx, path)
	if err != nil {
		return PHPRegistration{}, err
	}
	if !probe.NTS {
		return PHPRegistration{}, fmt.Errorf(
			"php %s di %s adalah build thread-safe (TS) — mod_fcgid membutuhkan build NTS; unduh varian 'Non Thread Safe' dari windows.php.net",
			probe.Full, path)
	}
	v := PHPVersion{Version: probe.Version, Path: path}
	if err := m.Registry.AddPHPVersion(v); err != nil {
		return PHPRegistration{}, err
	}
	iniState, iniPath, err := m.ensurePHPIni(v)
	if err != nil {
		return PHPRegistration{}, err
	}
	reg := PHPRegistration{
		Version: v,
		Full:    probe.Full,
		NTS:     probe.NTS,
		Arch:    probe.Arch,
		Modules: probe.Modules,
		PHPIni:  iniState,
		IniPath: iniPath,
	}
	if probe.Arch != "" && probe.Arch != "x64" {
		reg.Warnings = append(reg.Warnings,
			"arsitektur PHP terdeteksi "+probe.Arch+" — pastikan cocok dengan arsitektur Apache (umumnya x64).")
	}
	return reg, nil
}

// AutoRegisterResult reports one PHP folder scan.
type AutoRegisterResult struct {
	Root string `json:"root"`
	// Registered versions newly registered by this scan.
	Registered []string `json:"registered,omitempty"`
	// Skipped folders that were looked at but left alone, with reasons.
	Skipped []AutoRegisterSkip `json:"skipped,omitempty"`
}

// AutoRegisterSkip is one folder the scan left alone, with the reason.
type AutoRegisterSkip struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// samePath compares Windows folder paths ignoring separator style,
// trailing separators, and case (Windows paths are case-insensitive).
// Registry paths were typed by hand at register time, scan paths are
// built with joinPath — without this, the same folder compares unequal.
func samePath(a, b string) bool {
	norm := func(p string) string {
		p = strings.ReplaceAll(p, `\`, "/")
		p = strings.TrimRight(p, "/")
		return strings.ToLower(p)
	}
	return norm(a) == norm(b)
}

// Scan registers every PHP build found in ScanRoot's immediate
// subdirectories that is not already registered (compared by folder
// path, not version string). It is idempotent: already-registered
// folders are silently left alone, and it never unregisters anything.
// Per-folder failures (missing php.exe, thread-safe build, a version
// already registered from another folder) become Skipped entries, not
// errors — a bad folder must never break panel startup or a manual
// rescan. The only hard error is an unreadable registry or scan
// folder listing. An empty ScanRoot scans nothing and returns an
// empty result. Like Register, a newly registered build gets a
// generated php.ini when its folder has none.
func (m *PHPManager) Scan(ctx context.Context) (AutoRegisterResult, error) {
	res := AutoRegisterResult{Root: m.ScanRoot}
	if strings.TrimSpace(m.ScanRoot) == "" {
		return res, nil
	}
	reg, err := m.Registry.Load()
	if err != nil {
		return res, err
	}
	names, err := m.Files.ListNames(m.ScanRoot)
	if err != nil {
		return res, err
	}
	for _, name := range names {
		dir := joinPath(m.ScanRoot, name)
		if !m.Files.IsDir(dir) {
			continue // plain files in the php folder are not builds
		}
		registered := false
		for _, v := range reg.PHPVersions {
			if samePath(v.Path, dir) {
				registered = true
				break
			}
		}
		if registered {
			continue
		}
		r, err := m.Register(ctx, dir)
		if err != nil {
			reason := err.Error()
			if errors.Is(err, ErrPHPVersionExists) {
				reason = "versi sudah terdaftar dari folder lain"
			}
			res.Skipped = append(res.Skipped, AutoRegisterSkip{Path: dir, Reason: reason})
			continue
		}
		res.Registered = append(res.Registered, r.Version.Version)
	}
	return res, nil
}

// Remove unregisters a PHP version (refused while in use — enforced
// by RegistryService).
func (m *PHPManager) Remove(version string) error {
	return m.Registry.RemovePHPVersion(version)
}

// Modules probes a registered version and returns its module list.
func (m *PHPManager) Modules(ctx context.Context, version string) ([]string, error) {
	reg, err := m.Registry.Load()
	if err != nil {
		return nil, err
	}
	v, ok := reg.FindPHP(version)
	if !ok {
		return nil, fmt.Errorf("php version %q is not registered: %w", version, ErrPHPVersionNotRegistered)
	}
	probe, err := m.Prober.Probe(ctx, v.Path)
	if err != nil {
		return nil, err
	}
	return probe.Modules, nil
}

// ensurePHPIni writes the panel's production php.ini template into
// the PHP folder when none exists; an existing php.ini is sacred and
// left untouched.
func (m *PHPManager) ensurePHPIni(v PHPVersion) (state string, iniPath string, err error) {
	iniPath = joinPath(v.Path, "php.ini")
	if m.Files.Exists(iniPath) {
		return "existing", iniPath, nil
	}
	sessionDir := joinPath(joinPath(m.RuntimeBase, v.Version), "sessions")
	tmpDir := joinPath(joinPath(m.RuntimeBase, v.Version), "tmp")
	for _, d := range []string{sessionDir, tmpDir} {
		if err := m.Files.MkdirAll(d); err != nil {
			return "", "", fmt.Errorf("prepare php runtime dir %s: %w", d, err)
		}
	}
	content := RenderPHPIni(v, slashPath(sessionDir), slashPath(tmpDir))
	if err := m.Files.WriteFileAtomic(iniPath, []byte(content)); err != nil {
		return "", "", fmt.Errorf("write php.ini for php %s: %w", v.Version, err)
	}
	return "generated", iniPath, nil
}

// RenderPHPIni renders the panel's production php.ini for one version
// (Plan F2). Extensions are intentionally NOT force-enabled: a
// missing DLL would break every project on that version, so the
// template lists the common ones as a checklist to enable manually.
func RenderPHPIni(v PHPVersion, sessionDir, tmpDir string) string {
	var b strings.Builder
	b.WriteString("; php.ini dibuat SPanel+ untuk PHP " + v.Version + " (" + v.Path + ")\n")
	b.WriteString("; Template production: error tidak ditampilkan ke pengunjung,\n")
	b.WriteString("; OPcache aktif, path session & temp dipisah per versi.\n\n")
	b.WriteString("display_errors = Off\n")
	b.WriteString("log_errors = On\n")
	b.WriteString("memory_limit = 512M\n")
	b.WriteString("max_execution_time = 300\n")
	b.WriteString("post_max_size = 64M\n")
	b.WriteString("upload_max_filesize = 64M\n")
	b.WriteString("date.timezone = Asia/Jakarta\n\n")
	b.WriteString("session.save_path = \"" + sessionDir + "\"\n")
	b.WriteString("sys_temp_dir = \"" + tmpDir + "\"\n")
	b.WriteString("upload_tmp_dir = \"" + tmpDir + "\"\n\n")
	b.WriteString("[opcache]\n")
	b.WriteString("zend_extension=opcache\n")
	b.WriteString("opcache.enable = 1\n")
	b.WriteString("opcache.enable_cli = 0\n")
	b.WriteString("opcache.memory_consumption = 256\n")
	b.WriteString("opcache.max_accelerated_files = 20000\n")
	b.WriteString("opcache.validate_timestamps = 1\n")
	b.WriteString("opcache.revalidate_freq = 2\n\n")
	b.WriteString("; Extension — aktifkan yang DLL-nya ada di folder ext\\ versi ini,\n")
	b.WriteString("; contoh: extension=curl, extension=mbstring, extension=mysqli,\n")
	b.WriteString("; extension=openssl, extension=pdo_mysql, extension=gd,\n")
	b.WriteString("; extension=sqlsrv / pdo_sqlsrv (unduh terpisah, wajib NTS x64).\n")
	return b.String()
}
