package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gojalifs/spanel-plus/internal/config"
	"github.com/gojalifs/spanel-plus/internal/core"
)

func tlsTestApp(t *testing.T, dataDir string) *Application {
	t.Helper()
	cfg := config.Config{
		Port:               0, // Run tests pick their own port below
		DataDir:            dataDir,
		LogDir:             dataDir,
		ApacheService:      "apache-spanel",
		TLSApache:          true,
		TLSApachePort:      443,
		TLSCAYears:         100,
		TLSLeafDays:        365,
		TLSRenewBeforeDays: 30,
	}
	return &Application{cfg: cfg, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func initTLSApp(t *testing.T, dir string) {
	t.Helper()
	err := core.InitMaterial(dir, core.TLSInitOptions{
		IPs: []net.IP{net.ParseIP("127.0.0.1")}, CAYears: 100, LeafDays: 365, RenewDays: 30,
	})
	if err != nil {
		t.Fatalf("InitMaterial: %v", err)
	}
}

// TestGetCertificateHotSwap proves the dashboard serves a renewed
// leaf without a restart: GetCertificate reads the files per
// handshake, so replacing the pair changes the served cert.
func TestGetCertificateHotSwap(t *testing.T) {
	dir := t.TempDir()
	tlsDir := filepath.Join(dir, "tls")
	initTLSApp(t, tlsDir)
	a := tlsTestApp(t, dir)

	cert1, err := a.getCertificate(nil)
	if err != nil {
		t.Fatalf("getCertificate pertama: %v", err)
	}
	leaf1, err := x509.ParseCertificate(cert1.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := core.RegenerateLeaf(tlsDir, []net.IP{net.ParseIP("127.0.0.1")}, nil, 365); err != nil {
		t.Fatalf("RegenerateLeaf: %v", err)
	}
	cert2, err := a.getCertificate(nil)
	if err != nil {
		t.Fatalf("getCertificate kedua: %v", err)
	}
	leaf2, err := x509.ParseCertificate(cert2.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if leaf1.SerialNumber.Cmp(leaf2.SerialNumber) == 0 {
		t.Errorf("getCertificate harus mengembalikan leaf baru sesudah file diganti (serial sama)")
	}
}

// TestRunFailClosedTLS proves broken material fails startup before
// anything listens.
func TestRunFailClosedTLS(t *testing.T) {
	dir := t.TempDir()
	tlsDir := filepath.Join(dir, "tls")
	initTLSApp(t, tlsDir)
	_, certPath, _, _ := core.TLSPaths(tlsDir)
	if err := os.WriteFile(certPath, []byte("rusak"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := tlsTestApp(t, dir)
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	err := a.Run(context.Background(), handler)
	if err == nil {
		t.Fatalf("Run dengan material rusak harus gagal")
	}
	if got := err.Error(); !strings.Contains(got, "TLS") {
		t.Errorf("error Run harus menyebut TLS, dapat: %q", got)
	}
}

// TestRunHTTPSServes proves Run serves HTTPS on the panel port when
// material exists, and keeps serving after a leaf regenerate.
func TestRunHTTPSServes(t *testing.T) {
	dir := t.TempDir()
	tlsDir := filepath.Join(dir, "tls")
	initTLSApp(t, tlsDir)
	a := tlsTestApp(t, dir)

	// Pick a free port for this Run (config Port 0 would listen on
	// :0, but the test needs the real port — so bind a probe first).
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()
	a.cfg.Port = port

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- a.Run(ctx, handler) }()
	// Wait for the listener (poll with a TLS client that trusts ca.crt).
	caPath, _, _, _ := core.TLSPaths(tlsDir)
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	client := &http.Client{
		Timeout:   2 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
	}
	url := fmt.Sprintf("https://127.0.0.1:%d/", port)
	var lastErr error
	for i := 0; i < 50; i++ {
		resp, err := client.Get(url)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(body) != "ok" {
				t.Fatalf("body = %q, mau ok", body)
			}
			lastErr = nil
			break
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("panel HTTPS tidak menjawab: %v", lastErr)
	}
	// Regenerate the leaf: the same client (same CA pool) must still
	// get an answer — hot-swap, no restart.
	if err := core.RegenerateLeaf(tlsDir, []net.IP{net.ParseIP("127.0.0.1")}, nil, 365); err != nil {
		t.Fatalf("RegenerateLeaf: %v", err)
	}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("panel HTTPS sesudah regenerate tidak menjawab: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status sesudah regenerate = %d, mau 200", resp.StatusCode)
	}
	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Errorf("Run sesudah cancel harus nil, dapat %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Errorf("Run tidak berhenti sesudah cancel")
	}
}

// TestAppTLSMethods covers the API-facing TLS methods without Apache
// attached (renew no-op, renew due, regenerate, reinit).
func TestAppTLSMethods(t *testing.T) {
	dir := t.TempDir()
	tlsDir := filepath.Join(dir, "tls")
	initTLSApp(t, tlsDir)
	a := tlsTestApp(t, dir)

	info, err := a.TLSInfo()
	if err != nil {
		t.Fatalf("TLSInfo: %v", err)
	}
	if !info.Enabled || info.ApachePort != 443 || !info.TLSApache {
		t.Errorf("TLSInfo = %+v", info)
	}
	if _, err := a.TLSCACert(); err != nil {
		t.Errorf("TLSCACert: %v", err)
	}

	res, err := a.TLSRenew()
	if err != nil {
		t.Fatalf("TLSRenew no-op: %v", err)
	}
	if res.Changed {
		t.Errorf("renew leaf baru harus no-op")
	}

	// Leaf 1 hari + threshold 30 = jatuh tempo; tanpa Apache
	// terpasang, restart dilaporkan (bukan gagal).
	caPath, _, _, _ := core.TLSPaths(tlsDir)
	caPEM, _ := os.ReadFile(caPath)
	caKeyPEM, _ := os.ReadFile(core.TLSCAKeyPath(tlsDir))
	shortPEM, shortKey, err := core.IssueLeaf(caPEM, caKeyPEM, []net.IP{net.ParseIP("127.0.0.1")}, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, certPath, keyPath, _ := core.TLSPaths(tlsDir)
	if err := os.WriteFile(certPath, shortPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, shortKey, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err = a.TLSRenew()
	if err != nil {
		t.Fatalf("TLSRenew jatuh tempo: %v", err)
	}
	if !res.Changed {
		t.Errorf("renew jatuh tempo harus changed")
	}
	if res.ApacheRestarted || res.ApacheError == "" {
		t.Errorf("tanpa Apache terpasang: restarted=%v error=%q", res.ApacheRestarted, res.ApacheError)
	}

	res, err = a.TLSRegenerateLeaf(nil, nil)
	if err != nil {
		t.Fatalf("TLSRegenerateLeaf: %v", err)
	}
	if !res.Changed {
		t.Errorf("regenerate harus changed")
	}

	oldInfo, _ := a.TLSInfo()
	res, err = a.TLSReinitCA(nil, nil, 0)
	if err != nil {
		t.Fatalf("TLSReinitCA: %v", err)
	}
	newInfo, _ := a.TLSInfo()
	if newInfo.CAFingerprint == oldInfo.CAFingerprint {
		t.Errorf("reinit harus mengganti fingerprint CA")
	}
}
