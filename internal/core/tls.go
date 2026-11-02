// TLS material for the panel's private CA (HTTPS v1, 2026-11-02).
//
// The panel is a LAN tool with no domain, so neither a public CA nor
// ACME/Let's Encrypt can issue for it (LE never issues for IPs, and
// public CAs are forbidden from private IPs). Instead the panel mints
// its own private CA: clients install ca.crt once (manually or via
// Group Policy), and every leaf the panel issues afterwards —
// including renewals — is trusted automatically. A self-signed leaf
// was rejected for the same reason: every renewal would mean
// reinstalling on every client.
//
// Activation is presence-based, NOT a panel.json flag: a <tls-dir>
// holding a valid server.crt + server.key means the dashboard serves
// HTTPS; no material means plain HTTP as before; present-but-broken
// material fails startup loudly (fail-closed: an active TLS setup
// must never silently fall back to HTTP). The panel never writes
// panel.json; it does write its own files under the TLS dir.
//
// Name constraints harden the CA: a stolen CA key cannot mint names
// outside the private ranges (10/8, 172.16/12, 192.168/16, 127/8).
// Consequence, stated plainly: a future public-IP SAN needs a new CA.
package core

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// TLS file names inside the TLS directory. ca.key holds the CA
// private key: it is required for issuance (renew/regenerate) and
// must NEVER be served — only ca.crt leaves the machine.
const (
	TLSCAFile    = "ca.crt"
	TLSCAKeyFile = "ca.key"
	TLSCertFile  = "server.crt"
	TLSKeyFile   = "server.key"
	TLSMetaFile  = "tls.json"
)

// TLSCACN is the private CA's subject (the trust anchor clients see).
const TLSCACN = "SPanel+ Private CA"

// TLSCAKeyPath resolves the CA private key path under dir.
func TLSCAKeyPath(dir string) string {
	return filepath.Join(dir, TLSCAKeyFile)
}

// TLSPaths resolves the four TLS file paths under dir.
func TLSPaths(dir string) (ca, cert, key, meta string) {
	return filepath.Join(dir, TLSCAFile),
		filepath.Join(dir, TLSCertFile),
		filepath.Join(dir, TLSKeyFile),
		filepath.Join(dir, TLSMetaFile)
}

// HasTLSMaterial reports whether a leaf pair exists. It answers the
// presence question only — validity is ValidateMaterial's job.
func HasTLSMaterial(dir string) bool {
	_, cert, key, _ := TLSPaths(dir)
	if _, err := os.Stat(cert); err != nil {
		return false
	}
	if _, err := os.Stat(key); err != nil {
		return false
	}
	return true
}

// TLSMetadata records what init/regenerate chose, so a later
// regenerate-leaf can reuse the SANs when the operator gives none.
// Renew never touches it: the leaf's own SANs are authoritative then,
// and the validity days always come from the live panel.json.
type TLSMetadata struct {
	IPs             []string `json:"ips"`
	Names           []string `json:"names"`
	CAYears         int      `json:"ca_validity_years"`
	LeafDays        int      `json:"leaf_validity_days"`
	RenewBeforeDays int      `json:"renew_before_days"`
}

// TLSInitOptions carries everything tls init needs.
type TLSInitOptions struct {
	IPs       []net.IP
	DNSNames  []string
	CAYears   int
	LeafDays  int
	RenewDays int // recorded in metadata only
}

// TLSStatus is the observable TLS state for API, UI, and doctor.
type TLSStatus struct {
	Present       bool      `json:"present"`
	CAExpiry      time.Time `json:"ca_expiry"`
	LeafExpiry    time.Time `json:"leaf_expiry"`
	CAFingerprint string    `json:"ca_fingerprint"`
	LeafIPs       []string  `json:"leaf_ips"`
	LeafDNS       []string  `json:"leaf_dns"`
}

// privateRanges are the CA's permitted IP ranges (RFC 1918 + loopback).
func privateRanges() []*net.IPNet {
	var out []*net.IPNet
	for _, cidr := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.0/8"} {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(err) // compile-time constants
		}
		out = append(out, n)
	}
	return out
}

// randomSerial returns a 128-bit random serial (never zero, never
// reused in practice: each issuance draws fresh entropy).
func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("tls: serial acak: %w", err)
	}
	return n, nil
}

// skew is the NotBefore backdate tolerating clock skew between the
// panel and its clients.
const skew = -1 * time.Hour

// GenerateCA mints a private root CA. years must be >= 1; a NotAfter
// past year 9999 cannot be DER-encoded and fails loudly here, not
// halfway through init.
func GenerateCA(years int) (certPEM, keyPEM []byte, err error) {
	if years < 1 {
		return nil, nil, fmt.Errorf("tls: masa berlaku CA %d tahun tidak valid (minimal 1)", years)
	}
	now := time.Now()
	notAfter := now.AddDate(years, 0, 0)
	if notAfter.Year() > 9999 {
		return nil, nil, fmt.Errorf("tls: masa berlaku CA %d tahun tidak bisa di-encode (lewat tahun 9999)", years)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("tls: generate kunci CA: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: TLSCACN},
		NotBefore:    now.Add(skew),
		NotAfter:     notAfter,
		IsCA:         true,
		// MaxPathLenZero: this CA issues leaves directly; it must
		// never sign an intermediate.
		MaxPathLenZero:        true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		PermittedIPRanges:     privateRanges(),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("tls: buat sertifikat CA: %w", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM, nil
}

// IssueLeaf signs a server leaf from a CA pair. ips must hold at
// least one address (the CN is the first one; SANs are authoritative).
// days must be >= 1 and encodable.
func IssueLeaf(caCertPEM, caKeyPEM []byte, ips []net.IP, names []string, days int) (certPEM, keyPEM []byte, err error) {
	if len(ips) == 0 {
		return nil, nil, errors.New("tls: leaf butuh minimal 1 IP SAN")
	}
	if days < 1 {
		return nil, nil, fmt.Errorf("tls: masa berlaku leaf %d hari tidak valid (minimal 1)", days)
	}
	caCert, err := parseCert(caCertPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("tls: parse CA: %w", err)
	}
	caKey, err := parsePrivateKey(caKeyPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("tls: parse kunci CA: %w", err)
	}
	now := time.Now()
	notAfter := now.AddDate(0, 0, days)
	if notAfter.Year() > 9999 {
		return nil, nil, fmt.Errorf("tls: masa berlaku leaf %d hari tidak bisa di-encode (lewat tahun 9999)", days)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("tls: generate kunci leaf: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: ips[0].String()},
		NotBefore:    now.Add(skew),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  ips,
		DNSNames:     names,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("tls: buat sertifikat leaf: %w", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM, nil
}

// parseCert decodes one PEM certificate.
func parseCert(pemBytes []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("bukan PEM CERTIFICATE")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	return cert, nil
}

// parsePrivateKey decodes an RSA private key (PKCS#1 as issued here,
// PKCS#8 as a courtesy for hand-made material).
func parsePrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("bukan PEM private key")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse kunci privat: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("kunci privat bukan RSA")
	}
	return key, nil
}

// keyMatchesCert reports whether a private key belongs to a cert.
func keyMatchesCert(key *rsa.PrivateKey, cert *x509.Certificate) bool {
	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return false
	}
	return pub.N.Cmp(key.PublicKey.N) == 0 && pub.E == key.PublicKey.E
}

// readMaterial loads the served material (CA cert, leaf cert, leaf
// key). It deliberately does NOT read ca.key: serving needs only the
// certs and the leaf key, so a damaged CA key must not take the
// dashboard down — it fails renew instead, loudly, when issuance is
// attempted.
func readMaterial(dir string) (caCert *x509.Certificate, leafCert *x509.Certificate, leafKey *rsa.PrivateKey, err error) {
	caPath, certPath, keyPath, _ := TLSPaths(dir)
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("tls: baca %s: %w", caPath, err)
	}
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("tls: baca %s: %w", certPath, err)
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("tls: baca %s: %w", keyPath, err)
	}
	if caCert, err = parseCert(caPEM); err != nil {
		return nil, nil, nil, fmt.Errorf("tls: parse %s: %w", caPath, err)
	}
	if leafCert, err = parseCert(certPEM); err != nil {
		return nil, nil, nil, fmt.Errorf("tls: parse %s: %w", certPath, err)
	}
	if leafKey, err = parsePrivateKey(keyPEM); err != nil {
		return nil, nil, nil, fmt.Errorf("tls: parse %s: %w", keyPath, err)
	}
	if !keyMatchesCert(leafKey, leafCert) {
		return nil, nil, nil, fmt.Errorf("tls: kunci %s tidak cocok dengan sertifikat %s", keyPath, certPath)
	}
	return caCert, leafCert, leafKey, nil
}

// ValidateMaterial loads the material and verifies the leaf chains
// to the CA, is currently valid, and is usable for ServerAuth. Any
// failure means: do NOT serve HTTPS with this material.
func ValidateMaterial(dir string) error {
	caPath, _, _, _ := TLSPaths(dir)
	caCert, leafCert, _, err := readMaterial(dir)
	if err != nil {
		return err
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	if _, err := leafCert.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		return fmt.Errorf("tls: leaf %s tidak terverifikasi terhadap CA %s: %w", TLSCertFile, caPath, err)
	}
	return nil
}

// Status reads the observable TLS state. Absent material is not an
// error (Present=false); present-but-broken material is.
func Status(dir string) (TLSStatus, error) {
	if !HasTLSMaterial(dir) {
		return TLSStatus{}, nil
	}
	if err := ValidateMaterial(dir); err != nil {
		return TLSStatus{}, err
	}
	caPath, _, _, _ := TLSPaths(dir)
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return TLSStatus{}, fmt.Errorf("tls: baca %s: %w", caPath, err)
	}
	caCert, leafCert, _, err := readMaterial(dir)
	if err != nil {
		return TLSStatus{}, err
	}
	fp, err := CAFingerprintSHA256(caPEM)
	if err != nil {
		return TLSStatus{}, err
	}
	st := TLSStatus{
		Present:       true,
		CAExpiry:      caCert.NotAfter,
		LeafExpiry:    leafCert.NotAfter,
		CAFingerprint: fp,
	}
	for _, ip := range leafCert.IPAddresses {
		st.LeafIPs = append(st.LeafIPs, ip.String())
	}
	st.LeafDNS = append(st.LeafDNS, leafCert.DNSNames...)
	sort.Strings(st.LeafIPs)
	sort.Strings(st.LeafDNS)
	return st, nil
}

// CAFingerprintSHA256 is the SHA-256 of a CA certificate's DER bytes
// — the fingerprint operators compare when installing ca.crt.
func CAFingerprintSHA256(caCertPEM []byte) (string, error) {
	cert, err := parseCert(caCertPEM)
	if err != nil {
		return "", fmt.Errorf("tls: parse CA untuk fingerprint: %w", err)
	}
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:]), nil
}

// MaterialExpiry returns the CA and leaf NotAfter dates.
func MaterialExpiry(dir string) (caExpiry, leafExpiry time.Time, err error) {
	st, err := Status(dir)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return st.CAExpiry, st.LeafExpiry, nil
}

// DashboardTLSMode is the startup gate: (false, nil) = no material,
// serve HTTP; (true, nil) = valid material, serve HTTPS; (false, err)
// = material present but broken, FAIL TO START (fail-closed).
func DashboardTLSMode(dir string) (bool, error) {
	if !HasTLSMaterial(dir) {
		return false, nil
	}
	if err := ValidateMaterial(dir); err != nil {
		return false, err
	}
	return true, nil
}

// LoadServerCertificate reads the leaf pair for one TLS handshake.
// The dashboard's GetCertificate calls this per handshake, so a
// renewed leaf is picked up without a panel restart.
func LoadServerCertificate(dir string) (certPEM, keyPEM []byte, err error) {
	_, certPath, keyPath, _ := TLSPaths(dir)
	certPEM, err = os.ReadFile(certPath)
	if err != nil {
		return nil, nil, fmt.Errorf("tls: baca %s: %w", certPath, err)
	}
	keyPEM, err = os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("tls: baca %s: %w", keyPath, err)
	}
	return certPEM, keyPEM, nil
}

// writeAtomic writes data via temp file + rename in the same
// directory, so a crash never leaves a half-written cert or key.
func writeAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("tls: buat dir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tls-*")
	if err != nil {
		return fmt.Errorf("tls: temp untuk %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("tls: tulis %s: %w", path, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("tls: chmod %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("tls: tutup %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("tls: rename %s: %w", path, err)
	}
	return nil
}

// writeMetadata stores the init/regenerate record next to the certs.
func writeMetadata(dir string, meta TLSMetadata) error {
	_, _, _, metaPath := TLSPaths(dir)
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("tls: encode metadata: %w", err)
	}
	return writeAtomic(metaPath, append(raw, '\n'), 0o644)
}

// ReadMetadata loads tls.json; a missing file is not an error (older
// material predates it), it just means no remembered SANs.
func ReadMetadata(dir string) (TLSMetadata, error) {
	_, _, _, metaPath := TLSPaths(dir)
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return TLSMetadata{}, nil
		}
		return TLSMetadata{}, fmt.Errorf("tls: baca %s: %w", metaPath, err)
	}
	var meta TLSMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return TLSMetadata{}, fmt.Errorf("tls: parse %s: %w", metaPath, err)
	}
	return meta, nil
}

// writeLeafPair atomically replaces server.crt + server.key.
func writeLeafPair(dir string, certPEM, keyPEM []byte) error {
	_, certPath, keyPath, _ := TLSPaths(dir)
	if err := writeAtomic(certPath, certPEM, 0o644); err != nil {
		return err
	}
	if err := writeAtomic(keyPath, keyPEM, 0o600); err != nil {
		return err
	}
	return nil
}

// InitMaterial generates a CA + leaf and writes the full material.
// It refuses when a CA already exists — replacing a trust anchor is
// ReinitCA's job, with its explicit confirmation.
func InitMaterial(dir string, opts TLSInitOptions) error {
	caPath, _, _, _ := TLSPaths(dir)
	if _, err := os.Stat(caPath); err == nil {
		return fmt.Errorf("tls: CA sudah ada di %s — pakai reinit-ca bila memang mau ganti trust anchor (semua client harus install ulang)", caPath)
	}
	if len(opts.IPs) == 0 {
		return errors.New("tls: init butuh minimal 1 IP SAN")
	}
	caCert, caKey, err := GenerateCA(opts.CAYears)
	if err != nil {
		return err
	}
	leafCert, leafKey, err := IssueLeaf(caCert, caKey, opts.IPs, opts.DNSNames, opts.LeafDays)
	if err != nil {
		return err
	}
	if err := writeAtomic(caPath, caCert, 0o644); err != nil {
		return err
	}
	if err := writeAtomic(TLSCAKeyPath(dir), caKey, 0o600); err != nil {
		return err
	}
	if err := writeLeafPair(dir, leafCert, leafKey); err != nil {
		return err
	}
	meta := TLSMetadata{CAYears: opts.CAYears, LeafDays: opts.LeafDays, RenewBeforeDays: opts.RenewDays}
	for _, ip := range opts.IPs {
		meta.IPs = append(meta.IPs, ip.String())
	}
	meta.Names = append(meta.Names, opts.DNSNames...)
	return writeMetadata(dir, meta)
}

// RenewLeaf reissues the leaf when fewer than renewBeforeDays remain.
// It returns changed=false (a no-op, no restart needed) when the leaf
// is still comfortably valid. The new leaf keeps the old leaf's SANs;
// changing SANs is RegenerateLeaf's job.
func RenewLeaf(dir string, leafDays, renewBeforeDays int) (bool, error) {
	st, err := Status(dir)
	if err != nil {
		return false, err
	}
	if !st.Present {
		return false, errors.New("tls: belum ada material — jalankan tls init dulu")
	}
	if time.Until(st.LeafExpiry) > time.Duration(renewBeforeDays)*24*time.Hour {
		return false, nil
	}
	caPath, _, _, _ := TLSPaths(dir)
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return false, fmt.Errorf("tls: baca %s: %w", caPath, err)
	}
	_, leafCert, _, err := readMaterial(dir)
	if err != nil {
		return false, err
	}
	// The CA key is not stored next to the served material: issuance
	// needs it, so it lives in ca.key beside ca.crt (same dir, 0600,
	// never served). If it is missing, renew cannot proceed — loudly.
	caKeyPEM, err := os.ReadFile(TLSCAKeyPath(dir))
	if err != nil {
		return false, fmt.Errorf("tls: kunci CA %s hilang — renew tidak bisa jalan tanpa kunci CA (restore dari backup tls dir)", TLSCAKeyPath(dir))
	}
	leafCertPEM, leafKeyPEM, err := IssueLeaf(caPEM, caKeyPEM, leafCert.IPAddresses, leafCert.DNSNames, leafDays)
	if err != nil {
		return false, err
	}
	if err := writeLeafPair(dir, leafCertPEM, leafKeyPEM); err != nil {
		return false, err
	}
	return true, nil
}

// RegenerateLeaf issues a fresh leaf with the given SANs (for a
// changed server IP), always rewriting even when the old leaf is
// still valid. Metadata is updated to the new SANs.
func RegenerateLeaf(dir string, ips []net.IP, names []string, leafDays int) error {
	if len(ips) == 0 {
		return errors.New("tls: regenerate-leaf butuh minimal 1 IP SAN")
	}
	st, err := Status(dir)
	if err != nil {
		return err
	}
	if !st.Present {
		return errors.New("tls: belum ada material — jalankan tls init dulu")
	}
	caPath, _, _, _ := TLSPaths(dir)
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return fmt.Errorf("tls: baca %s: %w", caPath, err)
	}
	caKeyPEM, err := os.ReadFile(TLSCAKeyPath(dir))
	if err != nil {
		return fmt.Errorf("tls: kunci CA %s hilang — regenerate tidak bisa jalan tanpa kunci CA (restore dari backup tls dir)", TLSCAKeyPath(dir))
	}
	leafCertPEM, leafKeyPEM, err := IssueLeaf(caPEM, caKeyPEM, ips, names, leafDays)
	if err != nil {
		return err
	}
	if err := writeLeafPair(dir, leafCertPEM, leafKeyPEM); err != nil {
		return err
	}
	meta, err := ReadMetadata(dir)
	if err != nil {
		return err
	}
	meta.IPs = nil
	for _, ip := range ips {
		meta.IPs = append(meta.IPs, ip.String())
	}
	meta.Names = append(meta.Names[:0], names...)
	meta.LeafDays = leafDays
	return writeMetadata(dir, meta)
}

// ReinitCA replaces the whole trust anchor: new CA + new leaf with
// the given SANs. Every client must reinstall ca.crt afterwards —
// callers (CLI/API) confirm this explicitly before calling.
func ReinitCA(dir string, opts TLSInitOptions) error {
	if len(opts.IPs) == 0 {
		return errors.New("tls: reinit-ca butuh minimal 1 IP SAN")
	}
	caCert, caKey, err := GenerateCA(opts.CAYears)
	if err != nil {
		return err
	}
	leafCert, leafKey, err := IssueLeaf(caCert, caKey, opts.IPs, opts.DNSNames, opts.LeafDays)
	if err != nil {
		return err
	}
	caPath, _, _, _ := TLSPaths(dir)
	if err := writeAtomic(caPath, caCert, 0o644); err != nil {
		return err
	}
	if err := writeAtomic(TLSCAKeyPath(dir), caKey, 0o600); err != nil {
		return err
	}
	if err := writeLeafPair(dir, leafCert, leafKey); err != nil {
		return err
	}
	meta := TLSMetadata{CAYears: opts.CAYears, LeafDays: opts.LeafDays, RenewBeforeDays: opts.RenewDays}
	for _, ip := range opts.IPs {
		meta.IPs = append(meta.IPs, ip.String())
	}
	meta.Names = append(meta.Names, opts.DNSNames...)
	return writeMetadata(dir, meta)
}

// DisableMaterial renames the TLS dir aside (rollback to HTTP) and
// returns the new path. The material is kept, not deleted, so a
// mistaken disable is one rename away from recovery.
func DisableMaterial(dir string) (string, error) {
	if !HasTLSMaterial(dir) {
		return "", errors.New("tls: tidak ada material untuk di-disable")
	}
	dst := dir + ".disabled-" + time.Now().Format("20060102-150405")
	if err := os.Rename(dir, dst); err != nil {
		return "", fmt.Errorf("tls: rename %s ke %s: %w", dir, dst, err)
	}
	return dst, nil
}

// parseIPList parses comma/space-separated IP strings, failing loudly
// on the first unparseable entry (a typo'd SAN must never become a
// silently narrower certificate).
func parseIPList(raw []string) ([]net.IP, error) {
	var out []net.IP
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		ip := net.ParseIP(s)
		if ip == nil {
			return nil, fmt.Errorf("tls: IP tidak valid: %q", s)
		}
		out = append(out, ip)
	}
	return out, nil
}

// dedupIPs appends 127.0.0.1 (loopback dashboard access) and drops
// duplicates, preserving order.
func dedupIPs(ips []net.IP) []net.IP {
	seen := map[string]bool{}
	out := make([]net.IP, 0, len(ips)+1)
	for _, ip := range ips {
		if seen[ip.String()] {
			continue
		}
		seen[ip.String()] = true
		out = append(out, ip)
	}
	loop := net.ParseIP("127.0.0.1")
	if !seen[loop.String()] {
		out = append(out, loop)
	}
	return out
}

// DetectLocalIPs returns the machine's up, non-loopback IPv4
// addresses (link-local 169.254/16 excluded: it is not a stable LAN
// identity). Empty + nil error means: nothing detected.
func DetectLocalIPs() ([]net.IP, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("tls: daftar interface: %w", err)
	}
	var out []net.IP
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch a := addr.(type) {
			case *net.IPNet:
				ip = a.IP
			case *net.IPAddr:
				ip = a.IP
			}
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			if v4 := ip.To4(); v4 != nil {
				out = append(out, v4)
			}
		}
	}
	return out, nil
}

// ResolveInitSANs decides the leaf SANs for init: explicit IPs win;
// otherwise the machine's detected LAN IPs; 127.0.0.1 is always
// added. Configured-but-empty AND detected-empty fails loudly — a
// loopback-only certificate would serve nobody on the LAN.
func ResolveInitSANs(configuredIPs, configuredNames []string) (ips []net.IP, names []string, err error) {
	ips, err = parseIPList(configuredIPs)
	if err != nil {
		return nil, nil, err
	}
	if len(ips) == 0 {
		detected, derr := DetectLocalIPs()
		if derr != nil {
			return nil, nil, derr
		}
		if len(detected) == 0 {
			return nil, nil, errors.New("tls: tidak ada IP yang dikonfigurasi dan tidak ada IP LAN yang terdeteksi — isi --ips atau tls_ips dulu")
		}
		ips = detected
	}
	return dedupIPs(ips), configuredNames, nil
}

// ResolveRegenSANs decides the leaf SANs for regenerate/reinit:
// explicit flags win, then the remembered metadata SANs, then the
// current leaf's SANs. A SAN-less result fails loudly.
func ResolveRegenSANs(dir string, flagIPs, flagNames []string) (ips []net.IP, names []string, err error) {
	ips, err = parseIPList(flagIPs)
	if err != nil {
		return nil, nil, err
	}
	if len(ips) > 0 {
		return dedupIPs(ips), flagNames, nil
	}
	meta, err := ReadMetadata(dir)
	if err != nil {
		return nil, nil, err
	}
	if len(meta.IPs) > 0 {
		ips, err = parseIPList(meta.IPs)
		if err != nil {
			return nil, nil, fmt.Errorf("tls: SAN metadata rusak: %w", err)
		}
		return dedupIPs(ips), meta.Names, nil
	}
	st, err := Status(dir)
	if err != nil {
		return nil, nil, err
	}
	if !st.Present {
		return nil, nil, errors.New("tls: belum ada material — jalankan tls init dulu")
	}
	ips, err = parseIPList(st.LeafIPs)
	if err != nil {
		return nil, nil, err
	}
	return dedupIPs(ips), st.LeafDNS, nil
}
