package core

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// argsRunner returns canned output per last argument (for httpd -v
// vs -M distinctions the shared fakeRunner cannot express).
type argsRunner struct{ byArg map[string]string }

func (r argsRunner) Run(_ context.Context, _ string, args ...string) (string, error) {
	if len(args) > 0 {
		if out, ok := r.byArg[args[len(args)-1]]; ok {
			return out, nil
		}
	}
	return "", nil
}

func tlsDoctor(t *testing.T, withSSL bool) (Doctor, DoctorInput) {
	t.Helper()
	home := `C:\m0\Apache24`
	tlsDir := filepath.Join(t.TempDir(), "tls")
	if err := InitMaterial(tlsDir, TLSInitOptions{
		IPs: []net.IP{net.ParseIP("192.168.89.21")}, CAYears: 100, LeafDays: 365, RenewDays: 30,
	}); err != nil {
		t.Fatalf("InitMaterial: %v", err)
	}
	modules := "Loaded Modules:\n core_module (static)\n"
	if withSSL {
		modules += " ssl_module (shared)\n"
	}
	modules += " fcgid_module (shared)\n rewrite_module (shared)\n"
	d := Doctor{
		Runner: argsRunner{byArg: map[string]string{
			"-v": "Server version: Apache/2.4.69 (Win64)\n",
			"-M": modules,
			"-t": "Syntax OK\n",
		}},
		Files: fakeFiles{
			dirs: map[string]bool{home: true},
			files: map[string]bool{
				filepath.Join(home, "bin", "httpd.exe"):        true,
				filepath.Join(home, "modules", "mod_fcgid.so"): true,
			},
		},
		Ports: fakePorts{},
	}
	in := DoctorInput{
		ApacheHome: home,
		PanelPort:  8888,
		DataDir:    `C:\panel\data`,
		TLSDir:     tlsDir,
		TLSApache:  true,
	}
	return d, in
}

func findCheck(rep Report, name string) (Check, bool) {
	for _, c := range rep.Checks {
		if c.Name == name {
			return c, true
		}
	}
	return Check{}, false
}

func TestDoctorTLSActiveWithSSL(t *testing.T) {
	d, in := tlsDoctor(t, true)
	rep := d.Run(context.Background(), in)
	if !rep.Healthy {
		t.Fatalf("TLS aktif + ssl_module harus sehat: %+v", rep.Checks)
	}
	if c, ok := findCheck(rep, "mod-ssl"); !ok || c.Status != StatusPass {
		t.Errorf("mod-ssl = %+v, mau pass", c)
	}
	if c, ok := findCheck(rep, "tls-material"); !ok || c.Status != StatusPass {
		t.Errorf("tls-material = %+v, mau pass", c)
	}
}

func TestDoctorTLSActiveWithoutSSLFails(t *testing.T) {
	d, in := tlsDoctor(t, false)
	rep := d.Run(context.Background(), in)
	if rep.Healthy {
		t.Fatalf("TLS aktif tanpa ssl_module harus tidak sehat")
	}
	if c, ok := findCheck(rep, "mod-ssl"); !ok || c.Status != StatusFail {
		t.Errorf("mod-ssl = %+v, mau fail", c)
	}
}

func TestDoctorTLSBrokenMaterialFails(t *testing.T) {
	d, in := tlsDoctor(t, true)
	_, certPath, _, _ := TLSPaths(in.TLSDir)
	if err := os.WriteFile(certPath, []byte("rusak"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := d.Run(context.Background(), in)
	if rep.Healthy {
		t.Fatalf("material rusak harus tidak sehat")
	}
	if c, ok := findCheck(rep, "tls-material"); !ok || c.Status != StatusFail {
		t.Errorf("tls-material = %+v, mau fail", c)
	}
}

func TestDoctorTLSShortLeafWarns(t *testing.T) {
	d, in := tlsDoctor(t, true)
	// Leaf 10 hari: di bawah threshold warn 30 hari, di atas 0.
	caPath, _, _, _ := TLSPaths(in.TLSDir)
	caPEM, _ := os.ReadFile(caPath)
	caKeyPEM, _ := os.ReadFile(TLSCAKeyPath(in.TLSDir))
	shortPEM, shortKey, err := IssueLeaf(caPEM, caKeyPEM, []net.IP{net.ParseIP("192.168.89.21")}, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	_, certPath, keyPath, _ := TLSPaths(in.TLSDir)
	if err := os.WriteFile(certPath, shortPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, shortKey, 0o600); err != nil {
		t.Fatal(err)
	}
	rep := d.Run(context.Background(), in)
	if !rep.Healthy {
		t.Fatalf("leaf pendek harus warn (tetap sehat): %+v", rep.Checks)
	}
	if c, ok := findCheck(rep, "tls-material"); !ok || c.Status != StatusWarn {
		t.Errorf("tls-material = %+v, mau warn", c)
	}
}

func TestDoctorTLSApacheOffSkipsModSSL(t *testing.T) {
	d, in := tlsDoctor(t, false)
	in.TLSApache = false
	rep := d.Run(context.Background(), in)
	if _, ok := findCheck(rep, "mod-ssl"); ok {
		t.Errorf("tls_apache=false harus melewati cek mod-ssl")
	}
	if !rep.Healthy {
		t.Fatalf("tanpa TLS Apache harus sehat: %+v", rep.Checks)
	}
}

func TestTLSVHostForApply(t *testing.T) {
	dir := t.TempDir()
	tlsDir := filepath.Join(dir, "tls")
	if err := InitMaterial(tlsDir, TLSInitOptions{
		IPs: []net.IP{net.ParseIP("192.168.89.21")}, CAYears: 100, LeafDays: 365, RenewDays: 30,
	}); err != nil {
		t.Fatal(err)
	}
	m := &ApacheManager{Cfg: ApplyConfig{TLSDir: tlsDir, TLSApache: true}}
	vhost, err := m.TLSVHostForApply()
	if err != nil {
		t.Fatalf("TLSVHostForApply: %v", err)
	}
	if !vhost.Enabled || vhost.Port != 443 {
		t.Errorf("vhost = %+v, mau enabled port 443", vhost)
	}
	if !strings.Contains(vhost.CertFile, "server.crt") || !strings.Contains(vhost.KeyFile, "server.key") {
		t.Errorf("vhost file = %+v", vhost)
	}
	// Port kustom dihormati; TLSApache false menonaktifkan.
	m.Cfg.TLSApachePort = 8443
	vhost, _ = m.TLSVHostForApply()
	if vhost.Port != 8443 {
		t.Errorf("port kustom = %d, mau 8443", vhost.Port)
	}
	m.Cfg.TLSApache = false
	vhost, err = m.TLSVHostForApply()
	if err != nil || vhost.Enabled {
		t.Errorf("TLSApache false: vhost = %+v err = %v", vhost, err)
	}
	// Material rusak menggagalkan apply sebelum tulis apa pun.
	m.Cfg.TLSApache = true
	m.Cfg.TLSApachePort = 0
	_, certPath, _, _ := TLSPaths(tlsDir)
	if err := os.WriteFile(certPath, []byte("rusak"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.TLSVHostForApply(); err == nil {
		t.Errorf("material rusak harus menggagalkan TLSVHostForApply")
	}
}
