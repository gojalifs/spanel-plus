#Requires -Version 5.1
<#
.SYNOPSIS
    Setup (bila belum ada) + productionize SPanel+ dalam satu script.
.DESCRIPTION
    Satu script dari mesin kosong sampai produksi. Urutannya:
      0. Deteksi (read-only): apa yang sudah ada di-skip, apa yang
         belum ada masuk rencana. TIDAK ada download/mutasi sebelum
         konfirmasi (kecuali -Force).
      1. Provision (hanya yang belum ada):
         - panel.exe: disalin dari file panel*.exe sebelahan dengan
           script (diutamakan panel-v*.exe terbaru, aturan setup).
           Kalau tidak ada di PanelDir maupun di folder script,
           gagal dengan pesan jelas.
         - VC++ Redistributable dicek keras (vcruntime140.dll).
         - Apache: di-skip bila Apache24/bin/httpd.exe sudah ada.
           Bila belum ada, download dari Apache Lounge: discovery
           versi terbaru VS18 dulu, fallback ke pinned 2.4.69
           bila discovery gagal. Folder Apache24 yang ada tapi
           httpd.exe-nya hilang dianggap instalasi rusak dan
           dipasang ulang (bukan di-skip diam-diam).
         - mod_fcgid: di-skip bila modules/mod_fcgid.so sudah ada,
           bila belum download pinned 2.3.10 VS18 (VS harus sama
           dengan Apache, kalau tidak modul tidak mau di-load).
         - PHP 8.2, 8.3, 8.4 NTS x64 ke M0/php/<cabang>: per cabang
           di-skip bila php.exe sudah ada. Bila belum, discovery
           patch terbaru dari php.net dulu, fallback ke arsip
           pinned bila discovery gagal.
         - httpd.conf: di-patch idempotent (backup sekali ke
           .orig-m0): SRVROOT/ServerRoot/DocumentRoot/DirectoryIndex
           menunjuk M0Dir, rewrite diaktifkan, mod_php ditolak,
           IncludeOptional config generated panel dipastikan,
           dan LoadModule fcgid dipastikan tetap ada di httpd.conf
           sendiri (bukan hanya di file M0 - supaya -RemoveDummies
           tidak mematikan fcgid).
         - Service apache-spanel + SPanelPlus: dipasang bila belum
           ada (termasuk migrasi service Apache2.4 lama milik setup),
           lalu dipastikan Running.
      2. Productionize (perilaku deploy lama):
         panel.json: AD dummy dibuang; ganti ke AD sungguhan (LDAPS)
         ATAU ke mode admin-only bila -AdServer tidak diisi.
         ad_allow_insecure_ldap selalu dipaksa false. Bila panel.json
         belum ada, dibuat baru langsung dalam mode produksi
         (tidak pernah ditulis dummy dulu).
         Akun admin break-glass: bila belum ada, set-password
         interaktif (bukan gagal) - membuang dummy tanpa admin
         berarti tidak ada jalan login sama sekali.
      3. Opsional (-RemoveDummies): hapus folder dummy M0
         (app-php82, app-php83, app-laravel) + cabut Include
         conf-m0-projects.conf dari httpd.conf.
      4. Verifikasi: httpd -t + gate httpd -M (rewrite_module dan
         fcgid_module wajib ke-load - modul mati lolos dari httpd -t,
         tapi bikin pretty URL Laravel 404 diam-diam, bug 2026-10-28),
         doctor (dengan pengecualian panel-port, yang selalu FAIL
         dari CLI saat service jalan - itu artefak, bukan kerusakan),
         API panel, dan semua URL proyek aktif (status akhir < 400,
         sama seperti health check panel). Bila registry belum ada
         (mesin fresh, proyek menyusul), cek URL dilewati dengan
         peringatan, bukan gagal.
      5. Opsional (-PanelAllowSubnet): aturan firewall inbound
         untuk port panel, hanya dari subnet admin/IT.
    Yang TIDAK dilakukan script ini: mendaftarkan proyek/PHP ke
    panel (proyek didaftarkan belakangan dari dashboard/CLI;
    PHP yang sudah di-download tinggal didaftarkan dari dashboard
    Versi PHP), dan memasang TLS reverse proxy (panel sendiri
    plain HTTP - lihat bagian 7 deployment.md bila port panel
    dibuka ke luar LAN).
    PERINGATAN: sesudah deployment, JANGAN re-run script setup
    smoke - langkah 9 setup menulis ulang panel.json ke
    default dummy (AD dummy aktif lagi).
    CATATAN ENCODING: file ini WAJIB ASCII murni. PowerShell 5.1
    membaca file tanpa BOM sebagai ANSI; karakter non-ASCII di
    string kode merusak parse (bug 2026-10-28, commit d02da88).
.PARAMETER PanelDir
    Folder panel. Bila tidak diisi, script bertanya
    "Folder panel [C:\Panel]" (kosong = default) - sama
    seperti prompt Port Apache di setup. Parameter yang
    diisi selalu menang (untuk jalan non-interaktif).
    WAJIB format Windows absolut dengan backslash
    (mis. C:\Panel); slash (/) ditolak - prompt mengulang,
    parameter yang salah langsung gagal.
.PARAMETER M0Dir
    Folder M0. Bila tidak diisi, script bertanya
    "Folder M0 [C:\m0]" (kosong = default). Aturan format
    sama seperti -PanelDir.
.PARAMETER ApacheServiceName
    Nama service Apache. Hanya dipakai bila panel.json belum
    ada (instalasi fresh) atau panel.json tidak menyebut
    apache_service. Instalasi yang sudah ada memakai
    apache_service dari panel.json.
.PARAMETER ApachePort
    Port Apache. Hanya dipakai bila httpd.conf belum ada
    (Apache fresh): env APACHE_PORT menang, lalu parameter
    ini, lalu prompt "Port Apache [80]". Bila httpd.conf
    sudah ada, port yang sudah ada dipertahankan (tidak
    ditanya, tidak diubah). Port 8888 (port panel) ditolak.
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
    # Mesin kosong sampai produksi admin-only + hapus dummy:
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
    [string]$ApacheServiceName = "apache-spanel",
    [int]$ApachePort = 0,
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

function Download-File([string]$Url, [string]$Dest, [long]$MinBytes) {
    Info "Download $Url"
    try {
        Invoke-WebRequest -Uri $Url -OutFile $Dest -UseBasicParsing -ErrorAction Stop
    } catch {
        Fail "Download gagal dari $Url : $($_.Exception.Message)"
    }
    $size = (Get-Item $Dest).Length
    if ($size -lt $MinBytes) {
        Fail "File hasil download mencurigakan (size=$size): $Dest dari $Url"
    }
}

function Try-Download([string]$Url, [string]$Dest) {
    try {
        Invoke-WebRequest -Uri $Url -OutFile $Dest -UseBasicParsing -ErrorAction Stop
        return (Get-Item $Dest).Length -gt 1000000
    } catch {
        if (Test-Path $Dest) { Remove-Item $Dest -Force }
        return $false
    }
}

function Find-ChildFile([string]$Root, [string]$Name) {
    $found = Get-ChildItem -Path $Root -Recurse -Filter $Name -File -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($null -eq $found) { return $null }
    return $found.FullName
}

function Write-TextFile([string]$Path, [string]$Content) {
    $dir = Split-Path $Path -Parent
    if (-not (Test-Path $dir)) { New-Item -ItemType Directory -Path $dir -Force | Out-Null }
    # Tanpa BOM: Go encoding/json menolak BOM di awal panel.json.
    [System.IO.File]::WriteAllText($Path, $Content, (New-Object System.Text.UTF8Encoding $false))
}

function Discover-PhpUrl([string]$Branch) {
    # Discovery patch terbaru dari php.net (pola setup). Gagal -> $null,
    # pemanggil memakai fallback arsip pinned. Hanya baca halaman, tidak
    # download zip, jadi aman dipanggil saat deteksi rencana.
    try {
        $html = (Invoke-WebRequest -Uri "https://www.php.net/downloads.php?os=windows&version=$Branch" -UseBasicParsing -TimeoutSec 30 -ErrorAction Stop).Content
        $m = [regex]::Match($html, "php-$Branch\.[0-9]+-nts-Win32-vs[0-9]+-x64\.zip")
        if ($m.Success) { return "https://windows.php.net/downloads/releases/$($m.Value)" }
    } catch {
        return $null
    }
    return $null
}

function Discover-ApacheUrl {
    # Discovery Apache terbaru VS18 dari Apache Lounge. VS dibatasi ke
    # VS18 supaya mod_fcgid VS18 tetap bisa di-load (VS beda = modul
    # tidak mau di-load). Gagal -> $null, pemanggil memakai pinned.
    try {
        $html = (Invoke-WebRequest -Uri "https://www.apachelounge.com/download/" -UseBasicParsing -TimeoutSec 30 -ErrorAction Stop).Content
        $m = [regex]::Match($html, "httpd-2\.4\.[0-9]+-[0-9]+-Win64-VS18\.zip")
        if ($m.Success) { return "https://www.apachelounge.com/download/VS18/binaries/$($m.Value)" }
    } catch {
        return $null
    }
    return $null
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
# konsisten - tanpa konversi diam-diam yang menutupi salah ketik.
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

# --- 1. Deteksi (read-only): apa yang sudah ada, apa yang belum ---------------
# Prasyarat keras (gagal sebelum rencana, bukan sesudah konfirmasi).
if (-not (Test-Path "$env:SystemRoot\System32\vcruntime140.dll")) {
    Fail "vcruntime140.dll tidak ada - install dulu VC++ Redistributable 2015-2022 x64: https://aka.ms/vs/17/release/vc_redist.x64.exe lalu jalankan ulang script ini."
}

$panelExe = Join-Path $PanelDir "panel.exe"
$panelJsonPath = Join-Path $PanelDir "panel.json"
$httpd = Join-Path $M0Dir "Apache24\bin\httpd.exe"
$httpdConf = Join-Path $M0Dir "Apache24\conf\httpd.conf"
$dlDir = Join-Path $M0Dir "downloads"
$fcgidSo = Join-Path $M0Dir "Apache24\modules\mod_fcgid.so"

$havePanelExe = Test-Path $panelExe
$havePanelJson = Test-Path $panelJsonPath
$haveHttpd = Test-Path $httpd
$haveFcgid = Test-Path $fcgidSo

# Sumber panel exe bila belum terpasang (aturan setup: file panel*.exe
# sebelahan dengan script, diutamakan panel-v*.exe terbaru).
$panelExeSrc = $null
if (-not $havePanelExe) {
    $candidates = Get-ChildItem -Path $PSScriptRoot -Filter "panel*.exe" -File -ErrorAction SilentlyContinue
    $panelExeSrc = ($candidates | Where-Object { $_.Name -match '^panel-v.*\.exe$' } | Sort-Object LastWriteTime -Descending | Select-Object -First 1)
    if ($null -eq $panelExeSrc) { $panelExeSrc = $candidates | Select-Object -First 1 }
    if ($null -eq $panelExeSrc) {
        Fail "panel.exe tidak ada di $panelExe dan tidak ada file panel*.exe di folder script ($PSScriptRoot). Download dulu panel exe dari release GitHub (mis. panel-v0.6.2-m5.exe) dan taruh sebelahan dengan script ini, lalu jalankan ulang."
    }
}

# panel.json yang ada dibaca; yang belum ada dinormalisasi ke objek kosong
# supaya seluruh logika mode/plan di bawah tidak perlu bercabang $null.
$cfg = $null
if ($havePanelJson) {
    try {
        $cfg = [System.IO.File]::ReadAllText($panelJsonPath) | ConvertFrom-Json
    } catch {
        Fail "panel.json di $panelJsonPath rusak (bukan JSON valid): $($_.Exception.Message) - perbaiki atau pindahkan file itu dulu."
    }
} else {
    $cfg = [pscustomobject]@{}
}

$panelPort = 8888
if ($null -ne $cfg.port -and $cfg.port -gt 0) { $panelPort = $cfg.port }
$apacheService = $ApacheServiceName
if (-not [string]::IsNullOrWhiteSpace($cfg.apache_service)) { $apacheService = $cfg.apache_service }
if (-not [string]::IsNullOrWhiteSpace($cfg.data_dir)) {
    $dataDir = ($cfg.data_dir -replace '/', '\')
} elseif ($havePanelJson) {
    $dataDir = Join-Path $PanelDir "data"
} else {
    # panel.json baru nanti memakai data_dir = PanelDir (aturan setup).
    $dataDir = $PanelDir
}
$registryPath = Join-Path $dataDir "registry.json"
$adminPath = Join-Path $dataDir "admin.json"
$haveAdmin = Test-Path $adminPath
$haveRegistry = Test-Path $registryPath
$havePanelSvc = $null -ne (Get-Service -Name "SPanelPlus" -ErrorAction SilentlyContinue)
$haveApacheSvc = $null -ne (Get-Service -Name $apacheService -ErrorAction SilentlyContinue)

# Port Apache: yang sudah ada dipertahankan; yang fresh ditanya (aturan setup).
if (Test-Path $httpdConf) {
    $apachePort = 80
    $listen = [regex]::Match([System.IO.File]::ReadAllText($httpdConf), '(?m)^\s*Listen\s+(?:\S+:)?(\d+)')
    if ($listen.Success) { $apachePort = [int]$listen.Groups[1].Value }
    $freshHttpdConf = $false
} elseif (Test-Path $httpd) {
    Fail "httpd.conf tidak ditemukan di $httpdConf padahal httpd.exe ada - instalasi Apache rusak, jangan ditebak."
} else {
    $portRaw = $null
    if ($env:APACHE_PORT) { $portRaw = $env:APACHE_PORT }
    elseif ($ApachePort -ne 0) { $portRaw = "$ApachePort" }
    else {
        $portInput = Read-Host "Port Apache [80]"
        if ([string]::IsNullOrWhiteSpace($portInput)) { $portRaw = "80" } else { $portRaw = $portInput }
    }
    if ($portRaw -notmatch '^\d+$') { Fail "Port Apache harus angka, dapat: '$portRaw'" }
    $apachePort = [int]$portRaw
    if (($apachePort -lt 1) -or ($apachePort -gt 65535)) { Fail "Port Apache di luar rentang 1-65535: $apachePort" }
    if ($apachePort -eq 8888) { Fail "Port Apache tidak boleh sama dengan port panel (8888)" }
    $freshHttpdConf = $true
}

# URL yang akan dipakai bila harus download (discovery hanya baca halaman,
# bukan download zip, jadi aman di fase deteksi; gagal -> pinned).
$pinnedApacheUrl = "https://www.apachelounge.com/download/VS18/binaries/httpd-2.4.69-261002-Win64-VS18.zip"
$pinnedFcgidUrl = "https://www.apachelounge.com/download/VS18/modules/mod_fcgid-2.3.10-win64-VS18.zip"
$apacheUrlPlan = $pinnedApacheUrl
if (-not $haveHttpd) {
    $found = Discover-ApacheUrl
    if ($null -ne $found) { $apacheUrlPlan = $found }
}
$pinnedPhp = @{
    "8.2" = @("php-8.2.28-nts-Win32-vs16-x64.zip", "php-8.2.15-nts-Win32-vs16-x64.zip")
    "8.3" = @("php-8.3.31-nts-Win32-vs16-x64.zip", "php-8.3.28-nts-Win32-vs16-x64.zip")
    "8.4" = @("php-8.4.27-nts-Win32-vs17-x64.zip", "php-8.4.13-nts-Win32-vs17-x64.zip")
}
$phpPlan = @{}
foreach ($branch in @("8.2", "8.3", "8.4")) {
    $dest = Join-Path $M0Dir "php\$branch"
    $have = Test-Path (Join-Path $dest "php.exe")
    $url = $null
    if (-not $have) { $url = Discover-PhpUrl $branch }
    $phpPlan[$branch] = @{ Have = $have; Url = $url; Dest = $dest }
}
$m0IncludePresent = $false
if (Test-Path $httpdConf) {
    $m0IncludePresent = ([System.IO.File]::ReadAllText($httpdConf) -match 'conf-m0-projects\.conf')
}

# --- 2. Tentukan mode AD -------------------------------------------------------
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

# --- 3. Rencana + konfirmasi ---------------------------------------------------
# Semua baris di bawah ini hasil deteksi; belum ada mutasi apa pun.
$stamp = Get-Date -Format "yyyyMMdd-HHmmss"
$panelBackup = Join-Path $PanelDir "panel.json.bak-deploy-$stamp"
$httpdBackup = Join-Path (Join-Path $M0Dir "Apache24\conf") "httpd.conf.bak-deploy-$stamp"
Write-Host ""
Write-Host "RENCANA DEPLOYMENT" -ForegroundColor Green
Write-Host "  Panel      : $PanelDir (port $panelPort, service SPanelPlus)"
Write-Host "  Apache     : $M0Dir (port $apachePort, service $apacheService)"
Write-Host "  Mode login : $mode"
if ($mode -like "AD*") {
    Write-Host "  AD server  : $AdServer"
    Write-Host "  AD domain  : $AdDomain"
    Write-Host "  AD base DN : $AdBaseDn"
    Write-Host "  AD grup    : $AdGroupDn"
    Write-Host "  AD CA file : $(if ([string]::IsNullOrWhiteSpace($AdCaFile)) { '(system roots)' } else { $AdCaFile })"
}
Write-Host "  PROVISION (yang sudah ada di-skip, yang belum ada dipasang):"
if ($havePanelExe) {
    Write-Host "    Panel exe  : sudah ada, skip"
} else {
    Write-Host "    Panel exe  : SALIN dari $($panelExeSrc.Name) (folder script) -> $panelExe"
}
if ($haveHttpd) {
    Write-Host "    Apache     : sudah ada, skip"
} else {
    Write-Host "    Apache     : DOWNLOAD $apacheUrlPlan"
}
if ($haveFcgid) {
    Write-Host "    mod_fcgid  : sudah ada, skip"
} else {
    Write-Host "    mod_fcgid  : DOWNLOAD $pinnedFcgidUrl"
}
foreach ($branch in @("8.2", "8.3", "8.4")) {
    if ($phpPlan[$branch].Have) {
        Write-Host "    PHP $branch    : sudah ada, skip"
    } elseif ($null -ne $phpPlan[$branch].Url) {
        Write-Host "    PHP $branch    : DOWNLOAD $($phpPlan[$branch].Url)"
    } else {
        Write-Host "    PHP $branch    : DOWNLOAD (fallback arsip pinned, discovery gagal)"
    }
}
if ($havePanelSvc) {
    Write-Host "    Service panel  : sudah ada, skip pasang (di-restart saja)"
} else {
    Write-Host "    Service panel  : PASANG SPanelPlus"
}
if ($haveApacheSvc) {
    Write-Host "    Service Apache : sudah ada, skip pasang (di-restart saja)"
} else {
    Write-Host "    Service Apache : PASANG $apacheService"
}
if ($haveAdmin) {
    Write-Host "    Akun admin     : sudah ada, skip"
} else {
    Write-Host "    Akun admin     : SET-PASSWORD interaktif (break-glass wajib ada)"
}
if ($havePanelJson) {
    Write-Host "  Dummy users dibuang : $dummyCount"
    Write-Host "  Backup panel.json   : $panelBackup"
} else {
    Write-Host "  panel.json          : DIBUAT BARU langsung mode produksi (tidak pernah dummy)"
}
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

if (-not (Test-Path $dlDir)) { New-Item -ItemType Directory -Path $dlDir -Force | Out-Null }

# --- 4. Provision: panel exe -----------------------------------------------------
if (-not $havePanelExe) {
    if (-not (Test-Path $PanelDir)) { New-Item -ItemType Directory -Path $PanelDir -Force | Out-Null }
    $panelSvc = Get-Service -Name "SPanelPlus" -ErrorAction SilentlyContinue
    if ($null -ne $panelSvc -and $panelSvc.Status -eq "Running") {
        Info "Stop service panel lama dulu (file exe terkunci saat service jalan)"
        Stop-Service -Name "SPanelPlus" -Force
    }
    if ($panelExeSrc.FullName -ieq $panelExe) {
        Info "Panel exe sudah di $panelExe, skip salin"
    } else {
        Copy-Item $panelExeSrc.FullName $panelExe -Force
        Info "Panel exe: $($panelExeSrc.Name) -> $panelExe"
    }
}

# --- 5. Provision: Apache + mod_fcgid ----------------------------------------------
if (-not $haveHttpd) {
    $zip = Join-Path $dlDir (Split-Path $apacheUrlPlan -Leaf)
    Download-File $apacheUrlPlan $zip 1000000
    $tmp = Join-Path $M0Dir "_apache_tmp"
    if (Test-Path $tmp) { Remove-Item $tmp -Recurse -Force }
    Expand-Archive -Path $zip -DestinationPath $tmp -Force
    $inner = Find-ChildFile $tmp "httpd.exe"
    if ($null -eq $inner) { Fail "httpd.exe tidak ditemukan di dalam ZIP Apache - struktur ZIP berubah, laporkan ke Muse." }
    $apacheRoot = Split-Path (Split-Path $inner -Parent) -Parent
    $apacheDest = Join-Path $M0Dir "Apache24"
    if (Test-Path $apacheDest) {
        # Sisa run sebelumnya yang rusak (folder ada, httpd.exe hilang):
        # singkirkan dulu supaya Move-Item tidak menumpuk Apache24 di dalamnya.
        Remove-Item $apacheDest -Recurse -Force
    }
    Move-Item $apacheRoot $apacheDest -Force
    Remove-Item $tmp -Recurse -Force
    Info "Apache terpasang dari: $apacheUrlPlan"
} else { Info "Apache sudah ada, skip" }

if (-not $haveFcgid) {
    $zip = Join-Path $dlDir (Split-Path $pinnedFcgidUrl -Leaf)
    Download-File $pinnedFcgidUrl $zip 10000
    $tmp = Join-Path $M0Dir "_fcgid_tmp"
    if (Test-Path $tmp) { Remove-Item $tmp -Recurse -Force }
    Expand-Archive -Path $zip -DestinationPath $tmp -Force
    $so = Find-ChildFile $tmp "mod_fcgid.so"
    if ($null -eq $so) { Fail "mod_fcgid.so tidak ditemukan di dalam ZIP mod_fcgid - laporkan ke Muse." }
    Copy-Item $so $fcgidSo -Force
    Remove-Item $tmp -Recurse -Force
    Info "mod_fcgid terpasang dari: $pinnedFcgidUrl"
} else { Info "mod_fcgid.so sudah ada, skip" }

# --- 6. Provision: PHP 8.2 + 8.3 + 8.4 NTS x64 ---------------------------------------
foreach ($branch in @("8.2", "8.3", "8.4")) {
    $dest = $phpPlan[$branch].Dest
    if ($phpPlan[$branch].Have) { Info "PHP $branch sudah ada, skip"; continue }
    if (-not (Test-Path $dest)) { New-Item -ItemType Directory -Path $dest -Force | Out-Null }
    $zip = $null
    if ($null -ne $phpPlan[$branch].Url) {
        $candidate = Join-Path $dlDir (Split-Path $phpPlan[$branch].Url -Leaf)
        if (Try-Download $phpPlan[$branch].Url $candidate) { $zip = $candidate; Info "PHP $branch dari: $($phpPlan[$branch].Url)" }
    }
    if ($null -eq $zip) {
        foreach ($name in $pinnedPhp[$branch]) {
            foreach ($folder in @("releases", "releases/archives")) {
                $url = "https://windows.php.net/downloads/$folder/$name"
                $candidate = Join-Path $dlDir $name
                if (Try-Download $url $candidate) { $zip = $candidate; Info "PHP $branch dari arsip: $url"; break }
            }
            if ($null -ne $zip) { break }
        }
    }
    if ($null -eq $zip) { Fail "Semua jalur download PHP $branch NTS x64 gagal (discovery + arsip pinned). Laporkan output ini ke Muse." }
    Expand-Archive -Path $zip -DestinationPath $dest -Force
    if (-not (Test-Path (Join-Path $dest "php-cgi.exe"))) { Fail "php-cgi.exe tidak ada setelah ekstrak PHP $branch dari $zip - bukan build NTS yang benar." }
    if (-not (Test-Path (Join-Path $dest "php.ini"))) {
        Copy-Item (Join-Path $dest "php.ini-development") (Join-Path $dest "php.ini")
    }
    Info "PHP $branch siap: $((& (Join-Path $dest "php.exe") -v | Select-Object -First 1))"
}

# --- 7. Patch httpd.conf (backup sekali, idempotent) ----------------------------------
if (-not (Test-Path $httpdConf)) { Fail "httpd.conf tidak ditemukan di $httpdConf" }
if (-not (Test-Path "$httpdConf.orig-m0")) { Copy-Item $httpdConf "$httpdConf.orig-m0" }
$m0Slash = ($M0Dir -replace '\\', '/')
$text = [System.IO.File]::ReadAllText($httpdConf)
$text = $text -replace '(?m)^Define SRVROOT .*', "Define SRVROOT `"$m0Slash/Apache24`""
if ($text -notmatch '(?m)^Define SRVROOT ') { $text += "`r`nDefine SRVROOT `"$m0Slash/Apache24`"`r`n" }
$text = $text -replace '(?m)^ServerRoot .*', "ServerRoot `"$m0Slash/Apache24`""
if ($text -notmatch '(?m)^ServerRoot ') { $text += "`r`nServerRoot `"$m0Slash/Apache24`"`r`n" }
if ($freshHttpdConf) {
    $text = $text -replace '(?m)^#?\s*ServerName .*', "ServerName localhost:$apachePort"
    if ($text -notmatch '(?m)^ServerName ') { $text += "`r`nServerName localhost:$apachePort`r`n" }
    $text = $text -replace '(?m)^Listen .*', "Listen $apachePort"
}
$text = $text -replace '(?m)^(\s*)DirectoryIndex .*', '$1DirectoryIndex index.php index.html'
if ($text -notmatch '(?m)^\s*DirectoryIndex ') { $text += "`r`nDirectoryIndex index.php index.html`r`n" }
$text = $text -replace '(?m)^DocumentRoot .*', "DocumentRoot `"$m0Slash/htdocs`""
if ($text -notmatch '(?m)^DocumentRoot ') { $text += "`r`nDocumentRoot `"$m0Slash/htdocs`"`r`n" }
$text = $text -replace '\$\{SRVROOT\}/htdocs', "$m0Slash/htdocs"
$text = $text -replace "$m0Slash/Apache24/htdocs", "$m0Slash/htdocs"
$text = $text -replace '(?m)^\s*#\s*(LoadModule (rewrite|alias|authz_core|dir|mime)_module .*)$', '$1'
# Kalau baris rewrite tidak ada sama sekali (bukan cuma dikomen),
# uncomment di atas tidak menghasilkan apa-apa - tambahkan eksplisit.
# rewrite_module WAJIB aktif: .htaccess Laravel membungkus semua rule
# dalam <IfModule mod_rewrite.c>, jadi kalau modul ini mati, pretty URL
# (mis. /login) 404 diam-diam sementara / jalan via DirectoryIndex.
# Bug nyata 2026-10-28 di mesin max: httpd -M kosong untuk rewrite.
if ($text -notmatch '(?m)^\s*LoadModule rewrite_module ') {
    $text += "`r`nLoadModule rewrite_module modules/mod_rewrite.so`r`n"
    Info "LoadModule rewrite_module ditambahkan (barisnya tidak ada di httpd.conf)"
}
if ($text -match '(?m)^\s*LoadModule php') {
    Fail "httpd.conf masih memuat mod_php (LoadModule php...) - melanggar desain. Tempel baris itu ke Muse."
}
# fcgid WAJIB tetap ke-load dari httpd.conf sendiri. Dulu modul ini hanya
# hidup di file M0 (conf-m0-projects.conf), sehingga -RemoveDummies yang
# mencabut Include M0 ikut mematikan fcgid untuk semua proyek panel.
# Aturan: bila Include M0 dipertahankan, M0 yang me-load (tanpa duplikat);
# bila Include M0 dicabut atau tidak ada, httpd.conf sendiri yang me-load.
$needFcgidInConf = $RemoveDummies -or (-not $m0IncludePresent)
if ($needFcgidInConf -and ($text -notmatch '(?m)^\s*LoadModule fcgid_module ')) {
    $text += "`r`nLoadModule fcgid_module modules/mod_fcgid.so`r`n"
    $text += "`r`n<IfModule fcgid_module>`r`n"
    $text += "    FcgidMaxProcesses 20`r`n"
    $text += "    FcgidMaxProcessesPerClass 10`r`n"
    $text += "    FcgidMaxRequestsPerProcess 1000`r`n"
    $text += "    FcgidIOTimeout 120`r`n"
    $text += "    FcgidBusyTimeout 300`r`n"
    $text += "    FcgidIdleTimeout 120`r`n"
    $text += "    FcgidInitialEnv PHP_FCGI_MAX_REQUESTS 1000`r`n"
    $text += "    FcgidInitialEnv SystemRoot `"C:/Windows`"`r`n"
    $text += "    FcgidInitialEnv SystemDrive `"C:`"`r`n"
    $text += "    FcgidInitialEnv TEMP `"C:/Windows/Temp`"`r`n"
    $text += "    FcgidInitialEnv TMP `"C:/Windows/Temp`"`r`n"
    $text += "    FcgidInitialEnv windir `"C:/Windows`"`r`n"
    $text += "</IfModule>`r`n"
    Info "LoadModule fcgid_module + setting fcgid ditambahkan ke httpd.conf"
}
$panelDataSlash = ($dataDir -replace '\\', '/')
# Include generated panel HARUS menunjuk <data_dir>/generated/apache.
# Path lama <data_dir>/data/... adalah data_dir DEFAULT (bila panel.json
# tidak ada) - setup lama salah tulis path itu, sehingga config generated
# tidak pernah ter-load oleh Apache (gejala: proyek terdaftar tapi tetap
# listing/source PHP). Migrasi baris lama (plain maupun Optional).
$text = $text -replace '(?m)^Include(Optional)? "[^"]*data/generated/apache/\*\.conf"', "IncludeOptional `"$panelDataSlash/generated/apache/*.conf`""
if ($text -notmatch [regex]::Escape("$panelDataSlash/generated/apache/*.conf")) {
    $text += "`r`nIncludeOptional `"$panelDataSlash/generated/apache/*.conf`"`r`n"
}
[System.IO.File]::WriteAllText($httpdConf, $text, (New-Object System.Text.UTF8Encoding $false))
Info "httpd.conf ter-patch (backup: httpd.conf.orig-m0)"
if (-not (Test-Path (Join-Path $M0Dir "htdocs"))) { New-Item -ItemType Directory -Path (Join-Path $M0Dir "htdocs") -Force | Out-Null }
if (-not (Test-Path "$dataDir\generated\apache")) {
    New-Item -ItemType Directory -Path "$dataDir\generated\apache" -Force | Out-Null
}

# --- 8. Bukti lokal: httpd -t + gate modul (sebelum service dipasang) ------------------
$syntax = (& $httpd -t 2>&1) -join "`n"
Write-Host $syntax
if ($syntax -notmatch 'Syntax OK') {
    Fail "httpd -t TIDAK Syntax OK. Output persisnya di atas - kirim ke Muse, jangan paksa start Apache."
}
$mods = (& $httpd -M 2>&1) -join "`n"
if ($mods -notmatch 'rewrite_module') {
    Fail "rewrite_module TIDAK ke-load (httpd -M). Periksa LoadModule rewrite_module di httpd.conf dan file modules/mod_rewrite.so - jangan lanjutkan deployment."
}
Info "rewrite_module ter-load (httpd -M)"
Info "Apache version: $((& $httpd -v | Select-Object -First 1))"

# --- 9. Service Apache (apache-spanel) ---------------------------------------------------
# Migrasi dari nama lama: service Apache2.4 peninggalan setup lama HANYA
# dihapus bila binary-nya dari $M0Dir. Service Apache2.4 milik aplikasi
# lain TIDAK disentuh - fail keras.
$oldSvc = Get-Service -Name "Apache2.4" -ErrorAction SilentlyContinue
if ($null -ne $oldSvc) {
    $old = Get-CimInstance Win32_Service -Filter "Name='Apache2.4'"
    if ($old.PathName -match [regex]::Escape($M0Dir)) {
        Info "Service lama Apache2.4 (milik setup ini) ditemukan, migrasi ke $apacheService"
        Stop-Service -Name "Apache2.4" -ErrorAction SilentlyContinue
        & $httpd -k uninstall -n "Apache2.4"
    } else {
        Fail "Service Apache2.4 sudah ada tapi bukan dari $M0Dir (milik XAMPP/aplikasi lain?). Stop/uninstall service itu dulu."
    }
}
$existing = Get-Service -Name $apacheService -ErrorAction SilentlyContinue
if ($null -ne $existing) {
    $svc = Get-CimInstance Win32_Service -Filter "Name='$apacheService'"
    if ($svc.PathName -notmatch [regex]::Escape($M0Dir)) {
        Fail "Service $apacheService sudah ada tapi bukan dari $M0Dir (milik XAMPP/aplikasi lain?). Stop/uninstall service itu dulu, atau ganti nama service lewat parameter -ApacheServiceName (hanya berlaku bila panel.json belum ada)."
    }
    Info "Service $apacheService sudah ada (milik setup ini), skip install"
} else {
    Info "Install service Apache $apacheService"
    & $httpd -k install -n $apacheService
}
$svc = Get-Service -Name $apacheService
if ($svc.Status -ne "Running") { Start-Service -Name $apacheService }
Start-Sleep -Seconds 2
$svc = Get-Service -Name $apacheService
if ($svc.Status -ne "Running") {
    Fail "Service $apacheService gagal start. Kemungkinan port $apachePort dipakai aplikasi lain - cek dengan: netstat -ano | findstr :$apachePort"
}
Info "Service $apacheService running"

# --- 10. panel.json produksi (backup + tulis/modifikasi) --------------------------------
if ($havePanelJson) {
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
} else {
    $fresh = [ordered]@{
        port           = 8888
        data_dir       = $dataDir
        apache_service = $apacheService
        apache_home    = (($M0Dir -replace '\\', '/') + "/Apache24")
        ad_enabled     = ($mode -like "AD*")
    }
    if ($mode -like "AD*") {
        $fresh.ad_server = $AdServer
        $fresh.ad_domain = $AdDomain
        $fresh.ad_base_dn = $AdBaseDn
        $fresh.ad_group_dn = $AdGroupDn
        $fresh.ad_ca_file = $AdCaFile
    }
    $fresh.ad_allow_insecure_ldap = $false
    Write-TextFile $panelJsonPath ($fresh | ConvertTo-Json -Depth 4)
    Info "panel.json DIBUAT BARU langsung mode produksi ($mode)."
}

# --- 11. Akun admin break-glass (bila belum ada, set interaktif) -------------------------
# Membuang dummy tanpa admin berarti tidak ada jalan login sama sekali
# saat AD bermasalah - jadi di sini dibuat, bukan digagalkan.
if (-not (Test-Path $adminPath)) {
    Write-Host ""
    Write-Host "Akun admin lokal belum ada. Sekarang set password admin panel (ketik 2x saat diminta):"
    & $panelExe admin set-password
    if (-not (Test-Path $adminPath)) {
        Fail "admin.json tetap tidak ada sesudah set-password ($adminPath). Jalankan manual: $panelExe admin set-password"
    }
    Info "Akun admin dibuat."
} else {
    Info "Akun admin sudah ada, skip set-password."
}

# --- 12. Service panel ----------------------------------------------------------------------
# panel.json baru ditulis di langkah 10, jadi service panel yang sudah jalan
# HARUS di-restart supaya config baru (termasuk apache_service) ter-load.
$panelSvc = Get-Service -Name "SPanelPlus" -ErrorAction SilentlyContinue
if ($null -eq $panelSvc) {
    Info "Install service panel SPanelPlus"
    & $panelExe service install
} else {
    Info "Service SPanelPlus sudah ada, skip install"
    if ($panelSvc.Status -eq "Running") {
        Info "Restart service SPanelPlus supaya panel.json baru ter-load"
        Restart-Service -Name "SPanelPlus" -ErrorAction SilentlyContinue
        Start-Sleep -Seconds 1
    }
}
$panelSvc = Get-Service -Name "SPanelPlus"
if ($panelSvc.Status -ne "Running") {
    & $panelExe service start
    Start-Sleep -Seconds 2
}
$panelSvc = Get-Service -Name "SPanelPlus"
if ($panelSvc.Status -ne "Running") { Fail "Service SPanelPlus gagal start. Jalankan manual: $panelExe service start lalu kirim outputnya ke Muse." }
Info "Service SPanelPlus running"

# --- 13. Dummy M0 (opsional) ------------------------------------------------------------------
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

# --- 14. Gate final + restart Apache -------------------------------------------------------------
# Langkah 13 mengubah httpd.conf (bila -RemoveDummies), jadi gate diulang
# di sini untuk config final, bukan hanya config hasil patch langkah 7.
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

# --- 15. Verifikasi: doctor -----------------------------------------------------------------------
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
    Info "Doctor: panel-port FAIL ditoleransi (artefak CLI saat service jalan - port dipegang SPanelPlus sendiri)."
} else {
    Info "Doctor lulus."
}

# --- 16. Verifikasi: API panel ----------------------------------------------------------------------
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

# --- 17. Verifikasi: URL proyek aktif ------------------------------------------------------------------
if (-not $SkipProjectChecks) {
    if (-not (Test-Path $registryPath)) {
        Warn "Registry tidak ditemukan di $registryPath - mesin fresh, proyek menyusul. Pengecekan URL dilewati."
    } else {
        $reg = [System.IO.File]::ReadAllText($registryPath) | ConvertFrom-Json
        $active = @($reg.projects | Where-Object { $_.enabled -eq $true })
        if ($active.Count -eq 0) { Warn "Tidak ada proyek aktif di registry - pengecekan URL dilewati." }
        $curl = Get-Command curl.exe -ErrorAction SilentlyContinue
        if ($null -eq $curl -and $active.Count -gt 0) {
            Fail "curl.exe tidak ditemukan, padahal diperlukan untuk cek URL proyek. Pakai -SkipProjectChecks bila sengaja dilewati."
        }
        # Port Apache dari Listen pertama di httpd.conf (default 80).
        $apachePortCheck = 80
        $listen = [regex]::Match([System.IO.File]::ReadAllText($httpdConf), '(?m)^\s*Listen\s+(?:\S+:)?(\d+)')
        if ($listen.Success) { $apachePortCheck = [int]$listen.Groups[1].Value }
        $bad = @()
        foreach ($p in $active) {
            $url = "http://127.0.0.1$($p.url_path)"
            if ($apachePortCheck -ne 80) { $url = "http://127.0.0.1:$apachePortCheck$($p.url_path)" }
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
}

# --- 18. Firewall port panel (opsional) ------------------------------------------------------------------
if (-not [string]::IsNullOrWhiteSpace($PanelAllowSubnet)) {
    if ($PanelAllowSubnet -notmatch '^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(/\d{1,2})?$') {
        Fail "-PanelAllowSubnet harus IP/subnet, mis. 192.168.89.0/24 (dapat: $PanelAllowSubnet)."
    }
    Get-NetFirewallRule -DisplayName "SPanelPlus-Admin" -ErrorAction SilentlyContinue | Remove-NetFirewallRule
    New-NetFirewallRule -DisplayName "SPanelPlus-Admin" -Direction Inbound -Protocol TCP `
        -LocalPort $panelPort -RemoteAddress $PanelAllowSubnet -Action Allow -Profile Any | Out-Null
    Info "Firewall: port $panelPort hanya diizinkan dari $PanelAllowSubnet (rule SPanelPlus-Admin)."
}

# --- 19. Ringkasan + sisa manual ----------------------------------------------------------------------------
Write-Host ""
Write-Host "DEPLOYMENT SELESAI - panel sudah produksi." -ForegroundColor Green
Write-Host "  Mode login : $mode"
if ($havePanelJson) { Write-Host "  Backup     : $panelBackup" }
if ($RemoveDummies) { Write-Host "  Backup     : $httpdBackup" }
Write-Host "  PHP terpasang di $M0Dir\php (8.2, 8.3, 8.4 - yang belum ada di-download, yang sudah ada di-skip)"
Write-Host ""
Write-Host "SISA MANUAL (tidak bisa diotomasi dari script ini):"
if ($mode -like "AD*") {
    Write-Host "  [ ] Login AD user grup IT -> harus sukses; user non-IT -> harus ditolak; akun panel break-glass -> harus sukses."
} else {
    Write-Host "  [ ] Login akun panel break-glass dari browser admin."
}
Write-Host "  [ ] Buka URL proyek pilot dari workstation user (bukan hanya localhost)."
Write-Host "  [ ] Daftarkan PHP dari dashboard Versi PHP bila menambah proyek baru (folder PHP sudah siap di $M0Dir\php)."
Write-Host "  [ ] Mesin fresh TIDAK dibuatkan dummy M0 (app-php82/app-php83/app-laravel) - produksi tidak butuh dummy."
if ([string]::IsNullOrWhiteSpace($PanelAllowSubnet)) {
    Write-Host "  [ ] KUNCI PORT PANEL: panel di http://<server>:$panelPort masih plain HTTP tanpa TLS. Batasi via firewall hanya dari workstation admin/IT, mis:"
    Write-Host "        New-NetFirewallRule -DisplayName SPanelPlus-Admin -Direction Inbound -Protocol TCP -LocalPort $panelPort -RemoteAddress <subnet-admin> -Action Allow"
    Write-Host "      atau pasang TLS reverse proxy di depan panel. JANGAN biarkan port ini terbuka ke jaringan tak terpercaya."
}
Write-Host "  [ ] JANGAN re-run script setup smoke sesudah ini: langkah 9 setup menulis ulang panel.json ke default dummy."
