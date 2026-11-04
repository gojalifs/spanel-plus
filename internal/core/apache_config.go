package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Fase 2c — Apache settings from the dashboard.
//
// Three entry points share one safety gate (see SaveConfigFile):
//   - module checklist: toggle a LoadModule line in httpd.conf
//   - structured fcgid settings: stored in the registry, regenerated
//     by the normal apply pipeline, so they survive the next apply
//   - text editor: edit httpd.conf or a generated file directly
//
// The editor allowlist is derived from the panel's own configuration
// (ApacheHome + the panel's GeneratedDir), never from user input:
// a request for any other path is rejected before any file is read.
// That is what keeps the issue #2 split-brain from coming back
// through the editor — it can only open the live set, not the
// orphan folder.

// ApacheModule is one row of the module checklist.
type ApacheModule struct {
	Name         string `json:"name"` // e.g. "fcgid_module"
	File         string `json:"file"` // e.g. "modules/mod_fcgid.so"
	Enabled      bool   `json:"enabled"`
	Locked       bool   `json:"locked"` // true = the panel needs it; cannot be turned off
	LockedReason string `json:"locked_reason,omitempty"`
}

// lockedApacheModules are always required by the panel's generated
// config: fcgid serves every PHP project, dir provides DirectoryIndex,
// alias provides the Laravel/CI4 Alias mapping.
var lockedApacheModules = map[string]string{
	"fcgid_module": "dipakai panel untuk semua proyek PHP (mod_fcgid)",
	"dir_module":   "dipakai panel untuk DirectoryIndex",
	"alias_module": "dipakai panel untuk Alias proyek",
}

var loadModuleRe = regexp.MustCompile(`^(\s*)(#?)\s*LoadModule\s+(\S+)\s+(\S+)(.*)$`)

// ParseApacheModules merges LoadModule lines with .so files found in
// the modules folder. A .so with no LoadModule line appears as
// disabled, so it can be enabled from the checklist; a LoadModule
// line whose .so is missing still appears, because disabling it is
// still a meaningful action. Result is sorted by module name.
func ParseApacheModules(httpdConf string, moduleFiles []string, tlsApache bool) []ApacheModule {
	byName := map[string]*ApacheModule{}
	order := func(m *ApacheModule) {
		if _, ok := byName[m.Name]; !ok {
			byName[m.Name] = m
		}
	}
	for _, line := range strings.Split(httpdConf, "\n") {
		m := loadModuleRe.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		enabled := m[2] == ""
		mod := &ApacheModule{Name: m[3], File: m[4], Enabled: enabled}
		if prev, ok := byName[mod.Name]; ok {
			// Duplicate LoadModule lines: the first one wins for
			// display, but ToggleApacheModule rewrites every line.
			_ = prev
			continue
		}
		order(mod)
	}
	for _, f := range moduleFiles {
		if !strings.HasSuffix(strings.ToLower(f), ".so") {
			continue
		}
		base := strings.TrimSuffix(f, ".so")
		name := strings.TrimPrefix(base, "mod_") + "_module"
		if _, ok := byName[name]; ok {
			continue
		}
		byName[name] = &ApacheModule{Name: name, File: "modules/" + f, Enabled: false}
	}
	for _, mod := range byName {
		if reason, ok := lockedApacheModules[mod.Name]; ok {
			mod.Locked = true
			mod.LockedReason = reason
		}
		if tlsApache && mod.Name == "ssl_module" {
			mod.Locked = true
			mod.LockedReason = "dipakai panel untuk vhost HTTPS"
		}
	}
	out := make([]ApacheModule, 0, len(byName))
	for _, mod := range byName {
		out = append(out, *mod)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ToggleApacheModule rewrites every LoadModule line for name in
// httpdConf, commenting it out to disable or uncommenting to enable.
// It refuses to turn a locked module off while it is on, and refuses
// an unknown module outright — both are caller errors, not file edits.
func ToggleApacheModule(httpdConf, name string, enabled bool, tlsApache bool) (string, error) {
	reason, locked := lockedApacheModules[name]
	if tlsApache && name == "ssl_module" {
		locked = true
		reason = "dipakai panel untuk vhost HTTPS"
	}
	found := false
	lines := strings.Split(httpdConf, "\n")
	for i, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		m := loadModuleRe.FindStringSubmatch(line)
		if m == nil || m[3] != name {
			continue
		}
		found = true
		currentlyEnabled := m[2] == ""
		if locked && currentlyEnabled && !enabled {
			return "", fmt.Errorf("modul %q terkunci dan tidak bisa dimatikan: %s", name, reason)
		}
		if currentlyEnabled == enabled {
			continue
		}
		if enabled {
			lines[i] = m[1] + "LoadModule " + m[3] + " " + m[4] + m[5]
		} else {
			lines[i] = m[1] + "# LoadModule " + m[3] + " " + m[4] + m[5]
		}
	}
	if !found {
		// Enabling a module that has no LoadModule line yet means
		// appending one; disabling a module that is not listed is
		// already the desired state, so only the append case edits.
		if !enabled {
			return httpdConf, nil
		}
		return "", fmt.Errorf("modul %q tidak ditemukan di httpd.conf", name)
	}
	return strings.Join(lines, "\n"), nil
}

// ApacheConfigFile is one row of the editor file list.
type ApacheConfigFile struct {
	Name      string `json:"name"`      // "httpd.conf" or a generated "*.conf"
	Path      string `json:"path"`      // server-side path, shown for orientation only
	Generated bool   `json:"generated"` // true = written by the panel generator
	Durable   bool   `json:"durable"`   // true = survives the next panel apply
}

// HttpdConfPath is the main Apache config file for this panel.
func (m *ApacheManager) HttpdConfPath() string {
	return filepath.Join(m.Cfg.ApacheHome, "conf", "httpd.conf")
}

// ListConfigFiles returns httpd.conf first, then the generated files
// in the panel's own GeneratedDir, sorted. It never lists the orphan
// folder from the issue #2 split: GeneratedDir is the single live
// set, derived from the panel's DataDir.
func (m *ApacheManager) ListConfigFiles() ([]ApacheConfigFile, error) {
	out := []ApacheConfigFile{{
		Name: "httpd.conf", Path: m.HttpdConfPath(), Generated: false, Durable: true,
	}}
	names, err := m.Files.ListNames(m.Cfg.GeneratedDir)
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	for _, name := range names {
		if !strings.HasSuffix(name, ".conf") {
			continue
		}
		out = append(out, ApacheConfigFile{
			Name: name, Path: filepath.Join(m.Cfg.GeneratedDir, name),
			Generated: true, Durable: false,
		})
	}
	return out, nil
}

// resolveConfigPath maps an editor file name to its disk path, or
// rejects it. Names are bare file names only: any separator, parent
// reference, or name outside the allowlist is an error, so the editor
// can never be used to open an arbitrary file on the server.
func (m *ApacheManager) resolveConfigPath(name string) (string, error) {
	if name == "httpd.conf" {
		return m.HttpdConfPath(), nil
	}
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") || !strings.HasSuffix(name, ".conf") {
		return "", fmt.Errorf("file konfigurasi %q tidak diizinkan", name)
	}
	names, err := m.Files.ListNames(m.Cfg.GeneratedDir)
	if err != nil {
		return "", err
	}
	for _, n := range names {
		if n == name {
			return filepath.Join(m.Cfg.GeneratedDir, name), nil
		}
	}
	return "", fmt.Errorf("file konfigurasi %q tidak ditemukan", name)
}

// ReadConfigFile returns one allowlisted config file's contents.
func (m *ApacheManager) ReadConfigFile(name string) (string, error) {
	path, err := m.resolveConfigPath(name)
	if err != nil {
		return "", err
	}
	raw, err := m.Files.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// SaveConfigFile is the single safety gate for editor and module
// saves: backup the old file, write the new one atomically, run
// httpd -t, and only restart apache-spanel when the test passes.
// A failed test restores the old file and never restarts; a failed
// restart restores the old file and tries to restart onto it again,
// so a bad edit can never leave Apache down on a broken config.
func (m *ApacheManager) SaveConfigFile(ctx context.Context, name, content string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveConfigFileLocked(ctx, name, content)
}

func (m *ApacheManager) saveConfigFileLocked(ctx context.Context, name, content string) (string, error) {
	path, err := m.resolveConfigPath(name)
	if err != nil {
		return "", err
	}
	old, err := m.Files.ReadFile(path)
	if err != nil {
		return "", err
	}
	backup := filepath.Join(m.Cfg.BackupsDir, "config-editor", m.Clock.Now().Format("20060102-150405")+"-"+name)
	if err := m.Files.WriteFileAtomic(backup, old); err != nil {
		return "", fmt.Errorf("backup %s gagal: %w", name, err)
	}
	if err := m.Files.WriteFileAtomic(path, []byte(content)); err != nil {
		return "", err
	}
	if err := m.TestConfig(ctx); err != nil {
		_ = m.Files.WriteFileAtomic(path, old)
		m.logError("editor save failed httpd -t, restored", "file", name, "err", err)
		return "", fmt.Errorf("config tidak disimpan: %w (file lama sudah dikembalikan, Apache tidak di-restart)", err)
	}
	if err := m.Service.Restart(m.Cfg.ApacheService); err != nil {
		_ = m.Files.WriteFileAtomic(path, old)
		_ = m.Service.Restart(m.Cfg.ApacheService)
		m.logError("editor save restart failed, restored", "file", name, "err", err)
		return "", fmt.Errorf("config dikembalikan karena restart Apache gagal: %v (backup di %s)", err, backup)
	}
	m.logInfo("editor config saved and apache restarted", "file", name, "backup", backup)
	return backup, nil
}

// ListApacheModules reads httpd.conf and the modules folder and
// returns the checklist rows.
func (m *ApacheManager) ListApacheModules() ([]ApacheModule, error) {
	raw, err := m.Files.ReadFile(m.HttpdConfPath())
	if err != nil {
		return nil, err
	}
	files, err := m.Files.ListNames(filepath.Join(m.Cfg.ApacheHome, "modules"))
	if err != nil {
		files = nil
	}
	return ParseApacheModules(string(raw), files, m.Cfg.TLSApache), nil
}

// SetApacheModule toggles one module in httpd.conf through the same
// backup/test/restart gate as the editor. A module that only exists
// as a .so (no LoadModule line yet) is enabled by appending its line;
// disabling such a module is already the desired state.
func (m *ApacheManager) SetApacheModule(ctx context.Context, name string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	raw, err := m.Files.ReadFile(m.HttpdConfPath())
	if err != nil {
		return err
	}
	files, err := m.Files.ListNames(filepath.Join(m.Cfg.ApacheHome, "modules"))
	if err != nil {
		files = nil
	}
	modules := ParseApacheModules(string(raw), files, m.Cfg.TLSApache)
	var found *ApacheModule
	for i := range modules {
		if modules[i].Name == name {
			found = &modules[i]
			break
		}
	}
	if found == nil {
		return fmt.Errorf("modul %q tidak ditemukan di httpd.conf maupun folder modules", name)
	}
	if found.Enabled == enabled {
		return nil
	}
	listed := strings.Contains(string(raw), "LoadModule "+name+" ") || strings.Contains(string(raw), "LoadModule "+name+"\t")
	var updated string
	if !listed {
		if !enabled {
			return nil
		}
		updated = strings.TrimRight(string(raw), "\n") + "\nLoadModule " + name + " " + found.File + "\n"
	} else {
		updated, err = ToggleApacheModule(string(raw), name, enabled, m.Cfg.TLSApache)
		if err != nil {
			return err
		}
	}
	_, err = m.saveConfigFileLocked(ctx, "httpd.conf", updated)
	return err
}

// loadRegistry reads the registry the apply pipeline commits to.
func (m *ApacheManager) loadRegistry() (Registry, error) {
	raw, err := m.Files.ReadFile(m.Cfg.RegistryPath)
	if err != nil {
		return Registry{}, err
	}
	var reg Registry
	if err := json.Unmarshal(raw, &reg); err != nil {
		return Registry{}, err
	}
	return reg, nil
}

// GetFcgidSettings returns the effective fcgid settings (defaults
// filled in) so the dashboard form always shows real values.
func (m *ApacheManager) GetFcgidSettings() (FcgidSettings, error) {
	reg, err := m.loadRegistry()
	if err != nil {
		return FcgidSettings{}, err
	}
	return reg.Settings.Fcgid.Effective(), nil
}

// SetFcgidSettings stores new fcgid settings in the registry and runs
// the normal apply pipeline, so 00-panel-global.conf is regenerated
// with the new values, tested, restarted, and committed — and the
// next apply keeps them instead of resetting to the defaults.
func (m *ApacheManager) SetFcgidSettings(ctx context.Context, settings FcgidSettings) (ApplyResult, error) {
	if err := settings.Validate(); err != nil {
		return ApplyResult{}, err
	}
	reg, err := m.loadRegistry()
	if err != nil {
		return ApplyResult{}, err
	}
	candidate := reg.Clone()
	candidate.Settings.Fcgid = settings
	return m.Apply(ctx, candidate)
}
