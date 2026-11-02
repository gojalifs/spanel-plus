package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gojalifs/spanel-plus/internal/config"
)

// tlsTestCLI builds a CLI whose config points at a temp data dir.
func tlsTestCLI(t *testing.T, dataDir string) (*CLI, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cfg := config.Config{
		Port:               8888,
		DataDir:            dataDir,
		LogDir:             dataDir,
		ApacheService:      "apache-spanel",
		TLSApache:          true,
		TLSApachePort:      443,
		TLSCAYears:         100,
		TLSLeafDays:        365,
		TLSRenewBeforeDays: 30,
	}
	deps := Deps{
		Out:    out,
		ErrOut: errOut,
		LoadConfig: func() (config.Config, error) {
			return cfg, nil
		},
	}
	return New(deps), out, errOut
}

func TestCLITLSInitStatusRenewDisable(t *testing.T) {
	dir := t.TempDir()
	cli, out, _ := tlsTestCLI(t, dir)

	if code := cli.Run([]string{"tls", "status"}); code != 0 {
		t.Fatalf("tls status tanpa material: code %d", code)
	}
	if !strings.Contains(out.String(), "TLS tidak aktif") {
		t.Errorf("status tanpa material harus bilang tidak aktif: %q", out.String())
	}

	out.Reset()
	if code := cli.Run([]string{"tls", "init", "--ips", "192.168.89.21"}); code != 0 {
		t.Fatalf("tls init: code %d", code)
	}
	if !strings.Contains(out.String(), "TLS material dibuat") {
		t.Errorf("init harus konfirmasi: %q", out.String())
	}
	if !strings.Contains(out.String(), "192.168.89.21") {
		t.Errorf("init harus menampilkan SAN: %q", out.String())
	}

	out.Reset()
	if code := cli.Run([]string{"tls", "status"}); code != 0 {
		t.Fatalf("tls status: code %d", code)
	}
	if !strings.Contains(out.String(), "TLS aktif") || !strings.Contains(out.String(), "CA fingerprint") {
		t.Errorf("status harus menampilkan aktif + fingerprint: %q", out.String())
	}

	// Init kedua ditolak.
	if code := cli.Run([]string{"tls", "init", "--ips", "192.168.89.21"}); code == 0 {
		t.Errorf("init kedua harus gagal")
	}

	// Renew no-op (leaf baru).
	out.Reset()
	if code := cli.Run([]string{"tls", "renew"}); code != 0 {
		t.Fatalf("tls renew: code %d", code)
	}
	if !strings.Contains(out.String(), "no-op") {
		t.Errorf("renew leaf baru harus no-op: %q", out.String())
	}

	// Reinit tanpa --confirm ditolak; dengan --confirm jalan.
	if code := cli.Run([]string{"tls", "reinit-ca"}); code == 0 {
		t.Errorf("reinit tanpa --confirm harus ditolak")
	}
	out.Reset()
	if code := cli.Run([]string{"tls", "reinit-ca", "--confirm"}); code != 0 {
		t.Fatalf("tls reinit-ca --confirm: code %d", code)
	}
	if !strings.Contains(out.String(), "install ulang") {
		t.Errorf("reinit harus memperingatkan install ulang: %q", out.String())
	}

	// Export-ca menulis file.
	caFile := dir + "/spanel-ca.crt"
	if code := cli.Run([]string{"tls", "export-ca", caFile}); code != 0 {
		t.Fatalf("tls export-ca: code %d", code)
	}

	// Disable memindah direktori; status kembali tidak aktif.
	out.Reset()
	if code := cli.Run([]string{"tls", "disable"}); code != 0 {
		t.Fatalf("tls disable: code %d", code)
	}
	if !strings.Contains(out.String(), "di-disable") {
		t.Errorf("disable harus konfirmasi: %q", out.String())
	}
	out.Reset()
	if code := cli.Run([]string{"tls", "status"}); code != 0 {
		t.Fatalf("tls status sesudah disable: code %d", code)
	}
	if !strings.Contains(out.String(), "TLS tidak aktif") {
		t.Errorf("status sesudah disable harus tidak aktif: %q", out.String())
	}

	// Regenerate tanpa material gagal jelas.
	if code := cli.Run([]string{"tls", "regenerate-leaf"}); code == 0 {
		t.Errorf("regenerate tanpa material harus gagal")
	}
}

func TestCLITLSUnknownSubcommand(t *testing.T) {
	cli, _, _ := tlsTestCLI(t, t.TempDir())
	if code := cli.Run([]string{"tls", "ngawur"}); code != 2 {
		t.Errorf("subperintah tidak dikenal harus code 2, dapat %d", code)
	}
}
