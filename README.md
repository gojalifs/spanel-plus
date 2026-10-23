# SPanel+

Admin panel untuk Windows — cPanel-like, untuk pemakaian internal perusahaan.
Satu instance Apache, routing berbasis path (`IP/nama-proyek`, URL aplikasi tidak berubah),
multi-PHP per proyek via `mod_fcgid` (PHP `php-cgi.exe` per versi, build NTS, `mod_php` mati),
Laravel dipetakan `Alias` langsung ke folder `public` (`/public` hilang dari URL).
Panel-nya sendiri: Go, satu binary — Windows Service native + web UI ter-embed + tool CLI
dari core yang sama, termasuk supervisor proses CLI per proyek (queue/scheduler/artisan
memakai `php.exe` versi proyek itu).

Rencana lengkap & keputusan terkunci (D1–D10, milestone M0–M6, requirement automated
testing): [`docs/plan-fase-1.md`](docs/plan-fase-1.md)

## Status

**Fase 1 — M0 GO (terkunci oleh max, 2026-10-24). M1 skeleton Go selesai
dibangun, menunggu smoke test service di mesin Windows.**

- M0 membuktikan desain di mesin uji: satu Apache 2.4.69 VS18 +
  `mod_fcgid` 2.3.10 + PHP 8.2.35 & 8.3.36 NTS x64 melayani beberapa
  versi PHP dari satu port; penyebab kegagalan awal terdiagnosis tuntas
  (port 80 diduduki IIS, lalu `DirectoryIndex index.php` yang hilang —
  keduanya sudah diperbaiki di kit).
- M1 (repo Go): satu binary `panel` yang bisa jalan sebagai console &
  Windows Service, UI skeleton ter-embed di port panel (default 8888),
  subcommand `doctor` versi awal, logging terstruktur (JSON) ke
  `logs/panel.log`. Unit test lulus & cross-compile Windows terverifikasi
  di VM Muse; kriteria service M1 dibuktikan lewat
  [`docs/m1-smoke-test.md`](docs/m1-smoke-test.md) di mesin Windows.

## Menjalankan M0

Ekstrak/salin folder `m0/` dari repo ini, lalu di Git Bash sebagai Administrator:

```bash
cd m0/scripts
bash setup-m0.sh          # unduh + pasang + patch, idempotent, fail-loud
/c/m0/Apache24/bin/httpd.exe -w   # jalankan Apache (jendela terpisah)
bash verify-m0.sh         # cek K1-K5
bash diagnose-m0.sh       # bila ada FAIL: output diagnosa lengkap
```

Detail: [`m0/README-M0.md`](m0/README-M0.md)

## M1 — Skeleton Go

Prasyarat: Go **1.24+** (satu-satunya dependensi pihak ketiga:
`golang.org/x/sys` untuk Windows Service).

```bash
# build (Git Bash di Windows menghasilkan panel.exe)
go build -o panel.exe ./cmd/panel

# perintah
panel.exe version
panel.exe doctor              # cek path Apache, httpd -v, mod_fcgid, port, data dir
panel.exe serve               # console; bila dimulai SCM, jalan sebagai Windows Service
panel.exe service install     # Administrator: daftar service, start otomatis saat boot
panel.exe service start|stop|status|uninstall
```

- UI/API: `http://127.0.0.1:8888` — `GET /` (UI ter-embed),
  `GET /healthz`, `GET /api/v1/status`, `GET /api/v1/doctor`.
- Konfigurasi opsional: `panel.json` di sebelah binary (lihat
  `panel.example.json`); `apache_home` kosong = auto-discovery
  (`C:\xampp\apache`, `C:\Apache24`, `C:\m0\Apache24`).
- Semua file panel (`data/`, `logs/`) di folder binary — tidak ada
  yang ditulis ke folder proyek (Plan D9).

Struktur paket (SOLID, Plan 10.1 — core tidak tahu HTTP/CLI/SCM):

```
cmd/panel/          entrypoint: wiring dependensi saja
internal/core/      logika inti + interface port (CommandRunner, FileProbe, PortProbe, Clock, ServiceController) + doctor
internal/app/       komposisi aplikasi & lifecycle serve
internal/httpapi/   handler HTTP tipis + web UI ter-embed
internal/cli/       dispatcher subcommand tipis
internal/platform/  implementasi OS: exec, probe filesystem/port, Windows Service (x/sys)
internal/config/    panel.json (default, validasi, error keras bila file rusak)
internal/logging/   slog terstruktur ke file (+ console di mode console)
```

Test & CI:

```bash
bash scripts/test.sh   # gofmt check -> go vet -> go test ./... -> build (+ cross-compile Windows bila bukan Windows)
```

CI GitHub Actions menjalankan tahap yang sama di `windows-latest` +
`ubuntu-latest` tiap push/PR, sesuai Plan Bagian 10.3 — file
workflow-nya (`.github/workflows/ci.yml`) **belum masuk repo**: push
pertama ditolak GitHub (`403 Resource not accessible by integration`)
karena instalasi GitHub App Muse belum punya izin *Workflows*.
File-nya siap di staging Muse; masuk repo begitu izin itu diberikan
(atau di-commit manual dari mesin Windows). Cakupan unit test saat
M1: core/doctor 95%, httpapi 91%, config 86%, logging 82%, cli 76%.
