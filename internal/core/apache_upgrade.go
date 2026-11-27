package core

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Apache offline upgrade (Opsi A, sisi server yang offline).
//
// The operator copies a bundle file (built by `panel apache fetch`
// on an internet-connected dev PC) to the server and imports it.
// The import, in order:
//
//  1. verify the bundle: manifest format + SHA-256 of both inner
//     zips + the inner Apache zip really contains bin/httpd.exe.
//  2. refuse anything that is not strictly newer than the running
//     Apache (upgrade only goes forward; rollback uses the backup
//     folder, never an older bundle).
//  3. stage the new version in a sibling folder (<home>-<version>):
//     extract the Apache zip, place mod_fcgid.so, carry over the
//     operator's httpd.conf, conf/extra and htdocs from the running
//     install. The rest of the new zip (mime.types, bin/, ...) stays
//     pristine — that is the point of the upgrade.
//  4. stop the service, rename the running folder to a backup
//     (<home>-backup-<old-version>, -2/-3 if taken), rename the
//     staged folder into its place, run httpd -t, start the service
//     and verify httpd -v reports the new version.
//  5. on ANY failure after the swap, roll back: remove the failed
//     folder, rename the backup home, restart if it was running,
//     and return an error that names the backup path.
//
// Deliberate non-goals, stated so nobody "fixes" them later:
//   - apache_home is NEVER rewritten: panel.exe never writes
//     panel.json (that file belongs to the setup script), and the
//     swap keeps the same path, so the running panel needs no
//     restart and no config change. The versioned folder exists
//     only while staging; after the switch the new content lives
//     at the old path and the old content at the backup path.
//   - custom modules other than mod_fcgid are NOT carried over:
//     a .so built for the old Apache may not load under the new
//     one. They are reported as warnings instead.
//   - the service is left stopped when it was stopped before the
//     import (maintenance window), and started again when it was
//     running.

var versionRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// ApacheUpgradeResult is what the dashboard and the CLI print
// after a successful import.
type ApacheUpgradeResult struct {
	OldVersion string   `json:"old_version"`
	NewVersion string   `json:"new_version"`
	BackupPath string   `json:"backup_path"`
	Warnings   []string `json:"warnings"`
}

// compareVersions compares dotted versions ("2.4.69" < "2.4.71").
// Both inputs must match versionRe; anything else is treated as 0.
func compareVersions(a, b string) int {
	parse := func(v string) []int {
		var out []int
		for _, part := range strings.Split(v, ".") {
			n, err := strconv.Atoi(part)
			if err != nil {
				n = 0
			}
			out = append(out, n)
		}
		return out
	}
	pa, pb := parse(a), parse(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func zipEntryByName(zr *zip.Reader, name string) *zip.File {
	for _, f := range zr.File {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// zipEntryBySuffix finds a file anywhere inside a zip by its
// trailing path, e.g. "bin/httpd.exe" matches
// "Apache24/bin/httpd.exe". Zip layouts change between releases;
// the suffix is the stable part.
func zipEntryBySuffix(zr *zip.Reader, suffix string) *zip.File {
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if f.Name == suffix || strings.HasSuffix(f.Name, "/"+suffix) {
			return f
		}
	}
	return nil
}

func readZipEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, 256<<20))
}

// extractZipToStore extracts every entry under prefix from zr into
// dest (prefix stripped), through the FileStore so the whole import
// is testable in memory. Directory entries are created, not
// skipped: the Apache zip ships logs/ as an EMPTY directory entry,
// and httpd -t fails (AH02297) when that directory does not exist
// — proven 2026-11-27 (issue #5). Zip-slip entries ("../",
// absolute paths) are rejected.
func extractZipToStore(files FileStore, zr *zip.Reader, prefix, dest string) error {
	for _, f := range zr.File {
		if prefix != "" && !strings.HasPrefix(f.Name, prefix) {
			continue
		}
		rel := strings.TrimPrefix(f.Name, prefix)
		// The prefix's own directory entry ("Apache24/" when the
		// prefix is "Apache24/") strips to an empty relative path:
		// it is the root of the tree being extracted, not an
		// unsafe entry. Skipping it here (before the safety check)
		// is what the old code did implicitly by testing IsDir
		// first — proven 2026-11-27 (issue #5): without this, the
		// real Apache zip is rejected as "entri zip tidak aman".
		if rel == "" {
			continue
		}
		clean := filepath.ToSlash(filepath.Clean(rel))
		if clean == "." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
			return fmt.Errorf("entri zip tidak aman: %q", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := files.MkdirAll(joinPath(dest, filepath.FromSlash(clean))); err != nil {
				return fmt.Errorf("buat direktori %s: %w", clean, err)
			}
			continue
		}
		data, err := readZipEntry(f)
		if err != nil {
			return fmt.Errorf("baca %s dari zip: %w", f.Name, err)
		}
		if err := files.WriteFile(joinPath(dest, filepath.FromSlash(clean)), data); err != nil {
			return fmt.Errorf("tulis %s: %w", clean, err)
		}
	}
	return nil
}

// copyTree copies a directory tree through the FileStore
// (recursively). Missing src is not an error — the caller decides
// whether that deserves a warning.
func copyTree(files FileStore, src, dst string) error {
	names, err := files.ListNames(src)
	if err != nil {
		return err
	}
	for _, name := range names {
		s, d := joinPath(src, name), joinPath(dst, name)
		if files.IsDir(s) {
			if err := files.MkdirAll(d); err != nil {
				return err
			}
			if err := copyTree(files, s, d); err != nil {
				return err
			}
			continue
		}
		data, err := files.ReadFile(s)
		if err != nil {
			return err
		}
		if err := files.WriteFile(d, data); err != nil {
			return err
		}
	}
	return nil
}

func copyFileIfExists(files FileStore, src, dst string) (bool, error) {
	data, err := files.ReadFile(src)
	if err != nil {
		return false, nil
	}
	if err := files.WriteFile(dst, data); err != nil {
		return false, err
	}
	return true, nil
}

// moveDir renames a directory tree; when the rename fails (Windows:
// different drive letters) it falls back to copy+remove.
func moveDir(files FileStore, oldPath, newPath string) error {
	if err := files.Rename(oldPath, newPath); err == nil {
		return nil
	}
	if err := copyTree(files, oldPath, newPath); err != nil {
		return fmt.Errorf("salin %s -> %s: %w", oldPath, newPath, err)
	}
	if err := files.RemoveAll(oldPath); err != nil {
		return fmt.Errorf("hapus %s sesudah salin: %w", oldPath, err)
	}
	return nil
}

// ImportBundle upgrades the managed Apache from a bundle file
// (see the package comment). bundlePath is read through the
// manager's FileStore.
func (m *ApacheManager) ImportBundle(ctx context.Context, bundlePath string) (ApacheUpgradeResult, error) {
	raw, err := m.Files.ReadFile(bundlePath)
	if err != nil {
		return ApacheUpgradeResult{}, fmt.Errorf("bundle tidak bisa dibaca (%s): %w", bundlePath, err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return ApacheUpgradeResult{}, fmt.Errorf("bundle bukan file zip yang valid: %w", err)
	}
	manifestEntry := zipEntryByName(zr, "manifest.json")
	if manifestEntry == nil {
		return ApacheUpgradeResult{}, fmt.Errorf("bundle tidak berisi manifest.json — bukan bundle hasil 'panel apache fetch'")
	}
	manifestRaw, err := readZipEntry(manifestEntry)
	if err != nil {
		return ApacheUpgradeResult{}, fmt.Errorf("baca manifest.json: %w", err)
	}
	var manifest ApacheBundleManifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		return ApacheUpgradeResult{}, fmt.Errorf("manifest.json rusak: %w", err)
	}
	if manifest.FormatVersion != ApacheBundleFormat {
		return ApacheUpgradeResult{}, fmt.Errorf("format bundle %d tidak didukung (panel ini paham format %d) — buat ulang bundle dengan panel terbaru", manifest.FormatVersion, ApacheBundleFormat)
	}
	if !versionRe.MatchString(manifest.ApacheVersion) {
		return ApacheUpgradeResult{}, fmt.Errorf("versi Apache di manifest tidak valid: %q", manifest.ApacheVersion)
	}
	apacheEntry := zipEntryByName(zr, "apache.zip")
	fcgidEntry := zipEntryByName(zr, "mod_fcgid.zip")
	if apacheEntry == nil || fcgidEntry == nil {
		return ApacheUpgradeResult{}, fmt.Errorf("bundle tidak lengkap: apache.zip dan mod_fcgid.zip harus ada")
	}
	apacheZip, err := readZipEntry(apacheEntry)
	if err != nil {
		return ApacheUpgradeResult{}, fmt.Errorf("baca apache.zip: %w", err)
	}
	if sha256Hex(apacheZip) != manifest.ApacheSHA256 {
		return ApacheUpgradeResult{}, fmt.Errorf("apache.zip rusak (SHA-256 tidak cocok dengan manifest) — salin ulang bundle dari PC dev")
	}
	fcgidZip, err := readZipEntry(fcgidEntry)
	if err != nil {
		return ApacheUpgradeResult{}, fmt.Errorf("baca mod_fcgid.zip: %w", err)
	}
	if sha256Hex(fcgidZip) != manifest.FcgidSHA256 {
		return ApacheUpgradeResult{}, fmt.Errorf("mod_fcgid.zip rusak (SHA-256 tidak cocok dengan manifest) — salin ulang bundle dari PC dev")
	}
	azr, err := zip.NewReader(bytes.NewReader(apacheZip), int64(len(apacheZip)))
	if err != nil {
		return ApacheUpgradeResult{}, fmt.Errorf("apache.zip bukan zip yang valid: %w", err)
	}
	httpdEntry := zipEntryBySuffix(azr, "bin/httpd.exe")
	if httpdEntry == nil {
		return ApacheUpgradeResult{}, fmt.Errorf("apache.zip tidak berisi bin/httpd.exe — struktur zip berubah, laporkan ke Muse")
	}
	prefix := ""
	if idx := strings.Index(httpdEntry.Name, "bin/httpd.exe"); idx > 0 {
		prefix = httpdEntry.Name[:idx]
	}
	fzr, err := zip.NewReader(bytes.NewReader(fcgidZip), int64(len(fcgidZip)))
	if err != nil {
		return ApacheUpgradeResult{}, fmt.Errorf("mod_fcgid.zip bukan zip yang valid: %w", err)
	}
	soEntry := zipEntryBySuffix(fzr, "mod_fcgid.so")
	if soEntry == nil {
		return ApacheUpgradeResult{}, fmt.Errorf("mod_fcgid.zip tidak berisi mod_fcgid.so — struktur zip berubah, laporkan ke Muse")
	}
	soBytes, err := readZipEntry(soEntry)
	if err != nil {
		return ApacheUpgradeResult{}, fmt.Errorf("baca mod_fcgid.so: %w", err)
	}

	cur, err := m.ApacheInfo(ctx)
	if err != nil {
		return ApacheUpgradeResult{}, fmt.Errorf("tidak bisa membaca versi Apache yang sedang berjalan: %w", err)
	}
	if compareVersions(manifest.ApacheVersion, cur.Version) <= 0 {
		return ApacheUpgradeResult{}, fmt.Errorf("bundle Apache %s tidak lebih baru dari yang berjalan (%s) — upgrade hanya maju; untuk kembali ke versi lama pakai folder backup %s", manifest.ApacheVersion, cur.Version, cur.Home+"-backup-"+cur.Version)
	}
	home := cur.Home
	staging := home + "-" + manifest.ApacheVersion
	if m.Files.Exists(staging) {
		// Sisa run sebelumnya yang gagal/crash: singkirkan supaya
		// staging bersih; nama staging cukup khas sehingga tidak
		// mungkin menabrak folder operator.
		m.logInfo("apache upgrade: menghapus staging sisa", "path", staging)
		if err := m.Files.RemoveAll(staging); err != nil {
			return ApacheUpgradeResult{}, fmt.Errorf("hapus staging sisa %s: %w", staging, err)
		}
	}
	if err := extractZipToStore(m.Files, azr, prefix, staging); err != nil {
		_ = m.Files.RemoveAll(staging)
		return ApacheUpgradeResult{}, fmt.Errorf("ekstrak Apache %s gagal: %w", manifest.ApacheVersion, err)
	}
	// httpd.conf references log files relatively (logs/access_log,
	// logs/error.log), and Apache requires the directory to exist
	// for httpd -t — even when the zip ships logs/ as an empty
	// directory entry (see extractZipToStore), make sure it is
	// there. The directory starts EMPTY on purpose: old log files
	// stay in the backup folder, they are not carried over.
	if err := m.Files.MkdirAll(joinPath(staging, "logs")); err != nil {
		_ = m.Files.RemoveAll(staging)
		return ApacheUpgradeResult{}, fmt.Errorf("buat direktori logs di staging: %w", err)
	}
	if err := m.Files.WriteFile(joinPath(staging, filepath.Join("modules", "mod_fcgid.so")), soBytes); err != nil {
		_ = m.Files.RemoveAll(staging)
		return ApacheUpgradeResult{}, fmt.Errorf("pasang mod_fcgid.so: %w", err)
	}
	var warnings []string
	if ok, err := copyFileIfExists(m.Files, joinPath(home, filepath.Join("conf", "httpd.conf")), joinPath(staging, filepath.Join("conf", "httpd.conf"))); err != nil {
		_ = m.Files.RemoveAll(staging)
		return ApacheUpgradeResult{}, fmt.Errorf("salin httpd.conf lama: %w", err)
	} else if !ok {
		warnings = append(warnings, "httpd.conf lama tidak ditemukan — dipakai httpd.conf bawaan Apache baru; periksa LoadModule fcgid dan IncludeOptional sebelum restart berikutnya")
	}
	if m.Files.IsDir(joinPath(home, filepath.Join("conf", "extra"))) {
		if err := copyTree(m.Files, joinPath(home, filepath.Join("conf", "extra")), joinPath(staging, filepath.Join("conf", "extra"))); err != nil {
			_ = m.Files.RemoveAll(staging)
			return ApacheUpgradeResult{}, fmt.Errorf("salin conf/extra lama: %w", err)
		}
	}
	if m.Files.IsDir(joinPath(home, "htdocs")) {
		if err := copyTree(m.Files, joinPath(home, "htdocs"), joinPath(staging, "htdocs")); err != nil {
			_ = m.Files.RemoveAll(staging)
			return ApacheUpgradeResult{}, fmt.Errorf("salin htdocs lama: %w", err)
		}
	}
	// Modul .so lama selain mod_fcgid TIDAK ikut: binary lama bisa
	// saja tidak mau di-load Apache baru. Laporkan sebagai warning,
	// bukan error — operator yang memutuskan.
	oldModules, _ := m.Files.ListNames(joinPath(home, "modules"))
	newModules, _ := m.Files.ListNames(joinPath(staging, "modules"))
	newSet := map[string]bool{}
	for _, n := range newModules {
		newSet[n] = true
	}
	for _, n := range oldModules {
		if strings.HasSuffix(n, ".so") && n != "mod_fcgid.so" && !newSet[n] {
			warnings = append(warnings, fmt.Sprintf("modul lama %s tidak ikut ke Apache baru — salin manual dari backup bila masih dibutuhkan", n))
		}
	}
	if warnings == nil {
		warnings = []string{}
	}

	status, err := m.Service.Status(m.Cfg.ApacheService)
	if err != nil {
		_ = m.Files.RemoveAll(staging)
		return ApacheUpgradeResult{}, fmt.Errorf("tidak bisa membaca status service %s: %w", m.Cfg.ApacheService, err)
	}
	wasRunning := status == "running"
	if wasRunning {
		if err := m.Service.Stop(m.Cfg.ApacheService); err != nil {
			_ = m.Files.RemoveAll(staging)
			return ApacheUpgradeResult{}, fmt.Errorf("stop service %s gagal, tidak ada yang diubah: %w", m.Cfg.ApacheService, err)
		}
	}

	backupPath := home + "-backup-" + cur.Version
	for i := 2; m.Files.Exists(backupPath); i++ {
		backupPath = fmt.Sprintf("%s-backup-%s-%d", home, cur.Version, i)
	}
	rollback := func(failErr error) (ApacheUpgradeResult, error) {
		// Buang folder gagal, kembalikan backup, nyalakan lagi bila
		// sebelumnya jalan. Kalau rollback sendiri gagal, itu
		// darurat: sebutkan backup path-nya supaya operator bisa
		// memulihkan manual.
		_ = m.Files.RemoveAll(home)
		if err := moveDir(m.Files, backupPath, home); err != nil {
			m.logError("apache upgrade rollback gagal", "backup", backupPath, "err", err)
			return ApacheUpgradeResult{}, fmt.Errorf("UPGRADE GAGAL (%v) DAN ROLLBACK GAGAL (%v) — Apache dalam keadaan tidak lengkap; pulihkan manual dari backup %s", failErr, err, backupPath)
		}
		if wasRunning {
			if err := m.Service.Start(m.Cfg.ApacheService); err != nil {
				return ApacheUpgradeResult{}, fmt.Errorf("upgrade gagal (%v); versi lama sudah dikembalikan tapi service %s tidak bisa di-start: %w", failErr, m.Cfg.ApacheService, err)
			}
		}
		m.logError("apache upgrade di-rollback", "err", failErr)
		return ApacheUpgradeResult{}, failErr
	}

	if err := moveDir(m.Files, home, backupPath); err != nil {
		if wasRunning {
			_ = m.Service.Start(m.Cfg.ApacheService)
		}
		_ = m.Files.RemoveAll(staging)
		return ApacheUpgradeResult{}, fmt.Errorf("pindahkan folder lama ke backup %s gagal, tidak ada yang diubah: %w", backupPath, err)
	}
	if err := moveDir(m.Files, staging, home); err != nil {
		return rollback(fmt.Errorf("pindahkan folder baru ke %s gagal: %w", home, err))
	}
	if err := m.TestConfig(ctx); err != nil {
		return rollback(fmt.Errorf("httpd -t pada Apache %s gagal: %w", manifest.ApacheVersion, err))
	}
	if wasRunning {
		if err := m.Service.Start(m.Cfg.ApacheService); err != nil {
			return rollback(fmt.Errorf("start service %s gagal: %w", m.Cfg.ApacheService, err))
		}
	}
	ver, err := m.ApacheInfo(ctx)
	if err != nil {
		return rollback(fmt.Errorf("verifikasi versi sesudah upgrade gagal: %w", err))
	}
	if ver.Version != manifest.ApacheVersion {
		return rollback(fmt.Errorf("sesudah restart httpd -v melaporkan Apache %s, bukan %s", ver.Version, manifest.ApacheVersion))
	}
	m.logInfo("apache upgraded", "from", cur.Version, "to", manifest.ApacheVersion, "backup", backupPath)
	return ApacheUpgradeResult{
		OldVersion: cur.Version,
		NewVersion: manifest.ApacheVersion,
		BackupPath: backupPath,
		Warnings:   warnings,
	}, nil
}
