// Package config loads the panel's own settings (panel.json).
// The project/PHP registry is a separate concern and arrives in M2;
// this file only covers panel-level settings from Plan Bagian 4.2/4.3.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// FileName is the optional config file, looked up next to the binary.
const FileName = "panel.json"

// DefaultPort is the panel's own HTTP port (Plan Bagian 3 & 11: 8888,
// served by the panel service itself, never by the managed Apache).
const DefaultPort = 8888

// DefaultApacheService is the Windows service name of a stock
// Apache 2.4 installation (XAMPP and Apache Lounge both use it).
const DefaultApacheService = "Apache2.4"

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
