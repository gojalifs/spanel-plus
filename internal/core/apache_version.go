package core

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var apacheVersionRe = regexp.MustCompile(`Apache/(\d+\.\d+\.\d+)`)

// ParseApacheVersionOutput parses `httpd -v` output ("Server version:
// Apache/2.4.69 (Win64)...") into the patch version ("2.4.69").
// Shared by the doctor's httpd-version check and the About page so
// both report the same number from the same output.
func ParseApacheVersionOutput(out string) (string, error) {
	m := apacheVersionRe.FindStringSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("could not parse Apache version from httpd -v output: %q", strings.TrimSpace(out))
	}
	return m[1], nil
}

// ApacheInfo is what the About page shows for the main HTTP server:
// the installed Apache patch version plus the facts an upgrade
// decision needs (home folder, mod_fcgid and rotatelogs presence).
type ApacheInfo struct {
	Version    string `json:"version"`    // e.g. "2.4.69", "" when unknown
	Home       string `json:"home"`       // resolved ApacheHome
	ModFcgid   bool   `json:"mod_fcgid"`  // modules/mod_fcgid.so exists
	RotateLogs bool   `json:"rotatelogs"` // bin/rotatelogs.exe exists
}

// ApacheInfo probes the managed Apache installation: it runs
// <ApacheHome>/bin/httpd.exe -v and reports the parsed version
// together with mod_fcgid / rotatelogs presence. The mod_fcgid
// VERSION is deliberately not reported: no version string is
// readable from the .so binary (httpd -M lists the module name
// only), so presence is the honest fact — anything else would be
// a guess from the file name or the setup script's pin.
func (m *ApacheManager) ApacheInfo(ctx context.Context) (ApacheInfo, error) {
	home := m.Cfg.ApacheHome
	if home == "" {
		return ApacheInfo{}, fmt.Errorf("apache_home belum ter-resolve — set di panel.json atau pastikan Apache terinstal di lokasi standar")
	}
	httpd := filepath.Join(home, "bin", "httpd.exe")
	if !m.Files.Exists(httpd) {
		return ApacheInfo{}, fmt.Errorf("%s tidak ditemukan — %s bukan instalasi Apache", httpd, home)
	}
	out, err := m.Runner.Run(ctx, httpd, "-v")
	if err != nil {
		return ApacheInfo{}, fmt.Errorf("httpd -v gagal: %v (output: %s)", err, strings.TrimSpace(out))
	}
	ver, err := ParseApacheVersionOutput(out)
	if err != nil {
		return ApacheInfo{}, err
	}
	return ApacheInfo{
		Version:    ver,
		Home:       home,
		ModFcgid:   m.Files.Exists(filepath.Join(home, "modules", "mod_fcgid.so")),
		RotateLogs: HasRotateLogs(m.Files, home),
	}, nil
}
