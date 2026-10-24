# M3 — Smoke test dashboard UI di mesin Windows

M3 dinyatakan selesai bila **seluruh demo M0 dapat diulang murni dari
UI** (kriteria plan): daftarkan PHP, tambah proyek, ganti versi PHP —
tanpa menyentuh file config manual dan tanpa CLI. Prasyarat: smoke M2
sudah hijau (baris `Include` generated sudah terpasang di httpd.conf),
`panel.exe` v0.4.0-m3 di `C:\Panel\`, Apache M0 jalan.

Buka panel dengan double-click `panel.exe` (mode desktop).

## 1. Daftarkan PHP dari UI

Di kartu **Versi PHP**, isi folder `C:\m0\php\8.2` → klik
**Daftarkan versi PHP**. Ulangi untuk `C:\m0\php\8.3`.

- [ ] Toast menyebut versi terdeteksi dari binary (mis. "8.2.x, NTS
      x64") dan status php.ini (`generated` bila folder belum punya
      php.ini, `existing` bila sudah ada — yang existing TIDAK boleh
      berubah isinya).
- [ ] Tabel Versi PHP memuat 8.2 & 8.3.
- [ ] Klik **Extension** pada satu versi → daftar modul `php -m`
      tampil.

## 2. Tambah proyek dari UI

Di form **Tambah proyek**: nama `app-m3`, URL `/app-m3`, folder
`C:\m0\htdocs\app-m3` (salin dulu folder dummy:
`xcopy /E /I C:\m0\htdocs\app-php82 C:\m0\htdocs\app-m3`), tipe
PHP native, PHP 8.2, aktif → **Tambah & apply**.

- [ ] Toast sukses (bukan error), proyek muncul di tabel.
- [ ] Klik URL-nya → halaman dummy menjawab versi **8.2**.

## 3. Ganti PHP lewat preview diff (alur resmi F8)

Di tabel proyek, ubah dropdown PHP `app-m3` ke **8.3**.

- [ ] Panel **Preview perubahan config** muncul: baris merah `-`
      memuat path php-cgi 8.2, baris hijau `+` memuat 8.3.
- [ ] Klik **Apply perubahan ini** → toast sukses.
- [ ] Refresh halaman dummy → versi **8.3**.

## 4. Peringatan (F1/F4 versi pertama)

- [ ] Buat proyek Laravel dummy menunjuk folder yang `.env`-nya
      APP_URL masih berakhiran `/public` → kolom Peringatan memuat
      badge `app_url_mismatch` (arahkan kursor ke badge untuk pesan
      lengkapnya).
- [ ] Proyek bersih tidak memuat badge apa pun.

## 5. Nonaktifkan & hapus

- [ ] Matikan toggle **Aktif** `app-m3` → apply sukses, URL-nya 404.
- [ ] Nyalakan lagi → URL hidup lagi.
- [ ] **Hapus** proyek uji peringatan tadi → hilang dari tabel &
      config (URL 404), folder proyeknya sendiri tetap utuh di disk.

## 6. Utang tes yang ikut terbawa

Smoke M2 (CLI) dan kriteria service M1 (install/boot) belum sempat
diuji max saat M3 dibangun atas instruksi "lanjut dulu aja" — keduanya
tetap tercatat sebagai utang verifikasi dan sebaiknya dijalankan
sekalian sesi ini (`docs/m2-smoke-test.md`,
`docs/m1-smoke-test.md` Bagian 1b & 2).

## 7. Lapor balik

Tempel ke chat: hasil tiap langkah (terutama toast error bila ada),
plus screenshot dashboard dengan dua proyek & dua versi PHP tampil.
