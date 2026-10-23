package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults(`C:\panel`)
	if cfg.Port != DefaultPort {
		t.Errorf("Port = %d, want %d", cfg.Port, DefaultPort)
	}
	if cfg.ApacheService != DefaultApacheService {
		t.Errorf("ApacheService = %q, want %q", cfg.ApacheService, DefaultApacheService)
	}
	if cfg.DataDir == "" || cfg.LogDir == "" {
		t.Errorf("DataDir/LogDir must be derived from baseDir, got %+v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("defaults must validate: %v", err)
	}
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load without panel.json: %v", err)
	}
	if cfg.Port != DefaultPort {
		t.Errorf("Port = %d, want default %d", cfg.Port, DefaultPort)
	}
}

func TestLoadPartialFileKeepsDefaultsForRest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"port": 9999}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 9999 {
		t.Errorf("Port = %d, want 9999 from file", cfg.Port)
	}
	if cfg.ApacheService != DefaultApacheService {
		t.Errorf("ApacheService = %q, want default filled in", cfg.ApacheService)
	}
	if cfg.DataDir == "" {
		t.Error("DataDir must be filled from defaults")
	}
}

func TestLoadInvalidJSONIsLoudError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("Load with invalid JSON must fail loudly, not fall back to defaults")
	}
}

func TestLoadUnreadableConfigIsLoudError(t *testing.T) {
	dir := t.TempDir()
	// A directory named panel.json makes ReadFile fail with a
	// non-NotExist error, which must surface instead of defaulting.
	if err := os.Mkdir(filepath.Join(dir, FileName), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("Load with unreadable panel.json must fail loudly")
	}
}

func TestValidateRejectsEmptyFields(t *testing.T) {
	for _, mutate := range []func(*Config){
		func(c *Config) { c.DataDir = "" },
		func(c *Config) { c.LogDir = "" },
		func(c *Config) { c.ApacheService = "" },
	} {
		cfg := Defaults(`C:\panel`)
		mutate(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Errorf("Validate must reject %+v", cfg)
		}
	}
}

func TestValidateRejectsBadPort(t *testing.T) {
	cfg := Defaults(`C:\panel`)
	cfg.Port = 70000
	if err := cfg.Validate(); err == nil {
		t.Fatal("port 70000 must be rejected")
	}
	cfg.Port = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("port 0 must be rejected")
	}
}
