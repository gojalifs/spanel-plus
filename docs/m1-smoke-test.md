# M1 — Smoke test di mesin Windows

Skeleton Go M1 tidak bisa dinyatakan selesai hanya dari VM Linux Muse:
kriteria service (install, start saat boot, stop bersih) hanya sah bila
terbukti di mesin Windows (Plan Bagian 10.4). Checklist ini yang
membuktikannya. Jalankan di mesin uji yang sama dengan M0
(Apache M0 masih di `C:\m0\Apache24` — doctor akan menemukannya
otomatis).

## 0. Siapkan binary

Pilih satu:

- **Dari CI:** GitHub repo → tab Actions → run CI terbaru (hijau) →
  Artifacts → unduh `panel-windows-latest` → isi `panel.exe`.
- **Build sendiri:** pasang Go 1.24+ (`go version`), lalu di Git Bash
  dari root repo: `go build -o panel.exe ./cmd/panel`

Simpan `panel.exe` di folder sendiri, mis. `C:\panel\` (di luar folder
proyek, sesuai Plan D9). Semua file panel (`data\`, `logs\`,
`panel.json` opsional) hidup di folder itu.

## 1. Console mode — TERBUKTI 2026-10-24 (screenshot max)

- [x] `doctor` dari API: semua PASS terhadap Apache M0
      (`C:\m0\Apache24` auto-discovered, Apache 2.4.69, mod_fcgid,
      data dir `C:\Panel\data` writable), satu-satunya WARN adalah
      `panel-port` yang memang dirancang begitu.
- [x] UI tampil & kartu Status/Doctor terisi dari API
      (versi 0.1.0-m1 saat itu).

## 1b. Mode desktop (v0.2.0-m1, keputusan max 2026-10-24)

```bat
:: cukup double-click panel.exe, atau:
panel.exe app
```

- [ ] Jendela desktop SPanel+ terbuka (bukan tab browser), UI sama
      seperti mode web; jendela konsol bawaan double-click hilang.
- [ ] Tutup jendela → proses panel ikut berhenti bersih
      (`logs\panel.log`: `panel desktop closed`).
- [ ] Dengan service panel terpasang & jalan (langkah 2): buka
      `panel.exe app` → jendela tampil; tutup jendela → service
      TETAP jalan (UI dari browser/service masih menjawab).

## 2. Windows Service (terminal Administrator)

```bat
cd C:\panel
panel.exe service install
panel.exe service status
panel.exe service start
panel.exe service status
```

- [ ] `install` sukses; service `SPanelPlus` muncul di `services.msc`
      dengan Startup type **Automatic**.
- [ ] Setelah `start`: status `running`, UI di
      `http://127.0.0.1:8888` bisa dibuka.
- [ ] **Uji boot:** restart mesin → tanpa login/manual start, UI panel
      sudah bisa dibuka (service start otomatis).
- [ ] `panel.exe service stop` → status `stopped`, port 8888 bebas.
- [ ] `panel.exe service uninstall` → service hilang dari `services.msc`.

## 3. Lapor balik

Tempel ke chat: hasil checklist (centang + catatan gagalnya apa),
output `panel.exe doctor`, dan `panel.exe version`. Kegagalan di
langkah mana pun = M1 belum selesai; jangan diteruskan ke M2 sebelum
hijau, sesuai aturan milestone di Plan Bagian 10.
