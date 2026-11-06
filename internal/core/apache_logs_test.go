package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGeneratorApacheLogsRotated(t *testing.T) {
	reg := goldenFixture()
	out, err := Generator{ApacheLogs: ApacheLogConfig{Dir: "C:/Panel/data/logs/apache", Rotate: true}}.GenerateAll(reg)
	if err != nil {
		t.Fatal(err)
	}
	conf := out[GlobalConfigName]
	for _, want := range []string{
		`LogFormat "%h %l %u %t \"%r\" %>s %b \"%{Referer}i\" \"%{User-Agent}i\"" panel_combined`,
		`ErrorLog "|bin/rotatelogs.exe C:/Panel/data/logs/apache/error-%Y-%m-%d.log 86400"`,
		`CustomLog "|bin/rotatelogs.exe C:/Panel/data/logs/apache/access-%Y-%m-%d.log 86400" panel_combined`,
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("rotated global conf must contain %q, got:\n%s", want, conf)
		}
	}
	// Zero-value generator: no log directives, golden files unaffected.
	plain, err := Generator{}.GenerateAll(reg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain[GlobalConfigName], "ErrorLog") || strings.Contains(plain[GlobalConfigName], "CustomLog") {
		t.Errorf("Generator{} must not emit log directives, got:\n%s", plain[GlobalConfigName])
	}
}

func TestGeneratorApacheLogsFallback(t *testing.T) {
	reg := goldenFixture()
	out, err := Generator{ApacheLogs: ApacheLogConfig{Dir: "C:/Panel/data/logs/apache", Rotate: false}}.GenerateAll(reg)
	if err != nil {
		t.Fatal(err)
	}
	conf := out[GlobalConfigName]
	if strings.Contains(conf, "|bin/rotatelogs") {
		t.Errorf("fallback conf must not pipe through rotatelogs, got:\n%s", conf)
	}
	for _, want := range []string{
		`ErrorLog C:/Panel/data/logs/apache/error.log`,
		`CustomLog C:/Panel/data/logs/apache/access.log panel_combined`,
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("fallback global conf must contain %q, got:\n%s", want, conf)
		}
	}
}

func TestHasRotateLogs(t *testing.T) {
	fs := newMemFS()
	if HasRotateLogs(fs, `C:\apache`) {
		t.Error("missing rotatelogs must be false")
	}
	if err := fs.WriteFile(`C:\apache\bin\rotatelogs.exe`, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if !HasRotateLogs(fs, `C:\apache`) {
		t.Error("present rotatelogs must be true")
	}
}

func logRig(t *testing.T) (*managerRig, string) {
	t.Helper()
	rig := newManagerRig(t)
	logDir := filepath.Join("data", "logs", "apache")
	rig.mgr.Cfg.ApacheLogDir = logDir
	return rig, logDir
}

func TestListAndPruneApacheLogs(t *testing.T) {
	rig, logDir := logRig(t)
	today := time.Now().Format("2006-01-02")
	files := map[string]string{
		"access-" + today + ".log": "127.0.0.1 - - [06/Nov/2026] \"GET /emc HTTP/1.1\" 200 1234\n",
		"error-" + today + ".log":  "[Thu Nov 06 07:00:00 2026] [fcgid:warn] test\n",
		"access-2020-01-01.log":    "old\n",
		"error-2020-01-01.log":     "old\n",
		"access.log":               "plain fallback\n",
		"random.txt":               "foreign\n",
	}
	for name, content := range files {
		if err := rig.fs.WriteFile(filepath.Join(logDir, name), []byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	listed, err := rig.mgr.ListApacheLogs()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"access-2020-01-01.log", "error-2020-01-01.log"} {
		for _, f := range listed {
			if strings.HasSuffix(f.Path, name) {
				t.Errorf("prune must have removed %s", name)
			}
		}
	}
	if len(listed) != 3 {
		t.Fatalf("list after prune = %d files, want 3 (today access+error, plain fallback): %+v", len(listed), listed)
	}
	if listed[0].Kind != ApacheLogAccess || listed[0].Date != today {
		t.Errorf("newest access first, got %+v", listed[0])
	}
}

func TestReadApacheLog(t *testing.T) {
	rig, logDir := logRig(t)
	today := time.Now().Format("2006-01-02")
	if err := rig.fs.WriteFile(filepath.Join(logDir, "error-"+today+".log"), []byte("line1\nline2\n")); err != nil {
		t.Fatal(err)
	}
	got, err := rig.mgr.ReadApacheLog("error", "")
	if err != nil || !strings.Contains(got, "line2") {
		t.Errorf("ReadApacheLog(today) = %q, %v", got, err)
	}
	// Missing dated file falls back to the plain file.
	if err := rig.fs.WriteFile(filepath.Join(logDir, "access.log"), []byte("plain\n")); err != nil {
		t.Fatal(err)
	}
	got, err = rig.mgr.ReadApacheLog("access", "2020-01-01")
	if err != nil || !strings.Contains(got, "plain") {
		t.Errorf("ReadApacheLog(missing date) must fall back to plain file, got %q, %v", got, err)
	}
	if _, err := rig.mgr.ReadApacheLog("bogus", ""); err == nil {
		t.Error("unknown kind must fail")
	}
	if _, err := rig.mgr.ReadApacheLog("error", "06-11-2026"); err == nil {
		t.Error("malformed date must fail")
	}
}

func TestApplyWritesApacheLogDirectives(t *testing.T) {
	rig, logDir := logRig(t)
	if err := rig.fs.WriteFile(`C:\apache\bin\rotatelogs.exe`, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.mgr.Apply(context.Background(), baseRegistry("8.2")); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	raw, err := rig.fs.ReadFile(filepath.Join(rig.genDir, GlobalConfigName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "rotatelogs.exe") || !strings.Contains(string(raw), "panel_combined") {
		t.Errorf("applied global conf must carry log directives, got:\n%s", raw)
	}
	if !rig.fs.IsDir(logDir) {
		t.Errorf("apply must create the apache log dir %s", logDir)
	}
}
