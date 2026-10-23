# M0 — Spike Validasi (Gerbang Go / No-Go)

Kit ini buat menjalankan **M0 persis seperti di Plan Fase 1 Bagian 7**, tanpa menulis kode panel dulu.
Kalau M0 gagal, kita berhenti dan evaluasi ulang desain — jangan lanjut M1.

Mesin: **Windows uji / laptop Windows lu**, BUKAN server produksi, BUKAN VM Muse (Linux).
Semua serba portabel & terisolasi di `C:\m0\` — tidak menyentuh XAMPP yang sedang jalan.
Kalau port 80 lagi dipakai XAMPP/Skype/dll, stop dulu service itu selama M0, atau ganti `Listen 8080` (catat di hasil).

---

## Yang dibuktikan M0

| # | Kriteria lolos (SEMUA wajib) | Cara verifikasi |
|---|---|---|
| K1 | Dua `phpinfo()` menampilkan **versi PHP berbeda** dari **satu Apache, satu port** | Buka `/app-php82/?check` dan `/app-php83/?check`, atau jalankan `verify-m0.sh` |
| K2 | Laravel dummy bisa diakses di `IP/<nama>` **tanpa** `/public` | Buka `/app-laravel/?check` |
| K3 | `.env` Laravel dummy **tidak** bisa diakses via web (404) | Buka `/app-laravel/.env` → harus 404 |
| K4 | Queue worker dummy jalan pakai `php.exe` **versi proyek itu** dari terminal | `/c/m0/php/8.2/php.exe artisan queue:work --once` dari root proyek |
| K5 | Modul `mod_fcgid` yang cocok dengan Apache uji **ditemukan & tercatat** sumber + versinya | Isi `M0-RESULT.md` |

## Layout di mesin uji (dibuat kit ini)

```
C:\m0\
├─ Apache24\            # ekstrak Apache Lounge Win64 di sini (bin\, modules\, conf\)
│  └─ modules\mod_fcgid.so   # dari paket mod_fcgid Apache Lounge, cocok VS & x64
├─ php\
│  ├─ 8.2\              # ekstrak PHP 8.2 NTS x64 ZIP di sini (php.exe, php-cgi.exe, php.ini)
│  └─ 8.3\              # ekstrak PHP 8.3 NTS x64 ZIP di sini
├─ htdocs\              # salin dari kit: htdocs\
│  ├─ app-php82\index.php
│  ├─ app-php83\index.php
│  └─ app-laravel\     # .env, artisan, public\index.php, public\.htaccess, storage\logs\
└─ results\             # output verify-m0.sh (otomatis)
```

## Unduhan resmi (cek ulang versi persisnya di `M0-RESULT.md`)

1. **Visual C++ Redistributable 2015–2022 x64** — dari Microsoft (`aka.ms/vs/17/release/vc_redist.x64.exe`). Wajib sebelum Apache/PHP jalan.
2. **Apache 2.4 Win64** — [Apache Lounge download](https://www.apachelounge.com/download/)
   Ambil `httpd-2.4.x-win64-VSxx.zip` terbaru yang tersedia di sana. Catat nama file persisnya.
3. **mod_fcgid Win64** — di halaman download Apache Lounge yang sama, paket `mod_fcgid-...-win64-VSxx.zip`.
   **VS-nya harus sama** dengan paket Apache di atas (mis. sama-sama VS17), sama-sama x64. Isinya `mod_fcgid.so` → taruh di `Apache24\modules\`.
4. **PHP 8.2 NTS x64 ZIP** dan **PHP 8.3 NTS x64 ZIP** — dari https://windows.php.net/download/ (arsip bila minor terbaru sudah geser).
   Pilih **Non Thread Safe (NTS)**, **x64**, **Zip** (bukan installer, bukan Debug Pack).
   > Kenapa NTS? PHP manual: NTS buat FastCGI (`php-cgi.exe` via mod_fcgid). TS hanya buat `mod_php` — yang justru kita matikan total (keputusan D4).

## Langkah jalan (urut, jangan loncat)

### 0. Jalur CEPAT — setup otomatis bash (pakai ini)
Atas permintaan max (2026-10-23): script utama sekarang **bash**, jalan di **Git Bash sebagai Administrator** (atau WSL).
`verify-m0.sh` adalah pemeriksa, bukan installer — jalankan setup dulu.

```bash
# Git Bash sebagai Administrator, masuk ke folder scripts di dalam kit
cd /e/panel/m0-kit/scripts    # sesuaikan drive/lokasi kit
bash setup-m0.sh
# setelah Apache jalan (langkah 4), di jendela Git Bash lain:
bash verify-m0.sh
```

Script itu: download Apache + mod_fcgid dari Apache Lounge (di-discovery dari halaman download-nya, jadi VS build keduanya pasti sama), download PHP 8.2 & 8.3 NTS x64 dari php.net/windows.php.net (discovery per branch, fallback arsip pinned), salin `htdocs` + config kit ke `C:\m0\`, patch `httpd.conf`, bikin `php.ini`, lalu `httpd -t`.
Sifatnya **fail-loud**: download/discovery gagal = berhenti dengan URL + error persisnya. Jangan tebak URL sendiri — kirim output fail-nya ke Muse.
Satu-satunya yang tetap manual: **VC++ Redistributable 2015–2022 x64** (`https://aka.ms/vs/17/release/vc_redist.x64.exe`) — script ngecek `vcruntime140.dll` dan berhenti keras kalau belum ada.

Kalau jalur cepat ini beres, lompat langsung ke langkah 4–7 di bawah (langkah 1–3 sudah dikerjakan script).
Langkah manual di bawah ini sekarang statusnya **fallback** — hanya dipakai kalau script fail dan Muse minta jalur manual.

### 1. Siapkan folder & binary (fallback manual)
```bash
# Git Bash (bukan admin dulu)
mkdir -p /c/m0/php/8.2 /c/m0/php/8.3 /c/m0/results
# Ekstrak: Apache Lounge ZIP  -> C:\m0\Apache24\  (pastikan C:\m0\Apache24\bin\httpd.exe ada)
# Ekstrak: mod_fcgid.so      -> C:\m0\Apache24\modules\mod_fcgid.so
# Ekstrak: PHP 8.2 NTS ZIP   -> C:\m0\php\8.2\   (pastikan php.exe & php-cgi.exe di root folder itu)
# Ekstrak: PHP 8.3 NTS ZIP   -> C:\m0\php\8.3\
# Salin isi folder kit htdocs\ -> C:\m0\htdocs\
```

### 2. Buat php.ini tiap versi
```bash
cp /c/m0/php/8.2/php.ini-development /c/m0/php/8.2/php.ini
cp /c/m0/php/8.3/php.ini-development /c/m0/php/8.3/php.ini
# (M0 cukup php.ini-development mentah. Template production panel itu urusan M2/M3.)
/c/m0/php/8.2/php.exe -v
/c/m0/php/8.3/php.exe -v
```
Kedua perintah harus jalan dan versinya beda minor (8.2.x vs 8.3.x). Kalau error DLL/VCRUNTIME, VC Redist belum terpasang.

### 3. Pasang config Apache
Edit `C:\m0\Apache24\conf\httpd.conf` seminimal mungkin:
- `ServerRoot "C:/m0/Apache24"`
- `Listen 80`
- `ServerName localhost:80`
- `DocumentRoot "C:/m0/htdocs"` + blok `<Directory "C:/m0/htdocs">` → `Require all granted`
- Pastikan modul ini aktif (uncomment bila ada): `LoadModule rewrite_module`, `LoadModule alias_module`, `LoadModule authz_core_module`, `LoadModule dir_module`, `LoadModule mime_module`
- Pastikan **tidak ada** `LoadModule php_module` / `php8_module` aktif.
- Di **paling bawah** file, tambahkan satu baris:
  ```
  Include "C:/m0/conf-m0-projects.conf"
  ```
Salin `apache\conf-m0-projects.conf` dari kit ini ke `C:\m0\conf-m0-projects.conf`.

### 4. Test config & jalankan Apache (console dulu, JANGAN service dulu)
```bash
# Git Bash sebagai Administrator
/c/m0/Apache24/bin/httpd.exe -t
# harus: Syntax OK — kalau tidak, STOP, catat error persisnya, jangan paksa start
# (opsional install service) /c/m0/Apache24/bin/httpd.exe -k install -n ApacheM0
/c/m0/Apache24/bin/httpd.exe -w -e debug   # console (Ctrl+C buat stop)
# atau bila sudah install service: net start ApacheM0  (dari CMD admin)
```

### 5. Verifikasi otomatis
```bash
bash scripts/verify-m0.sh
```
Script memeriksa K1–K4, menulis hasil ke `C:\m0\results\verify-*.txt`, dan mencetak `M0: PASS` / `M0: FAIL` per kriteria.
Buka juga manual di browser biar mata ikut ngecek:
- http://127.0.0.1/app-php82/ → phpinfo, PHP 8.2.x, Server API = FPM/FastCGI atau CGI/FastCGI
- http://127.0.0.1/app-php83/ → phpinfo, PHP 8.3.x
- http://127.0.0.1/app-laravel/ → halaman dummy Laravel, tanpa perlu `/public`
- http://127.0.0.1/app-laravel/.env → **404**

### 6. Uji queue dummy (K4)
```bash
cd /c/m0/htdocs/app-laravel
/c/m0/php/8.2/php.exe artisan queue:work --once
# harus cetak versi PHP 8.2.x + "queue dummy processed" dan nulis storage\logs\queue-dummy.log
/c/m0/php/8.3/php.exe artisan queue:work --once
# sengaja versi salah -> tetap jalan, tapi log menunjukkan 8.3: ini bukti versi nempel ke perintah, bukan ke proyek.
# Di panel nanti, supervisor selalu pakai php.exe versi registry proyek.
```

### 7. Isi hasil
Salin `M0-RESULT.template.md` → `M0-RESULT.md`, isi semua kolom (versi Apache/PHP/mod_fcgid persis, nama file ZIP, hasil K1–K5, error apa pun yang ketemu), lalu kirim balik ke Muse (tempel isinya di chat ini).
**M0 dinyatakan lolos hanya bila K1–K5 semua PASS di file hasil itu.**

## Kalau verify K1/K2 FAIL 404 (Apache jawab, file tidak ketemu)
Jangan utak-atik manual. Jalankan `bash scripts/diagnose-m0.sh` dan tempel SEMUA outputnya ke Muse.
Output itu memuat: isi `C:\m0\htdocs`, baris kunci `httpd.conf` aktif, siapa yang listen di port 80, dan 40 baris terakhir `Apache24\logs\error.log` (di situ Apache mencatat path persis yang dia cari saat 404).

## Troubleshooting cepat

| Gejala | Penyebab paling sering |
|---|---|
| `httpd -t` gagal load `mod_fcgid.so` | VS build mod_fcgid ≠ VS build Apache, atau x86 vs x64 campur |
| Halaman .php malah ke-download / tampil source | `SetHandler fcgid-script` / `FcgidWrapper` di conf proyek tidak kepakai — cek Include kepasang & tidak ada `php_module` |
| Kedua app tampil versi PHP sama | `FcgidWrapper` kedua Directory ketuker, atau php-cgi proses lama belum mati — restart Apache |
| `app-laravel` 403/404 | `Alias` tidak cocok dengan path fisik, atau `<Directory ...\public>` belum `Require all granted` |
| `.env` bisa dibuka (200) | Alias bocor ke root proyek — STOP, ini gagal K3; cek Alias hanya menunjuk ke `public` |
| php.exe error VCRUNTIME140.dll | VC Redistributable x64 belum terpasang / perlu restart |

## Batas M0
Tidak ada panel, tidak ada Go, tidak ada service panel, tidak ada proyek nyata.
Dummy Laravel di kit ini **bukan** Laravel asli — dia meniru kontrak yang mau dibuktikan (front controller di `public/`, `.env` di root, `artisan` CLI). Bila mau, ganti dengan `composer create-project laravel/laravel` asli di mesin uji sebagai uji tambahan, tapi hasil dummy sudah cukup buat keputusan go/no-go.
