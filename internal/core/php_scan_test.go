package core

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// dirProber probes per directory: the key is the normalized folder
// path (forward slashes), the value the probe result or error.
type dirProber struct {
	byDir map[string]PHPProbeResult
	errs  map[string]error
}

func (p dirProber) Probe(_ context.Context, dir string) (PHPProbeResult, error) {
	key := strings.ReplaceAll(dir, `\`, "/")
	if err, ok := p.errs[key]; ok {
		return PHPProbeResult{}, err
	}
	if res, ok := p.byDir[key]; ok {
		return res, nil
	}
	return PHPProbeResult{}, errors.New("no php here: " + dir)
}

func newScanRig(t *testing.T) (*PHPManager, *memFS) {
	t.Helper()
	fs := newMemFS()
	store := &memStore{loadErr: ErrRegistryNotFound}
	regSvc := &RegistryService{Store: store, Files: fs, Clock: &tickClock{}}
	mgr := &PHPManager{
		Registry:    regSvc,
		Prober:      dirProber{byDir: map[string]PHPProbeResult{}, errs: map[string]error{}},
		Files:       fs,
		RuntimeBase: `C:\Panel\data\php-runtime`,
		ScanRoot:    `C:\php`,
	}
	return mgr, fs
}

func TestScanRegistersNewBuildsSkipsRest(t *testing.T) {
	mgr, fs := newScanRig(t)
	ctx := context.Background()
	// Layout: 8.2 + 8.3 good, 8.4 TS, broken probe, a plain file.
	for _, d := range []string{`C:\php\8.2`, `C:\php\8.3`, `C:\php\8.4`, `C:\php\broken`} {
		_ = fs.MkdirAll(d)
		_ = fs.WriteFile(joinPath(d, "php.exe"), nil)
		_ = fs.WriteFile(joinPath(d, "php-cgi.exe"), nil)
	}
	_ = fs.WriteFile(`C:\php\notes.txt`, []byte("not a build"))
	mgr.Prober = dirProber{
		byDir: map[string]PHPProbeResult{
			`C:/php/8.2`: {Version: "8.2", Full: "8.2.29", NTS: true, Arch: "x64"},
			`C:/php/8.3`: {Version: "8.3", Full: "8.3.13", NTS: true, Arch: "x64"},
			`C:/php/8.4`: {Version: "8.4", Full: "8.4.27", NTS: false, Arch: "x64"},
		},
		errs: map[string]error{`C:/php/broken`: errors.New("php.exe gagal jalan")},
	}
	res, err := mgr.Scan(ctx)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Registered) != 2 {
		t.Fatalf("Registered = %v, want 2 entries", res.Registered)
	}
	got := map[string]bool{res.Registered[0]: true, res.Registered[1]: true}
	if !got["8.2"] || !got["8.3"] {
		t.Errorf("Registered = %v, want {8.2 8.3} in any order", res.Registered)
	}
	if len(res.Skipped) != 2 {
		t.Fatalf("Skipped = %+v, want 2 entries (TS + broken)", res.Skipped)
	}
	// Idempotent: a second scan registers nothing and skips nothing
	// new (already-registered folders are silently left alone).
	res2, err := mgr.Scan(ctx)
	if err != nil {
		t.Fatalf("second Scan: %v", err)
	}
	if len(res2.Registered) != 0 || len(res2.Skipped) != 2 {
		t.Errorf("second scan = %+v, want no registrations and the same 2 skips", res2)
	}
}

func TestScanDuplicateVersionFromAnotherFolder(t *testing.T) {
	mgr, _ := newScanRig(t)
	ctx := context.Background()
	_ = mgr.Files.MkdirAll(`C:\php\8.3`)
	_ = mgr.Files.MkdirAll(`C:\php\8.3-alt`)
	for _, d := range []string{`C:\php\8.3`, `C:\php\8.3-alt`} {
		_ = mgr.Files.WriteFile(joinPath(d, "php.exe"), nil)
		_ = mgr.Files.WriteFile(joinPath(d, "php-cgi.exe"), nil)
	}
	mgr.Prober = dirProber{byDir: map[string]PHPProbeResult{
		`C:/php/8.3`:     {Version: "8.3", Full: "8.3.13", NTS: true, Arch: "x64"},
		`C:/php/8.3-alt`: {Version: "8.3", Full: "8.3.13", NTS: true, Arch: "x64"},
	}}
	res, err := mgr.Scan(ctx)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Registered) != 1 || res.Registered[0] != "8.3" {
		t.Errorf("Registered = %v, want [8.3]", res.Registered)
	}
	if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0].Reason, "sudah terdaftar") {
		t.Errorf("Skipped = %+v, want the duplicate-version skip", res.Skipped)
	}
}

func TestScanEmptyRootAndMissingDir(t *testing.T) {
	mgr, _ := newScanRig(t)
	mgr.ScanRoot = ""
	res, err := mgr.Scan(context.Background())
	if err != nil || len(res.Registered) != 0 || len(res.Skipped) != 0 {
		t.Errorf("empty ScanRoot must scan nothing, got %+v, %v", res, err)
	}
	mgr.ScanRoot = `C:\tidak-ada`
	res, err = mgr.Scan(context.Background())
	if err != nil || len(res.Registered) != 0 || len(res.Skipped) != 0 {
		t.Errorf("missing scan dir must scan nothing, got %+v, %v", res, err)
	}
}

func TestScanRegistryErrorIsHardError(t *testing.T) {
	fs := newMemFS()
	store := &memStore{loadErr: errors.New("registry rusak")}
	regSvc := &RegistryService{Store: store, Files: fs, Clock: &tickClock{}}
	mgr := &PHPManager{Registry: regSvc, Prober: dirProber{}, Files: fs, ScanRoot: `C:\php`}
	if _, err := mgr.Scan(context.Background()); err == nil {
		t.Errorf("unreadable registry must be a hard scan error")
	}
}
