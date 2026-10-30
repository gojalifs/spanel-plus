package core

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// PHP extension management (checklist in the panel, no hand-editing
// php.ini).
//
// Design notes — read before touching this file:
//
//   - Extensions belong to a PHP VERSION, not to a project: toggling
//     an extension affects every project served by that version.
//   - An existing php.ini is only ever rewritten by an explicit
//     checklist save, and only after a backup copy is written first.
//     Registration (ensurePHPIni) still never touches an existing
//     php.ini; SetExtensions is the deliberate, user-initiated
//     exception to that rule.
//   - Enabling an extension whose DLL is missing is refused outright:
//     a half-enabled version would break every project on it (the
//     same reason the generated php.ini template lists extensions
//     as comments instead of enabling them).
//   - After a write, the version is re-probed. If the toggled
//     extension did not actually change load state, the backup is
//     restored and an error is returned — never a silent half-state.

// Extension statuses reported by the inventory.
const (
	// ExtensionActive: loaded by php -m.
	ExtensionActive = "active"
	// ExtensionInactive: not loaded, but can be enabled (DLL present
	// and/or an ini line exists to uncomment).
	ExtensionInactive = "inactive"
	// ExtensionBuiltin: loaded by php -m but has no DLL and no ini
	// line — compiled into PHP, always on, not toggleable.
	ExtensionBuiltin = "builtin"
	// ExtensionBroken: php.ini enables it but php -m does not load
	// it (DLL missing or failed to load).
	ExtensionBroken = "broken"
	// ExtensionNotInstalled: a well-known extension (currently only
	// the SQL Server drivers) with no DLL, no ini line, and not
	// loaded — shown so the user knows what to install.
	ExtensionNotInstalled = "not_installed"
)

// PHPExtension is one row of the checklist for one PHP version.
type PHPExtension struct {
	Name       string `json:"name"`
	Zend       bool   `json:"zend"`
	Status     string `json:"status"`
	Loaded     bool   `json:"loaded"`
	HasDLL     bool   `json:"has_dll"`
	IniState   string `json:"ini_state"` // "enabled" | "disabled" | "absent"
	Toggleable bool   `json:"toggleable"`
	Hint       string `json:"hint,omitempty"`
}

// PHPExtensionInventory is the full checklist for one PHP version.
type PHPExtensionInventory struct {
	Version    string         `json:"version"`
	IniPath    string         `json:"ini_path"`
	Extensions []PHPExtension `json:"extensions"`
}

// SetExtensionsResult reports one checklist save.
type SetExtensionsResult struct {
	Inventory   PHPExtensionInventory `json:"inventory"`
	Changed     bool                  `json:"changed"`
	BackupPath  string                `json:"backup_path,omitempty"`
	AutoEnabled []string              `json:"auto_enabled,omitempty"`
}

// knownZendExtensions are Zend extensions that cannot be told apart
// from regular extensions by DLL name alone; they need a
// zend_extension= line, not extension=.
var knownZendExtensions = map[string]bool{"opcache": true, "xdebug": true}

// sqlServerExtensions are the well-known drivers that never ship
// with PHP itself; they are listed as not_installed when absent so
// the checklist can point at what to install.
var sqlServerExtensions = []string{"sqlsrv", "pdo_sqlsrv"}

func sqlServerHint(name string) string {
	return "Tidak terinstal: unduh Microsoft Drivers for PHP for SQL Server (NTS x64) dan taruh php_" + name + ".dll di folder ext/, plus instal Microsoft ODBC Driver for SQL Server."
}

// normalizeExtensionName reduces an ini value, DLL file name, or
// php -m entry to one comparable key: lowercase, without the php_
// prefix and .dll suffix.
func normalizeExtensionName(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.Trim(s, `"'`)
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	s = strings.TrimPrefix(s, "php_")
	s = strings.TrimSuffix(s, ".dll")
	return s
}

// normalizeZendModuleName reduces a [Zend Modules] entry ("Zend
// OPcache", "Xdebug") to the extension key ("opcache", "xdebug").
func normalizeZendModuleName(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	return strings.TrimPrefix(s, "zend ")
}

// dllFileName is the Windows DLL file for an extension key.
func dllFileName(name string) string {
	return "php_" + name + ".dll"
}

// --- php.ini parsing ---

var iniExtensionRe = regexp.MustCompile(`^(extension|zend_extension)\s*=\s*"?([^"\s;]+)"?`)
var iniExtensionDirRe = regexp.MustCompile(`^extension_dir\s*=\s*"?([^"\s;]+)"?`)

type iniExtensionEntry struct {
	name    string
	zend    bool
	enabled bool
	index   int // line index in strings.Split(content, "\n")
}

// parseIniExtensionEntries finds every extension/zend_extension line,
// active or commented-out, in order.
func parseIniExtensionEntries(content string) []iniExtensionEntry {
	var entries []iniExtensionEntry
	for i, raw := range strings.Split(content, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		s := strings.TrimSpace(line)
		enabled := true
		if strings.HasPrefix(s, ";") {
			enabled = false
			s = strings.TrimSpace(strings.TrimLeft(s, ";"))
		}
		m := iniExtensionRe.FindStringSubmatch(s)
		if m == nil {
			continue
		}
		name := normalizeExtensionName(m[2])
		if name == "" {
			continue
		}
		entries = append(entries, iniExtensionEntry{name: name, zend: m[1] == "zend_extension", enabled: enabled, index: i})
	}
	return entries
}

// parseIniExtensionDir returns the uncommented extension_dir value,
// or "" when unset.
func parseIniExtensionDir(content string) string {
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, ";") {
			continue
		}
		if m := iniExtensionDirRe.FindStringSubmatch(s); m != nil {
			return m[1]
		}
	}
	return ""
}

// resolveExtensionDir is the folder whose DLLs the checklist scans:
// the ini's extension_dir when set, else <php>/ext.
func resolveExtensionDir(phpDir, iniContent string) string {
	if d := parseIniExtensionDir(iniContent); d != "" {
		if strings.Contains(d, ":") || strings.HasPrefix(d, "/") || strings.HasPrefix(d, "\\") {
			return d
		}
		return joinPath(phpDir, d)
	}
	return joinPath(phpDir, "ext")
}

// --- php.ini editing ---

type extensionChange struct {
	enable bool
	zend   bool
}

// applyExtensionEdits rewrites ini lines for the given changes and
// reports whether anything changed. Rules:
//   - enable with an existing line: uncomment the first occurrence
//     and comment any duplicate active lines (a duplicate would
//     make PHP warn "module already loaded");
//   - enable without a line: append extension=<name> (or
//     zend_extension=<name> for Zend extensions);
//   - disable: comment every active line for the name.
//
// Everything else in the file is preserved byte-for-byte, including
// per-line CRLF endings. When nothing changes, the input is returned
// untouched.
func applyExtensionEdits(content string, changes map[string]extensionChange) (string, bool) {
	if len(changes) == 0 {
		return content, false
	}
	names := make([]string, 0, len(changes))
	for name := range changes {
		names = append(names, name)
	}
	sort.Strings(names)

	fileCRLF := strings.Contains(content, "\r\n")
	endsWithNL := strings.HasSuffix(content, "\n")
	var lines []string
	if content != "" {
		lines = strings.Split(content, "\n")
		if endsWithNL {
			lines = lines[:len(lines)-1] // trailing "" from the final \n
		}
	}
	crs := make([]bool, len(lines))
	cores := make([]string, len(lines))
	for i, raw := range lines {
		cores[i] = strings.TrimSuffix(raw, "\r")
		crs[i] = strings.HasSuffix(raw, "\r")
	}
	entries := parseIniExtensionEntries(content)

	changed := false
	comment := func(core string) string {
		return ";" + strings.TrimSpace(core)
	}
	uncomment := func(core string) string {
		return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(core), ";"))
	}
	for _, name := range names {
		ch := changes[name]
		var idxs []int
		for _, e := range entries {
			if e.name == name {
				idxs = append(idxs, e.index)
			}
		}
		if ch.enable {
			kept := false
			for _, ix := range idxs {
				// Only entries whose directive kind matches the
				// change are touched; a zend_extension line for
				// the same name is a different directive.
				if entriesForIndex(entries, ix).zend != ch.zend {
					continue
				}
				if entriesForIndex(entries, ix).enabled && !kept {
					kept = true
					continue
				}
				if entriesForIndex(entries, ix).enabled {
					cores[ix] = comment(cores[ix])
					changed = true
					continue
				}
				if !kept {
					cores[ix] = uncomment(cores[ix])
					kept = true
					changed = true
				}
			}
			if !kept {
				directive := "extension"
				if ch.zend {
					directive = "zend_extension"
				}
				cores = append(cores, directive+"="+name)
				crs = append(crs, fileCRLF)
				changed = true
			}
		} else {
			for _, ix := range idxs {
				if entriesForIndex(entries, ix).zend != ch.zend {
					continue
				}
				if entriesForIndex(entries, ix).enabled {
					cores[ix] = comment(cores[ix])
					changed = true
				}
			}
		}
	}
	if !changed {
		return content, false
	}
	var b strings.Builder
	for i, core := range cores {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(core)
		// The last line's \r is supplied by the trailing-newline
		// block below when the file ends with a newline; adding it
		// here too would double it (extension=curl\r\r\n).
		if crs[i] && !(endsWithNL && i == len(cores)-1) {
			b.WriteString("\r")
		}
	}
	if endsWithNL || len(cores) > 0 && content == "" {
		// Preserve the file's trailing newline convention; a file
		// that had none keeps none unless lines were appended,
		// in which case the join above already separates them.
		if endsWithNL {
			if fileCRLF {
				b.WriteString("\r\n")
			} else {
				b.WriteString("\n")
			}
		}
	}
	return b.String(), true
}

func entriesForIndex(entries []iniExtensionEntry, ix int) iniExtensionEntry {
	for _, e := range entries {
		if e.index == ix {
			return e
		}
	}
	return iniExtensionEntry{index: ix}
}

// --- inventory ---

// BuildExtensionInventory merges php -m output, the ext/ DLL scan,
// and php.ini lines into one checklist. phpModules are [PHP Modules]
// entries, zendModules are normalized [Zend Modules] entries.
func BuildExtensionInventory(version, iniPath, iniContent string, phpModules, zendModules, extFiles []string) PHPExtensionInventory {
	loadedPHP := map[string]bool{}
	for _, m := range phpModules {
		loadedPHP[strings.ToLower(strings.TrimSpace(m))] = true
	}
	loadedZend := map[string]bool{}
	for _, m := range zendModules {
		if n := normalizeZendModuleName(m); n != "" {
			loadedZend[n] = true
		}
	}
	dlls := map[string]bool{}
	for _, f := range extFiles {
		if !strings.HasSuffix(strings.ToLower(f), ".dll") {
			continue
		}
		if n := normalizeExtensionName(f); n != "" {
			dlls[n] = true
		}
	}
	type iniInfo struct {
		enabled  bool
		hasZend  bool
		hasEntry bool
	}
	ini := map[string]*iniInfo{}
	for _, e := range parseIniExtensionEntries(iniContent) {
		info := ini[e.name]
		if info == nil {
			info = &iniInfo{}
			ini[e.name] = info
		}
		info.hasEntry = true
		if e.zend {
			info.hasZend = true
		}
		if e.enabled {
			info.enabled = true
		}
	}

	namesSet := map[string]bool{}
	for n := range loadedPHP {
		namesSet[n] = true
	}
	for n := range loadedZend {
		namesSet[n] = true
	}
	for n := range dlls {
		namesSet[n] = true
	}
	for n := range ini {
		namesSet[n] = true
	}
	for _, n := range sqlServerExtensions {
		namesSet[n] = true
	}
	names := make([]string, 0, len(namesSet))
	for n := range namesSet {
		names = append(names, n)
	}
	sort.Strings(names)

	inv := PHPExtensionInventory{Version: version, IniPath: iniPath}
	for _, name := range names {
		loaded := loadedPHP[name] || loadedZend[name]
		hasDLL := dlls[name]
		info := ini[name]
		iniState := "absent"
		if info != nil {
			if info.enabled {
				iniState = "enabled"
			} else {
				iniState = "disabled"
			}
		}
		zend := loadedZend[name] || knownZendExtensions[name]
		if info != nil && info.hasZend {
			zend = true
		}
		ext := PHPExtension{
			Name: name, Zend: zend, Loaded: loaded, HasDLL: hasDLL, IniState: iniState,
		}
		switch {
		case loaded && !hasDLL && iniState == "absent":
			ext.Status = ExtensionBuiltin
			ext.Toggleable = false
			ext.Hint = "Bawaan PHP — selalu aktif."
		case loaded:
			ext.Status = ExtensionActive
			ext.Toggleable = iniState != "absent"
			if !ext.Toggleable {
				ext.Hint = "Termuat tapi tidak ada barisnya di php.ini ini — mungkin dimuat dari file ini lain (cek php --ini)."
			}
		case iniState == "enabled":
			ext.Status = ExtensionBroken
			ext.Toggleable = true
			if name == "sqlsrv" || name == "pdo_sqlsrv" {
				ext.Hint = "Aktif di php.ini tapi tidak termuat — pastikan DLL-nya NTS x64 dan Microsoft ODBC Driver for SQL Server terinstal."
			} else {
				ext.Hint = "Aktif di php.ini tapi tidak termuat — periksa DLL dan dependensinya di folder ext/."
			}
		case iniState == "disabled":
			ext.Status = ExtensionInactive
			ext.Toggleable = true
			if !hasDLL {
				ext.Hint = "DLL tidak ada di folder ext/ — instal dulu sebelum mengaktifkan."
			}
		case hasDLL:
			ext.Status = ExtensionInactive
			ext.Toggleable = true
		default:
			ext.Status = ExtensionNotInstalled
			ext.Toggleable = false
			ext.Hint = sqlServerHint(name)
		}
		inv.Extensions = append(inv.Extensions, ext)
	}
	return inv
}

// --- PHPManager methods ---

// Extensions returns the checklist for one registered PHP version.
func (m *PHPManager) Extensions(ctx context.Context, version string) (PHPExtensionInventory, error) {
	reg, err := m.Registry.Load()
	if err != nil {
		return PHPExtensionInventory{}, err
	}
	v, ok := reg.FindPHP(version)
	if !ok {
		return PHPExtensionInventory{}, fmt.Errorf("php version %q is not registered: %w", version, ErrPHPVersionNotRegistered)
	}
	return m.inventory(ctx, v)
}

func (m *PHPManager) inventory(ctx context.Context, v PHPVersion) (PHPExtensionInventory, error) {
	iniPath := joinPath(v.Path, "php.ini")
	raw, err := m.Files.ReadFile(iniPath)
	if err != nil {
		return PHPExtensionInventory{}, fmt.Errorf("php.ini untuk php %s tidak ditemukan di %s — daftarkan ulang versi ini: %w", v.Version, iniPath, err)
	}
	probe, err := m.Prober.Probe(ctx, v.Path)
	if err != nil {
		return PHPExtensionInventory{}, err
	}
	extDir := resolveExtensionDir(v.Path, string(raw))
	names, err := m.Files.ListNames(extDir)
	if err != nil {
		return PHPExtensionInventory{}, err
	}
	return BuildExtensionInventory(v.Version, iniPath, string(raw), probe.Modules, probe.ZendModules, names), nil
}

// SetExtensions applies one checklist save. desired maps extension
// keys to wanted enabled state, for changed rows only; unknown or
// non-toggleable names are refused, and enabling an extension
// without its DLL is refused. Enabling pdo_sqlsrv auto-enables pdo
// (its dependency) when pdo is not already active.
func (m *PHPManager) SetExtensions(ctx context.Context, version string, desired map[string]bool) (SetExtensionsResult, error) {
	norm := map[string]bool{}
	for k, v := range desired {
		nk := normalizeExtensionName(k)
		if nk == "" {
			return SetExtensionsResult{}, fmt.Errorf("nama extension tidak valid: %q", k)
		}
		norm[nk] = v
	}
	reg, err := m.Registry.Load()
	if err != nil {
		return SetExtensionsResult{}, err
	}
	v, ok := reg.FindPHP(version)
	if !ok {
		return SetExtensionsResult{}, fmt.Errorf("php version %q is not registered: %w", version, ErrPHPVersionNotRegistered)
	}
	iniPath := joinPath(v.Path, "php.ini")
	original, err := m.Files.ReadFile(iniPath)
	if err != nil {
		return SetExtensionsResult{}, fmt.Errorf("php.ini untuk php %s tidak ditemukan di %s — daftarkan ulang versi ini: %w", v.Version, iniPath, err)
	}
	inv, err := m.inventory(ctx, v)
	if err != nil {
		return SetExtensionsResult{}, err
	}
	byName := map[string]PHPExtension{}
	for _, e := range inv.Extensions {
		byName[e.Name] = e
	}
	for name, want := range norm {
		ext, ok := byName[name]
		if !ok {
			return SetExtensionsResult{}, fmt.Errorf("extension %q tidak dikenal untuk php %s", name, version)
		}
		if !ext.Toggleable {
			// A non-toggleable extension requested in its current
			// state is a no-op, not an error: a full-checklist
			// submission legitimately includes builtins (checked,
			// always loaded) and not-installed rows (unchecked).
			// Only a request that would CHANGE it is refused.
			if want == ext.Loaded {
				delete(norm, name)
				continue
			}
			return SetExtensionsResult{}, fmt.Errorf("extension %q tidak bisa diubah dari panel (status: %s)", name, ext.Status)
		}
		if want && ext.IniState != "enabled" && !ext.HasDLL {
			return SetExtensionsResult{}, fmt.Errorf("extension %q tidak bisa diaktifkan: DLL-nya tidak ada di folder ext/ — instal dulu", name)
		}
	}
	// Dependency: pdo_sqlsrv needs pdo.
	var autoEnabled []string
	if want, ok := norm["pdo_sqlsrv"]; ok && want {
		if cur, has := norm["pdo"]; has && !cur {
			return SetExtensionsResult{}, fmt.Errorf("pdo_sqlsrv membutuhkan pdo — tidak bisa menonaktifkan pdo sambil mengaktifkan pdo_sqlsrv")
		}
		if _, has := norm["pdo"]; !has {
			pdo, ok := byName["pdo"]
			if !ok {
				return SetExtensionsResult{}, fmt.Errorf("pdo_sqlsrv membutuhkan pdo, tapi pdo tidak tersedia untuk php %s", version)
			}
			// A loaded pdo already satisfies the dependency — that
			// includes builtin pdo (Loaded, but no ini line and no
			// DLL: IniState "absent"). Only an unloaded pdo needs
			// an ini line added.
			if !pdo.Loaded && pdo.IniState != "enabled" {
				if !pdo.Toggleable {
					return SetExtensionsResult{}, fmt.Errorf("pdo_sqlsrv membutuhkan pdo, tapi pdo tidak bisa diubah dari panel (status: %s)", pdo.Status)
				}
				if !pdo.HasDLL {
					return SetExtensionsResult{}, fmt.Errorf("pdo_sqlsrv membutuhkan pdo, tapi DLL pdo tidak ada di folder ext/")
				}
				norm["pdo"] = true
				autoEnabled = append(autoEnabled, "pdo")
			}
		}
	}
	changes := map[string]extensionChange{}
	for name, want := range norm {
		ext := byName[name]
		currently := ext.IniState == "enabled"
		if want != currently {
			changes[name] = extensionChange{enable: want, zend: ext.Zend}
		}
	}
	if len(changes) == 0 {
		return SetExtensionsResult{Inventory: inv, Changed: false}, nil
	}
	backupPath := iniPath + ".panel-bak-" + time.Now().Format("20060102-150405")
	if err := m.Files.WriteFile(backupPath, original); err != nil {
		return SetExtensionsResult{}, fmt.Errorf("backup php.ini gagal: %w", err)
	}
	edited, _ := applyExtensionEdits(string(original), changes)
	if err := m.Files.WriteFileAtomic(iniPath, []byte(edited)); err != nil {
		return SetExtensionsResult{}, fmt.Errorf("tulis php.ini gagal: %w", err)
	}
	// Verify against a fresh probe; on any mismatch restore the
	// backup instead of leaving a half-applied php.ini.
	restore := func() {
		_ = m.Files.WriteFileAtomic(iniPath, original)
	}
	probe, err := m.Prober.Probe(ctx, v.Path)
	if err != nil {
		restore()
		return SetExtensionsResult{}, fmt.Errorf("verifikasi php -m gagal sesudah php.ini diubah — php.ini dikembalikan ke backup %s: %w", backupPath, err)
	}
	loaded := map[string]bool{}
	for _, mod := range probe.Modules {
		loaded[strings.ToLower(strings.TrimSpace(mod))] = true
	}
	for _, mod := range probe.ZendModules {
		loaded[normalizeZendModuleName(mod)] = true
	}
	for name, ch := range changes {
		if ch.enable && !loaded[name] {
			restore()
			cause := "periksa DLL dan dependensinya di folder ext/"
			if name == "sqlsrv" || name == "pdo_sqlsrv" {
				cause = "pastikan DLL-nya NTS x64 dan Microsoft ODBC Driver for SQL Server terinstal"
			}
			return SetExtensionsResult{}, fmt.Errorf("extension %q gagal termuat sesudah php.ini diubah — php.ini dikembalikan ke backup %s (%s)", name, backupPath, cause)
		}
		if !ch.enable && loaded[name] {
			restore()
			return SetExtensionsResult{}, fmt.Errorf("extension %q masih termuat sesudah dinonaktifkan — php.ini dikembalikan ke backup %s (mungkin dimuat dari file ini lain; cek php --ini)", name, backupPath)
		}
	}
	fresh, err := m.inventory(ctx, v)
	if err != nil {
		restore()
		return SetExtensionsResult{}, fmt.Errorf("baca ulang inventaris gagal — php.ini dikembalikan ke backup %s: %w", backupPath, err)
	}
	return SetExtensionsResult{Inventory: fresh, Changed: true, BackupPath: backupPath, AutoEnabled: autoEnabled}, nil
}
