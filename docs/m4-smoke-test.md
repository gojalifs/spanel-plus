# M4 — Smoke test Supervisor & Scheduler di mesin Windows

M4 dinyatakan selesai bila kriteria plan terbukti: **proses queue
dummy yang dimatikan paksa dinyalakan ulang panel dan tercatat di
log**, dan **`schedule:run` tereksekusi tiap menit** untuk proyek
yang mengaktifkannya. Prasyarat: panel v0.5.0-m4, proyek dummy
Laravel dari smoke M3 (atau buat baru bernama `app-m4` dari folder
dummy mana pun).

Persiapan dummy (sekali): di folder proyek dummy, buat file
`worker.php` yang berputar lama:

```php
<?php
file_put_contents(__DIR__.'/worker-alive.txt', date('H:i:s')." mulai\n", FILE_APPEND);
while (true) { sleep(5); }
```

Dan pastikan file `artisan` dummy ada di folder proyek (untuk
scheduler): file PHP bernama `artisan` berisi
`<?php file_put_contents(__DIR__.'/schedule-alive.txt', date('H:i:s')."\n", FILE_APPEND); echo "ok\n";`

Lalu dari dashboard: **Edit proyek → Proses CLI**:
- Template **queue Laravel**, lalu ubah perintahnya jadi
  `worker.php`, autostart ✓ → Simpan & apply.
- Template **scheduler Laravel**, autostart ✓ → Simpan & apply.

Jalankan panel sebagai service/console seperti biasa (supervisor
hidup selama panel jalan).

## 1. Autostart

- [ ] Buka/refresh dashboard: tabel **Proses CLI** memuat `queue`
      berstatus **running** dengan PID terisi.
- [ ] File log `data\projects\<proyek>\queue.log` berisi marker
      `--- panel start queue` + proyek benar-benar jalan
      (`worker-alive.txt` terbuat).

## 2. Kriteria utama: dimatikan paksa → dinyalakan ulang

- [ ] Dari Task Manager, **End task** PID queue tadi.
- [ ] Dalam beberapa detik (backoff mulai 1 detik), dashboard
      menampilkan PID **baru** untuk queue, kolom Restart bertambah,
      status kembali **running**.
- [ ] `queue.log` memuat baris `--- panel exit code=...` untuk
      proses yang dimatikan, disusul marker start baru.

## 3. Stop dari panel itu final

- [ ] Klik **Stop** di dashboard → status **stopped** dan TIDAK
      menyala lagi sendiri setelah 10 detik.
- [ ] Klik **Start** → running lagi dengan PID baru.

## 4. Scheduler

- [ ] Biarkan ±2 menit: file `schedule-alive.txt` di folder proyek
      bertambah satu baris per menit.
- [ ] Tombol **Jalankan sekarang** pada baris scheduler → toast
      exit 0, dan `data\projects\<proyek>\scheduler.log` memuat
      outputnya.

## 5. Perintah sekali jalan

- [ ] Box **Jalankan**: pilih proyek, perintah `artisan --version`
      (atau `worker.php` versi sekali-jalan) → output tampil dengan
      penanda `[exit 0]`.

## 6. Lapor balik

Tempel: isi `queue.log`, `scheduler.log`, dan screenshot tabel
Proses CLI sesudah uji matikan paksa (PID baru + Restart ≥ 1).
