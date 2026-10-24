# M5 — Smoke test CLI parity + auth + doctor di mesin Windows

M5 dinyatakan selesai bila kriteria plan terbukti: **dari CLI saja,
tanpa membuka UI, layanan Apache yang dihentikan dari luar panel
dipulihkan** — dan semua perubahan tercatat di audit log. Prasyarat:
panel v0.6.1-m5, dan panel sedang jalan (service atau console `serve`).
Perintah state CLI di M5 berbicara ke panel lewat HTTP API, jadi
**panel harus jalan dulu** — ini perubahan dari smoke M2, yang
memakai CLI langsung ke registry. Bila panel mati, CLI gagal keras
dengan petunjuk menyalakannya, bukan jalan pintas lokal.

## 0. Setup admin (sekali, di server)

- [ ] Di folder panel, jalankan di console:
      `panel admin set-password` — isi password baru 2x (minimal 8 karakter).
- [ ] Periksa `data\admin.json` ada dan isinya HANYA hash bcrypt
      (tidak ada password mentah di file mana pun).
- [ ] `panel login` — isi password → "Login berhasil".
- [ ] Buka dashboard: form **Login admin** tampil bila belum login;
      sesudah login, dashboard tampil dan tombol **Logout** ada.
- [ ] `panel logout`, lalu `panel project list` → ditolak dengan
      petunjuk `panel login`. Login lagi untuk langkah berikut.

## 1. Paritas CLI ↔ UI

- [ ] `panel project list` menampilkan proyek yang sama dengan tabel
      dashboard; `panel php list` sama dengan tabel PHP dashboard.
- [ ] `panel process list` menampilkan status runtime (running/PID/
      restart) yang sama dengan tabel Proses CLI dashboard — bukan
      lagi sekadar definisi seperti di M4.
- [ ] `panel process stop <proyek> <nama>` lalu `panel process start
      <proyek> <nama>`: status di dashboard berubah sesuai.
- [ ] `panel process log <proyek> <nama>` menampilkan isi yang sama
      dengan penonton log dashboard.
- [ ] `panel project config <proyek>`, `panel project warnings
      <proyek>`, `panel project preview <proyek> <versi>` menjawab
      sama dengan panel Config / badge peringatan / preview diff di UI.

## 2. Kriteria utama: skenario darurat (tanpa UI)

- [ ] Dari luar panel (Services / `sc stop Apache2.4`), hentikan
      service Apache.
- [ ] `panel apache status` → **stopped**.
- [ ] `panel doctor` → baris `apache-service` **FAIL** dengan
      petunjuk `panel apache restart`; baris lain tetap PASS
      (httpd-syntax, mod-fcgid, PHP, proyek).
- [ ] `panel apache restart` → service kembali **running**;
      `panel apache status` mengonfirmasi, dan situs proyek menjawab
      lagi (buka `http://localhost/<proyek>` atau `?check`).
- [ ] Selama langkah ini UI tidak dibuka sama sekali.

## 3. Doctor menyeluruh

- [ ] `panel doctor` menampilkan, selain cek lama: `httpd-syntax`
      (hasil `httpd -t`), `apache-service`, satu baris per versi PHP
      terdaftar, dan satu baris per proyek (folder + Laravel
      `storage/` + `.env` bila ada).
- [ ] Uji satu FAIL jujur: ganti sementara nama folder satu proyek
      dummy → doctor FAIL di baris proyek itu; kembalikan namanya →
      PASS lagi.

## 4. Audit log

- [ ] Sesudah langkah 1–2, buka `data\audit.log`: ada baris JSON per
      aksi ubah-state (login, restart Apache, start/stop proses)
      dengan actor + waktu + aksi. Perintah baca (`project list`,
      `doctor`) TIDAK tercatat — log harus tetap pendek.

## 5. Catatan

- Sesi CLI tersimpan di `data\.cli-session` (0600 di Linux; di
  Windows hanya bisa dibaca operator lokal). Sesi berlaku 10 menit inactivity (sliding — aktivitas me-reset timer);
  sesudah panel di-restart, semua sesi mati dan login ulang
  diperlukan (by design).
- Cookie sesi TIDAK Secure karena panel disajikan via HTTP di LAN
  internal — sesuai rekomendasi plan, firewall port panel (8888)
  khusus LAN, jangan dibuka ke internet.
- Bila semua langkah di atas PASS, M5 lulus — dan smoke M1–M4 yang
  tertunda (service, CLI M2, UI M3, supervisor M4) tetap dicatat
  sebagai utang terpisah di bawah sampai dijalankan.

## 6. Login AD dummy (M5-AD, tanpa DC sungguhan)

Prasyarat: `panel.json` di folder yang sama dengan `panel.exe` berisi:

```json
{
  "ad_enabled": true, "ad_server": "dummy", "ad_domain": "siws.local",
  "ad_base_dn": "DC=siws,DC=local", "ad_group_dn": "CN=IS,DC=siws,DC=local",
  "ad_dummy_users": [
    {"username": "budi", "password_hash": "<hash dari panel admin hash-password>", "groups": ["IS"]},
    {"username": "andi", "password_hash": "<hash>", "groups": ["HRD"]}
  ]
}
```

Langkah:

1. `panel admin hash-password` → tempel hash-nya ke `ad_dummy_users`
   (satu untuk budi, satu untuk andi). Restart panel sesudah edit `panel.json`.
2. `panel doctor` → baris `ad-config` harus `warn` dengan keterangan
   "AD dummy mode aktif" (peringatan ini sengaja: dummy bukan AD sungguhan).
3. Buka UI → toggle login menampilkan **Akun panel** dan **Akun AD**.
   Pilih Akun AD, login sebagai `andi` → harus DITOLAK dengan pesan generik
   ("username/password salah atau bukan anggota grup IT").
4. Login sebagai `budi` → harus BERHASIL; dashboard terbuka; `data/audit.log`
   mencatat login dengan actor `ad:budi` (bukan `admin`).
5. `panel login --ad --username budi` dari CLI → harus BERHASIL; perintah
   state berikutnya (mis. `panel project list`) harus jalan; `panel logout`
   menutup sesi.
6. Login lokal (`panel login` / Akun panel di UI) tetap jalan berdampingan
   dengan login AD — ini akun break-glass bila AD bermasalah.
7. Sesi idle (manual, butuh 11 menit): login, diam 11 menit tanpa klik →
   request berikutnya harus 401 + form login muncul lagi. Sebaliknya,
   klik-klik menu di dalam jendela 10 menit TIDAK boleh me-logout.

Catatan: nilai dummy (siws.local / grup IS) hanya placeholder. Sebelum
produksi, ganti `ad_server` ke `ldaps://<fqdn-dc>:636`, sesuaikan
`ad_base_dn`/`ad_group_dn` dengan DN asli (cek di ADUC), hapus
`ad_dummy_users`, dan uji ulang langkah 3–4 terhadap DC sungguhan.
