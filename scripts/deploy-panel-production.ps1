<#
.SYNOPSIS
    Productionize SPanel+ sesudah smoke test & rangkaian pilot proyek selesai.
.DESCRIPTION
    Script ini BUKAN setup dari nol (itu tugas setup-panel-smoke).
    Ia mengubah instalasi yang sudah lolos smoke + pilot menjadi produksi:
      1. Backup panel.json (+ httpd.conf bila dummy ikut dihapus).
      2. panel.json: AD dummy dibuang; ganti ke AD sungguhan (LDAPS)
         ATAU ke mode admin-only bila -AdServer tidak diisi.
         ad_allow_insecure_ldap selalu dipaksa false.
      3. Opsional (-RemoveDummies): hapus folder dummy M0
         (app-php82, app-php83, app-laravel) + cabut Include
         conf-m0-projects.conf dari httpd.conf.
      4. Restart SPanelPlus (panel.json dibaca saat start) +
         httpd -t + gate httpd -M (rewrite_module & fcgid_module
         wajib ke-load — modul mati lolos dari httpd -t, tapi
         bikin pretty URL Laravel 404 diam-diam, bug 2026-10-28)
         + restart service Apache.
      5. Verifikasi: doctor (dengan pengecualian panel-port,
         yang selalu FAIL dari CLI saat service jalan — itu
         artefak, bukan kerusakan), API panel, dan semua URL
         proyek aktif (status akhir < 400, sama seperti
         health check panel).
      6. Opsional (-PanelAllowSubnet): aturan firewall inbound
         untuk port panel, hanya dari subnet admin/IT.
    Yang TIDAK dilakukan script ini: mendaftarkan proyek/PHP
    (pilot sudah terdaftar), instal Apache/PHP, dan memasang
    TLS reverse proxy (panel sendiri plain HTTP — lihat §7
    deployment.md bila port panel dibuka ke luar LAN).
    PERINGATAN: sesudah deployment, JANGAN re-run script setup
    smoke — langkah 9 setup menulis ulang panel.json ke
    default dummy (AD dummy aktif lagi).
.PARAMETER PanelDir
    Folder panel. Bila tidak diisi, script bertanya
    "Folder panel [C:\Panel]" (kosong = default) — sama
    seperti prompt Port Apache di setup. Parameter yang
    diisi selalu menang (untuk jalan non-interaktif).
    WAJIB format Windows absolut dengan backslash
    (mis. C:\Panel); slash (/) ditolak — prompt mengulang,
    parameter yang salah langsung gagal.
.PARAMETER M0Dir
    Folder M0. Bila tidak diisi, script bertanya
    "Folder M0 [C:\m0]" (kosong = default). Aturan format
    sama seperti -PanelDir.
.PARAMETER AdServer
    Server AD produksi, wajib ldaps://<fqdn-dc>:636 bila diisi.
    Kosong = mode admin-only (ad_enabled=false). Bila dikosongkan
    sementara panel.json sudah berisi AD sungguhan (bukan dummy),
    setting AD yang ada DIPERTAHANKAN (re-run aman).
.PARAMETER AdDomain
    Domain AD (mis. siws.local). Bila -AdServer diisi tapi ini
    kosong, diambil dari panel.json bila ada, bila tidak ada -> gagal.
.PARAMETER AdBaseDn
    Base DN (mis. DC=siws,DC=local). Aturan fallback sama.
.PARAMETER AdGroupDn
    DN grup IT yang boleh login (mis. CN=IS,OU=...,DC=siws,DC=local).
    Aturan fallback sama.
.PARAMETER AdCaFile
    File CA DC (opsional; kosong = percaya system roots).
.PARAMETER PanelAllowSubnet
    Subnet yang boleh mengakses port panel (mis. 192.168.89.0/24).
    Kosong = firewall tidak diubah, hanya peringatan.
.PARAMETER RemoveDummies
    Hapus folder dummy M0 + cabut Include M0 dari httpd.conf.
.PARAMETER SkipProjectChecks
    Lewati pengecekan URL proyek (mis. ada proyek yang sengaja
    sedang down saat go-live).
.PARAMETER Force
    Lewati konfirmasi interaktif.
.EXAMPLE
    # Produksi admin-only + hapus dummy (AD menyusul belakangan):
    .\deploy-panel-production.ps1 -RemoveDummies
.EXAMPLE
    # Produksi AD sungguhan + firewall hanya dari subnet kantor:
    .\deploy-panel-production.ps1 -AdServer ldaps://dc.siws.local:636 `
        -AdDomain siws.local -AdBaseDn "DC=siws,DC=local" `
        -AdGroupDn "CN=IS,DC=siws,DC=local" `
        -RemoveDummies -PanelAllowSubnet 192.168.89.0/24
#>

[CmdletBinding()]
param(
    [string]$PanelDir = "",
    [string]$M0Dir = "",
    [string]$AdServer = "",
    [string]$AdDomain = "",
    [string]$AdBaseDn = "",
    [string]$AdGroupDn = "",
    [string]$AdCaFile = "",
    [string]$PanelAllowSubnet = "",
    [switch]$RemoveDummies,
    [switch]$SkipProjectChecks,
    [switch]$Force
)

$ErrorActionPreference = "Stop"

function Fail([string]$Message) {
    Write-Host ""
    Write-Host "DEPLOY FAIL: $Message" -ForegroundColor Red
    Write-Host "Berhenti di sini - jangan anggap produksi. Kirim pesan fail ini ke Muse."
    exit 1
}

function Info([string]$Message) {
    Write-Host "==> $Message" -ForegroundColor Cyan
}

function Warn([string]$Message) {
    Write-Host "PERINGATAN: $Message" -ForegroundColor Yellow
}

function Wait-ServiceRunning([string]$Name, [int]$TimeoutSec = 60) {
    for ($i = 0; $i -lt $TimeoutSec; $i += 2) {
        $svc = Get-Service -Name $Name -ErrorAction SilentlyContinue
        if ($null -ne $svc -and $svc.Status -eq 'Running') { return }
        Start-Sleep -Seconds 2
    }
    Fail "Service $Name tidak Running sesudah ${TimeoutSec} detik."
}

# --- 0. Harus Administrator ---------------------------------------------------
$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Fail "Script ini harus jalan dari PowerShell sebagai Administrator (klik kanan PowerShell -> Run as administrator)."
}

# --- Folder instalasi: parameter menang; kalau tidak diisi, tanya -------------
# Pola yang sama dengan prompt Port Apache di setup: default ditampilkan
# di dalam kurung siku, jawaban kosong = default. Untuk jalan
# non-interaktif (scheduled task dkk.), isi -PanelDir/-M0Dir eksplisit.
# Path WAJIB gaya Windows absolut dengan backslash. Slash (/) ditolak:
# panel.json memang memakai slash di dalamnya, tapi input script ini
# harus backslash supaya Join-Path, pesan error, dan perbandingan path
# konsisten — tanpa konversi diam-diam yang menutupi salah ketik.
# Prompt mengulang sampai valid; parameter yang salah langsung gagal.
function Test-WindowsPath([string]$Path) {
    if ([string]::IsNullOrWhiteSpace($Path)) { return $false }
    if ($Path -match '/') { return $false }
    return ($Path -match '^[a-zA-Z]:\\') -or ($Path -match '^\\\\')
}

function Read-WindowsPath([string]$Prompt, [string]$Default) {
    while ($true) {
        $in = Read-Host "$Prompt [$Default]"
        if ([string]::IsNullOrWhiteSpace($in)) { return $Default }
        $p = $in.Trim()
        if (-not (Test-WindowsPath $p)) {
            Write-Host "Path tidak valid: pakai format Windows absolut dengan backslash, mis. C:\Panel (slash / tidak diterima)." -ForegroundColor Yellow
            continue
        }
        return $p
    }
}

if ([string]::IsNullOrWhiteSpace($PanelDir)) {
    $PanelDir = Read-WindowsPath "Folder panel" "C:\Panel"
} elseif (-not (Test-WindowsPath $PanelDir)) {
    Fail "-PanelDir harus path Windows absolut dengan backslash, mis. C:\Panel (dapat: $PanelDir)."
}
if ([string]::IsNullOrWhiteSpace($M0Dir)) {
    $M0Dir = Read-WindowsPath "Folder M0" "C:\m0"
} elseif (-not (Test-WindowsPath $M0Dir)) {
    Fail "-M0Dir harus path Windows absolut dengan backslash, mis. C:\m0 (dapat: $M0Dir)."
}
Info "Folder panel: $PanelDir"
Info "Folder M0: $M0Dir"

# --- 1. Prasyarat: instalasi setup sudah ada ----------------------------------
$panelExe = Join-Path $PanelDir "panel.exe"
$panelJsonPath = Join-Path $PanelDir "panel.json"
$httpd = Join-Path $M0Dir "Apache24\bin\httpd.exe"
$httpdConf = Join-Path $M0Dir "Apache24\conf\httpd.conf"
if (-not (Test-Path $panelExe)) { Fail "panel.exe tidak ditemukan di $panelExe - jalankan setup dulu." }
if (-not (Test-Path $panelJsonPath)) { Fail "panel.json tidak ditemukan di $panelJsonPath - jalankan setup dulu." }
if (-not (Test-Path $httpd)) { Fail "httpd.exe tidak ditemukan di $httpd." }
if ($null -eq (Get-Service -Name "SPanelPlus" -ErrorAction SilentlyContinue)) {
    Fail "Service SPanelPlus tidak ditemukan - jalankan setup dulu."
}

$cfg = [System.IO.File]::ReadAllText($panelJsonPath) | ConvertFrom-Json
$panelPort = 8888
if ($null -ne $cfg.port -and $cfg.port -gt 0) { $panelPort = $cfg.port }
$apacheService = "apache-spanel"
if (-not [string]::IsNullOrWhiteSpace($cfg.apache_service)) { $apacheService = $cfg.apache_service }
if ($null -eq (Get-Service -Name $apacheService -ErrorAction SilentlyContinue)) {
    Fail "Service Apache $apacheService tidak ditemukan - jalankan setup dulu."
}
if ([string]::IsNullOrWhiteSpace($cfg.data_dir)) {
    $dataDir = Join-Path $PanelDir "data"
} else {
    $dataDir = ($cfg.data_dir -replace '/', '\')
}
$registryPath = Join-Path $dataDir "registry.json"
$adminPath = Join-Path $dataDir "admin.json"

# --- 2. Break-glass: akun admin lokal wajib ada SEBELUM AD dummy dibuang -------
# Kalau tidak ada, membuang dummy = tidak ada jalan login sama sekali
# saat AD bermasalah. (data_dir dari panel.json; default <PanelDir>\data.)
if (-not (Test-Path $adminPath)) {
    Fail "Akun admin lokal belum ada ($adminPath tidak ditemukan). Jalankan dulu: $panelExe admin set-password — lalu login sekali untuk memastikan — baru jalankan deployment ini."
}

# --- 3. Tentukan mode AD -------------------------------------------------------
# Prioritas: parameter > AD sungguhan yang sudah ada > admin-only.
# (ad_server "dummy" TIDAK dihitung sebagai AD sungguhan.)
$existingRealAd = (-not [string]::IsNullOrWhiteSpace($cfg.ad_server)) -and ($cfg.ad_server -ne "dummy")
if (-not [string]::IsNullOrWhiteSpace($AdServer)) {
    $mode = "AD"
    if ($AdServer -notmatch '^ldaps://') {
        Fail "-AdServer harus ldaps:// (produksi menolak LDAP polos). Contoh: ldaps://dc.siws.local:636"
    }
    $adHost = ($AdServer -replace '^ldaps://', '') -split ':' | Select-Object -First 1
    if ($adHost -match '^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$') {
        Fail "-AdServer memakai IP ($adHost); LDAPS butuh FQDN DC untuk validasi sertifikat."
    }
    if ([string]::IsNullOrWhiteSpace($AdDomain)) { $AdDomain = $cfg.ad_domain }
    if ([string]::IsNullOrWhiteSpace($AdBaseDn)) { $AdBaseDn = $cfg.ad_base_dn }
    if ([string]::IsNullOrWhiteSpace($AdGroupDn)) { $AdGroupDn = $cfg.ad_group_dn }
    if ([string]::IsNullOrWhiteSpace($AdDomain)) { Fail "-AdDomain kosong dan tidak ada di panel.json." }
    if ([string]::IsNullOrWhiteSpace($AdBaseDn)) { Fail "-AdBaseDn kosong dan tidak ada di panel.json." }
    if ([string]::IsNullOrWhiteSpace($AdGroupDn)) { Fail "-AdGroupDn kosong dan tidak ada di panel.json (cek DN grup IT di ADUC)." }
} elseif ($existingRealAd -and $cfg.ad_enabled) {
    $mode = "AD (dipertahankan)"
    $AdServer = $cfg.ad_server
    $AdDomain = $cfg.ad_domain
    $AdBaseDn = $cfg.ad_base_dn
    $AdGroupDn = $cfg.ad_group_dn
    if ([string]::IsNullOrWhiteSpace($AdCaFile)) { $AdCaFile = $cfg.ad_ca_file }
    Info "AD sungguhan sudah ada di panel.json dan -AdServer tidak diisi: setting AD dipertahankan."
} else {
    $mode = "admin-only"
    Info "Mode admin-only: AD dimatikan (ad_enabled=false), login hanya akun panel break-glass."
}

$dummyCount = 0
if ($null -ne $cfg.ad_dummy_users) { $dummyCount = @($cfg.ad_dummy_users).Count }

# --- 4. Rencana + konfirmasi ---------------------------------------------------
$stamp = Get-Date -Format "yyyyMMdd-HHmmss"
$panelBackup = Join-Path $PanelDir "panel.json.bak-deploy-$stamp"
$httpdBackup = Join-Path (Join-Path $M0Dir "Apache24\conf") "httpd.conf.bak-deploy-$stamp"
Write-Host ""
Write-Host "RENCANA DEPLOYMENT" -ForegroundColor Green
Write-Host "  Panel      : $PanelDir (port $panelPort, service SPanelPlus)"
Write-Host "  Apache     : $M0Dir (service $apacheService)"
Write-Host "  Mode login : $mode"
if ($mode -like "AD*") {
    Write-Host "  AD server  : $AdServer"
    Write-Host "  AD domain  : $AdDomain"
    Write-Host "  AD base DN : $AdBaseDn"
    Write-Host "  AD grup    : $AdGroupDn"
    Write-Host "  AD CA file : $(if ([string]::IsNullOrWhiteSpace($AdCaFile)) { '(system roots)' } else { $AdCaFile })"
}
Write-Host "  Dummy users dibuang : $dummyCount"
Write-Host "  Backup panel.json   : $panelBackup"
if ($RemoveDummies) {
    Write-Host "  Dummy M0 dihapus    : app-php82, app-php83, app-laravel + Include conf-m0-projects.conf"
    Write-Host "  Backup httpd.conf   : $httpdBackup"
} else {
    Write-Host "  Dummy M0            : DIBIARKAN (pakai -RemoveDummies untuk menghapus)"
}
if ([string]::IsNullOrWhiteSpace($PanelAllowSubnet)) {
    Write-Host "  Firewall port panel : TIDAK DIUBAH (lihat peringatan di akhir)"
} else {
    Write-Host "  Firewall port panel : hanya dari $PanelAllowSubnet"
}
if (-not $Force) {
    $ok = Read-Host "Lanjut deployment? Ketik Y untuk lanjut"
    if ($ok -ne 'Y') { Write-Host "Dibatalkan oleh user."; exit 0 }
}

# --- 5. Backup + tulis panel.json ----------------------------------------------
Copy-Item $panelJsonPath $panelBackup -Force
Info "Backup panel.json: $panelBackup"
$cfg.ad_enabled = ($mode -like "AD*")
if ($mode -like "AD*") {
    $cfg.ad_server = $AdServer
    $cfg.ad_domain = $AdDomain
    $cfg.ad_base_dn = $AdBaseDn
    $cfg.ad_group_dn = $AdGroupDn
    $cfg.ad_ca_file = $AdCaFile
}
$cfg.ad_allow_insecure_ldap = $false
$cfg.PSObject.Properties.Remove('ad_dummy_users')
[System.IO.File]::WriteAllText($panelJsonPath, ($cfg | ConvertTo-Json -Depth 10), (New-Object System.Text.UTF8Encoding $false))
Info "panel.json ditulis: AD dummy dibuang, ad_allow_insecure_ldap=false."

# --- 6. Dummy M0 (opsional) -----------------------------------------------------
if ($RemoveDummies) {
    if (-not (Test-Path $httpdConf)) { Fail "httpd.conf tidak ditemukan di $httpdConf." }
    Copy-Item $httpdConf $httpdBackup -Force
    Info "Backup httpd.conf: $httpdBackup"
    foreach ($d in @("app-php82", "app-php83", "app-laravel")) {
        $p = Join-Path (Join-Path $M0Dir "htdocs") $d
        if (Test-Path $p) {
            Remove-Item $p -Recurse -Force
            Info "Folder dummy dihapus: $p"
        }
    }
    $httpdText = [System.IO.File]::ReadAllText($httpdConf)
    $httpdText2 = $httpdText -replace '(?m)^[^\n]*conf-m0-projects\.conf[^\n]*\n?', ''
    if ($httpdText2 -ne $httpdText) {
        [System.IO.File]::WriteAllText($httpdConf, $httpdText2, (New-Object System.Text.UTF8Encoding $false))
        Info "Include conf-m0-projects.conf dicabut dari httpd.conf."
    } else {
        Info "Include conf-m0-projects.conf sudah tidak ada di httpd.conf."
    }
}

# --- 7. Restart + gate -----------------------------------------------------------
Restart-Service -Name "SPanelPlus" -Force
Wait-ServiceRunning "SPanelPlus"
Info "Service SPanelPlus di-restart (panel.json baru ter-load)."

$syntax = (& $httpd -t 2>&1) -join "`n"
Write-Host $syntax
if ($syntax -notmatch 'Syntax OK') {
    Fail "httpd -t TIDAK Syntax OK. Output di atas - jangan lanjut; pulihkan dari $httpdBackup bila perlu."
}
$mods = (& $httpd -M 2>&1) -join "`n"
foreach ($m in @("rewrite_module", "fcgid_module")) {
    if ($mods -notmatch $m) {
        Fail "$m TIDAK ke-load (httpd -M). Periksa LoadModule di httpd.conf dan file modules/$m.so - jangan lanjutkan deployment."
    }
}
Info "rewrite_module + fcgid_module ter-load (httpd -M)."

Restart-Service -Name $apacheService -Force
Wait-ServiceRunning $apacheService
Info "Service $apacheService di-restart."

# --- 8. Verifikasi: doctor -------------------------------------------------------
# doctor CLI exit 1 bila ada FAIL. Satu-satunya FAIL yang ditoleransi
# adalah panel-port: dari CLI, doctor tidak bisa membuktikan port panel
# dipegang service panel sendiri, jadi port yang sedang dipakai service
# selalu FAIL padahal itu by design. FAIL lain = gagal deployment.
$doctorOut = (& $panelExe doctor 2>&1) -join "`n"
Write-Host $doctorOut
$doctorCode = $LASTEXITCODE
$failNames = @([regex]::Matches($doctorOut, '(?m)^FAIL\s+(\S+)') | ForEach-Object { $_.Groups[1].Value })
$realFails = @($failNames | Where-Object { $_ -ne 'panel-port' })
if ($doctorCode -ne 0 -and $realFails.Count -gt 0) {
    Fail "Doctor TIDAK SEHAT (FAIL: $($realFails -join ', ')). Perbaiki baris FAIL di atas."
}
if ($failNames -contains 'panel-port') {
    Info "Doctor: panel-port FAIL ditoleransi (artefak CLI saat service jalan — port dipegang SPanelPlus sendiri)."
} else {
    Info "Doctor lulus."
}

# --- 9. Verifikasi: API panel -----------------------------------------------------
try {
    $st = Invoke-WebRequest -Uri "http://127.0.0.1:$panelPort/api/v1/auth/status" -UseBasicParsing -TimeoutSec 10 |
        Select-Object -ExpandProperty Content | ConvertFrom-Json
} catch {
    Fail "API panel tidak menjawab di http://127.0.0.1:$panelPort/api/v1/auth/status : $($_.Exception.Message)"
}
if ($st.configured -ne $true) { Fail "API panel bilang akun admin belum dibuat (configured=false)." }
$wantAd = ($mode -like "AD*")
if ($st.ad_enabled -ne $wantAd) {
    Fail "API panel bilang ad_enabled=$($st.ad_enabled), harusnya $wantAd - SPanelPlus mungkin belum restart dengan panel.json baru."
}
Info "API panel menjawab: configured=true, ad_enabled=$wantAd."

# --- 10. Verifikasi: URL proyek aktif ----------------------------------------------
if (-not $SkipProjectChecks) {
    if (-not (Test-Path $registryPath)) {
        Fail "Registry tidak ditemukan di $registryPath - proyek pilot belum terdaftar?"
    }
    $reg = [System.IO.File]::ReadAllText($registryPath) | ConvertFrom-Json
    $active = @($reg.projects | Where-Object { $_.enabled -eq $true })
    if ($active.Count -eq 0) { Warn "Tidak ada proyek aktif di registry - pengecekan URL dilewati." }
    $curl = Get-Command curl.exe -ErrorAction SilentlyContinue
    if ($null -eq $curl -and $active.Count -gt 0) {
        Fail "curl.exe tidak ditemukan, padahal diperlukan untuk cek URL proyek. Pakai -SkipProjectChecks bila sengaja dilewati."
    }
    # Port Apache dari Listen pertama di httpd.conf (default 80).
    $apachePort = 80
    $listen = [regex]::Match([System.IO.File]::ReadAllText($httpdConf), '(?m)^\s*Listen\s+(?:\S+:)?(\d+)')
    if ($listen.Success) { $apachePort = [int]$listen.Groups[1].Value }
    $bad = @()
    foreach ($p in $active) {
        $url = "http://127.0.0.1$($p.url_path)"
        if ($apachePort -ne 80) { $url = "http://127.0.0.1:$apachePort$($p.url_path)" }
        $code = (& curl.exe -s -o NUL -w '%{http_code}' -L --max-redirs 10 --max-time 10 $url) -as [int]
        if ($code -lt 400 -and $code -gt 0) {
            Info "URL proyek $($p.name) menjawab $code : $url"
        } else {
            $bad += "$($p.name) -> $url (HTTP $code)"
        }
    }
    if ($bad.Count -gt 0) {
        Fail "Proyek berikut TIDAK menjawab dengan baik (aturan sama seperti health check panel):`n  $($bad -join "`n  ")"
    }
}

# --- 11. Firewall port panel (opsional) ----------------------------------------------
if (-not [string]::IsNullOrWhiteSpace($PanelAllowSubnet)) {
    if ($PanelAllowSubnet -notmatch '^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(/\d{1,2})?$') {
        Fail "-PanelAllowSubnet harus IP/subnet, mis. 192.168.89.0/24 (dapat: $PanelAllowSubnet)."
    }
    Get-NetFirewallRule -DisplayName "SPanelPlus-Admin" -ErrorAction SilentlyContinue | Remove-NetFirewallRule
    New-NetFirewallRule -DisplayName "SPanelPlus-Admin" -Direction Inbound -Protocol TCP `
        -LocalPort $panelPort -RemoteAddress $PanelAllowSubnet -Action Allow -Profile Any | Out-Null
    Info "Firewall: port $panelPort hanya diizinkan dari $PanelAllowSubnet (rule SPanelPlus-Admin)."
}

# --- 12. Ringkasan + sisa manual -----------------------------------------------------
Write-Host ""
Write-Host "DEPLOYMENT SELESAI - panel sudah produksi." -ForegroundColor Green
Write-Host "  Mode login : $mode"
Write-Host "  Backup     : $panelBackup"
if ($RemoveDummies) { Write-Host "  Backup     : $httpdBackup" }
Write-Host ""
Write-Host "SISA MANUAL (tidak bisa diotomasi dari script ini):"
if ($mode -like "AD*") {
    Write-Host "  [ ] Login AD user grup IT -> harus sukses; user non-IT -> harus ditolak; akun panel break-glass -> harus sukses."
} else {
    Write-Host "  [ ] Login akun panel break-glass dari browser admin."
}
Write-Host "  [ ] Buka URL proyek pilot dari workstation user (bukan hanya localhost)."
if ([string]::IsNullOrWhiteSpace($PanelAllowSubnet)) {
    Write-Host "  [ ] KUNCI PORT PANEL: panel di http://<server>:$panelPort masih plain HTTP tanpa TLS. Batasi via firewall hanya dari workstation admin/IT, mis:"
    Write-Host "        New-NetFirewallRule -DisplayName SPanelPlus-Admin -Direction Inbound -Protocol TCP -LocalPort $panelPort -RemoteAddress <subnet-admin> -Action Allow"
    Write-Host "      atau pasang TLS reverse proxy di depan panel. JANGAN biarkan port ini terbuka ke jaringan tak terpercaya."
}
Write-Host "  [ ] JANGAN re-run script setup smoke sesudah ini: langkah 9 setup menulis ulang panel.json ke default dummy."
