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

## 1. Console mode

```bat
cd C:\panel
panel.exe version
panel.exe doctor
panel.exe serve
```

- [ ] `doctor` (panel belum jalan): semua PASS, termasuk
      `httpd-version` membaca Apache dari `C:\m0\Apache24` dan
      `mod-fcgid` PASS. Exit code 0.
- [ ] Browser ke `http://127.0.0.1:8888` — UI skeleton tampil,
      kartu Status terisi dari API, kartu Doctor tampil
      (baris `panel-port` WARN "held by this panel itself" itu benar).
- [ ] `curl http://127.0.0.1:8888/healthz` → `{"status":"ok"}`
- [ ] Ctrl+C di jendela `serve` → panel berhenti bersih,
      `logs\panel.log` berisi baris `panel stopped cleanly`.

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
