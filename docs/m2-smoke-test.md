# M2 — Smoke test end-to-end di mesin Windows

M2 (registry + ConfigGen + pipa apply) dinyatakan selesai bila skenario
kriteria plan terbukti di mesin beneran: **ganti versi PHP proyek dummy
lewat CLI berjalan end-to-end, termasuk skenario gagal yang ter-rollback
otomatis.** Unit test membuktikan logikanya; checklist ini membuktikan
integrasinya dengan Apache + service Windows beneran.

Prasyarat: Apache M0 masih jalan dari M0/M1 (`C:\m0\Apache24`, PHP di
`C:\m0\php\8.2` & `C:\m0\php\8.3`, dummy di `C:\m0\htdocs`), dan
`panel.exe` v0.3.0-m2 di `C:\Panel\`. Semua perintah dari terminal
**Administrator** (restart service Apache butuh elevasi), di folder
`C:\Panel`.

> Catatan adopsi satu kali: apply menulis config ke
> `C:\Panel\data\generated\apache\*.conf`. Agar Apache membacanya,
> tambahkan SATU baris ini di akhir `C:\m0\Apache24\conf\httpd.conf`
> (di luar blok mana pun), lalu restart Apache sekali:
>
> ```
> IncludeOptional "C:/Panel/data/generated/apache/*.conf"
> ```
>
> (IncludeOptional, bukan Include biasa: Include biasa membuat `httpd -t`
> gagal bila direktori generated masih kosong — persis sebelum apply
> pertama panel. Ditemukan saat setup fresh 2026-10-26.)
>
> Config M0 (`C:\m0\conf-m0-projects.conf`) JANGAN dimatikan: baris
> `LoadModule fcgid_module` tinggal di sana, dan panel sengaja tidak
> menduplikatnya. Smoke ini memakai folder dummy BARU (`app-m2`) yang
> tidak disentuh config M0, jadi tidak ada tumpang tindih blok
> `<Directory>`.

## 1. Daftarkan PHP & proyek dummy

```bat
xcopy /E /I C:\m0\htdocs\app-php82 C:\m0\htdocs\app-m2
panel.exe php add --version 8.2 --path C:\m0\php\8.2
panel.exe php add --version 8.3 --path C:\m0\php\8.3
panel.exe php list
panel.exe project add --name app-m2 --url /app-m2 --path C:\m0\htdocs\app-m2 --type php-native --php 8.2
panel.exe project list
```

- [ ] `php list` menampilkan 8.2 & 8.3.
- [ ] `project add` berakhir `Apply OK` (snapshot tertulis, config
      ditulis, health check lulus).
- [ ] `curl http://127.0.0.1/app-m2/?check` membalas versi **8.2**.

## 2. Skenario kriteria: ganti versi PHP

```bat
panel.exe project set-php app-m2 8.3
curl http://127.0.0.1/app-m2/?check
```

- [ ] `set-php` berakhir `Apply OK` dengan health check `/app-m2`.
- [ ] Balasan `?check` sekarang versi **8.3** — proyek yang sama,
      Apache yang sama, URL yang sama.

## 3. Skenario gagal yang harus menolak SEBELUM apply

```bat
panel.exe project set-php app-m2 7.4
```

- [ ] Ditolak: `php version "7.4" is not registered` — tidak ada
      restart, tidak ada perubahan (curl tetap 8.3).

## 4. Skenario rollback otomatis

Buat proyek yang health check-nya pasti gagal (folder ada, tapi tidak
ada index yang bisa menjawab — Apache membalas 403/404):

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

## 5. Lapor balik

Tempel ke chat: output langkah 1–4 (termasuk pesan error langkah 3
& 4) dan isi `C:\Panel\data\registry.json`. Kegagalan di langkah mana
pun = M2 belum selesai; jangan lanjut M3 sebelum hijau.
