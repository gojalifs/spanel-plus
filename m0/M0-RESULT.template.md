# Hasil M0 — isi & kirim balik ke Muse

- Tanggal uji:
- Mesin (Windows versi / laptop / VM / server uji):
- Nama file Apache ZIP (persis):
- Versi Apache (`httpd -v`):
- Nama file mod_fcgid ZIP (persis) + sumbernya:
- Versi VC build Apache & mod_fcgid (harus sama):
- Nama file PHP 8.2 ZIP (persis):  | `php -v`:
- Nama file PHP 8.3 ZIP (persis):  | `php -v`:
- Port yang dipakai (80 / lain):

| Kriteria | PASS/FAIL | Bukti (tempel output `?check` / status HTTP / log queue) |
|---|---|---|
| K1 dua versi PHP, satu Apache |  |  |
| K2 Laravel dummy tanpa `/public` |  |  |
| K3 `.env` 404 |  |  |
| K4 queue dummy pakai php.exe proyek (8.2) |  |  |
| K5 mod_fcgid cocok & tercatat |  |  |

- Output `verify-m0.sh` (tempel isi file `C:\m0\results\verify-*.txt`):
- Error / kejutan apa pun selama setup:
- Keputusan: GO ke M1 / NO-GO (alasannya):
