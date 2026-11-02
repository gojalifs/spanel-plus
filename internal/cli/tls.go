package cli

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/gojalifs/spanel-plus/internal/config"
	"github.com/gojalifs/spanel-plus/internal/core"
)

// TLS commands are file-direct on purpose (rencana HTTPS §2b): when
// the service fails to start on broken material, the dashboard is
// unreachable, so the CLI is the only recovery path. They never talk
// to the API and never need the panel running.

const tlsUsage = `TLS/HTTPS panel (private CA milik panel — client install ca.crt sekali):

  panel tls init [--ips a,b] [--names n] [--ca-years n] [--leaf-days n]
      Buat CA + leaf. Menolak bila CA sudah ada (pakai reinit-ca).
      Tanpa --ips: pakai tls_ips di panel.json, atau deteksi IP LAN
      (127.0.0.1 selalu ditambah). Sesudah init: restart service panel,
      lalu install ca.crt di client.
  panel tls status
      Expiry CA + leaf, fingerprint CA, SAN — dari file langsung.
  panel tls renew
      Terbitkan ulang leaf bila sisa < renew threshold (default 30 hari);
      no-op bila belum waktunya. Sesudah leaf berganti, restart
      apache-spanel agar Apache memakai leaf baru.
  panel tls regenerate-leaf [--ips a,b] [--names n]
      Leaf baru dengan SAN saat ini (untuk IP server berubah).
  panel tls reinit-ca --confirm [--ca-years n]
      CA BARU: semua client harus install ulang ca.crt. Wajib --confirm.
  panel tls export-ca <file>
      Salin ca.crt (publik) ke file.
  panel tls disable
      Pindahkan direktori tls ke samping (rollback ke HTTP); restart
      panel untuk kembali HTTP, re-apply proyek untuk hapus vhost :443.
`

// runTLS dispatches `panel tls <subcommand>`.
func (c *CLI) runTLS(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(c.deps.Out, tlsUsage)
		return 0
	}
	cfg, code := c.loadConfig()
	if code != 0 {
		return code
	}
	dir := cfg.EffectiveTLSDir()
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(c.deps.Out, tlsUsage)
		return 0
	case "status":
		return c.tlsStatus(cfg, dir)
	case "init":
		return c.tlsInit(cfg, dir, args[1:])
	case "renew":
		return c.tlsRenew(cfg, dir)
	case "regenerate-leaf":
		return c.tlsRegenerateLeaf(cfg, dir, args[1:])
	case "reinit-ca":
		return c.tlsReinitCA(cfg, dir, args[1:])
	case "export-ca":
		return c.tlsExportCA(dir, args[1:])
	case "disable":
		return c.tlsDisable(dir)
	default:
		fmt.Fprintf(c.deps.ErrOut, "subperintah tls tidak dikenal: %q\n\n%s", args[0], tlsUsage)
		return 2
	}
}

func (c *CLI) tlsErr(err error) int {
	fmt.Fprintf(c.deps.ErrOut, "ERROR: %v\n", err)
	return 1
}

// splitFlagList splits a comma-separated flag value (empty = nil).
func splitFlagList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// intFlag parses an optional integer flag (absent = 0, no error).
func intFlag(flags map[string]string, key string) (int, error) {
	raw, ok := flags[key]
	if !ok {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("flag --%s harus angka, dapat %q", key, raw)
	}
	return n, nil
}

// withDefault returns v when non-zero, else def.
func withDefault(v, def int) int {
	if v != 0 {
		return v
	}
	return def
}

// firstNonEmpty returns the flag list when given, else the configured
// list (flag > panel.json, per rencana).
func firstNonEmpty(flag, cfg []string) []string {
	if len(flag) > 0 {
		return flag
	}
	return cfg
}

func (c *CLI) tlsStatus(cfg config.Config, dir string) int {
	st, err := core.Status(dir)
	if err != nil {
		return c.tlsErr(err)
	}
	if !st.Present {
		fmt.Fprintf(c.deps.Out, "TLS tidak aktif — dashboard HTTP.\nJalankan: panel tls init --ips <IP-statik-server>\n")
		return 0
	}
	fmt.Fprintf(c.deps.Out, "TLS aktif\n")
	fmt.Fprintf(c.deps.Out, "  dir          : %s\n", dir)
	fmt.Fprintf(c.deps.Out, "  CA expiry    : %s\n", st.CAExpiry.Format("2006-01-02 15:04"))
	fmt.Fprintf(c.deps.Out, "  leaf expiry  : %s\n", st.LeafExpiry.Format("2006-01-02 15:04"))
	fmt.Fprintf(c.deps.Out, "  CA fingerprint (SHA-256): %s\n", st.CAFingerprint)
	fmt.Fprintf(c.deps.Out, "  leaf IP      : %s\n", strings.Join(st.LeafIPs, ", "))
	if len(st.LeafDNS) > 0 {
		fmt.Fprintf(c.deps.Out, "  leaf DNS     : %s\n", strings.Join(st.LeafDNS, ", "))
	}
	fmt.Fprintf(c.deps.Out, "  Apache HTTPS : %v (port %d)\n", cfg.TLSApache, cfg.EffectiveTLSApachePort())
	return 0
}

func (c *CLI) tlsInit(cfg config.Config, dir string, args []string) int {
	_, flags, err := parseFlags(args)
	if err != nil {
		return c.tlsErr(err)
	}
	caYears, err := intFlag(flags, "ca-years")
	if err != nil {
		return c.tlsErr(err)
	}
	leafDays, err := intFlag(flags, "leaf-days")
	if err != nil {
		return c.tlsErr(err)
	}
	ips, names, err := core.ResolveInitSANs(
		firstNonEmpty(splitFlagList(flags["ips"]), cfg.TLSIPs),
		firstNonEmpty(splitFlagList(flags["names"]), cfg.TLSNames),
	)
	if err != nil {
		return c.tlsErr(err)
	}
	caYears = withDefault(caYears, cfg.EffectiveTLSCAYears())
	leafDays = withDefault(leafDays, cfg.EffectiveTLSLeafDays())
	if caYears > 100 {
		fmt.Fprintf(c.deps.Out, "PERINGATAN: CA %d tahun akan hidup lebih lama dari operatornya — catat prosedur rotasi untuk penerus.\n", caYears)
	}
	if err := core.InitMaterial(dir, core.TLSInitOptions{
		IPs: ips, DNSNames: names,
		CAYears: caYears, LeafDays: leafDays,
		RenewDays: cfg.EffectiveTLSRenewBeforeDays(),
	}); err != nil {
		return c.tlsErr(err)
	}
	fmt.Fprintf(c.deps.Out, "TLS material dibuat di %s\n", dir)
	if err := c.printTLSStatus(dir); err != nil {
		return c.tlsErr(err)
	}
	fmt.Fprintf(c.deps.Out, "Langkah berikut:\n")
	fmt.Fprintf(c.deps.Out, "  1. Restart service panel (SPanelPlus) — dashboard jadi https://<IP>:%d\n", cfg.Port)
	fmt.Fprintf(c.deps.Out, "  2. Install ca.crt di client: panel tls export-ca spanel-ca.crt, lalu install ke Trusted Root (atau via GPO).\n")
	fmt.Fprintf(c.deps.Out, "  3. Apply proyek apa saja agar Apache dapat vhost :%d.\n", cfg.EffectiveTLSApachePort())
	return 0
}

// printTLSStatus prints the status block after a mutation (init,
// renew, regenerate, reinit) so the operator sees the new expiry.
func (c *CLI) printTLSStatus(dir string) error {
	st, err := core.Status(dir)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.deps.Out, "  CA expiry    : %s\n", st.CAExpiry.Format("2006-01-02 15:04"))
	fmt.Fprintf(c.deps.Out, "  leaf expiry  : %s\n", st.LeafExpiry.Format("2006-01-02 15:04"))
	fmt.Fprintf(c.deps.Out, "  CA fingerprint (SHA-256): %s\n", st.CAFingerprint)
	ipStrs := make([]string, 0, len(st.LeafIPs))
	ipStrs = append(ipStrs, st.LeafIPs...)
	fmt.Fprintf(c.deps.Out, "  leaf SAN     : %s\n", strings.Join(append(ipStrs, st.LeafDNS...), ", "))
	return nil
}

func (c *CLI) tlsRenew(cfg config.Config, dir string) int {
	changed, err := core.RenewLeaf(dir, cfg.EffectiveTLSLeafDays(), cfg.EffectiveTLSRenewBeforeDays())
	if err != nil {
		return c.tlsErr(err)
	}
	if !changed {
		st, err := core.Status(dir)
		if err != nil {
			return c.tlsErr(err)
		}
		fmt.Fprintf(c.deps.Out, "Leaf masih berlaku sampai %s — renew belum waktunya (no-op).\n", st.LeafExpiry.Format("2006-01-02 15:04"))
		return 0
	}
	fmt.Fprintf(c.deps.Out, "Leaf diperbarui.\n")
	if err := c.printTLSStatus(dir); err != nil {
		return c.tlsErr(err)
	}
	fmt.Fprintf(c.deps.Out, "Dashboard memakai leaf baru tanpa restart (hot-swap).\n")
	fmt.Fprintf(c.deps.Out, "Apache: restart agar memakai leaf baru — panel apache restart (bila panel jalan) atau Restart-Service apache-spanel.\n")
	return 0
}

func (c *CLI) tlsRegenerateLeaf(cfg config.Config, dir string, args []string) int {
	_, flags, err := parseFlags(args)
	if err != nil {
		return c.tlsErr(err)
	}
	ips, names, err := core.ResolveRegenSANs(dir, splitFlagList(flags["ips"]), splitFlagList(flags["names"]))
	if err != nil {
		return c.tlsErr(err)
	}
	if err := core.RegenerateLeaf(dir, ips, names, cfg.EffectiveTLSLeafDays()); err != nil {
		return c.tlsErr(err)
	}
	fmt.Fprintf(c.deps.Out, "Leaf baru diterbitkan.\n")
	if err := c.printTLSStatus(dir); err != nil {
		return c.tlsErr(err)
	}
	fmt.Fprintf(c.deps.Out, "Apache: restart agar memakai leaf baru — panel apache restart atau Restart-Service apache-spanel.\n")
	return 0
}

func (c *CLI) tlsReinitCA(cfg config.Config, dir string, args []string) int {
	// --confirm is a bare boolean (parseFlags only treats --disabled
	// as boolean), so strip it before flag parsing.
	confirm := false
	rest := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--confirm" {
			confirm = true
			continue
		}
		rest = append(rest, a)
	}
	if !confirm {
		fmt.Fprintf(c.deps.ErrOut, "DITOLAK: reinit-ca mengganti trust anchor — semua client harus install ulang ca.crt.\nJalankan lagi dengan --confirm bila memang itu yang dimaksud.\n")
		return 2
	}
	_, flags, err := parseFlags(rest)
	if err != nil {
		return c.tlsErr(err)
	}
	caYears, err := intFlag(flags, "ca-years")
	if err != nil {
		return c.tlsErr(err)
	}
	var ips []net.IP
	var names []string
	if core.HasTLSMaterial(dir) {
		ipList, nameList, err := core.ResolveRegenSANs(dir, splitFlagList(flags["ips"]), splitFlagList(flags["names"]))
		if err != nil {
			return c.tlsErr(err)
		}
		ips, names = ipList, nameList
	} else {
		ipList, nameList, err := core.ResolveInitSANs(
			firstNonEmpty(splitFlagList(flags["ips"]), cfg.TLSIPs),
			firstNonEmpty(splitFlagList(flags["names"]), cfg.TLSNames),
		)
		if err != nil {
			return c.tlsErr(err)
		}
		ips, names = ipList, nameList
	}
	caYears = withDefault(caYears, cfg.EffectiveTLSCAYears())
	if caYears > 100 {
		fmt.Fprintf(c.deps.Out, "PERINGATAN: CA %d tahun akan hidup lebih lama dari operatornya — catat prosedur rotasi untuk penerus.\n", caYears)
	}
	if err := core.ReinitCA(dir, core.TLSInitOptions{
		IPs: ips, DNSNames: names,
		CAYears: caYears, LeafDays: cfg.EffectiveTLSLeafDays(),
		RenewDays: cfg.EffectiveTLSRenewBeforeDays(),
	}); err != nil {
		return c.tlsErr(err)
	}
	fmt.Fprintf(c.deps.Out, "PERINGATAN KERAS: CA diganti — CA lama tidak berlaku lagi.\n")
	fmt.Fprintf(c.deps.Out, "Semua client HARUS install ulang ca.crt yang baru, kalau tidak browser menolak dashboard.\n")
	if err := c.printTLSStatus(dir); err != nil {
		return c.tlsErr(err)
	}
	fmt.Fprintf(c.deps.Out, "Apache: restart agar memakai leaf baru — panel apache restart atau Restart-Service apache-spanel.\n")
	return 0
}

func (c *CLI) tlsExportCA(dir string, args []string) int {
	pos, _, err := parseFlags(args)
	if err != nil {
		return c.tlsErr(err)
	}
	if len(pos) != 1 {
		fmt.Fprintf(c.deps.ErrOut, "usage: panel tls export-ca <file>\n")
		return 2
	}
	caPath, _, _, _ := core.TLSPaths(dir)
	raw, err := os.ReadFile(caPath)
	if err != nil {
		return c.tlsErr(fmt.Errorf("tls: baca %s: %w", caPath, err))
	}
	if err := os.WriteFile(pos[0], raw, 0o644); err != nil {
		return c.tlsErr(fmt.Errorf("tls: tulis %s: %w", pos[0], err))
	}
	fmt.Fprintf(c.deps.Out, "ca.crt disalin ke %s (sertifikat publik — aman dibagikan ke client).\n", pos[0])
	return 0
}

func (c *CLI) tlsDisable(dir string) int {
	dst, err := core.DisableMaterial(dir)
	if err != nil {
		return c.tlsErr(err)
	}
	fmt.Fprintf(c.deps.Out, "TLS di-disable: %s dipindah ke %s (material tidak dihapus).\n", dir, dst)
	fmt.Fprintf(c.deps.Out, "Restart service panel untuk kembali ke HTTP; re-apply proyek untuk menghapus vhost :443 dari Apache.\n")
	return 0
}
