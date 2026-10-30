package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// iniDrivenProber simulates real php -m: a module is loaded when its
// ini line is active AND its DLL exists in ext/. Built-ins are always
// loaded. Used so SetExtensions tests exercise the verify step.
type iniDrivenProber struct{ fs *memFS }

func (p iniDrivenProber) Probe(_ context.Context, dir string) (PHPProbeResult, error) {
	mods := []string{"Core", "date", "standard"}
	var zend []string
	raw, err := p.fs.ReadFile(joinPath(dir, "php.ini"))
	if err != nil {
		return PHPProbeResult{Version: "8.3", Full: "8.3.13", NTS: true, Arch: "x64", Modules: mods}, nil
	}
	for _, e := range parseIniExtensionEntries(string(raw)) {
		if !e.enabled {
			continue
		}
		if !p.fs.Exists(joinPath(joinPath(dir, "ext"), dllFileName(e.name))) {
			continue
		}
		if e.zend {
			zend = append(zend, e.name)
		} else {
			mods = append(mods, e.name)
		}
	}
	return PHPProbeResult{Version: "8.3", Full: "8.3.13", NTS: true, Arch: "x64", Modules: mods, ZendModules: zend}, nil
}

func newExtRig(t *testing.T) (*PHPManager, *memFS) {
	t.Helper()
	probe := PHPProbeResult{Version: "8.3", Full: "8.3.13", NTS: true, Arch: "x64", Modules: []string{"curl", "mbstring"}}
	mgr, fs, _ := newPHPManagerRig(t, probe)
	if _, err := mgr.Register(context.Background(), `C:\php\8.3`); err != nil {
		t.Fatalf("Register: %v", err)
	}
	mgr.Prober = iniDrivenProber{fs}
	return mgr, fs
}

func writeIni(t *testing.T, fs *memFS, content string) {
	t.Helper()
	if err := fs.WriteFile(`C:\php\8.3/php.ini`, []byte(content)); err != nil {
		t.Fatalf("write ini: %v", err)
	}
}

func writeDLL(t *testing.T, fs *memFS, name string) {
	t.Helper()
	if err := fs.WriteFile(filepath.Join(`C:\php\8.3\ext`, "php_"+name+".dll"), []byte("dll")); err != nil {
		t.Fatalf("write dll: %v", err)
	}
}

func readIni(t *testing.T, fs *memFS) string {
	t.Helper()
	raw, err := fs.ReadFile(`C:\php\8.3/php.ini`)
	if err != nil {
		t.Fatalf("read ini: %v", err)
	}
	return string(raw)
}

func countBackups(t *testing.T, fs *memFS) int {
	t.Helper()
	names, err := fs.ListNames(`C:\php\8.3`)
	if err != nil {
		t.Fatalf("ListNames: %v", err)
	}
	n := 0
	for _, name := range names {
		if strings.HasPrefix(name, "php.ini.panel-bak-") {
			n++
		}
	}
	return n
}

func TestParseIniExtensionEntries(t *testing.T) {
	content := "extension=mbstring\n;extension=curl\n  ; extension = gd\nzend_extension=opcache\n;zend_extension=xdebug\nextension=\"php_pdo_sqlsrv.dll\" ; trailing\nextension_dir = \"C:/php/8.3/ext\"\n# not ini\nfoo=bar\n"
	got := parseIniExtensionEntries(content)
	want := []iniExtensionEntry{
		{name: "mbstring", enabled: true},
		{name: "curl", enabled: false},
		{name: "gd", enabled: false},
		{name: "opcache", zend: true, enabled: true},
		{name: "xdebug", zend: true, enabled: false},
		{name: "pdo_sqlsrv", enabled: true},
	}
	if len(got) != len(want) {
		t.Fatalf("entries = %+v, want %d entries", got, len(want))
	}
	for i, w := range want {
		if got[i].name != w.name || got[i].zend != w.zend || got[i].enabled != w.enabled {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], w)
		}
	}
	if d := parseIniExtensionDir(content); d != "C:/php/8.3/ext" {
		t.Errorf("extension_dir = %q, want C:/php/8.3/ext", d)
	}
}

func TestParsePHPModulesSections(t *testing.T) {
	php, zend := ParsePHPModulesSections("[PHP Modules]\nbcmath\ncurl\n\n[Zend Modules]\nZend OPcache\nXdebug\n")
	if len(php) != 2 || php[1] != "curl" {
		t.Errorf("php = %v", php)
	}
	if len(zend) != 2 || zend[0] != "opcache" || zend[1] != "xdebug" {
		t.Errorf("zend = %v, want [opcache xdebug]", zend)
	}
}

func TestApplyExtensionEdits(t *testing.T) {
	// Uncomment.
	out, changed := applyExtensionEdits(";extension=curl\n", map[string]extensionChange{"curl": {enable: true}})
	if !changed || out != "extension=curl\n" {
		t.Errorf("uncomment = %q changed=%v", out, changed)
	}
	// Append regular + zend.
	out, changed = applyExtensionEdits("display_errors = Off\n", map[string]extensionChange{
		"curl":    {enable: true},
		"opcache": {enable: true, zend: true},
	})
	if !changed || !strings.Contains(out, "extension=curl\n") || !strings.Contains(out, "zend_extension=opcache\n") {
		t.Errorf("append = %q changed=%v", out, changed)
	}
	// Disable.
	out, changed = applyExtensionEdits("extension=mbstring\n", map[string]extensionChange{"mbstring": {enable: false}})
	if !changed || out != ";extension=mbstring\n" {
		t.Errorf("disable = %q changed=%v", out, changed)
	}
	// Duplicate active lines are normalized to one.
	out, changed = applyExtensionEdits("extension=curl\nextension=curl\n", map[string]extensionChange{"curl": {enable: true}})
	if !changed || out != "extension=curl\n;extension=curl\n" {
		t.Errorf("dedup = %q changed=%v", out, changed)
	}
	// CRLF preserved per line.
	out, changed = applyExtensionEdits(";extension=curl\r\n", map[string]extensionChange{"curl": {enable: true}})
	if !changed || out != "extension=curl\r\n" {
		t.Errorf("crlf = %q changed=%v", out, changed)
	}
	// No change: byte-identical, false.
	in := "extension=mbstring\n"
	out, changed = applyExtensionEdits(in, map[string]extensionChange{"mbstring": {enable: true}})
	if changed || out != in {
		t.Errorf("no-change = %q changed=%v, want identical+false", out, changed)
	}
}

func TestBuildExtensionInventory(t *testing.T) {
	ini := "extension=mbstring\n;extension=curl\n;extension=gd\nextension=pdo_sqlsrv\nzend_extension=opcache\n"
	inv := BuildExtensionInventory("8.3", `C:\php\8.3/php.ini`, ini,
		[]string{"Core", "date", "standard", "mbstring"}, []string{"opcache"},
		[]string{"php_curl.dll", "php_mbstring.dll", "php_gd.dll", "php_pdo_sqlsrv.dll", "php_sqlsrv.dll"})
	byName := map[string]PHPExtension{}
	for _, e := range inv.Extensions {
		byName[e.Name] = e
	}
	cases := []struct {
		name       string
		status     string
		toggleable bool
	}{
		{"mbstring", ExtensionActive, true},
		{"curl", ExtensionInactive, true},
		{"gd", ExtensionInactive, true},
		{"pdo_sqlsrv", ExtensionBroken, true},
		{"sqlsrv", ExtensionInactive, true},
		{"opcache", ExtensionActive, true},
		{"core", ExtensionBuiltin, false},
		{"date", ExtensionBuiltin, false},
	}
	for _, c := range cases {
		e, ok := byName[c.name]
		if !ok {
			t.Errorf("missing extension %s", c.name)
			continue
		}
		if e.Status != c.status || e.Toggleable != c.toggleable {
			t.Errorf("%s = status %s toggleable %v, want %s %v", c.name, e.Status, e.Toggleable, c.status, c.toggleable)
		}
	}
	if !byName["opcache"].Zend {
		t.Errorf("opcache must be flagged Zend")
	}
	if byName["pdo_sqlsrv"].Hint == "" || !strings.Contains(byName["pdo_sqlsrv"].Hint, "ODBC") {
		t.Errorf("pdo_sqlsrv broken hint = %q, want ODBC mention", byName["pdo_sqlsrv"].Hint)
	}

	// Absent SQL Server drivers are listed as not_installed.
	inv2 := BuildExtensionInventory("8.3", `C:\php\8.3/php.ini`, "", []string{"Core"}, nil, nil)
	byName2 := map[string]PHPExtension{}
	for _, e := range inv2.Extensions {
		byName2[e.Name] = e
	}
	for _, n := range []string{"sqlsrv", "pdo_sqlsrv"} {
		e, ok := byName2[n]
		if !ok || e.Status != ExtensionNotInstalled || e.Toggleable {
			t.Errorf("%s = %+v, want not_installed non-toggleable", n, e)
		}
	}
}

func TestSetExtensionsEnableDisable(t *testing.T) {
	mgr, fs := newExtRig(t)
	writeIni(t, fs, ";extension=curl\nextension=mbstring\n")
	writeDLL(t, fs, "curl")
	writeDLL(t, fs, "mbstring")
	res, err := mgr.SetExtensions(context.Background(), "8.3", map[string]bool{"curl": true, "mbstring": false})
	if err != nil {
		t.Fatalf("SetExtensions: %v", err)
	}
	if !res.Changed || res.BackupPath == "" {
		t.Errorf("result = changed %v backup %q, want changed+backup", res.Changed, res.BackupPath)
	}
	ini := readIni(t, fs)
	if !strings.Contains(ini, "extension=curl\n") || !strings.Contains(ini, ";extension=mbstring\n") {
		t.Errorf("ini after save:\n%s", ini)
	}
	if countBackups(t, fs) != 1 {
		t.Errorf("want exactly 1 backup, got %d", countBackups(t, fs))
	}
	for _, e := range res.Inventory.Extensions {
		if e.Name == "curl" && e.Status != ExtensionActive {
			t.Errorf("curl status = %s, want active", e.Status)
		}
		if e.Name == "mbstring" && e.Status != ExtensionInactive {
			t.Errorf("mbstring status = %s, want inactive", e.Status)
		}
	}
}

func TestSetExtensionsRollback(t *testing.T) {
	// Static prober never reports gd as loaded: verification must fail
	// and the ini must come back byte-identical.
	probe := PHPProbeResult{Version: "8.3", Full: "8.3.13", NTS: true, Arch: "x64", Modules: []string{"Core"}}
	mgr, fs, _ := newPHPManagerRig(t, probe)
	if _, err := mgr.Register(context.Background(), `C:\php\8.3`); err != nil {
		t.Fatalf("Register: %v", err)
	}
	writeIni(t, fs, ";extension=gd\n")
	writeDLL(t, fs, "gd")
	before := readIni(t, fs)
	if _, err := mgr.SetExtensions(context.Background(), "8.3", map[string]bool{"gd": true}); err == nil {
		t.Fatalf("SetExtensions must fail when the extension never loads")
	} else if !strings.Contains(err.Error(), "dikembalikan") {
		t.Errorf("error must mention rollback, got: %v", err)
	}
	if got := readIni(t, fs); got != before {
		t.Errorf("ini was not restored:\n%s", got)
	}
}

func TestSetExtensionsValidation(t *testing.T) {
	mgr, fs := newExtRig(t)
	writeIni(t, fs, ";extension=gd\n;extension=pdo_sqlsrv\n")
	writeDLL(t, fs, "pdo")
	writeDLL(t, fs, "pdo_sqlsrv")

	// Unknown name.
	if _, err := mgr.SetExtensions(context.Background(), "8.3", map[string]bool{"nope": true}); err == nil {
		t.Errorf("unknown extension must be refused")
	}
	// Enable without DLL.
	if _, err := mgr.SetExtensions(context.Background(), "8.3", map[string]bool{"gd": true}); err == nil {
		t.Errorf("enable without DLL must be refused")
	}
	if countBackups(t, fs) != 0 {
		t.Errorf("refused saves must not write backups")
	}
	// pdo_sqlsrv auto-enables pdo.
	res, err := mgr.SetExtensions(context.Background(), "8.3", map[string]bool{"pdo_sqlsrv": true})
	if err != nil {
		t.Fatalf("pdo_sqlsrv enable: %v", err)
	}
	if len(res.AutoEnabled) != 1 || res.AutoEnabled[0] != "pdo" {
		t.Errorf("autoEnabled = %v, want [pdo]", res.AutoEnabled)
	}
	ini := readIni(t, fs)
	if !strings.Contains(ini, "extension=pdo_sqlsrv\n") || !strings.Contains(ini, "extension=pdo\n") {
		t.Errorf("ini after pdo_sqlsrv save:\n%s", ini)
	}
	// pdo_sqlsrv + disable pdo conflicts.
	if _, err := mgr.SetExtensions(context.Background(), "8.3", map[string]bool{"pdo_sqlsrv": true, "pdo": false}); err == nil {
		t.Errorf("pdo_sqlsrv + disable pdo must conflict")
	}
}

func TestSetExtensionsNoChange(t *testing.T) {
	mgr, fs := newExtRig(t)
	writeIni(t, fs, "extension=mbstring\n")
	writeDLL(t, fs, "mbstring")
	res, err := mgr.SetExtensions(context.Background(), "8.3", map[string]bool{"mbstring": true})
	if err != nil {
		t.Fatalf("SetExtensions: %v", err)
	}
	if res.Changed || res.BackupPath != "" {
		t.Errorf("no-change result = %+v, want unchanged without backup", res)
	}
	if countBackups(t, fs) != 0 {
		t.Errorf("no-change save must not write backups")
	}
}

// builtinPdoProber wraps iniDrivenProber and additionally reports pdo
// as always loaded (compiled in: no DLL, no ini line) — the shape
// that used to break pdo_sqlsrv activation with "pdo tidak bisa
// diubah (status: builtin)".
type builtinPdoProber struct{ iniDrivenProber }

func (p builtinPdoProber) Probe(ctx context.Context, dir string) (PHPProbeResult, error) {
	res, err := p.iniDrivenProber.Probe(ctx, dir)
	if err != nil {
		return res, err
	}
	res.Modules = append(res.Modules, "pdo")
	return res, nil
}

func TestSetExtensionsBuiltinPdo(t *testing.T) {
	mgr, fs := newExtRig(t)
	base, ok := mgr.Prober.(iniDrivenProber)
	if !ok {
		t.Fatalf("rig prober must be iniDrivenProber, got %T", mgr.Prober)
	}
	mgr.Prober = builtinPdoProber{base}
	ctx := context.Background()
	writeIni(t, fs, ";extension=pdo_sqlsrv\n")
	writeDLL(t, fs, "pdo_sqlsrv")
	// NOTE: no pdo DLL, no pdo ini line — pdo is builtin.

	// Inventory sanity: pdo builtin, pdo_sqlsrv inactive.
	inv, err := mgr.Extensions(ctx, "8.3")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]PHPExtension{}
	for _, e := range inv.Extensions {
		byName[e.Name] = e
	}
	if byName["pdo"].Status != ExtensionBuiltin || !byName["pdo"].Loaded || byName["pdo"].Toggleable {
		t.Fatalf("pdo must be builtin+loaded+untoggleable, got %+v", byName["pdo"])
	}
	if byName["pdo_sqlsrv"].Status != ExtensionInactive {
		t.Fatalf("pdo_sqlsrv must be inactive, got %+v", byName["pdo_sqlsrv"])
	}

	// Enabling pdo_sqlsrv must succeed WITHOUT auto-enabling pdo:
	// builtin pdo already satisfies the dependency.
	res, err := mgr.SetExtensions(ctx, "8.3", map[string]bool{"pdo_sqlsrv": true})
	if err != nil {
		t.Fatalf("pdo_sqlsrv enable with builtin pdo: %v", err)
	}
	if len(res.AutoEnabled) != 0 {
		t.Errorf("autoEnabled = %v, want none (pdo is builtin)", res.AutoEnabled)
	}
	ini := readIni(t, fs)
	if !strings.Contains(ini, "extension=pdo_sqlsrv\n") {
		t.Errorf("ini must enable pdo_sqlsrv:\n%s", ini)
	}
	if strings.Contains(ini, "extension=pdo\n") {
		t.Errorf("builtin pdo must not gain an ini line:\n%s", ini)
	}

	// Requesting builtin pdo in its current state is a no-op, not an error.
	res, err = mgr.SetExtensions(ctx, "8.3", map[string]bool{"pdo": true})
	if err != nil {
		t.Fatalf("builtin pdo requested as-is must not error: %v", err)
	}
	if res.Changed {
		t.Errorf("builtin pdo requested as-is must not change anything")
	}

	// Disabling builtin pdo is still refused.
	if _, err := mgr.SetExtensions(ctx, "8.3", map[string]bool{"pdo": false}); err == nil {
		t.Errorf("disabling builtin pdo must be refused")
	}
}
