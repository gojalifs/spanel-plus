package core

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// makeUpgradeBundle builds a bundle in memory: manifest + inner
// Apache zip (Apache24/bin/httpd.exe whose CONTENT is the version,
// so the fake runner's -v probe follows the swap) + inner fcgid zip.
func makeUpgradeBundle(t *testing.T, version string, tamper bool) []byte {
	t.Helper()
	mkzip := func(files map[string]string) []byte {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for name, content := range files {
			w, err := zw.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte(content)); err != nil {
				t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	apacheZip := mkzip(map[string]string{
		"Apache24/bin/httpd.exe":                 version,
		"Apache24/conf/mime.types":               "text/html html",
		"Apache24/conf/extra/httpd-default.conf": "# new default",
	})
	fcgidZip := mkzip(map[string]string{"mod_fcgid.so": "so-bytes"})
	manifest := ApacheBundleManifest{
		FormatVersion:  ApacheBundleFormat,
		ApacheVersion:  version,
		ApacheVS:       ApacheVS,
		ApacheFilename: "httpd-" + version + "-261112-Win64-VS18.zip",
		ApacheSHA256:   sha256Hex(apacheZip),
		ApacheURL:      "https://example.invalid/apache.zip",
		FcgidVersion:   ApacheFcgidVersion,
		FcgidSHA256:    sha256Hex(fcgidZip),
		FcgidURL:       "https://example.invalid/fcgid.zip",
		CreatedAt:      time.Now().UTC(),
	}
	if tamper {
		manifest.ApacheSHA256 = strings.Repeat("0", 64)
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range []struct {
		name string
		data []byte
	}{{"manifest.json", manifestJSON}, {"apache.zip", apacheZip}, {"mod_fcgid.zip", fcgidZip}} {
		w, err := zw.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(e.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// seedUpgradeRig plants a running Apache 2.4.69 in the rig's
// memFS: httpd.exe (content = version), mod_fcgid, operator conf,
// htdocs, and one custom module that must NOT be carried over.
func seedUpgradeRig(t *testing.T, rig *managerRig) {
	t.Helper()
	home := `C:\apache`
	write := func(rel, content string) {
		if err := rig.fs.WriteFile(filepath.Join(home, rel), []byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join("bin", "httpd.exe"), "2.4.69")
	write(filepath.Join("modules", "mod_fcgid.so"), "so-old")
	write(filepath.Join("modules", "mod_custom.so"), "custom")
	write(filepath.Join("conf", "httpd.conf"), "LoadModule fcgid_module modules/mod_fcgid.so\nIncludeOptional conf/generated/*.conf\n")
	write(filepath.Join("conf", "extra", "httpd-default.conf"), "# operator default")
	write(filepath.Join("htdocs", "index.html"), "<h1>ours</h1>")
	// The -v probe reads the httpd.exe file content, so it follows
	// the folder swap exactly like the real binary would.
	rig.runner.versionFrom = func(cmd string) string {
		b, err := rig.fs.ReadFile(cmd)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
}

func TestApacheManagerImportBundle(t *testing.T) {
	rig := newManagerRig(t)
	seedUpgradeRig(t, rig)
	bundle := makeUpgradeBundle(t, "2.4.71", false)
	bundlePath := joinPath(`C:\data`, "bundle.zip")
	if err := rig.fs.WriteFile(bundlePath, bundle); err != nil {
		t.Fatal(err)
	}
	res, err := rig.mgr.ImportBundle(context.Background(), bundlePath)
	if err != nil {
		t.Fatalf("ImportBundle: %v", err)
	}
	if res.OldVersion != "2.4.69" || res.NewVersion != "2.4.71" {
		t.Errorf("result = %+v", res)
	}
	if !strings.Contains(res.BackupPath, "2.4.69") {
		t.Errorf("backup path must name the old version, got %q", res.BackupPath)
	}
	// Home now runs the new version; the backup keeps the old one.
	home := `C:\apache`
	if got, _ := rig.fs.ReadFile(filepath.Join(home, "bin", "httpd.exe")); string(got) != "2.4.71" {
		t.Errorf("home httpd.exe = %q, want 2.4.71", got)
	}
	if got, _ := rig.fs.ReadFile(filepath.Join(res.BackupPath, "bin", "httpd.exe")); string(got) != "2.4.69" {
		t.Errorf("backup httpd.exe = %q, want 2.4.69", got)
	}
	// Operator files carried over; new zip defaults kept.
	if got, _ := rig.fs.ReadFile(filepath.Join(home, "conf", "httpd.conf")); !strings.Contains(string(got), "IncludeOptional") {
		t.Errorf("httpd.conf not carried over: %q", got)
	}
	if got, _ := rig.fs.ReadFile(filepath.Join(home, "conf", "extra", "httpd-default.conf")); string(got) != "# operator default" {
		t.Errorf("conf/extra not carried over: %q", got)
	}
	if got, _ := rig.fs.ReadFile(filepath.Join(home, "htdocs", "index.html")); string(got) != "<h1>ours</h1>" {
		t.Errorf("htdocs not carried over: %q", got)
	}
	if got, _ := rig.fs.ReadFile(filepath.Join(home, "conf", "mime.types")); string(got) != "text/html html" {
		t.Errorf("mime.types must come from the NEW zip, got %q", got)
	}
	if got, _ := rig.fs.ReadFile(filepath.Join(home, "modules", "mod_fcgid.so")); string(got) != "so-bytes" {
		t.Errorf("mod_fcgid.so must come from the bundle, got %q", got)
	}
	// Custom module: not carried, but warned about.
	if rig.fs.Exists(filepath.Join(home, "modules", "mod_custom.so")) {
		t.Error("mod_custom.so must NOT be carried over to the new Apache")
	}
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "mod_custom.so") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings must name mod_custom.so, got %v", res.Warnings)
	}
	// Service was stopped for the swap and started again.
	if rig.svc.stops != 1 || rig.svc.starts != 1 {
		t.Errorf("stops=%d starts=%d, want 1 each", rig.svc.stops, rig.svc.starts)
	}
	// Staging is gone (it became home); no leftover.
	if rig.fs.Exists(home + "-2.4.71") {
		t.Error("staging folder must not remain after a successful import")
	}
	// httpd.conf references logs/ relatively (logs/access_log) and
	// httpd -t requires the directory to exist (issue #5: the real
	// Apache zip ships logs/ as an EMPTY directory entry, so the
	// home folder must have it even though no log file is carried
	// over — old logs stay in the backup folder).
	if !rig.fs.IsDir(filepath.Join(home, "logs")) {
		t.Error("home must have a logs/ directory after import")
	}
}

// Regression test for issue #5: the Apache zip ships logs/ as an
// empty directory entry. Extraction must create it — skipping
// directory entries (the old behaviour) left staging without
// logs/ and httpd -t failed with AH02297.
func TestExtractZipToStoreCreatesEmptyDirs(t *testing.T) {
	fs := newMemFS()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if _, err := zw.Create("Apache24/logs/"); err != nil {
		t.Fatal(err)
	}
	w, err := zw.Create("Apache24/bin/httpd.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("2.4.71")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	dest := `C:\staging`
	if err := extractZipToStore(fs, zr, "Apache24/", dest); err != nil {
		t.Fatal(err)
	}
	if !fs.IsDir(filepath.Join(dest, "logs")) {
		t.Error("extraction must create the logs/ directory entry")
	}
	if got, err := fs.ReadFile(filepath.Join(dest, "bin", "httpd.exe")); err != nil || string(got) != "2.4.71" {
		t.Errorf("httpd.exe = %q, %v", got, err)
	}
}

func TestApacheManagerImportBundleRefusals(t *testing.T) {
	rig := newManagerRig(t)
	seedUpgradeRig(t, rig)
	bundlePath := joinPath(`C:\data`, "bundle.zip")

	write := func(b []byte) {
		if err := rig.fs.WriteFile(bundlePath, b); err != nil {
			t.Fatal(err)
		}
	}
	// Same version: not an upgrade.
	write(makeUpgradeBundle(t, "2.4.69", false))
	if _, err := rig.mgr.ImportBundle(context.Background(), bundlePath); err == nil || !strings.Contains(err.Error(), "tidak lebih baru") {
		t.Errorf("same version must be refused, got %v", err)
	}
	// Older version: downgrade refused.
	write(makeUpgradeBundle(t, "2.4.68", false))
	if _, err := rig.mgr.ImportBundle(context.Background(), bundlePath); err == nil || !strings.Contains(err.Error(), "tidak lebih baru") {
		t.Errorf("older version must be refused, got %v", err)
	}
	// Tampered inner zip: hash mismatch fails before anything moves.
	write(makeUpgradeBundle(t, "2.4.71", true))
	if _, err := rig.mgr.ImportBundle(context.Background(), bundlePath); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Errorf("tampered bundle must fail on hash, got %v", err)
	}
	// Nothing moved: home still runs 2.4.69 and the service was
	// never stopped by any of the refusals.
	if got, _ := rig.fs.ReadFile(filepath.Join(`C:\apache`, "bin", "httpd.exe")); string(got) != "2.4.69" {
		t.Errorf("home changed by a refused import: %q", got)
	}
	if rig.svc.stops != 0 {
		t.Errorf("refusals must not stop the service, stops=%d", rig.svc.stops)
	}
}

func TestApacheManagerImportBundleRollback(t *testing.T) {
	rig := newManagerRig(t)
	seedUpgradeRig(t, rig)
	// The bundle's manifest says 2.4.71 but its httpd.exe reports
	// 2.4.70: the post-restart verification must catch the mismatch
	// and roll back to 2.4.69.
	bundle := makeUpgradeBundle(t, "2.4.71", false)
	// Rebuild the inner apache zip with the wrong version content
	// while keeping the manifest (and its hash) for 2.4.71: emulate
	// by rewriting the httpd.exe content inside the bundle's
	// apache.zip and fixing the manifest hash to match the new zip.
	var buf bytes.Buffer
	zr, err := zip.NewReader(bytes.NewReader(bundle), int64(len(bundle)))
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string][]byte{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		var b bytes.Buffer
		_, _ = b.ReadFrom(rc)
		_ = rc.Close()
		entries[f.Name] = b.Bytes()
	}
	var inner bytes.Buffer
	zw := zip.NewWriter(&inner)
	w, _ := zw.Create("Apache24/bin/httpd.exe")
	_, _ = w.Write([]byte("2.4.70"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	entries["apache.zip"] = inner.Bytes()
	var manifest ApacheBundleManifest
	if err := json.Unmarshal(entries["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(entries["apache.zip"])
	manifest.ApacheSHA256 = hex.EncodeToString(sum[:])
	manifestJSON, _ := json.Marshal(manifest)
	entries["manifest.json"] = manifestJSON
	zw2 := zip.NewWriter(&buf)
	for _, name := range []string{"manifest.json", "apache.zip", "mod_fcgid.zip"} {
		fw, _ := zw2.Create(name)
		_, _ = fw.Write(entries[name])
	}
	if err := zw2.Close(); err != nil {
		t.Fatal(err)
	}
	bundlePath := joinPath(`C:\data`, "bundle.zip")
	if err := rig.fs.WriteFile(bundlePath, buf.Bytes()); err != nil {
		t.Fatal(err)
	}
	_, err = rig.mgr.ImportBundle(context.Background(), bundlePath)
	if err == nil || !strings.Contains(err.Error(), "2.4.70") {
		t.Fatalf("version mismatch must fail naming 2.4.70, got %v", err)
	}
	// Rolled back: home is 2.4.69 again, service running, no
	// staging and no backup left behind.
	home := `C:\apache`
	if got, _ := rig.fs.ReadFile(filepath.Join(home, "bin", "httpd.exe")); string(got) != "2.4.69" {
		t.Errorf("home after rollback = %q, want 2.4.69", got)
	}
	if rig.fs.Exists(home+"-2.4.71") || rig.fs.Exists(home+"-backup-2.4.69") {
		t.Error("rollback must leave no staging and no backup folder behind")
	}
	if st, _ := rig.svc.Status(""); st != "running" {
		t.Errorf("service after rollback = %q, want running", st)
	}
}

func TestCompareVersions(t *testing.T) {
	cases := [][3]any{{"2.4.69", "2.4.71", -1}, {"2.4.71", "2.4.69", 1}, {"2.4.71", "2.4.71", 0}, {"2.4.9", "2.4.10", -1}}
	for _, c := range cases {
		if got := compareVersions(c[0].(string), c[1].(string)); got != c[2].(int) {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c[0], c[1], got, c[2])
		}
	}
}
