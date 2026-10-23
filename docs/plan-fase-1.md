# Plan Fase 1 — Admin Panel Windows untuk Multi-PHP per Path

Status: **TERKUNCI** oleh max, 2026-10-23 22:16 WIB ("Oke, kunci fase 1. Mulai buat plan")
Sifat: dokumen rencana kerja Fase 1. Keputusan di Bagian 1 sudah final; yang berubah selama pengerjaan dicatat di Bagian 13 (log perubahan).

---

## 1. Keputusan Terkunci

| # | Keputusan | Isi |
|---|-----------|-----|
| D1 | Target | Pemakaian internal perusahaan saja. Bukan produk jualan, bukan public hosting. |
| D2 | Engine | **Satu instance Apache** (`httpd`). Bukan Nginx, bukan banyak Apache. |
| D3 | Routing | Berbasis **path**: `IP/nama-proyek` dalam satu port (80). Tanpa domain/DNS internal, tanpa beda port per aplikasi. URL aplikasi yang berjalan sekarang **tidak berubah**. |
| D4 | Multi-PHP | Via **`mod_fcgid`** (PHP = `php-cgi.exe` per versi). `mod_php` **dimatikan total** — syarat mutlak multi-PHP. Build PHP yang dipakai: **NTS** (Non-Thread-Safe). |
| D5 | Laravel | Path proyek dipetakan dengan `Alias` langsung ke folder `public` proyek. `/public` hilang dari URL; root proyek (`.env`, `vendor/`) tidak terekspos web. Folder proyek tidak pindah. |
| D6 | Stack panel | **Go**, satu binary `panel.exe`, dua mode: `serve` (Windows Service) dan mode CLI (subcommand). Tanpa runtime tambahan di server. |
| D7 | UI panel | Web UI jalan dari service panel sendiri, di port sendiri (bukan numpang Apache yang diurus), frontend ter-embed di binary. Gaya: template server-rendered + htmx (tanpa SPA). |
| D8 | CLI manager | Panel mengelola proses CLI per proyek (queue worker, scheduler, perintah artisan) memakai `php.exe` **versi milik proyek itu**, dari folder root proyek. Supervisor bawaan panel — pengganti NSSM & Task Scheduler. |
| D9 | Penyimpanan panel | Registry = **file JSON** di folder panel. Semua file hasil generate panel (config Apache, override, log, backup) hidup di **folder panel sendiri, di luar folder proyek**. Folder proyek tetap murni (siap git di Fase 2). |
| D10 | Git | **Fase 2.** Fase 1 hanya menyiapkan slot datanya (`repo_url`, `branch`, post-deploy) di registry. |

## 2. Di Luar Scope Fase 1 (Eksplisit)

- Nginx, multi-server / multi-mesin, multi-tenant, reseller, billing, paket hosting.
- DNS server, mail server, FTP server.
- Git server & deploy berbasis git (Fase 2a deploy, Fase 2b git server/Gitea).
- SSL/HTTPS publik (Let's Encrypt butuh domain). HTTPS internal = kandidat fase lanjut.
- Database manager penuh (phpMyAdmin-like), file manager web.
- Manajemen user OS / Windows ACL per proyek.
- Installer grafis — Fase 1 cukup ZIP + perintah service install dari CLI panel.

## 3. Prasyarat & Asumsi Server

- Windows dengan hak Administrator untuk: install/uninstall Windows Service, restart service Apache, menulis ke folder instalasi Apache & folder panel.
- Apache: mode adopsi instalasi XAMPP yang sudah berjalan **atau** Apache standalone (Apache Lounge), diputuskan per server pilot (Bagian 11). Dua-duanya diperlakukan sama oleh panel setelah adopsi: satu `httpd`, satu service, satu file `httpd.conf` yang menunjuk Include ke folder generate panel.
- Modul `mod_fcgid` tersedia dan cocok dengan build Apache (arsitektur x64 & VC runtime yang sama). Panel harus membundel/memvalidasi modul ini (cek di `doctor`).
- Binary PHP per versi dari arsip ZIP `windows.php.net`, build **NTS x64**, disimpan per versi oleh panel (impor dari file ZIP/folder lokal — server LAN tidak diasumsikan punya internet).
- Extension per versi (mis. `sqlsrv`/`pdo_sqlsrv`, `pdo_mysql`, `mbstring`, `openssl`, dsb.) harus cocok versi PHP + NTS + x64; panel memvalidasi lewat `php.exe -m` tiap versi ditambahkan.
- Port panel (default direncanakan 8888, dapat diganti) dibuka hanya untuk LAN dan tidak bentrok dengan aplikasi lain — dicek saat instalasi.

## 4. Arsitektur

### 4.1 Komponen

```
panel.exe
├─ mode serve  → Windows Service
│   ├─ Core
│   │   ├─ Registry        : baca/tulis registry proyek & versi PHP (JSON, atomic write)
│   │   ├─ ConfigGen       : generate config Apache per proyek dari template (golden-tested)
│   │   ├─ ApacheManager   : backup → httpd -t → restart service → health check → rollback bila gagal
│   │   ├─ PhpManager      : daftar versi PHP, validasi binary & extension, generate php.ini
│   │   ├─ Supervisor      : jaga proses CLI per proyek (start/stop/auto-restart/backoff, log per proses)
│   │   ├─ Scheduler       : ticker 1 menit → schedule:run untuk proyek Laravel yang mengaktifkannya
│   │   └─ LogService      : tail log Apache, log Laravel (storage/logs), log proses supervisor
│   ├─ HTTP API (JSON)     : satu-satunya jalan UI & CLI mengubah state (satu otak, dua muka)
│   └─ Web UI (embed)      : template + htmx, status realtime sederhana (polling/SSE)
└─ mode CLI → subcommand yang memanggil API yang sama
    (service, project, php, apache, process, doctor)
```

Catatan presisi: proses `php-cgi` untuk request web di-spawn dan dijaga oleh **`mod_fcgid` di dalam Apache**, bukan oleh Supervisor panel. Panel mengatur *konfigurasinya* (wrapper per folder, batas proses, timeout). Supervisor panel hanya mengurus proses CLI (queue/scheduler/perintah).

### 4.2 Layout folder panel (di luar folder proyek)

```
C:\panel\
├─ panel.exe
├─ data\
│  ├─ registry.json            # proyek + versi PHP + setting global
│  └─ admin.json               # kredensial admin panel (hash bcrypt)
├─ generated\apache\           # *.conf per proyek, di-Include oleh httpd.conf (1 baris)
├─ php\<versi>\                # binary PHP per versi (php-cgi.exe, php.exe, php.ini hasil generate)
├─ projects\<nama>\            # per proyek: override php.ini (.user.ini), log supervisor
├─ backups\<timestamp>\        # snapshot registry + generated conf sebelum tiap apply
└─ logs\                       # log panel & log proses
```

Satu-satunya jejak panel di instalasi Apache: blok `LoadModule fcgid_module` + satu baris `Include` ke `generated\apache\*.conf` + penonaktifan `php_module`. Selebihnya milik panel dan dapat dicabut bersih.

### 4.3 Model data registry (sketsa)

```jsonc
{
  "settings": {
    "apache_home": "C:/xampp/apache",       // atau path standalone
    "apache_service": "Apache2.4",
    "document_root": "C:/xampp/htdocs",
    "panel_port": 8888
  },
  "php_versions": [
    { "version": "8.2", "path": "C:/panel/php/8.2", "ini": "C:/panel/php/8.2/php.ini" },
    { "version": "7.4", "path": "C:/panel/php/7.4", "ini": "C:/panel/php/7.4/php.ini" }
  ],
  "projects": [
    {
      "name": "wh_system",
      "url_path": "/wh_system",
      "fs_path": "C:/xampp/htdocs/wh_system",
      "type": "laravel",            // laravel | php-native | static
      "public_subdir": "public",    // laravel: "public", native: "", static: "dist" dsb.
      "php_version": "8.2",
      "enabled": true,
      "cli_processes": [
        { "name": "queue", "command": "artisan queue:work", "autostart": true, "restart": "on-failure" },
        { "name": "scheduler", "kind": "schedule:run", "autostart": true }
      ],
      "repo_url": "", "branch": "", "post_deploy": [],   // slot Fase 2
      "created_at": "...", "updated_at": "..."
    }
  ]
}
```

Aturan validasi registry: `url_path` unik & diawali `/`; `fs_path` harus ada; `php_version` harus terdaftar; nama proyek unik dan aman untuk nama file config.

## 5. Fitur Fase 1

**F1 — Adopsi & discovery.** Deteksi instalasi Apache (path, versi `httpd -v`, nama & status service). Scan `document_root`: daftar folder calon proyek, deteksi tipe (ada `artisan` → Laravel; ada `index.php` → php-native; selain itu static), dan **peringatan migrasi**: `.htaccess` berisi `php_value`/`php_flag` (direktif `mod_php` — jadi error 500 di bawah fcgid, harus dipindah ke `php.ini`/`.user.ini`).

**F2 — PHP Version Manager.** Tambah versi dari ZIP/folder lokal → ekstrak ke `php\<versi>\` → validasi (`php-cgi.exe` ada, `php.exe -v` cocok, build NTS) → generate `php.ini` dari template production (OPcache aktif, `session.save_path` & `sys_temp_dir` per versi, daftar extension yang bisa dipilih) → tampilkan hasil `php -m`. Hapus versi ditolak bila masih dipakai proyek.

**F3 — Project Manager.** CRUD proyek sesuai model data. Ganti versi PHP = satu aksi atomik (Bagian 6). Preview config hasil generate (diff terhadap yang aktif) sebelum apply. Health check HTTP ke `127.0.0.1<url_path>` setelah restart Apache; status per proyek tampil di dashboard.

**F4 — Penghilang `/public`.** Untuk tipe Laravel, ConfigGen memetakan `Alias "<url_path>" "<fs_path>/public"` + `<Directory>` untuk folder `public` itu (handler fcgid + wrapper versi PHP proyek + `AllowOverride All`). Panel memeriksa `APP_URL` di `.env` proyek dan memberi peringatan bila tidak sama dengan `http://<IP><url_path>` (tanpa `/public`).

**F5 — CLI Manager / Supervisor.** Definisi proses per proyek (template Laravel: queue & scheduler). Start/stop/restart per proses, autostart saat panel start, auto-restart dengan backoff saat crash, batas percobaan, log stdout/stderr per proses ke `projects\<nama>\`. Runner perintah sekali jalan (artisan/composer) dengan output tertangkap — memakai `php.exe` versi proyek, cwd folder root proyek. Semua eksekusi tercatat di log panel.

**F6 — Service & autentikasi panel.** `panel.exe service install|uninstall|start|stop`. Panel hanya untuk admin: satu akun admin, password tersimpan sebagai hash bcrypt, cookie sesi, logout. Rekomendasi operasional: firewall port panel khusus LAN. Semua aksi ubah-state lewat API tercatat (siapa, kapan, apa) di log panel.

**F7 — Tool CLI.** Subcommand setara API: `service …`, `project list|add|set-php|enable|disable`, `php list|add`, `apache test|restart|status`, `process list|start|stop`, dan `doctor` — diagnostik satu perintah: keberadaan & arsitektur `mod_fcgid`, `httpd -t`, konflik port panel, akses tulis folder proyek (`storage/` Laravel), versi PHP yang terdaftar valid.

**F8 — Dashboard UI.** Ringkasan: status Apache, jumlah proyek aktif, versi PHP, proses CLI yang jalan. Tabel proyek: path URL, tipe, versi PHP, status HTTP, aksi (ganti PHP, proses, log). Detail proyek: preview/diff config, daftar proses + log tail, log error Apache & log Laravel, peringatan `APP_URL` & `php_value`. Alur ubah config selalu: edit → preview diff → apply.

**F9 — Backup & rollback.** Tiap apply: snapshot `registry.json` + seluruh `generated\apache\` ke `backups\<timestamp>\` (retensi 20). Restore satu klik / satu perintah CLI. Rollback otomatis bila `httpd -t` gagal atau health check proyek yang diubah gagal setelah restart.

## 6. Alur Kunci

**Ganti versi PHP sebuah proyek:**
1. Admin pilih versi baru (UI/CLI) → API validasi versi terdaftar & proyek ada.
2. ConfigGen menulis conf proyek ke file sementara; ApacheManager membuat snapshot backup.
3. `httpd -t` pada konfigurasi kandidat. Gagal → batal, config aktif tidak disentuh, error ditampilkan utuh.
4. Terapkan conf, restart service Apache, health check `url_path` proyek.
5. Health check gagal → kembalikan snapshot, restart lagi, tandai operasi GAGAL dengan log. Sukses → commit registry, tampilkan ringkasan.

**Menambah proyek baru:** isi form → validasi path & keunikan `url_path` → (khusus Laravel) cek `public/` ada → generate conf → alur apply yang sama (langkah 2–5) → proyek tampil di dashboard dengan status hasil health check.

**Panel start (boot server):** service mulai → baca registry → Supervisor menyalakan proses CLI ber-`autostart` → Scheduler mulai ticker. Panel **tidak** menyalakan Apache; Apache service milik Windows sendiri — panel hanya memantau statusnya.

## 7. Tahapan Implementasi

Estimasi bersifat kasar (kerja paruh waktu, berpasangan dengan Muse) dan ditandai sebagai estimasi, bukan janji.
Setiap milestone mencakup automated test untuk fitur yang dibangunnya (Bagian 10) — milestone belum selesai sebelum test-nya hijau di CI.

### M0 — Spike validasi (gerbang go/no-go) · estimasi 1 akhir pekan
Tanpa menulis panel. Manual di mesin uji: satu Apache + `mod_fcgid` + dua versi PHP NTS + dua folder dummy berisi `phpinfo()` di path berbeda + satu proyek Laravel dummy.
Kriteria lolos (semua wajib):
- Dua `phpinfo()` menampilkan **versi PHP berbeda** dari satu Apache, satu port.
- Laravel dummy dapat diakses di `IP/<nama>` **tanpa** `/public`; `.env`-nya tidak dapat diakses via web (404).
- Queue worker dummy jalan memakai `php.exe` versi proyek dari terminal.
- Modul `mod_fcgid` yang cocok dengan Apache uji ditemukan & tercatat sumber + versinya.
Gagal di sini = berhenti dan evaluasi ulang desain sebelum menulis kode panel.

### M1 — Skeleton Go · estimasi 1 akhir pekan
Repo Go, struktur paket mengikuti komponen Bagian 4.1 (SOLID: core tidak tahu HTTP; API & CLI klien tipis). `panel.exe` bisa: jalan sebagai console & sebagai Windows Service, menyajikan UI kosong ter-embed di port panel, subcommand CLI kerangka (`--help`, `doctor` versi awal: cek path Apache & `httpd -v`). Logging terstruktur ke file. Kriteria: service ter-install, start saat boot (uji restart mesin uji), stop bersih.

### M2 — Registry + ConfigGen + pipa apply · estimasi 1–2 akhir pekan
Registry JSON (atomic write + validasi), template config Apache per tipe proyek, unit test golden-file untuk hasil generate, ApacheManager lengkap (backup → `httpd -t` → restart → health check → rollback). Kriteria: ganti versi PHP proyek dummy lewat **CLI** berjalan end-to-end termasuk skenario gagal yang ter-rollback otomatis.

### M3 — PHP Manager + Project CRUD (API + UI) · estimasi 1–2 akhir pekan
F2, F3, F4, F8 versi pertama: dashboard, tabel proyek, form tambah/edit, preview diff, peringatan `APP_URL` & scanner `php_value`. Kriteria: seluruh demo M0 dapat diulang murni dari UI tanpa menyentuh file config manual.

### M4 — Supervisor & Scheduler · estimasi 1 akhir pekan
F5 lengkap: definisi proses per proyek, lifecycle + backoff, log per proses, runner perintah sekali jalan, ticker scheduler. Kriteria: matikan paksa proses queue dummy → panel menyalakan ulang & mencatatnya; `schedule:run` tereksekusi tiap menit untuk proyek yang mengaktifkannya.

### M5 — CLI parity + doctor + auth · estimasi 1 akhir pekan
F6 & F7 lengkap: login admin, audit log aksi, seluruh subcommand setara fitur UI, `doctor` menyeluruh. Kriteria: skenario darurat — Apache dihentikan dari luar panel → dari CLI: `panel apache status`, `panel doctor`, `panel apache restart` memulihkan layanan tanpa membuka UI.

### M6 — Pilot & hardening · estimasi 1–2 akhir pekan + masa observasi
Checklist migrasi per proyek nyata (Bagian 8). Pilot pada **satu proyek nyata paling tidak kritis** di server target, berdampingan dengan XAMPP lama. Observasi minimal 1 minggu kerja sebelum proyek kedua dipindah. Runbook instalasi & troubleshooting ditulis dari pengalaman pilot. Fase 1 dinyatakan selesai bila memenuhi Bagian 12.

**Total estimasi: ± 6–9 akhir pekan paruh waktu sampai pilot stabil.**

## 8. Strategi Migrasi dari XAMPP (Mode Adopsi)

1. Backup penuh folder XAMPP + export config asli (`httpd.conf`, `httpd-xampp.conf`) — ini jalur pulang.
2. Pasang `mod_fcgid`; daftarkan **satu versi PHP default** = versi PHP XAMPP yang sekarang — proyek yang belum dipindah berperilaku sama seperti sebelumnya (hanya handler-nya berubah dari `mod_php` ke fcgid).
3. Nonaktifkan `php_module`; arahkan Include ke folder generate panel; `httpd -t`; restart pada jendela perawatan.
4. Daftarkan proyek satu per satu di panel (mulai paling tidak kritis): scan peringatan `php_value` → bersihkan → tetapkan versi PHP → apply → health check.
5. Rollback keseluruhan: kembalikan config asli dari backup, aktifkan lagi `php_module`, restart — XAMPP kembali seperti semula.
Prinsip: tidak ada pemindahan folder proyek, tidak ada perubahan URL, tidak ada perubahan database di seluruh proses ini.

## 9. Risiko & Mitigasi

| Risiko | Dampak | Mitigasi |
|--------|--------|----------|
| Satu config rusak → semua proyek mati (Apache tunggal) | Tinggi | Gerbang `httpd -t` sebelum apply, backup snapshot, rollback otomatis, retensi 20 snapshot (F9) |
| `mod_fcgid` tidak cocok dengan build Apache XAMPP | Sedang | Dibuktikan di M0 sebelum kode panel ditulis; `doctor` memvalidasi arsitektur/VC; versi modul dicatat & dibundel |
| `.htaccess` proyek berisi `php_value`/`php_flag` → 500 setelah pindah fcgid | Sedang | Scanner F1 wajib dijalankan sebelum proyek di-apply; panduan pindah ke `php.ini`/`.user.ini` |
| Extension PHP (mis. sqlsrv) tidak cocok versi/NTS/x64 | Sedang | Validasi `php -m` di F2; versi tidak lolos validasi tidak bisa dipilih proyek |
| Service account Apache tidak bisa tulis `storage/`/cache Laravel | Sedang | Cek akses tulis masuk `doctor`; dicatat di checklist pilot per proyek |
| Health check gagal palsu (aplikasi lambat start) | Rendah | Retry health check dengan jeda sebelum rollback dinyatakan; hasil & log ditampilkan, rollback tidak diam-diam |
| Kurva belajar Go (maintainer tunggal) | Sedang | M0–M1 sekaligus tahap belajar terbimbing; dependensi pihak ketiga seminimal mungkin (stdlib diutamakan); core terpisah rapi dari UI/API |
| Panel diretas dari LAN = kendali penuh server | Tinggi (konsekuensi) | Auth admin + hash bcrypt, port panel dibatasi LAN via firewall, semua aksi tercatat; panel tidak diekspos ke internet (D1) |

## 10. Pengujian — Automated Testing Wajib (requirement max, 2026-10-23)

Automated testing adalah bagian resmi Fase 1, bukan pelengkap. Go mendukung ini secara bawaan (`go test`) — mayoritas logika panel justru dapat dites tanpa Windows sama sekali, asal aturan desain di 10.1 dipatuhi sejak M1.

### 10.1 Aturan desain demi testability (mengikat sejak M1)
- Semua efek samping di balik interface: eksekusi perintah (`CommandRunner`), kontrol service Windows (`ServiceController`), jam (`Clock`), dan akses file registry. Core menerima interface ini (dependency inversion); test memakai fake.
- Tidak ada logika bisnis di handler HTTP/CLI — handler hanya menerjemahkan input ke pemanggilan core (sesuai arsitektur 4.1).
- Hasil ConfigGen **deterministik** (urutan blok & kunci stabil) agar dapat diuji golden-file.
- Setiap perbaikan bug wajib disertai test regresi yang gagal sebelum perbaikan dan lulus sesudahnya.
- Patokan cakupan: paket core ≥ 80% (patokan arah, bukan angka vanity); yang wajib 100% teruji adalah jalur berbahaya: validasi registry, generate config, dan logika keputusan apply/rollback.

### 10.2 Lapisan test
1. **Unit test** — jalan di mana saja (termasuk VM Linux Muse & CI), target selesai dalam hitungan detik–menit:
   - ConfigGen: golden-file per tipe proyek (laravel/php-native/static) & per versi PHP.
   - Registry: validasi (path duplikat, versi tak terdaftar, nama ilegal), atomic write, baca registry rusak → error jelas, bukan diam-diam kosong.
   - Scanner `php_value`/`php_flag`; generator `php.ini`; validasi `APP_URL`.
   - Supervisor: mesin status start/stop/crash + backoff memakai proses fake & jam fake.
   - ApacheManager: urutan keputusan backup → test → restart → health check → rollback memakai `CommandRunner`/`ServiceController` fake, termasuk skenario `httpd -t` gagal & health check gagal.
   - Handler API: `httptest` dengan core fake (status code & bentuk JSON).
2. **Integration test (Windows-only)** — build tag `integration`, TIDAK ikut `go test ./...` biasa; dijalankan eksplisit `go test -tags=integration ./...` di mesin Windows / runner CI Windows:
   - Apache portabel asli di folder sementara + PHP NTS asli: terapkan config hasil generate → `httpd -t` → Apache jalan di port uji → assert HTTP: `phpinfo()` per path menunjukkan versi yang benar, `.env` proyek Laravel membalas 404, `/public` tidak diperlukan.
   - Supervisor: spawn proses dummy berumur panjang → bunuh paksa → assert auto-restart & log tertangkap.
   - CLI ↔ API: bangun `panel.exe`, jalankan mode serve di port uji, jalankan subcommand CLI terhadapnya, assert hasil & perubahan registry.
   - Versi Apache/PHP yang dipakai integration test disematkan (pinned) sama dengan versi produksi dan di-cache oleh CI agar tidak unduh ulang tiap run.
3. **Smoke test service (semi-otomatis, mesin uji khusus):** skrip PowerShell memasang service di VM Windows uji (snapshot bersih), assert panel merespons & service menyala setelah reboot VM. Tidak dijalankan per commit (butuh hak Administrator & waktu boot) — dijalankan per milestone & sebelum rilis pilot.
4. **Pilot manual terstruktur (M6):** satu proyek nyata tidak kritis, trafik internal biasa, observasi log Apache & supervisor minimal 1 minggu kerja. Ini melengkapi, bukan menggantikan, lapisan 1–3.

### 10.3 Pipeline CI
- GitHub Actions, runner `windows-latest` (untuk unit + build) — repo proyek panel di GitHub mengikuti pola repo max yang lain.
- Tahap wajib per push/PR: `gofmt` check → `go vet` → `go test ./...` (unit) → build `panel.exe`. Job terpisah (boleh per PR atau terjadwal) menjalankan integration test dengan artefak Apache/PHP pinned yang di-cache.
- Skrip lokal setara CI disediakan (`test.ps1`) agar hasil lokal dan CI konsisten.
- Milestone dinyatakan selesai hanya bila seluruh tahap CI yang relevan hijau; kegagalan test tidak boleh "dilewati" dengan menonaktifkan test.

### 10.4 Yang secara jujur TIDAK bisa diautomatisasi penuh
- Perilaku server produksi nyata: Group Policy/antivirus kantor yang mengganggu `php-cgi`, hak akses folder proyek bervariasi, koneksi proyek ke database produksi — dicakup pilot & `doctor`, bukan test.
- Instalasi Windows Service dengan elevasi di server beneran (CI memakai VM uji; server produksi tetap checklist M6).
- Uji beban/performa bukan bagian test fungsional Fase 1.
- Tidak ada klaim lolos tanpa bukti: unit & CI dapat berjalan di VM Muse, tetapi integration test & smoke service hanya sah bila terbukti di mesin/runner Windows; verifikasi M0–M6 tetap didampingi max di mesin Windows-nya.

## 11. Keputusan Terbuka (Tidak Menghalangi M0)

1. Nama produk panel (sementara: `panel`).
2. Port default panel (usulan 8888) — pastikan tidak bentrok dengan aplikasi yang ada di server pilot.
3. Server pilot memakai adopsi XAMPP yang berjalan, atau Apache standalone baru dengan layout sama (keputusan saat M6, berdasarkan hasil M0).
4. Set versi PHP awal yang didaftarkan (usulan awal: 7.4 untuk legacy, 8.1, 8.2, 8.3 — disesuaikan inventaris proyek nyata).
5. Bahasa UI panel (Indonesia / Inggris).

**Data yang dibutuhkan dari max sebelum M3/M6:** inventaris proyek di server — nama folder, tipe (Laravel/native/static), versi PHP yang dibutuhkan, pakai queue/scheduler atau tidak, tingkat kritikalitas. Sebagian dapat digali otomatis oleh discovery F1, sisanya konfirmasi manual.

## 12. Definisi Selesai Fase 1

- [ ] M0 lolos semua kriteria go/no-go.
- [ ] `panel.exe` ter-install sebagai Windows Service di server pilot, UI dapat diakses dari LAN dengan login admin.
- [ ] Minimal 2 versi PHP terdaftar & lolos validasi extension.
- [ ] Minimal 3 proyek nyata berjalan lewat panel, minimal 2 di antaranya beda versi PHP, semua tanpa `/public` di URL (untuk Laravel) dan tanpa perubahan URL dari sebelumnya.
- [ ] Ganti versi PHP satu proyek berhasil dari UI dan dari CLI, termasuk satu uji rollback yang disengaja.
- [ ] Queue worker + scheduler satu proyek Laravel nyata dijaga Supervisor selama masa observasi pilot tanpa intervensi Task Scheduler/NSSM.
- [ ] Automated testing berjalan sesuai Bagian 10: unit test lulus di CI per push, integration test Windows lulus untuk versi Apache/PHP produksi, dan smoke test service lulus di VM uji.
- [ ] Runbook instalasi, migrasi per proyek, dan pemulihan darurat (CLI) tertulis dan pernah dipraktikkan sekali.

## 13. Log Perubahan

- 2026-10-23 — Plan dibuat & Fase 1 dikunci (D1–D10) dari rangkaian diskusi 22–23 Okt 2026.
- 2026-10-23 22:28 WIB — Atas permintaan max, automated testing dijadikan requirement resmi: Bagian 10 ditulis ulang (aturan testability, unit/integration/smoke/pilot, pipeline CI GitHub Actions, batas yang tidak bisa diautomatisasi), kriteria penyelesaian milestone & Definisi Selesai diperketat mengikuti.
- 2026-10-24 00:37 WIB — max menyatakan "go sudah terkunci" dan memerintahkan eksekusi M1. M1 dibangun hari yang sama: skeleton Go satu binary (console + Windows Service via `golang.org/x/sys`, start otomatis saat boot), UI ter-embed di port 8888, `doctor` awal (path Apache + auto-discovery, `httpd -v`, mod_fcgid, port panel, data dir), logging terstruktur JSON; aturan testability 10.1 diterapkan (semua side-effect di balik interface port di `internal/core`). Unit test lulus di VM Linux Muse + cross-compile `panel.exe` (PE32+ x64) terverifikasi; kriteria service M1 dinyatakan lulus hanya setelah smoke test di mesin Windows per `docs/m1-smoke-test.md` (Bagian 10.4). Skrip lokal pengujian berbentuk bash (`scripts/test.sh`, Git Bash) menggantikan rujukan `test.ps1` di 10.3 — mengikuti keputusan max di M0 untuk memakai bash, bukan PowerShell.

## Lampiran — Peta Fase 2 (belum direncanakan, hanya penunjuk arah)

- **Fase 2a — Deploy berbasis git:** proyek dikaitkan ke repo (`repo_url`/`branch` yang slotnya disiapkan Fase 1), tombol Deploy = pull → post-deploy (composer install, migrate, restart proses CLI).
- **Fase 2b — Git server:** panel mengelola companion service Gitea (juga satu binary Go) — bukan implementasi protokol git dari nol.
- Kandidat lain pasca-Fase 1: HTTPS internal (CA lokal), manajemen database ringan, backup terjadwal proyek.
