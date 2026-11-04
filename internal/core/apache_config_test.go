package core

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseApacheModules(t *testing.T) {
	conf := "LoadModule fcgid_module modules/mod_fcgid.so\n# LoadModule rewrite_module modules/mod_rewrite.so\nLoadModule dir_module modules/mod_dir.so\n"
	mods := ParseApacheModules(conf, []string{"mod_fcgid.so", "mod_rewrite.so", "mod_ssl.so"}, false)
	byName := map[string]ApacheModule{}
	for _, m := range mods {
		byName[m.Name] = m
	}
	if !byName["fcgid_module"].Enabled || !byName["fcgid_module"].Locked {
		t.Errorf("fcgid must be enabled and locked, got %+v", byName["fcgid_module"])
	}
	if byName["rewrite_module"].Enabled {
		t.Errorf("commented rewrite must be disabled")
	}
	if byName["ssl_module"].Enabled {
		t.Errorf(".so-only ssl must appear disabled")
	}
	if byName["ssl_module"].File != "modules/mod_ssl.so" {
		t.Errorf("ssl file = %q", byName["ssl_module"].File)
	}
}

func TestToggleApacheModuleLocked(t *testing.T) {
	conf := "LoadModule fcgid_module modules/mod_fcgid.so\n"
	if _, err := ToggleApacheModule(conf, "fcgid_module", false, false); err == nil {
		t.Error("disabling a locked module must fail")
	}
	out, err := ToggleApacheModule("# LoadModule rewrite_module modules/mod_rewrite.so\n", "rewrite_module", true, false)
	if err != nil || !strings.Contains(out, "LoadModule rewrite_module") || strings.Contains(out, "# LoadModule") {
		t.Errorf("enabling rewrite = %q, %v", out, err)
	}
}

func TestFcgidSettingsEffectiveAndValidate(t *testing.T) {
	if got := (FcgidSettings{}).Effective(); got.MaxRequestLen != 16777216 || got.MaxProcesses != 20 {
		t.Errorf("defaults = %+v", got)
	}
	if err := (FcgidSettings{MaxRequestLen: 1000}).Validate(); err == nil {
		t.Error("MaxRequestLen below the mod_fcgid default must fail validation")
	}
}

func TestConfigGenCustomFcgidSurvivesGoldenShape(t *testing.T) {
	reg := goldenFixture()
	reg.Settings.Fcgid = FcgidSettings{MaxRequestLen: 67108864}
	out, err := Generator{}.GenerateAll(reg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out[GlobalConfigName], "FcgidMaxRequestLen 67108864") {
		t.Errorf("custom MaxRequestLen must reach the global conf, got:\n%s", out[GlobalConfigName])
	}
	if !strings.Contains(out[GlobalConfigName], "FcgidMaxProcesses 20") {
		t.Errorf("other values must stay at defaults, got:\n%s", out[GlobalConfigName])
	}
}

func editorRig(t *testing.T) *managerRig {
	t.Helper()
	rig := newManagerRig(t)
	httpd := filepath.Join(`C:\apache`, "conf", "httpd.conf")
	conf := "LoadModule fcgid_module modules/mod_fcgid.so\nLoadModule rewrite_module modules/mod_rewrite.so\n"
	if err := rig.fs.WriteFile(httpd, []byte(conf)); err != nil {
		t.Fatal(err)
	}
	return rig
}

func TestEditorSavePassesAndRestarts(t *testing.T) {
	rig := editorRig(t)
	backup, err := rig.mgr.SaveConfigFile(context.Background(), "httpd.conf", "LoadModule fcgid_module modules/mod_fcgid.so\n# edited\n")
	if err != nil {
		t.Fatal(err)
	}
	if backup == "" || rig.svc.restarts != 1 {
		t.Errorf("save must backup and restart once, backup=%q restarts=%d", backup, rig.svc.restarts)
	}
	raw, _ := rig.fs.ReadFile(filepath.Join(`C:\apache`, "conf", "httpd.conf"))
	if !strings.Contains(string(raw), "# edited") {
		t.Errorf("new content must be live, got %q", raw)
	}
}

func TestEditorSaveRollsBackOnHttpdT(t *testing.T) {
	rig := editorRig(t)
	rig.runner.err = errors.New("Syntax error")
	_, err := rig.mgr.SaveConfigFile(context.Background(), "httpd.conf", "broken {{{")
	if err == nil {
		t.Fatal("httpd -t failure must fail the save")
	}
	if rig.svc.restarts != 0 {
		t.Errorf("failed test must not restart, restarts=%d", rig.svc.restarts)
	}
	raw, _ := rig.fs.ReadFile(filepath.Join(`C:\apache`, "conf", "httpd.conf"))
	if strings.Contains(string(raw), "broken") {
		t.Errorf("old content must be restored, got %q", raw)
	}
}

func TestEditorRejectsPathTraversal(t *testing.T) {
	rig := editorRig(t)
	for _, name := range []string{"../registry.json", "C:/Windows/win.ini", "secret.txt", "httpd.conf/evil"} {
		if _, err := rig.mgr.ReadConfigFile(name); err == nil {
			t.Errorf("name %q must be rejected", name)
		}
	}
	if _, err := rig.mgr.ReadConfigFile("00-panel-global.conf"); err != nil {
		t.Errorf("generated file must be readable: %v", err)
	}
}

func TestSetApacheModule(t *testing.T) {
	rig := editorRig(t)
	if err := rig.mgr.SetApacheModule(context.Background(), "rewrite_module", false); err != nil {
		t.Fatal(err)
	}
	raw, _ := rig.fs.ReadFile(filepath.Join(`C:\apache`, "conf", "httpd.conf"))
	if !strings.Contains(string(raw), "# LoadModule rewrite_module") {
		t.Errorf("rewrite must be commented out, got %q", raw)
	}
	if err := rig.mgr.SetApacheModule(context.Background(), "fcgid_module", false); err == nil {
		t.Error("locked fcgid must not be disabled")
	}
}

func TestSetFcgidSettingsApplies(t *testing.T) {
	rig := editorRig(t)
	res, err := rig.mgr.SetFcgidSettings(context.Background(), FcgidSettings{MaxRequestLen: 33554432})
	if err != nil {
		t.Fatal(err)
	}
	if res.BackupDir == "" {
		t.Error("apply result must carry a backup dir")
	}
	raw, _ := rig.fs.ReadFile(filepath.Join(rig.genDir, GlobalConfigName))
	if !strings.Contains(string(raw), "FcgidMaxRequestLen 33554432") {
		t.Errorf("generated global conf must carry the new value, got:\n%s", raw)
	}
	rawReg, _ := rig.fs.ReadFile(rig.regPath)
	if !strings.Contains(string(rawReg), "33554432") {
		t.Errorf("registry must commit the new value, got:\n%s", rawReg)
	}
}
