package core

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mustInitTLS(t *testing.T, dir string) {
	t.Helper()
	err := InitMaterial(dir, TLSInitOptions{
		IPs:       []net.IP{net.ParseIP("192.168.89.21"), net.ParseIP("10.0.0.5")},
		DNSNames:  []string{"panel.local"},
		CAYears:   100,
		LeafDays:  365,
		RenewDays: 30,
	})
	if err != nil {
		t.Fatalf("InitMaterial: %v", err)
	}
}

func TestGenerateCAProperties(t *testing.T) {
	for _, years := range []int{100, 1000} {
		certPEM, keyPEM, err := GenerateCA(years)
		if err != nil {
			t.Fatalf("GenerateCA(%d): %v", years, err)
		}
		cert, err := parseCert(certPEM)
		if err != nil {
			t.Fatalf("parse CA: %v", err)
		}
		if !cert.IsCA {
			t.Errorf("CA %d tahun: IsCA harus true", years)
		}
		if cert.KeyUsage&x509.KeyUsageCertSign == 0 {
			t.Errorf("CA %d tahun: KeyUsage harus memuat CertSign", years)
		}
		if cert.Subject.CommonName != TLSCACN {
			t.Errorf("CA CN = %q, mau %q", cert.Subject.CommonName, TLSCACN)
		}
		wantAfter := time.Now().AddDate(years, 0, 0)
		if d := cert.NotAfter.Sub(wantAfter); d > 2*time.Minute || d < -2*time.Minute {
			t.Errorf("CA %d tahun: NotAfter = %v, mau sekitar %v", years, cert.NotAfter, wantAfter)
		}
		if d := cert.NotBefore.Sub(time.Now().Add(-time.Hour)); d > 2*time.Minute || d < -2*time.Minute {
			t.Errorf("CA: NotBefore = %v, mau sekitar sekarang-1jam", cert.NotBefore)
		}
		if _, err := parsePrivateKey(keyPEM); err != nil {
			t.Errorf("parse kunci CA: %v", err)
		}
		if len(cert.PermittedIPRanges) != 4 {
			t.Errorf("CA: PermittedIPRanges = %d, mau 4", len(cert.PermittedIPRanges))
		}
	}
}

func TestGenerateCAInvalidYears(t *testing.T) {
	for _, years := range []int{0, -5, 10000} {
		if _, _, err := GenerateCA(years); err == nil {
			t.Errorf("GenerateCA(%d) harus gagal", years)
		}
	}
}

func TestIssueLeafProperties(t *testing.T) {
	caCert, caKey, err := GenerateCA(100)
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	ips := []net.IP{net.ParseIP("192.168.89.21"), net.ParseIP("127.0.0.1")}
	certPEM, keyPEM, err := IssueLeaf(caCert, caKey, ips, []string{"panel.local"}, 365)
	if err != nil {
		t.Fatalf("IssueLeaf: %v", err)
	}
	cert, err := parseCert(certPEM)
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	if len(cert.IPAddresses) != 2 || cert.IPAddresses[0].String() != "192.168.89.21" {
		t.Errorf("leaf SAN IP = %v, mau [192.168.89.21 127.0.0.1]", cert.IPAddresses)
	}
	if len(cert.DNSNames) != 1 || cert.DNSNames[0] != "panel.local" {
		t.Errorf("leaf SAN DNS = %v, mau [panel.local]", cert.DNSNames)
	}
	if cert.Subject.CommonName != "192.168.89.21" {
		t.Errorf("leaf CN = %q, mau IP utama", cert.Subject.CommonName)
	}
	hasServerAuth := false
	for _, u := range cert.ExtKeyUsage {
		if u == x509.ExtKeyUsageServerAuth {
			hasServerAuth = true
		}
	}
	if !hasServerAuth {
		t.Errorf("leaf harus punya ExtKeyUsageServerAuth")
	}
	if cert.IsCA {
		t.Errorf("leaf tidak boleh IsCA")
	}
	key, err := parsePrivateKey(keyPEM)
	if err != nil {
		t.Fatalf("parse kunci leaf: %v", err)
	}
	if !keyMatchesCert(key, cert) {
		t.Errorf("kunci leaf tidak cocok dengan sertifikat")
	}
	// Serial unik tiap terbit.
	certPEM2, _, err := IssueLeaf(caCert, caKey, ips, nil, 365)
	if err != nil {
		t.Fatalf("IssueLeaf kedua: %v", err)
	}
	cert2, _ := parseCert(certPEM2)
	if cert.SerialNumber.Cmp(cert2.SerialNumber) == 0 {
		t.Errorf("serial leaf harus unik tiap terbit")
	}
}

func TestNameConstraintsPrivateVerifies(t *testing.T) {
	caCert, caKey, err := GenerateCA(100)
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	leafPEM, _, err := IssueLeaf(caCert, caKey, []net.IP{net.ParseIP("192.168.89.21")}, []string{"panel.local"}, 365)
	if err != nil {
		t.Fatalf("IssueLeaf: %v", err)
	}
	leaf, _ := parseCert(leafPEM)
	ca, _ := parseCert(caCert)
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		t.Errorf("leaf IP privat + DNS harus terverifikasi terhadap CA: %v", err)
	}
}

// TestNameConstraintsPublicIP proves (not assumes) that a public-IP
// leaf is rejected under this IP-constrained anchor: a stolen CA
// key cannot mint public names.
func TestNameConstraintsPublicIP(t *testing.T) {
	caCert, caKey, err := GenerateCA(100)
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	leafPEM, _, err := IssueLeaf(caCert, caKey, []net.IP{net.ParseIP("8.8.8.8")}, nil, 365)
	if err != nil {
		t.Fatalf("IssueLeaf: %v", err)
	}
	leaf, _ := parseCert(leafPEM)
	ca, _ := parseCert(caCert)
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err == nil {
		t.Errorf("leaf IP publik harus ditolak oleh CA dengan PermittedIPRanges privat")
	}
}

func TestInitStatusValidateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	tlsDir := filepath.Join(dir, "tls")
	mustInitTLS(t, tlsDir)
	if !HasTLSMaterial(tlsDir) {
		t.Fatalf("HasTLSMaterial harus true sesudah init")
	}
	if err := ValidateMaterial(tlsDir); err != nil {
		t.Fatalf("ValidateMaterial: %v", err)
	}
	st, err := Status(tlsDir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Present {
		t.Errorf("Status.Present harus true")
	}
	if len(st.CAFingerprint) != 64 {
		t.Errorf("fingerprint = %q, mau 64 hex", st.CAFingerprint)
	}
	if len(st.LeafIPs) != 2 || st.LeafIPs[0] != "10.0.0.5" || st.LeafIPs[1] != "192.168.89.21" {
		// Init memakai SAN persis seperti diberi (loopback ditambah
		// ResolveInitSANs di lapisan CLI/API, bukan InitMaterial).
		t.Errorf("LeafIPs = %v, mau [10.0.0.5 192.168.89.21] (terurut)", st.LeafIPs)
	}
	if st.LeafDNS[0] != "panel.local" {
		t.Errorf("LeafDNS = %v, mau [panel.local]", st.LeafDNS)
	}
	if time.Until(st.LeafExpiry) < 364*24*time.Hour {
		t.Errorf("leaf expiry terlalu dekat: %v", st.LeafExpiry)
	}
	if time.Until(st.CAExpiry) < 99*365*24*time.Hour {
		t.Errorf("CA expiry terlalu dekat: %v", st.CAExpiry)
	}
	// Init kedua harus ditolak (CA sudah ada).
	if err := InitMaterial(tlsDir, TLSInitOptions{IPs: []net.IP{net.ParseIP("192.168.1.1")}, CAYears: 100, LeafDays: 365}); err == nil {
		t.Errorf("init kedua harus ditolak bila CA sudah ada")
	}
	// Sertifikat server bisa dibaca untuk handshake.
	certPEM, keyPEM, err := LoadServerCertificate(tlsDir)
	if err != nil {
		t.Fatalf("LoadServerCertificate: %v", err)
	}
	cert, _ := parseCert(certPEM)
	key, _ := parsePrivateKey(keyPEM)
	if !keyMatchesCert(key, cert) {
		t.Errorf("pasangan server.crt/server.key tidak cocok")
	}
	// ca.key ada (0600) dan tidak pernah diserve: hanya ada di disk.
	if _, err := os.Stat(TLSCAKeyPath(tlsDir)); err != nil {
		t.Errorf("ca.key harus ada sesudah init: %v", err)
	}
}

func TestRenewLeaf(t *testing.T) {
	dir := t.TempDir()
	tlsDir := filepath.Join(dir, "tls")
	mustInitTLS(t, tlsDir)
	// Leaf baru (365 hari) + threshold 30 hari = no-op.
	changed, err := RenewLeaf(tlsDir, 365, 30)
	if err != nil {
		t.Fatalf("RenewLeaf no-op: %v", err)
	}
	if changed {
		t.Errorf("renew sebelum waktunya harus no-op")
	}
	// Buat leaf berumur 1 hari lewat regenerasi langsung, lalu renew
	// dengan threshold 30 hari harus mengganti.
	caPath, _, _, _ := TLSPaths(tlsDir)
	caPEM, _ := os.ReadFile(caPath)
	caKeyPEM, _ := os.ReadFile(TLSCAKeyPath(tlsDir))
	shortPEM, shortKey, err := IssueLeaf(caPEM, caKeyPEM, []net.IP{net.ParseIP("192.168.89.21")}, nil, 1)
	if err != nil {
		t.Fatalf("IssueLeaf pendek: %v", err)
	}
	if err := writeLeafPair(tlsDir, shortPEM, shortKey); err != nil {
		t.Fatalf("tulis leaf pendek: %v", err)
	}
	oldSt, _ := Status(tlsDir)
	changed, err = RenewLeaf(tlsDir, 365, 30)
	if err != nil {
		t.Fatalf("RenewLeaf jatuh tempo: %v", err)
	}
	if !changed {
		t.Errorf("renew jatuh tempo harus mengganti leaf")
	}
	newSt, err := Status(tlsDir)
	if err != nil {
		t.Fatalf("Status sesudah renew: %v", err)
	}
	if !newSt.LeafExpiry.After(oldSt.LeafExpiry) {
		t.Errorf("expiry sesudah renew (%v) harus maju dari %v", newSt.LeafExpiry, oldSt.LeafExpiry)
	}
	if newSt.LeafIPs[0] != "192.168.89.21" {
		t.Errorf("renew harus mempertahankan SAN leaf lama: %v", newSt.LeafIPs)
	}
	if err := ValidateMaterial(tlsDir); err != nil {
		t.Errorf("material sesudah renew harus valid: %v", err)
	}
	// Renew tanpa material = error jelas.
	if _, err := RenewLeaf(filepath.Join(dir, "kosong"), 365, 30); err == nil {
		t.Errorf("renew tanpa material harus gagal")
	}
}

func TestFailClosed(t *testing.T) {
	dir := t.TempDir()

	// 1. Sertifikat korup.
	corrupt := filepath.Join(dir, "c1")
	mustInitTLS(t, corrupt)
	_, cCert, _, _ := TLSPaths(corrupt)
	if err := os.WriteFile(cCert, []byte("bukan-sertifikat"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, err := DashboardTLSMode(corrupt); ok || err == nil {
		t.Errorf("DashboardTLSMode material korup: ok=%v err=%v, mau gagal", ok, err)
	}

	// 2. Kunci hilang.
	missing := filepath.Join(dir, "c2")
	mustInitTLS(t, missing)
	_, _, mKey, _ := TLSPaths(missing)
	if err := os.Remove(mKey); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMaterial(missing); err == nil {
		t.Errorf("kunci hilang harus gagal validasi")
	}

	// 3. Kunci tidak cocok dengan sertifikat (kunci RSA lain).
	mismatch := filepath.Join(dir, "c3")
	mustInitTLS(t, mismatch)
	_, otherKeyPEM, err := GenerateCA(100)
	if err != nil {
		t.Fatal(err)
	}
	_, _, mmKey, _ := TLSPaths(mismatch)
	if err := os.WriteFile(mmKey, otherKeyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMaterial(mismatch); err == nil {
		t.Errorf("kunci mismatch harus gagal validasi")
	}

	// 4. Leaf kedaluwarsa (dibuat manual: IssueLeaf menolak days < 1).
	expired := filepath.Join(dir, "c4")
	mustInitTLS(t, expired)
	caPath, eCert, eKey, _ := TLSPaths(expired)
	caPEM, _ := os.ReadFile(caPath)
	caKeyPEM, _ := os.ReadFile(TLSCAKeyPath(expired))
	caCert, err := parseCert(caPEM)
	if err != nil {
		t.Fatal(err)
	}
	caKey, err := parsePrivateKey(caKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := randomSerial()
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "192.168.89.21"},
		NotBefore:    time.Now().Add(-2 * time.Hour),
		NotAfter:     time.Now().Add(-time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("192.168.89.21")},
	}, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	expiredPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	expiredKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(leafKey)})
	if err := os.WriteFile(eCert, expiredPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(eKey, expiredKeyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMaterial(expired); err == nil {
		t.Errorf("leaf kedaluwarsa harus gagal validasi")
	}
	if ok, err := DashboardTLSMode(expired); ok || err == nil {
		t.Errorf("DashboardTLSMode leaf kedaluwarsa: ok=%v err=%v, mau gagal", ok, err)
	}

	// 5. Hanya ca.crt tanpa leaf = tidak ada material = HTTP, bukan gagal.
	lonely := filepath.Join(dir, "c5")
	if err := os.MkdirAll(lonely, 0o755); err != nil {
		t.Fatal(err)
	}
	caOnly, _, _ := GenerateCA(100)
	if err := os.WriteFile(filepath.Join(lonely, TLSCAFile), caOnly, 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, err := DashboardTLSMode(lonely); ok || err != nil {
		t.Errorf("ca.crt tanpa leaf: ok=%v err=%v, mau (false, nil)", ok, err)
	}
}

func TestRegenerateAndDisable(t *testing.T) {
	dir := t.TempDir()
	tlsDir := filepath.Join(dir, "tls")
	mustInitTLS(t, tlsDir)

	// Metadata tercatat dari init.
	meta, err := ReadMetadata(tlsDir)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if meta.CAYears != 100 || meta.LeafDays != 365 {
		t.Errorf("metadata = %+v, mau CAYears 100 LeafDays 365", meta)
	}

	// Regenerate dengan SAN baru: material valid, metadata ikut baru.
	newIPs := []net.IP{net.ParseIP("192.168.89.99"), net.ParseIP("127.0.0.1")}
	if err := RegenerateLeaf(tlsDir, newIPs, []string{"baru.local"}, 365); err != nil {
		t.Fatalf("RegenerateLeaf: %v", err)
	}
	st, err := Status(tlsDir)
	if err != nil {
		t.Fatalf("Status sesudah regenerate: %v", err)
	}
	if len(st.LeafIPs) != 2 || st.LeafIPs[0] != "127.0.0.1" || st.LeafIPs[1] != "192.168.89.99" {
		t.Errorf("LeafIPs sesudah regenerate = %v", st.LeafIPs)
	}
	meta, _ = ReadMetadata(tlsDir)
	if len(meta.IPs) != 2 || meta.Names[0] != "baru.local" {
		t.Errorf("metadata sesudah regenerate = %+v", meta)
	}

	// ResolveRegenSANs: flag menang atas metadata, metadata menang
	// atas leaf, tanpa ketiganya = leaf.
	ips, _, err := ResolveRegenSANs(tlsDir, []string{"10.1.1.1"}, nil)
	if err != nil {
		t.Fatalf("ResolveRegenSANs flag: %v", err)
	}
	if ips[0].String() != "10.1.1.1" {
		t.Errorf("flag harus menang: %v", ips)
	}
	ips, _, err = ResolveRegenSANs(tlsDir, nil, nil)
	if err != nil {
		t.Fatalf("ResolveRegenSANs metadata: %v", err)
	}
	if ips[0].String() != "192.168.89.99" {
		t.Errorf("metadata harus menang atas leaf: %v", ips)
	}
	// Buat leaf menyimpang (tanpa update metadata) untuk membuktikan
	// precedence: metadata tetap menang atas leaf.
	caPath, _, _, _ := TLSPaths(tlsDir)
	caPEM, _ := os.ReadFile(caPath)
	caKeyPEM, _ := os.ReadFile(TLSCAKeyPath(tlsDir))
	divPEM, divKey, err := IssueLeaf(caPEM, caKeyPEM, []net.IP{net.ParseIP("10.9.9.9")}, nil, 365)
	if err != nil {
		t.Fatalf("IssueLeaf menyimpang: %v", err)
	}
	if err := writeLeafPair(tlsDir, divPEM, divKey); err != nil {
		t.Fatalf("tulis leaf menyimpang: %v", err)
	}
	ips, _, err = ResolveRegenSANs(tlsDir, nil, nil)
	if err != nil {
		t.Fatalf("ResolveRegenSANs metadata atas leaf menyimpang: %v", err)
	}
	if ips[0].String() != "192.168.89.99" {
		t.Errorf("metadata harus tetap menang atas leaf menyimpang: %v", ips)
	}
	if err := os.Remove(filepath.Join(tlsDir, TLSMetaFile)); err != nil {
		t.Fatal(err)
	}
	ips, names, err := ResolveRegenSANs(tlsDir, nil, nil)
	if err != nil {
		t.Fatalf("ResolveRegenSANs leaf: %v", err)
	}
	if ips[0].String() != "10.9.9.9" || len(names) != 0 {
		t.Errorf("tanpa metadata harus pakai leaf: %v %v", ips, names)
	}
	if _, _, err := ResolveRegenSANs(tlsDir, []string{"bukan-ip"}, nil); err == nil {
		t.Errorf("IP flag tidak valid harus gagal")
	}

	// Disable: direktori pindah nama, material utuh, disable kedua gagal.
	dst, err := DisableMaterial(tlsDir)
	if err != nil {
		t.Fatalf("DisableMaterial: %v", err)
	}
	if HasTLSMaterial(tlsDir) {
		t.Errorf("dir lama harus kosong sesudah disable")
	}
	if !HasTLSMaterial(dst) {
		t.Errorf("dir disable %s harus memuat material", dst)
	}
	if _, err := DisableMaterial(tlsDir); err == nil {
		t.Errorf("disable tanpa material harus gagal")
	}
}

func TestResolveInitSANs(t *testing.T) {
	ips, names, err := ResolveInitSANs([]string{"192.168.1.10"}, []string{"panel.local"})
	if err != nil {
		t.Fatalf("ResolveInitSANs configured: %v", err)
	}
	if len(ips) != 2 || ips[0].String() != "192.168.1.10" || ips[1].String() != "127.0.0.1" {
		t.Errorf("configured harus menang + loopback ditambah: %v", ips)
	}
	if len(names) != 1 || names[0] != "panel.local" {
		t.Errorf("names = %v", names)
	}
	// Duplikat loopback tidak ganda.
	ips, _, err = ResolveInitSANs([]string{"127.0.0.1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) != 1 {
		t.Errorf("loopback duplikat harus didedup: %v", ips)
	}
	if _, _, err := ResolveInitSANs([]string{"bukan-ip"}, nil); err == nil {
		t.Errorf("IP configured tidak valid harus gagal")
	}
	// Tanpa configured: deteksi (mesin ini punya interface non-loopback).
	ips, _, err = ResolveInitSANs(nil, nil)
	if err != nil {
		t.Fatalf("ResolveInitSANs deteksi: %v", err)
	}
	foundLoop := false
	for _, ip := range ips {
		if ip.String() == "127.0.0.1" {
			foundLoop = true
		}
	}
	if !foundLoop || len(ips) < 2 {
		t.Errorf("deteksi harus memberi IP LAN + loopback: %v", ips)
	}
}

func TestReinitCA(t *testing.T) {
	dir := t.TempDir()
	tlsDir := filepath.Join(dir, "tls")
	mustInitTLS(t, tlsDir)
	oldSt, _ := Status(tlsDir)
	err := ReinitCA(tlsDir, TLSInitOptions{
		IPs: []net.IP{net.ParseIP("192.168.89.21")}, CAYears: 100, LeafDays: 365, RenewDays: 30,
	})
	if err != nil {
		t.Fatalf("ReinitCA: %v", err)
	}
	newSt, err := Status(tlsDir)
	if err != nil {
		t.Fatalf("Status sesudah reinit: %v", err)
	}
	if newSt.CAFingerprint == oldSt.CAFingerprint {
		t.Errorf("reinit harus mengganti CA (fingerprint sama)")
	}
	if err := ValidateMaterial(tlsDir); err != nil {
		t.Errorf("material sesudah reinit harus valid: %v", err)
	}
}
