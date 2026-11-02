package app

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/gojalifs/spanel-plus/internal/core"
)

// TLSInfo is the JSON shape of GET /api/v1/tls/status (and the base
// of the TLS action responses). Times are zero when no material
// exists; RenewError/RenewAt describe the last failed auto-renew.
type TLSInfo struct {
	Enabled       bool      `json:"enabled"`
	CAPresent     bool      `json:"ca_present"`
	CAExpiry      time.Time `json:"ca_expiry,omitempty"`
	LeafExpiry    time.Time `json:"leaf_expiry,omitempty"`
	CAFingerprint string    `json:"ca_fingerprint,omitempty"`
	LeafIPs       []string  `json:"leaf_ips,omitempty"`
	LeafDNS       []string  `json:"leaf_dns,omitempty"`
	ApachePort    int       `json:"apache_port"`
	TLSApache     bool      `json:"tls_apache"`
	RenewError    string    `json:"renew_error,omitempty"`
	RenewAt       time.Time `json:"renew_at,omitempty"`
	Warning       string    `json:"warning,omitempty"`
}

// TLSRenewResult describes a leaf renewal: whether the leaf actually
// changed, and whether Apache picked it up. A failed Apache restart
// does NOT fail the renewal itself (the leaf is renewed; the
// dashboard already serves it via hot-swap) — it is reported, so the
// operator retries the restart instead of wondering why :443 still
// serves the old leaf.
type TLSRenewResult struct {
	Changed         bool      `json:"changed"`
	LeafExpiry      time.Time `json:"leaf_expiry"`
	ApacheRestarted bool      `json:"apache_restarted"`
	ApacheError     string    `json:"apache_error,omitempty"`
}

// TLSMaterialDir resolves where this panel's TLS material lives.
func (a *Application) TLSMaterialDir() string {
	return a.cfg.EffectiveTLSDir()
}

// TLSInfo reads the observable TLS state. Present-but-broken material
// is an error, not a silent "disabled".
func (a *Application) TLSInfo() (TLSInfo, error) {
	st, err := core.Status(a.cfg.EffectiveTLSDir())
	if err != nil {
		return TLSInfo{}, err
	}
	info := TLSInfo{
		Enabled:       st.Present,
		CAPresent:     st.Present,
		CAExpiry:      st.CAExpiry,
		LeafExpiry:    st.LeafExpiry,
		CAFingerprint: st.CAFingerprint,
		LeafIPs:       st.LeafIPs,
		LeafDNS:       st.LeafDNS,
		ApachePort:    a.cfg.EffectiveTLSApachePort(),
		TLSApache:     a.cfg.TLSApache,
	}
	if msg, at := a.tlsRenewError(); msg != "" {
		info.RenewError, info.RenewAt = msg, at
	}
	return info, nil
}

// TLSCACert returns the public CA certificate for download. The CA
// private key is never reachable through the API.
func (a *Application) TLSCACert() ([]byte, error) {
	caPath, _, _, _ := core.TLSPaths(a.cfg.EffectiveTLSDir())
	raw, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("tls: baca %s: %w", caPath, err)
	}
	return raw, nil
}

// TLSInit creates the CA + leaf. Empty ips/names fall back to the
// panel.json lists, then to LAN detection (see core.ResolveInitSANs).
func (a *Application) TLSInit(ips, names []string, caYears, leafDays int) (TLSInfo, error) {
	dir := a.cfg.EffectiveTLSDir()
	resolvedIPs, resolvedNames, err := core.ResolveInitSANs(
		firstNonEmpty(ips, a.cfg.TLSIPs),
		firstNonEmpty(names, a.cfg.TLSNames),
	)
	if err != nil {
		return TLSInfo{}, err
	}
	caYears = defaultIfZero(caYears, a.cfg.EffectiveTLSCAYears())
	leafDays = defaultIfZero(leafDays, a.cfg.EffectiveTLSLeafDays())
	if err := core.InitMaterial(dir, core.TLSInitOptions{
		IPs: resolvedIPs, DNSNames: resolvedNames,
		CAYears: caYears, LeafDays: leafDays,
		RenewDays: a.cfg.EffectiveTLSRenewBeforeDays(),
	}); err != nil {
		return TLSInfo{}, err
	}
	info, err := a.TLSInfo()
	if err != nil {
		return TLSInfo{}, err
	}
	if caYears > 100 {
		info.Warning = fmt.Sprintf("CA %d tahun akan hidup lebih lama dari operatornya — catat prosedur rotasi untuk penerus", caYears)
	}
	return info, nil
}

// TLSRenew renews the leaf when due (no-op otherwise) and restarts
// Apache when the leaf changed, so :443 picks it up.
func (a *Application) TLSRenew() (TLSRenewResult, error) {
	dir := a.cfg.EffectiveTLSDir()
	changed, err := core.RenewLeaf(dir, a.cfg.EffectiveTLSLeafDays(), a.cfg.EffectiveTLSRenewBeforeDays())
	if err != nil {
		return TLSRenewResult{}, err
	}
	res := TLSRenewResult{Changed: changed}
	st, err := core.Status(dir)
	if err != nil {
		return TLSRenewResult{}, err
	}
	res.LeafExpiry = st.LeafExpiry
	if changed {
		res.ApacheRestarted, res.ApacheError = a.restartApacheForTLS()
	}
	return res, nil
}

// TLSRegenerateLeaf issues a fresh leaf with the given SANs (empty =
// remembered metadata, then the current leaf's SANs).
func (a *Application) TLSRegenerateLeaf(ips, names []string) (TLSRenewResult, error) {
	dir := a.cfg.EffectiveTLSDir()
	resolvedIPs, resolvedNames, err := core.ResolveRegenSANs(dir, ips, names)
	if err != nil {
		return TLSRenewResult{}, err
	}
	if err := core.RegenerateLeaf(dir, resolvedIPs, resolvedNames, a.cfg.EffectiveTLSLeafDays()); err != nil {
		return TLSRenewResult{}, err
	}
	res := TLSRenewResult{Changed: true}
	st, err := core.Status(dir)
	if err != nil {
		return TLSRenewResult{}, err
	}
	res.LeafExpiry = st.LeafExpiry
	res.ApacheRestarted, res.ApacheError = a.restartApacheForTLS()
	return res, nil
}

// TLSReinitCA replaces the whole trust anchor. Callers confirm
// explicitly first: every client must reinstall ca.crt afterwards.
func (a *Application) TLSReinitCA(ips, names []string, caYears int) (TLSRenewResult, error) {
	dir := a.cfg.EffectiveTLSDir()
	var resolvedIPs []net.IP
	var resolvedNames []string
	var err error
	if core.HasTLSMaterial(dir) {
		resolvedIPs, resolvedNames, err = core.ResolveRegenSANs(dir, ips, names)
	} else {
		resolvedIPs, resolvedNames, err = core.ResolveInitSANs(
			firstNonEmpty(ips, a.cfg.TLSIPs),
			firstNonEmpty(names, a.cfg.TLSNames),
		)
	}
	if err != nil {
		return TLSRenewResult{}, err
	}
	caYears = defaultIfZero(caYears, a.cfg.EffectiveTLSCAYears())
	if err := core.ReinitCA(dir, core.TLSInitOptions{
		IPs: resolvedIPs, DNSNames: resolvedNames,
		CAYears: caYears, LeafDays: a.cfg.EffectiveTLSLeafDays(),
		RenewDays: a.cfg.EffectiveTLSRenewBeforeDays(),
	}); err != nil {
		return TLSRenewResult{}, err
	}
	res := TLSRenewResult{Changed: true}
	st, err := core.Status(dir)
	if err != nil {
		return TLSRenewResult{}, err
	}
	res.LeafExpiry = st.LeafExpiry
	res.ApacheRestarted, res.ApacheError = a.restartApacheForTLS()
	return res, nil
}

// restartApacheForTLS restarts the managed Apache after a leaf change,
// unless Apache TLS is disabled (no vhost to refresh) or no Apache
// manager is attached (degraded mode: the leaf is still renewed).
func (a *Application) restartApacheForTLS() (bool, string) {
	if !a.cfg.TLSApache {
		return false, ""
	}
	if a.apache == nil {
		return false, "apache tidak terpasang di aplikasi (restart dilewati)"
	}
	if err := a.apache.Restart(); err != nil {
		return false, err.Error()
	}
	return true, ""
}

// tlsRenewError returns the last auto-renew failure, if any.
func (a *Application) tlsRenewError() (string, time.Time) {
	a.tlsRenewMu.Lock()
	defer a.tlsRenewMu.Unlock()
	return a.tlsRenewErr, a.tlsRenewAt
}

func (a *Application) setTLSRenewError(msg string) {
	a.tlsRenewMu.Lock()
	defer a.tlsRenewMu.Unlock()
	a.tlsRenewErr = msg
	a.tlsRenewAt = time.Now()
}

func (a *Application) clearTLSRenewError() {
	a.tlsRenewMu.Lock()
	defer a.tlsRenewMu.Unlock()
	a.tlsRenewErr = ""
	a.tlsRenewAt = time.Time{}
}

// tlsRenewLoop checks at startup and every 24h whether the leaf is
// due for renewal. A failure is loud (panel log) and visible (the
// TLS status carries it for the dashboard banner); it never blocks
// startup and never retries in a tight loop.
func (a *Application) tlsRenewLoop(ctx context.Context) {
	a.tlsRenewCheck()
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.tlsRenewCheck()
		}
	}
}

func (a *Application) tlsRenewCheck() {
	dir := a.cfg.EffectiveTLSDir()
	if !core.HasTLSMaterial(dir) {
		return
	}
	res, err := a.TLSRenew()
	if err != nil {
		a.setTLSRenewError(err.Error())
		a.logger.Error("TLS auto-renew gagal", "err", err)
		return
	}
	a.clearTLSRenewError()
	if res.Changed {
		a.logger.Info("TLS leaf diperbarui otomatis", "leaf_expiry", res.LeafExpiry)
		if !res.ApacheRestarted && res.ApacheError != "" {
			a.logger.Warn("TLS leaf diperbarui tapi restart Apache gagal", "err", res.ApacheError)
		}
	}
}

// getCertificate loads the current leaf pair for one TLS handshake.
// Reading per handshake is deliberate: a renewed leaf is served
// without restarting the panel.
func (a *Application) getCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	certPEM, keyPEM, err := core.LoadServerCertificate(a.cfg.EffectiveTLSDir())
	if err != nil {
		return nil, err
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("tls: pasangan server.crt/server.key tidak valid: %w", err)
	}
	return &cert, nil
}

func firstNonEmpty(flag, cfg []string) []string {
	if len(flag) > 0 {
		return flag
	}
	return cfg
}

func defaultIfZero(v, def int) int {
	if v != 0 {
		return v
	}
	return def
}
