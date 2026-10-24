# SPanel+

Admin panel untuk Windows — cPanel-like, untuk pemakaian internal perusahaan.
Satu instance Apache, routing berbasis path (`IP/nama-proyek`, URL aplikasi tidak berubah),
multi-PHP per proyek via `mod_fcgid` (PHP `php-cgi.exe` per versi, build NTS, `mod_php` mati),
Laravel dipetakan `Alias` langsung ke folder `public` (`/public` hilang dari URL).
Panel-nya sendiri: Go, satu binary — aplikasi desktop (jendela WebView2 dari exe) +
Windows Service native + UI ter-embed + tool CLI dari core yang sama, termasuk
supervisor proses CLI per proyek (queue/scheduler/artisan memakai `php.exe`
versi proyek itu).

Rencana lengkap & keputusan terkunci (D1–D10, milestone M0–M6, requirement automated
testing): [`docs/plan-fase-1.md`](docs/plan-fase-1.md)

## Status

**Fase 1 — M0 GO (terkunci oleh max, 2026-10-24). M1 skeleton Go +
mode desktop terbukti di mesin Windows. M2 (registry + ConfigGen +
pipa apply) selesai dibangun, menunggu smoke test end-to-end di mesin
Windows ([`docs/m2-smoke-test.md`](docs/m2-smoke-test.md)).**

- M0 membuktikan desain di mesin uji: satu Apache 2.4.69 VS18 +
  `mod_fcgid` 2.3.10 + PHP 8.2.35 & 8.3.36 NTS x64 melayani beberapa
  versi PHP dari satu port; penyebab kegagalan awal terdiagnosis tuntas
  (port 80 diduduki IIS, lalu `DirectoryIndex index.php` yang hilang —
  keduanya sudah diperbaiki di kit).
- M1 (repo Go): satu binary `panel` yang bisa jalan sebagai console &
  Windows Service, UI skeleton ter-embed di port panel (default 8888),
  subcommand `doctor` versi awal, logging terstruktur (JSON) ke
  `logs/panel.log`. Unit test lulus & cross-compile Windows terverifikasi
  di VM Muse; mode console + UI + doctor (lawan Apache M0) sudah
  terbukti dari screenshot mesin Windows max 2026-10-24 — sisa kriteria
  service dibuktikan lewat
  [`docs/m1-smoke-test.md`](docs/m1-smoke-test.md).
- **M2 (v0.3.0-m2):** registry JSON (tulis atomik, validasi struktur +
  lingkungan — registri invalid tidak pernah sampai ke disk/Apache),
  ConfigGen deterministik per tipe proyek (golden-file tested, pola
  persis config M0 yang terbukti), dan pipa apply yang aman:
  snapshot → tulis config → `httpd -t` → restart → health check →
  commit registry; gagal di tengah = rollback otomatis + restart ke
  config lama, dengan pesan error yang menyebut tahapnya. Perintah:
  `project add|set-php|enable|disable|list`, `php add|list|remove`,
  `apache test|status|restart`.
- **Mode desktop (v0.2.0-m1, keputusan max 2026-10-24):** panel dibuka
  sebagai aplikasi desktop, bukan tab browser — double-click
  `panel.exe` (atau `panel app`) membuka jendela WebView2 berisi UI
  yang sama. Bila service panel sudah jalan (mis. dari boot), jendela
  memakai server itu; bila belum, server in-process dinyalakan dan
  dimatikan bersih saat jendela ditutup. Revisi D7 di plan.

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

Prasyarat build: Go **1.24+**. Prasyarat jalan: WebView2 Runtime
(sudah termasuk di Windows 10/11 lewat Microsoft Edge) untuk mode
desktop. Dependensi pihak ketiga: `golang.org/x/sys` (Windows
Service) dan `github.com/jchv/go-webview2` (jendela desktop, pure Go
tanpa CGO).

```bash
# build (Git Bash di Windows menghasilkan panel.exe)
go build -o panel.exe ./cmd/panel

# perintah
panel.exe                     # = panel app: buka aplikasi desktop (atau double-click exe-nya)
panel.exe app                 # sama: jendela desktop WebView2
panel.exe version
panel.exe doctor              # cek path Apache, httpd -v, mod_fcgid, port, data dir
panel.exe serve               # server tanpa jendela; bila dimulai SCM, jalan sebagai Windows Service
panel.exe service install     # Administrator: daftar service, start otomatis saat boot
panel.exe service start|stop|status|uninstall
panel.exe php add --version 8.2 --path C:\panel\php\8.2
panel.exe php list|remove <versi>
panel.exe project add --name app --url /app --path C:\htdocs\app --type laravel --php 8.2
panel.exe project set-php app 8.3   # lewat pipa apply penuh (rollback otomatis bila gagal)
panel.exe project enable|disable|list
panel.exe apache test|status|restart
```

Registry proyek & PHP tinggal di `data/registry.json`; config Apache
hasil generate di `data/generated/apache/*.conf` (Apache diarahkan ke
sana lewat satu baris `Include` — lihat `docs/m2-smoke-test.md`);
snapshot pra-apply di `data/backups/<timestamp>/` (retensi 20).

- UI/API tetap di `http://127.0.0.1:8888` (localhost) — `GET /` (UI
  ter-embed), `GET /healthz`, `GET /api/v1/status`,
  `GET /api/v1/doctor`. Mode desktop menampilkannya di jendela
  WebView2; membuka alamat itu dari browser juga tetap bisa selama
  `serve`/service jalan.
- Konfigurasi opsional: `panel.json` di sebelah binary (lihat
  `panel.example.json`); `apache_home` kosong = auto-discovery
  (`C:\xampp\apache`, `C:\Apache24`, `C:\m0\Apache24`).
- Semua file panel (`data/`, `logs/`) di folder binary — tidak ada
  yang ditulis ke folder proyek (Plan D9).

Struktur paket (SOLID, Plan 10.1 — core tidak tahu HTTP/CLI/SCM):

```
cmd/panel/          entrypoint: wiring dependensi saja
internal/core/      logika inti + interface port (CommandRunner, FileProbe, PortProbe, Clock, ServiceController, FileStore) + doctor, registry, configgen, pipa apply
internal/app/       komposisi aplikasi & lifecycle serve
internal/httpapi/   handler HTTP tipis + web UI ter-embed
internal/desktop/   launcher mode desktop (server in-process/reuse + jendela WebView2)
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
saat ini: core 89% (configgen & health check 100%, validasi registry
±100%, pipa apply 84% termasuk semua skenario rollback), desktop 96%,
httpapi 91%, config 86%, logging 82%, cli 68%.
