# Panduan Deploy SPanel+

> Berlaku untuk **v0.6.2-m5** (dan selanjutnya selama tidak ada catatan
> perubahan perilaku di release notes). Dokumen ini mengatur deploy
> **produksi/pilot di satu server Windows**, bukan smoke test — untuk
> smoke test, pakai `docs/smoke-test-tutorial.md`.
>
> Prinsip yang tidak berubah dari Fase 1: **satu instance Apache**,
> routing path (`IP/nama-proyek`), multi-PHP via `mod_fcgid`
> (mod_php mati), Laravel di-`Alias` ke `public/`, satu binary Go
> untuk console + service + desktop, dan file panel selalu di luar
> folder proyek.

## 0. Gambaran deploy

```
Server Windows (satu mesin)
├── Service SPanelPlus ("SPanel+ Panel Service", start otomatis saat boot)
│   └── C:\Panel\panel.exe serve  →  UI/API di :8888 (HTTP, semua interface)
├── Service apache-spanel (Apache 2.4.69 VS18 + mod_fcgid VS18)
│   └── C:\m0\Apache24  →  situs proyek di :80 (atau port Apache pilihan)
├── C:\Panel\panel.json          (konfigurasi panel — SATU file ini saja)
├── <data_dir>/                  (registry, admin, audit, generated, backups;
│   setup smoke memakai data_dir C:/Panel, jadi file-file ini langsung
│   di C:\Panel — bila data_dir diset C:/Panel/data, mereka di sana)
└── C:\m0\htdocs\               (DocumentRoot Apache; folder proyek di luar sini
    boleh di mana saja — panel tidak memindahkan proyek)
```

Dua service, dua port, dua tugas. Panel tidak melayani situs proyek
dan Apache tidak melayani UI panel. Kalau salah satu mati, yang lain
tetap jalan — tapi perubahan proyek lewat panel butuh keduanya hidup.

## 1. Prasyarat

- Windows 10/11 atau Windows Server 64-bit, dengan hak **Administrator**.
- **VC++ Redistributable 2015–2022 x64** terpasang
  (`vcruntime140.dll` harus ada di `C:\Windows\System32`).
  Tanpa ini, `httpd.exe` dan `php-cgi.exe` gagal start dengan error
  `VCRUNTIME140.dll` — instal dulu, lalu ulangi deploy.
- Port yang harus bebas (atau sudah dialokasikan untuk panel):
  - **80** (default Apache; bisa diganti, lihat §2) — konflik paling
    umum: IIS, XAMPP, Skype. Kalau port 80 sudah dipakai aplikasi
    lain, service `apache-spanel` gagal start.
  - **8888** (panel) — hanya boleh dipakai panel sendiri.
  - **636** keluar ke Domain Controller (hanya bila login AD dipakai).
- Untuk login AD (produksi, §4): FQDN DC bisa di-resolve DNS
  internal dari server panel, sertifikat DC dipercaya (system roots
  atau `ad_ca_file`), dan DN grup IT sudah dikonfirmasi di ADUC.
  Tanpa ketiganya, login AD gagal tertutup (fail-closed) — login
  akun panel lokal tetap jalan.
- Akun yang menjalankan deploy harus bisa membaca/menulis
  `C:\Panel` dan `C:\m0`. Service panel berjalan sebagai service
  Windows standar; working directory-nya `System32`, jadi semua
  path panel selalu absolut.

## 2. Instalasi

### 2a. Jalur yang disarankan: deploy script (setup + produksi sekaligus)

`scripts/deploy-panel-production.ps1` (PowerShell sebagai
Administrator) adalah jalur utama: dari mesin kosong sampai
produksi dalam satu jalan. Ia mendeteksi dulu apa yang sudah
ada (di-skip) dan apa yang belum ada (dipasang), menampilkan
rencana lengkap, dan baru bermutasi sesudah konfirmasi Y
(kecuali -Force):
- panel.exe disalin dari file panel*.exe sebelahan dengan script
  (diutamakan panel-v*.exe terbaru) bila belum ada di PanelDir.
- Apache di-download (discovery versi terbaru VS18 dari Apache
  Lounge, fallback pinned 2.4.69) bila httpd.exe belum ada;
  mod_fcgid pinned 2.3.10 VS18 bila .so-nya belum ada.
- PHP 8.2/8.3/8.4 NTS x64 di-download per cabang (discovery patch
  terbaru dari php.net, fallback arsip pinned) bila php.exe
  cabang itu belum ada. Folder PHP yang sudah dicopy manual
  di-skip, tidak di-download ulang.
- Sebelum download, script mengecek folder tujuan (ada isinya
  atau tidak) dan folder downloads di M0: zip Apache/PHP yang
  sudah ada di downloads langsung diekstrak tanpa download
  ulang; folder tujuan yang sudah ada isinya tapi exe-nya
  hilang membuat script gagal keras dengan pesan jelas (bukan
  men-download diam-diam atau menghapus copy manual).
- httpd.conf di-patch idempotent (backup sekali .orig-m0),
  service apache-spanel + SPanelPlus dipasang/dipastikan jalan.
- panel.json dibuat baru langsung mode produksi bila belum ada,
  atau dimodifikasi (AD dummy dibuang, ke AD sungguhan via LDAPS
  atau ke mode admin-only, ad_allow_insecure_ldap=false) bila
  sudah ada. Akun admin break-glass dibuat interaktif bila
  belum ada. Opsional hapus dummy M0 (-RemoveDummies).
- Verifikasi: httpd -t + gate httpd -M (rewrite + fcgid wajib
  ke-load), doctor, API panel, URL proyek aktif (bila registry
  belum ada karena mesin fresh, cek URL dilewati dengan
  peringatan). Mesin fresh TIDAK dibuatkan dummy M0.
Sesudah deployment, **jangan re-run script setup smoke** -
langkah 9 setup menulis ulang `panel.json` ke default dummy.
Setup script smoke (`scripts/setup-panel-smoke-v2.sh` /
`scripts/setup-panel-smoke.ps1`) tetap ada untuk jalur smoke
test saja; untuk produksi, deploy script ini sudah mencakup
langkah-langkah setup yang relevan.

### 2b. Jalur manual (tanpa setup script)

1. Salin `panel.exe` ke `C:\Panel\panel.exe`.
2. Salin `panel.example.json` menjadi `C:\Panel\panel.json`, lalu
   isi field-fieldnya sesuai §3. Jangan biarkan `apache_home`
   kosong di mesin yang punya XAMPP — doctor akan auto-discover
   XAMPP duluan (urutan: XAMPP, `C:\Apache24`, `C:\m0\Apache24`).
3. Instal Apache 2.4.69 VS18 + `mod_fcgid` VS18 yang cocok ke
   `C:\m0\Apache24` (VS build harus sama — mod_fcgid VS16 di
   Apache VS18 tidak akan load).
4. Jalankan sebagai Administrator:
   ```
   C:\Panel\panel.exe service install
   C:\Panel\panel.exe service start
   ```
   Service `SPanelPlus` ber-start otomatis saat boot.
   Instal service Apache dengan nama `apache-spanel`:
   ```
   C:\m0\Apache24\bin\httpd.exe -k install -n apache-spanel
   net start apache-spanel
   ```

### 2c. Aturan path yang paling sering menjebak

**Setiap `panel.exe` membaca `panel.json` di foldernya sendiri**
(Plan D9), bukan dari folder tempat perintah diketik. Akibatnya:

- Service membaca `C:\Panel\panel.json`.
- `panel.exe` di folder download (mis. `D:\Panel\panel.exe`)
  membaca `D:\Panel\panel.json` — file lain, atau default bila
  tidak ada (auto-discover Apache, `data_dir` = folder exe
  + `\data`, admin dari file lain).
- Semua perintah CLI pasca-install **wajib** memakai full path
  `C:\Panel\panel.exe`: `doctor`, `admin set-password`,
  `service …`, `php …`, `login`. Perintah yang dijalankan dari
  exe yang salah akan terlihat "berhasil" tapi mengubah config
  atau akun yang salah.

## 3. Konfigurasi produksi (`C:\Panel\panel.json`)

Satu-satunya file konfigurasi. Panel membaca ulang file ini
**saat start** — sesudah mengubahnya, restart service
`SPanelPlus` (atau `C:\Panel\panel.exe service stop` +
`service start`). Mengubah file tanpa restart tidak berpengaruh
pada service yang sedang jalan.

| Field | Produksi | Catatan |
|---|---|---|
| `port` | `8888` (atau port panel pilihan) | Panel listen di semua interface (`:<port>`), HTTP tanpa TLS dari panel sendiri — lihat §7. |
| `data_dir` | `C:/Panel` (setup) atau `C:/Panel/data` | Folder registry, `admin.json`, `audit.log`, config generated, backups. Backup folder ini (§6). |
| `log_dir` | kosong (default) atau path eksplisit | Log panel sendiri. |
| `apache_home` | `C:/m0/Apache24` (dikunci, jangan kosong) | Kosong = auto-discover; di mesin ber-XAMPP, XAMPP yang kepilih. |
| `apache_service` | `apache-spanel` | Nama service Apache yang dikelola panel (restart/status/doctor memakai nama ini). |
| `document_root` | `C:/m0/htdocs` atau kosong | Kosong = semua proyek dapat `Alias` eksplisit (selalu benar, hanya kurang minimal). |
| `ad_enabled` | `true` (bila AD dipakai) | `false` = hanya akun panel lokal. |
| `ad_server` | `ldaps://<fqdn-dc>:636` | WAJIB FQDN (bukan IP mentah) agar validasi sertifikat LDAPS cocok. `ldap://` hanya diterima bila StartTLS berhasil. Nilai `"dummy"` **tidak pernah** untuk produksi. |
| `ad_domain` | mis. `siws.local` | Suffix UPN; username boleh `sAMAccountName` (panel membentuk UPN) atau UPN lengkap. |
| `ad_base_dn` | DN riil dari ADUC | Mis. `DC=siws,DC=local`. |
| `ad_group_dn` | DN grup IT riil dari ADUC | Anggota grup ini = admin penuh. DN yang salah/placeholder membuat **semua** login AD ditolak (fail-closed), bukan lolos. |
| `ad_ca_file` | path PEM bila CA DC internal | Kosong = system roots. |
| `ad_allow_insecure_ldap` | **`false`** | `true` hanya untuk test; panel menulis warning keras tiap start bila aktif. |
| `ad_dummy_users` | **hapus seluruhnya** | Hanya dibaca bila `ad_server` = `"dummy"`. |

Dua hal yang tidak ada di `panel.json` dan tidak boleh dicari
di sana: password admin (disimpan ter-hash di
`<data_dir>/admin.json` via `panel admin set-password`, perintah
console saja) dan definisi proyek/PHP (disimpan di
`<data_dir>/registry.json` lewat UI/CLI).

## 4. Login produksi: AD sungguhan + akun panel break-glass

1. Isi field AD di `panel.json` sesuai tabel §3, hapus
   `ad_dummy_users`, pastikan `ad_server` bukan `"dummy"`.
2. Restart `SPanelPlus`.
3. Uji login dengan akun anggota grup IT (harus lolos) dan akun
   di luar grup IT (harus ditolak dengan pesan gagal generik —
   panel tidak membocorkan alasan penolakan).
4. Uji login akun panel lokal (`admin` + password dari
   `panel admin set-password`) sebagai break-glass bila DC down.
5. Periksa `audit.log`: aktor login AD tercatat sebagai
   `ad:<username>`, akun lokal sebagai `local:admin` — audit
   per orang inilah alasan login AD dipakai (bukan akun bersama).

Sesi berlaku **10 menit idle sliding** untuk login lokal maupun
AD: tiap request API terautentikasi me-reset timer dan cookie
diterbitkan ulang. Tidak ada batas umur absolut sesi. Restart
panel mengeluarkan semua sesi (sesi hanya di memori) — akun
tidak hilang, hanya perlu login ulang.

## 5. Registrasi PHP dan proyek

- **PHP**: daftarkan tiap instalasi PHP lewat dashboard
  (PHP Manager) atau CLI:
  ```
  C:\Panel\panel.exe php add --path <folder-php>
  ```
  Panel mem-probe `php -v`/`php -m`, mendaftarkan versi yang
  terdeteksi, dan **menolak build TS** (hanya NTS yang dipakai
  `mod_fcgid`). `php.ini` produksi hanya dibuat bila belum ada.
- **Extension per versi PHP**: kelola dari dashboard (PHP
  Manager → tombol Extension → checklist), tanpa edit
  `php.ini` manual. Checklist berlaku per versi PHP, bukan
  per proyek: mengubah extension PHP 8.2 memengaruhi semua
  proyek yang memakai 8.2. Kotak tercentang berarti extension
  sedang aktif — aktif di `php.ini` atau sedang termuat
  (`php -m`) — sehingga extension bawaan (Core, date, dsb.)
  tampil tercentang meski tidak punya baris ini (dan tidak
  bisa diutak-atik). Sebelum menulis, panel mem-backup
  `php.ini` ke file `php.ini.panel-bak-<timestamp>` di folder
  yang sama, lalu memverifikasi dengan `php -m` sesudah
  tulis; bila extension tidak termuat sesuai harapan, backup
  dikembalikan dan simpan dilaporkan gagal. Extension yang
  tidak punya DLL di `ext/` ditolak diaktifkan. DLL SQL
  Server (`php_sqlsrv.dll`, `php_pdo_sqlsrv.dll`) dan
  Microsoft ODBC Driver tetap instalasi manual: unduh driver
  Microsoft untuk PHP (NTS x64, sesuai versi PHP) ke `ext/`,
  instal ODBC Driver, lalu centang `pdo_sqlsrv` di panel
  (`pdo` ikut aktif otomatis sebagai dependensinya).
- **Proyek**: tambahkan lewat dashboard (nama proyek = segmen
  path URL, mis. proyek `app-laravel` dilayani di
  `http://<server>/app-laravel`). Laravel dan CodeIgniter 4
  di-`Alias` ke folder `public/`-nya; proyek native/statis memakai foldernya
  langsung. Untuk CI4, panel memeriksa folder `writable/` dan
  `app.baseURL` di `.env` (pola peringatan yang sama dengan
  `APP_URL` Laravel); scheduler Laravel (`artisan schedule:run`)
  tidak berlaku untuk CI4 — proses terjadwal CI4 didaftarkan
  sebagai proses CLI biasa bila perlu. Panel menulis config generated ke
  `<data_dir>/generated/apache/*.conf`, lalu menjalankan
  pipeline apply: snapshot → tulis config → `httpd -t` →
  restart Apache → health-check → commit registry. Gagal di
  tengah jalan mengembalikan snapshot persis seperti semula.
- **Port Apache selain 80 (mis. 8000) didukung.** Panel membaca
  port dari direktif `Listen` pertama di `httpd.conf` saat
  service start, lalu memakai port itu untuk health-check apply
  dan untuk link proyek di dashboard (port 80 tetap tampil
  tanpa port di URL, seperti biasa). Tidak ada field
  `apache_port` di `panel.json` — `httpd.conf` adalah satu-satunya
  sumber kebenaran, jadi tidak mungkin config panel meleset
  dari port yang benar-benar dipakai Apache. Konsekuensi: bila
  `Listen` diubah sesudah panel jalan, restart service
  `SPanelPlus` supaya deteksi port ikut berubah; mengubah port
  tanpa restart panel membuat health-check dan link dashboard
  masih mengarah ke port lama.
- **Dummy smoke test bukan bagian produksi.** Folder dummy
  (`app-php82`, `app-php83`, `app-laravel`) dan config M0-nya
  hanya untuk smoke test. Sebelum go-live: hapus folder dummy
  dari `C:\m0\htdocs` dan Include config M0-nya dari
  `httpd.conf` bila tidak lagi diperlukan, restart
  `apache-spanel`, dan verifikasi URL dummy membalas 404.
  Jangan pernah mendaftarkan dummy sebagai proyek produksi.

## 6. Operasional

### Layanan

```
C:\Panel\panel.exe service status    # status SPanelPlus
C:\Panel\panel.exe service stop      # stop panel (supervisor ikut berhenti)
C:\Panel\panel.exe service start     # start panel
sc query apache-spanel                # status Apache
net stop apache-spanel / net start apache-spanel
```

Perintah `panel service` hanya ada `install|uninstall|start|
stop|status` — tidak ada `restart`; restart = stop + start.
Saat mengganti `panel.exe`, service harus di-stop dulu (file
exe terkunci selama service jalan).

### Update binary panel

1. `C:\Panel\panel.exe service stop` (panel juga menghentikan
   proses supervisor/scheduler yang dikelolanya).
2. Ganti `C:\Panel\panel.exe` dengan binary baru (simpan binary
   lama sebagai cadangan, mis. `panel-<versi-lama>.exe`).
3. `C:\Panel\panel.exe service start`, lalu
   `C:\Panel\panel.exe version` dan `doctor`.
4. Config (`panel.json`), registry, admin, dan audit tidak
   tersentuh update binary — semuanya di luar exe.

Rollback binary = langkah yang sama dengan binary lama.
Rollback config Apache = snapshot di
`<data_dir>/backups/<yyyyMMdd-HHmmss>` (retensi 20 snapshot
terbaru; snapshot diambil otomatis tiap apply).

### Backup

Yang wajib dibackup berkala (di luar mesin bila mungkin):

- `C:\Panel\panel.json`
- `<data_dir>/` seluruhnya: `registry.json`, `admin.json`,
  `audit.log`, `generated/`, `backups/`
- `C:\m0\Apache24\conf\httpd.conf` dan Include panel/M0-nya

Folder proyek **bukan** data panel — backup proyek mengikuti
prosedur masing-masing proyek (panel tidak memindahkan atau
mengunci folder proyek).

### Log dan audit

- Log panel: `log_dir` (atau default) + output service.
- Audit: `<data_dir>/audit.log`, satu baris JSON per aksi,
  ber-aktor per orang (`local:admin` / `ad:<username>`).
- Log proses proyek (supervisor/scheduler):
  `<data_dir>/projects/<proyek>/<nama>.log`.
- Supervisor: autostart, backoff 1 detik → 30 detik, menyerah
  sesudah 5 gagal beruntun yang tidak stabil; counter reset
  sesudah ≥60 detik stabil. Scheduler menjalankan
  `artisan schedule:run` tiap menit untuk proyek Laravel yang
  mengaktifkannya.

## 7. Keamanan — baca sebelum membuka akses

- **Panel tidak menyediakan TLS untuk UI-nya.** Cookie sesi
  `HttpOnly SameSite=Lax`, **bukan** `Secure`. Karena itu port
  panel (8888) **tidak boleh** dibuka ke jaringan yang tidak
  dipercaya. Di LAN internal, batasi lewat Windows Firewall
  hanya ke workstation admin/IT. Bila UI harus diakses dari
  luar LAN, letakkan reverse proxy TLS di depannya (di luar
  panel) — panel sendiri tidak menerbitkan sertifikat.
- Jangan pernah mengaktifkan `ad_allow_insecure_ldap` atau
  `ad_server: "dummy"` di produksi. Keduanya hanya untuk test
  dan keduanya tercatat sebagai warning.
- Jangan memakai akun admin bersama untuk kerja harian — semua
  anggota grup IT login dengan akun AD masing-masing supaya
  audit bisa menunjuk orang.
- Simpan password break-glass akun lokal di tempat yang aman
  dan terpisah dari server (bila DC down, inilah satu-satunya
  jalan masuk).
- Seluruh anggota grup IT saat ini mendapat **akses admin
  penuh** — belum ada peran read-only (rencana lanjutan,
  belum diputuskan). Artinya keanggotaan grup IT = kewenangan
  mengubah config Apache dan proyek.

## 8. Verifikasi pasca-deploy

Jalankan dari server, selalu dengan full path:

```
C:\Panel\panel.exe version     # harus versi yang di-deploy
C:\Panel\panel.exe doctor      # lihat catatan di bawah
```

Doctor lolos bila:

- `apache-home` menunjuk `C:\m0\Apache24` (bukan
  auto-discovered XAMPP) dan `apache-service` melaporkan
  `apache-spanel` running.
- `httpd-version` 2.4.69, `mod-fcgid` pass, `httpd-syntax`
  `Syntax OK`.
- `data-dir` writable.
- WARN yang wajar: `php-versions`/`projects` hanya bila
  memang belum ada PHP/proyek terdaftar; `ad-config` hanya
  bila AD dummy masih aktif (di produksi seharusnya tidak).

Dua perilaku doctor yang **bukan** kerusakan:

- `panel-port` FAIL saat service panel hidup: port 8888
  dipegang service panel sendiri. Doctor CLI tidak bisa
  membedakan pemegangnya (hanya doctor dari dashboard/API
  yang menampilkan WARN). Konfirmasi dengan
  `netstat -ano | findstr :8888` — PID-nya harus `panel.exe`.
- `panel-port` WARN dari dashboard adalah kondisi yang sama,
  dilihat dari dalam panel sendiri.

Lalu verifikasi dari browser/terminal lain:

- UI panel terbuka di `http://<server>:8888`, login lokal dan
  login AD sesuai §4.
- Tiap proyek membalas di `http://<server>/<proyek>/` dengan
  versi PHP yang benar (`?check` untuk dummy hanya berlaku
  saat smoke test).
- **Uji boot (kriteria M1):** restart mesin, lalu buka UI
  tanpa start manual apa pun — kedua service harus sudah
  jalan sendiri (keduanya `StartAutomatic`).

## 9. Troubleshooting deploy

| Gejala | Penyebab umum & tindakan |
|---|---|
| Service `apache-spanel` gagal start, port dipakai | Port Apache dipakai IIS/XAMPP/Skype — cek `netstat -ano \| findstr :<port>`, stop dulu service itu. |
| Doctor `apache-home` menunjuk XAMPP (auto-discovered) | `panel.json` tidak mengunci `apache_home` — isi `C:/m0/Apache24`, restart `SPanelPlus`. |
| Doctor `mod-fcgid` fail | `mod_fcgid.so` tidak ada di Apache yang ditunjuk panel (biasanya karena salah `apache_home`), atau VS build mod_fcgid != VS build Apache. |
| Doctor `httpd-syntax` fail | `httpd -t` gagal pada config Apache yang ditunjuk — baca outputnya; bila menunjuk XAMPP, itu config XAMPP, bukan panel. |
| Proyek terdaftar tapi Apache tetap listing / tampil source PHP | IncludeOptional generated di `httpd.conf` tidak mengarah ke `<data_dir>/generated/apache` (bug setup lama menulis `C:/Panel/data/...`) — perbaiki path-nya, `httpd -t`, restart `apache-spanel`. |
| Login bilang admin belum dibuat | `admin.json` tidak ada di `data_dir` service — hampir selalu karena password di-set lewat exe di folder lain (lihat §2c). Set ulang lewat `C:\Panel\panel.exe admin set-password`. |
| Doctor FAIL `panel-port` padahal panel jalan | Port dipegang service panel sendiri — by design, lihat §8. |
| `httpd.exe`/`php.exe` error `VCRUNTIME140.dll` | VC Redistributable x64 belum terpasang / perlu restart. |
| Halaman `.php` malah ke-download / tampil source | `SetHandler fcgid-script` / `FcgidWrapper` tidak kepakai — cek Include generated kepasang & tidak ada `php_module`. |
| Login AD selalu ditolak | DN grup salah (fail-closed), FQDN DC tidak ter-resolve, port 636 tertutup, atau sertifikat DC tidak dipercaya — cek ketiganya, lalu `ad_ca_file` bila CA internal. |
| Ganti `panel.exe` gagal (file terkunci) | Service masih jalan — stop dulu, baru salin. |

## 10. Checklist go-live

- [ ] `C:\Panel\panel.exe version` = versi yang di-deploy.
- [ ] `doctor` hijau kecuali `panel-port` (by design, §8) dan
      WARN yang memang diharapkan.
- [ ] `panel.json` produksi: `apache_home` terkunci,
      `apache_service` = `apache-spanel`, AD sungguhan (bukan
      dummy), `ad_dummy_users` dihapus,
      `ad_allow_insecure_ldap` = `false`.
- [ ] Login anggota grup IT lolos, non-anggota ditolak,
      login akun lokal (break-glass) teruji.
- [ ] Audit log mencatat aktor per orang.
- [ ] PHP dan proyek produksi terdaftar; dummy smoke test
      sudah dihapus dan URL-nya 404.
- [ ] Uji boot: restart mesin, kedua service jalan sendiri,
      UI terbuka tanpa start manual.
- [ ] Backup `panel.json` + `<data_dir>/` + `httpd.conf`
      sudah berjalan (jadwal di luar panel).
- [ ] Firewall: port 8888 hanya untuk workstation admin/IT.
- [ ] Password break-glass tersimpan aman di luar server.
- [ ] Dokumen ini + `panel.json` final tersimpan untuk tim IT
      (8 orang) sebagai acuan operasional.
