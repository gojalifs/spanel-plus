// Package config loads the panel's own settings (panel.json).
// The project/PHP registry is a separate concern and arrives in M2;
// this file only covers panel-level settings from Plan Bagian 4.2/4.3.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gojalifs/spanel-plus/internal/core"
)

// FileName is the optional config file, looked up next to the binary.
const FileName = "panel.json"

// DefaultPort is the panel's own HTTP port (Plan Bagian 3 & 11: 8888,
// served by the panel service itself, never by the managed Apache).
const DefaultPort = 8888

// DefaultApacheService is the Windows service name of the Apache
// install this panel manages. It intentionally differs from the stock
// "Apache2.4" name (XAMPP and Apache Lounge both use that) so a
// panel-managed Apache never collides with another stock install on
// the same machine.
const DefaultApacheService = "apache-spanel"

// Config is the panel-level configuration.
type Config struct {
	// Port the panel UI/API listens on.
	Port int `json:"port"`
	// DataDir holds registry.json, admin.json, generated files, backups.
	DataDir string `json:"data_dir"`
	// LogDir holds the panel's own log files.
	LogDir string `json:"log_dir"`
	// ApacheHome is the managed Apache installation root
	// (the folder containing bin/httpd.exe). Empty means: let doctor
	// auto-discover from DefaultApacheCandidates.
	ApacheHome string `json:"apache_home"`
	// ApacheService is the Windows service name of the managed Apache.
	ApacheService string `json:"apache_service"`
	// DocumentRoot is the managed Apache's document root (the folder
	// project URL paths naturally map onto). Used by ConfigGen to
	// decide whether a project needs an explicit Alias. May be empty
	// (then every project gets an Alias — always correct, just less
	// minimal).
	DocumentRoot string `json:"document_root"`
	// ADEnabled turns on directory login (M5-AD). The whole AD
	// configuration lives here, in this one JSON file — no service
	// account, no separate file, no Group Policy.
	ADEnabled bool `json:"ad_enabled"`
	// ADServer is the directory endpoint: ldaps://<dc-fqdn>:636,
	// ldap://<dc-fqdn> (StartTLS is then required), or "dummy" for
	// the development directory (never production).
	ADServer string `json:"ad_server"`
	// ADDomain is the UPN suffix, e.g. siws.local.
	ADDomain string `json:"ad_domain"`
	// ADBaseDN is the search base, e.g. DC=siws,DC=local.
	ADBaseDN string `json:"ad_base_dn"`
	// ADGroupDN is the IT group DN, e.g. CN=IS,DC=siws,DC=local.
	// Until the real DN is confirmed, a placeholder here fails
	// closed (nobody passes the group check) rather than open.
	ADGroupDN string `json:"ad_group_dn"`
	// ADCAFile is an optional PEM file for a DC certificate issued
	// by an internal CA; empty means system roots.
	ADCAFile string `json:"ad_ca_file"`
	// ADAllowInsecureLDAP permits plain LDAP without TLS. Test-only:
	// the panel logs a loud warning on every start while it is set.
	ADAllowInsecureLDAP bool `json:"ad_allow_insecure_ldap"`
	// ADDummyUsers is the development directory (only read when
	// ADServer is "dummy"): username + bcrypt password_hash +
	// group CNs. Generate hashes with: panel admin hash-password.
	ADDummyUsers []core.DummyUser `json:"ad_dummy_users,omitempty"`
}

// DefaultApacheCandidates are probed, in order, when ApacheHome is empty.
// C:/m0/Apache24 is the M0 spike install; on a pilot server the XAMPP
// path is the expected hit.
func DefaultApacheCandidates() []string {
	return []string{
		`C:\xampp\apache`,
		`C:\Apache24`,
		`C:\m0\Apache24`,
	}
}

// PHPScanRoot derives the PHP auto-register scan folder from an Apache
// installation root under the M0 layout: <root>/Apache24 lives next
// to <root>/php, so the scan folder is <root>/php. Empty when
// apacheHome is empty or has no parent (no scan then). The split
// accepts either separator, so panel.json paths written either way
// work; the result always uses forward slashes, matching the
// panel.json convention (C:/m0/php).
func PHPScanRoot(apacheHome string) string {
	home := strings.ReplaceAll(strings.TrimRight(apacheHome, `/\`), `\`, "/")
	i := strings.LastIndex(home, "/")
	if i < 0 {
		return ""
	}
	return home[:i] + "/php"
}

// Defaults returns the config used when no panel.json exists.
// baseDir is the folder the binary lives in: every panel file lives
// under it (Plan D9 — nothing is written into project folders).
func Defaults(baseDir string) Config {
	return Config{
		Port:          DefaultPort,
		DataDir:       filepath.Join(baseDir, "data"),
		LogDir:        filepath.Join(baseDir, "logs"),
		ApacheHome:    "",
		ApacheService: DefaultApacheService,
	}
}

// Load reads baseDir/panel.json over Defaults(baseDir). A missing file
// is fine (defaults apply); a present-but-invalid file is a loud error —
// silently falling back to defaults would hide a broken install.
func Load(baseDir string) (Config, error) {
	cfg := Defaults(baseDir)
	path := filepath.Join(baseDir, FileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, cfg.Validate()
		}
		return Config{}, fmt.Errorf("config: read %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("config: parse %s: %w", path, err)
	}
	// Re-apply defaults for fields the file left at zero values, so a
	// partial panel.json (e.g. only a custom port) still works.
	def := Defaults(baseDir)
	if cfg.DataDir == "" {
		cfg.DataDir = def.DataDir
	}
	if cfg.LogDir == "" {
		cfg.LogDir = def.LogDir
	}
	if cfg.ApacheService == "" {
		cfg.ApacheService = def.ApacheService
	}
	return cfg, cfg.Validate()
}

// Validate rejects settings the panel cannot run with.
func (c Config) Validate() error {
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("config: port %d out of range 1-65535", c.Port)
	}
	if c.DataDir == "" {
		return fmt.Errorf("config: data_dir must not be empty")
	}
	if c.LogDir == "" {
		return fmt.Errorf("config: log_dir must not be empty")
	}
	if c.ApacheService == "" {
		return fmt.Errorf("config: apache_service must not be empty")
	}
	return nil
}
