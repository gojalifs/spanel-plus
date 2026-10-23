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

**Fase 1 — M0 (spike validasi go/no-go) sedang berjalan.**

- Setup otomatis bash (`m0/scripts/setup-m0.sh`, Git Bash/WSL) teruji mengunduh &
  memasang Apache 2.4.69 VS18 + mod_fcgid 2.3.10 + PHP 8.2.35 & 8.3.36 NTS x64.
- Terdiagnosis di mesin uji: port 80 awalnya diduduki Microsoft IIS (sudah dibebaskan),
  lalu `DirectoryIndex index.php` yang hilang (sudah diperbaiki di kit v6).
- Menunggu hasil `verify-m0.sh` final: kriteria K1–K5 (dua versi PHP dari satu Apache,
  Laravel dummy tanpa `/public`, `.env` 404, queue dummy, mod_fcgid tercatat).
  M1 (skeleton Go) tidak dimulai sebelum K1–K5 semua PASS.

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
