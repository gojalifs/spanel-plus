# Tutorial Smoke Test SPanel+ (gabungan M1–M5-AD)

Satu dokumen untuk seluruh smoke test Windows, dari setup sampai laporan.
Menggantikan checklist terpisah `m1-smoke-test.md` … `m5-smoke-test.md`
(isinya sama, tapi dokumen-dokumen itu masih menulis bentuk CLI lama dan
baris `Include` biasa — yang berlaku adalah dokumen ini).

Panel yang dites: **v0.6.2-m5** (dashboard harus menampilkan versi itu;
kalau masih 0.6.1 berarti exe lama yang kejalan). Akun dummy AD:
`budi` / `budi-kuat-1` (grup IS, harus lolos) dan `andi` / `andi-kuat-1`
(grup HRD, harus ditolak). Password admin adalah yang lu set saat setup.

Aturan main: satu bagian gagal = bagian itu belum lulus, tapi **lanjutkan
saja bagian lain** dan laporkan semuanya sekaligus di akhir. Jangan
berhenti di tengah seperti aturan lama — ini sesi gabungan.

## 0. Setup dulu (sekali, kalau belum)

Jalankan dari Git Bash sebagai Administrator (klik kanan Git Bash -> Run
as administrator), dari folder yang berisi `panel-v0.6.2-m5.exe` dan
script setup:

```bash
bash setup-panel-smoke-v5.sh
```

Script mengurus: Apache 2.4.69 + mod_fcgid, PHP 8.2 & 8.3, dummy M0,
`httpd.conf`, `panel.json` (AD dummy), password admin, service Apache
(`Apache2.4`) dan panel (`SPanelPlus`). Script berhenti keras
(`SETUP FAIL`) bila ada langkah gagal — tempel pesan itu ke Muse, jangan
diakali manual.

**Verifikasi setup lolos bila semua ini benar:**

- `panel.exe version` menampilkan v0.6.2-m5.
- `panel.exe doctor` menjelaskan tiap baris (satu-satunya WARN yang
  wajar: `panel-port`, by design).
- `http://127.0.0.1/app-php82/?check` membalas `php=8.2.x`.
- `http://127.0.0.1/app-php83/?check` membalas `php=8.3.x`.
- `http://127.0.0.1/app-laravel/` tampil tanpa perlu `/public`.
- `http://127.0.0.1/app-laravel/.env` membalas 404.
- Panel UI bisa dibuka di `http://127.0.0.1:8888`.

Catatan: baris Include untuk config generated panel di `httpd.conf`
harus `IncludeOptional`, bukan `Include` biasa — `Include` biasa
membuat `httpd -t` gagal bila direktori generated masih kosong
(sebelum apply pertama). Script setup sudah menulis yang benar;
kalau lu edit `httpd.conf` manual, pakai bentuk `IncludeOptional`.

## 1. M1 — Desktop + Windows Service

Semua perintah dari terminal Administrator, di folder `C:\Panel`.

**1a. Mode desktop:**

```bat
:: cukup double-click panel.exe, atau:
panel.exe app
```

- [ ] Jendela desktop SPanel+ terbuka (bukan tab browser), UI sama
      seperti mode web; jendela konsol bawaan double-click hilang.
- [ ] Tutup jendela -> panel berhenti bersih.

**1b. Service (dengan service panel terpasang & jalan, ulangi 1a):**

- [ ] Buka `panel.exe app` -> jendela tampil; tutup jendela -> service
      TETAP jalan (UI dari service masih menjawab).

**1c. Siklus service:**

```bat
panel.exe service install
panel.exe service status
panel.exe service start
panel.exe service status
```

- [ ] `install` sukses; service `SPanelPlus` muncul di `services.msc`
      dengan Startup type **Automatic**.
- [ ] Setelah `start`: status `running`, UI di
      `http://127.0.0.1:8888` bisa dibuka.
- [ ] **Uji boot:** restart mesin -> tanpa login/manual start, UI panel
      sudah bisa dibuka (service start otomatis).
- [ ] `panel.exe service stop` -> status `stopped`, port 8888 bebas.
- [ ] `panel.exe service uninstall` -> service hilang dari `services.msc`.
- [ ] Sesudah itu install + start lagi untuk bagian berikut (atau
      pakai `panel.exe serve` di console — sama saja, yang penting
      panel jalan).

## 2. M2 — CLI end-to-end (ganti PHP + rollback)

> PERHATIAN — bentuk CLI sudah berubah sejak M5. Dua hal yang
> berbeda dari dokumen M2 lama, dan yang berlaku adalah yang di sini:
>
> 1. Panel harus jalan + login dulu: semua perintah state sekarang
>    lewat API panel. Perintah tanpa login harus gagal dengan
>    petunjuk login, bukan jalan pintas lokal.
> 2. `php add` tidak lagi pakai flag `--version` — versi terdeteksi
>    dari binary (`php -v`).

```bat
panel.exe login
panel.exe php add --path C:\m0\php\8.2
panel.exe php add --path C:\m0\php\8.3
panel.exe php list
xcopy /E /I C:\m0\htdocs\app-php82 C:\m0\htdocs\app-m2
panel.exe project add --name app-m2 --url /app-m2 --path C:\m0\htdocs\app-m2 --type php-native --php 8.2
```

- [ ] `php list` menampilkan 8.2 & 8.3.
- [ ] `project add` berakhir `Apply OK` (snapshot tertulis, config
      ditulis, health check lulus).
- [ ] `curl http://127.0.0.1/app-m2/?check` membalas versi **8.2**.

**Ganti versi PHP (skenario kriteria):**

```bat
panel.exe project set-php app-m2 8.3
curl http://127.0.0.1/app-m2/?check
```

- [ ] `set-php` berakhir `Apply OK` dengan health check `/app-m2`.
- [ ] Balasan `?check` sekarang versi **8.3** — proyek yang sama,
      Apache yang sama, URL yang sama.

**Penolakan sebelum apply:**

```bat
panel.exe project set-php app-m2 7.4
```

- [ ] Ditolak: `php version "7.4" is not registered` — tidak ada
      restart, tidak ada perubahan (curl tetap 8.3).

**Rollback otomatis:**

```bat
mkdir C:\m0\htdocs\kosong
panel.exe project add --name kosong --url /kosong --path C:\m0\htdocs\kosong --type php-native --php 8.2
```

- [ ] `project add` GAGAL (tunggu ±5 detik health check) dengan pesan
      `health check /kosong` + `rolled back`.
- [ ] Sesudahnya: `panel.exe project list` TIDAK memuat `kosong`
      (registry tidak jadi commit), dan `?check` app-m2 tetap
      menjawab 8.3 (config lama hidup lagi persis).
- [ ] Folder snapshot ada di `C:\Panel\data\backups\` berisi
      `registry.json` + `generated\` versi sebelum percobaan.

## 3. M3 — Dashboard UI murni (tanpa CLI, tanpa edit config)

Buka panel dengan double-click `panel.exe` (mode desktop).
Prasyarat: bagian 2 sudah hijau.

**3a. Daftarkan PHP dari UI:**

Di kartu **Versi PHP**, isi folder `C:\m0\php\8.2` -> klik
**Daftarkan versi PHP**. Ulangi untuk `C:\m0\php\8.3`.

- [ ] Toast menyebut versi terdeteksi dari binary (mis. "8.2.x, NTS
      x64") dan status php.ini (`generated` bila folder belum punya
      php.ini, `existing` bila sudah ada — yang existing TIDAK boleh
      berubah isinya).
- [ ] Tabel Versi PHP memuat 8.2 & 8.3.
- [ ] Klik **Extension** pada satu versi -> daftar modul `php -m`
      tampil.

**3b. Tambah proyek dari UI:**

Di form **Tambah proyek**: nama `app-m3`, URL `/app-m3`, folder
`C:\m0\htdocs\app-m3` (salin dulu folder dummy:
`xcopy /E /I C:\m0\htdocs\app-php82 C:\m0\htdocs\app-m3`), tipe
PHP native, PHP 8.2, aktif -> **Tambah & apply**.

- [ ] Toast sukses (bukan error), proyek muncul di tabel.
- [ ] Klik URL-nya -> halaman dummy menjawab versi **8.2**.

**3c. Ganti PHP lewat preview diff (alur resmi):**

Di tabel proyek, ubah dropdown PHP `app-m3` ke **8.3**.

- [ ] Panel **Preview perubahan config** muncul: baris merah `-`
      memuat path php-cgi 8.2, baris hijau `+` memuat 8.3.
- [ ] Klik **Apply perubahan ini** -> toast sukses.
- [ ] Refresh halaman dummy -> versi **8.3**.

**3d. Peringatan:**

Siapkan dulu proyek Laravel dengan APP_URL bermasalah:

```bat
xcopy /E /I C:\m0\htdocs\app-laravel C:\m0\htdocs\app-laravel-bad
```

Edit `C:\m0\htdocs\app-laravel-bad\.env`: ubah APP_URL menjadi
`http://127.0.0.1/app-laravel-bad/public` (sengaja berakhiran
`/public`). Lalu tambahkan dari UI sebagai proyek Laravel
menunjuk folder itu.

- [ ] Kolom Peringatan proyek itu memuat badge `app_url_mismatch`
      (arahkan kursor ke badge untuk pesan lengkapnya).
- [ ] Proyek bersih tidak memuat badge apa pun.

**3e. Nonaktifkan & hapus:**

- [ ] Matikan toggle **Aktif** `app-m3` -> apply sukses, URL-nya 404.
- [ ] Nyalakan lagi -> URL hidup lagi.
- [ ] **Hapus** proyek `app-laravel-bad` -> hilang dari tabel &
      config (URL 404), folder proyeknya sendiri tetap utuh di disk.

## 4. M4 — Supervisor & Scheduler

Pakai proyek dummy (mis. `app-m3` dari bagian 3).

**Persiapan dummy (sekali):** di folder proyek, buat file `worker.php`:

```php
<?php
file_put_contents(__DIR__.'/worker-alive.txt', date('H:i:s')." mulai\n", FILE_APPEND);
while (true) { sleep(5); }
```

Dan pastikan file `artisan` dummy ada di folder proyek (untuk
scheduler): file PHP bernama `artisan` berisi
`<?php file_put_contents(__DIR__.'/schedule-alive.txt', date('H:i:s')."\n", FILE_APPEND); echo "ok\n"; ?>`
(versi lengkapnya ada di dummy `app-laravel` dari setup).

Lalu dari dashboard: **Edit proyek -> Proses CLI**:

- Template **queue Laravel**, lalu ubah perintahnya jadi
  `worker.php`, autostart centang -> Simpan & apply.
- Template **scheduler Laravel**, autostart centang -> Simpan & apply.

Jalankan panel sebagai service/console seperti biasa (supervisor
hidup selama panel jalan).

**4a. Autostart:**

- [ ] Buka/refresh dashboard: tabel **Proses CLI** memuat `queue`
      berstatus **running** dengan PID terisi.
- [ ] File log `data\projects\<proyek>\queue.log` berisi marker
      `--- panel start queue` + proyek benar-benar jalan
      (`worker-alive.txt` terbuat).

**4b. Kriteria utama: dimatikan paksa -> dinyalakan ulang:**

- [ ] Dari Task Manager, **End task** PID queue tadi.
- [ ] Dalam beberapa detik (backoff mulai 1 detik), dashboard
      menampilkan PID **baru** untuk queue, kolom Restart bertambah,
      status kembali **running**.
- [ ] `queue.log` memuat baris `--- panel exit code=...` untuk
      proses yang dimatikan, disusul marker start baru.

**4c. Stop dari panel itu final:**

- [ ] Klik **Stop** di dashboard -> status **stopped** dan TIDAK
      menyala lagi sendiri setelah 10 detik.
- [ ] Klik **Start** -> running lagi dengan PID baru.

**4d. Scheduler:**

- [ ] Biarkan ±2 menit: file `schedule-alive.txt` di folder proyek
      bertambah satu baris per menit.
- [ ] Tombol **Jalankan sekarang** pada baris scheduler -> toast
      exit 0, dan `data\projects\<proyek>\scheduler.log` memuat
      outputnya.

**4e. Perintah sekali jalan:**

- [ ] Box **Jalankan**: pilih proyek, perintah `artisan --version`
      (atau `worker.php` versi sekali-jalan) -> output tampil dengan
      penanda `[exit 0]`.

## 5. M5 — Auth + paritas CLI + skenario darurat

**5a. Login/logout:**

- [ ] Login/logout lewat UI dan CLI (`panel.exe login` /
      `panel.exe logout`) dua-duanya jalan.
- [ ] Tanpa login, endpoint API menjawab 401 + `auth_required`.

**5b. Paritas CLI:**

- [ ] Beberapa perintah CLI lewat panel yang jalan (`project list`,
      `process list`, `apache status`) hasilnya sama dengan UI.

**5c. Skenario darurat tanpa UI:**

- [ ] Hentikan Apache dari luar (services.msc), lalu dari CLI saja
      pulihkan (`panel.exe apache restart` + cek status) — tidak
      boleh perlu membuka UI.

**5d. Audit & doctor:**

- [ ] `C:\Panel\data\audit.log` memuat baris login/logout dan
      perubahan state dengan actor yang benar.
- [ ] `panel.exe doctor` menyeluruh menjelaskan tiap baris
      (httpd -t, service Apache, PHP, proyek).

## 6. M5-AD — Login direktori dummy

Prasyarat: `panel.json` sudah berisi blok AD dummy dari setup
(domain `siws.local`, grup `IS`). Kalau panel jalan sebelum blok itu
ditambahkan, restart panel dulu.

- [ ] `panel.exe doctor` -> baris `ad-config` berstatus **warn**
      "AD dummy mode aktif".
- [ ] UI login menampilkan toggle **Akun panel** dan **Akun AD**.
- [ ] Login Akun AD sebagai `budi` / `budi-kuat-1` -> lolos, masuk
      dashboard.
- [ ] Login Akun AD sebagai `andi` / `andi-kuat-1` -> ditolak dengan
      pesan generik (bukan anggota grup IT).
- [ ] `panel.exe login --ad --username budi` dari CLI -> lolos.
- [ ] Login Akun panel tetap jalan (break-glass tidak pernah mati
      karena AD).
- [ ] Audit mencatat actor `ad:budi` untuk login AD (bukan `admin`).

## Lapor balik

Tempel ke chat, per bagian (0–6): PASS/FAIL tiap kriteria, pesan error
persis kalau ada, plus:

- Output `panel.exe doctor` dan `panel.exe version`.
- Screenshot: dashboard (bagian 3), tabel Proses CLI sesudah kill PID
  (bagian 4), kartu login + toggle AD (bagian 6).
- Isi `queue.log` dan `scheduler.log` (bagian 4).
- Isi `C:\Panel\data\registry.json` (bagian 2).

## Troubleshooting cepat

| Gejala | Penyebab paling sering |
|---|---|
| `httpd -t` gagal `Could not open directory` / `No matches for the wildcard` pada baris generated | Baris Include panel harus `IncludeOptional`, bukan `Include` biasa (lihat bagian 0) |
| `httpd -t` gagal load `mod_fcgid.so` | VS build mod_fcgid != VS build Apache, atau x86 vs x64 campur — setup script memakai VS18 untuk keduanya |
| Halaman .php malah ke-download / tampil source | `SetHandler fcgid-script` / `FcgidWrapper` tidak kepakai — cek Include kepasang & tidak ada `php_module` |
| Kedua app tampil versi PHP sama | `FcgidWrapper` kedua Directory ketuker, atau php-cgi proses lama belum mati — restart Apache |
| `app-laravel` 403/404 | `Alias` tidak cocok dengan path fisik, atau `<Directory ...\public>` belum `Require all granted` |
| `.env` bisa dibuka (200) | Alias bocor ke root proyek — STOP, ini gagal kriteria; cek Alias hanya menunjuk ke `public` |
| `php.exe` / `httpd.exe` error VCRUNTIME140.dll | VC Redistributable x64 belum terpasang / perlu restart |
| Service Apache2.4 sudah ada tapi bukan dari `C:\m0` | Milik XAMPP/aplikasi lain — stop/uninstall dulu sebelum setup |
| Port 80 dipakai aplikasi lain | Cek `netstat -ano \| findstr :80`, stop dulu service itu (IIS/XAMPP/Skype) |
| Doctor WARN `panel-port` | By design, bukan kegagalan |
