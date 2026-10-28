#Requires -Version 5.1
<#
.SYNOPSIS
  Setup lingkungan smoke test SPanel+ dari NOL di mesin Windows.

.DESCRIPTION
  Satu script untuk menggantikan seluruh setup manual M0 + M1-M5-AD:
  download Apache + mod_fcgid + PHP, buat proyek dummy M0, patch httpd.conf,
  tulis panel.json (termasuk AD dummy), set password admin, dan install +
  start service Apache & panel. Idempotent: komponen yang sudah ada di-skip,
  baris Include tidak diduplikasi, backup config dibuat sekali.

  Yang script ini TIDAK bisa hilangkan (realita Windows):
  - Harus jalan dari PowerShell sebagai Administrator.
  - Butuh Visual C++ Redistributable 2015-2022 x64 (script berhenti keras
    kalau vcruntime140.dll belum ada).
  - Uji boot M1 tetap butuh restart mesin (manual, sesudah script selesai).
  - Script ini interaktif: dua prompt hash dummy + set-password admin
    diketik langsung di console saat script memintanya.

  Cara jalanin (PowerShell sebagai Administrator, dari folder yang sama
  dengan file panel-*.exe yang sudah di-download):
    powershell -ExecutionPolicy Bypass -File .\setup-panel-smoke.ps1

  Yang perlu ada di folder yang sama dengan script: SATU file panel exe
  (mis. panel-v0.6.2-m5.exe atau panel.exe). Script menyalinnya ke
  C:\Panel\panel.exe.
#>
param(
    [string]$PanelDir = "C:\Panel",
    [string]$M0Dir = "C:\m0",
    [string]$ApacheServiceName = "apache-spanel",
    [int]$ApachePort = 0
)

$ErrorActionPreference = "Stop"

function Fail([string]$Message) {
    Write-Host ""
    Write-Host "SETUP FAIL: $Message" -ForegroundColor Red
    Write-Host "Berhenti di sini - jangan lanjut ke smoke test. Kirim pesan fail ini ke Muse."
    exit 1
}

function Info([string]$Message) {
    Write-Host "==> $Message" -ForegroundColor Cyan
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

# --- 0. Harus Administrator ---------------------------------------------------
$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Fail "Script ini harus jalan dari PowerShell sebagai Administrator (klik kanan PowerShell -> Run as administrator)."
}

# --- 1. Panel exe: cari di folder script, salin ke C:\Panel -------------------
# Preferensi: file berversi (panel-v*.exe, yang terbaru) di atas panel.exe
# polos, supaya folder yang berisi keduanya tidak salah pilih exe lama.
$candidates = Get-ChildItem -Path $PSScriptRoot -Filter "panel*.exe" -File
$panelExeSrc = ($candidates | Where-Object { $_.Name -match '^panel-v.*\.exe$' } | Sort-Object LastWriteTime -Descending | Select-Object -First 1)
if ($null -eq $panelExeSrc) { $panelExeSrc = $candidates | Select-Object -First 1 }
if ($null -eq $panelExeSrc) {
    Fail "Tidak ada file panel*.exe di folder script ($PSScriptRoot). Download dulu panel-v0.6.2-m5.exe dan taruh sebelahan dengan script ini."
}
if (-not (Test-Path $PanelDir)) { New-Item -ItemType Directory -Path $PanelDir -Force | Out-Null }
$panelSvc = Get-Service -Name "SPanelPlus" -ErrorAction SilentlyContinue
if ($null -ne $panelSvc -and $panelSvc.Status -eq "Running") {
    Info "Stop service panel lama dulu (file exe terkunci saat service jalan)"
    Stop-Service -Name "SPanelPlus" -Force
}
$panelExe = Join-Path $PanelDir "panel.exe"
if ($panelExeSrc.FullName -ieq $panelExe) {
    # Script + exe sudah di C:\Panel dan exe sudah bernama panel.exe:
    # menyalin file ke dirinya sendiri ditolak PowerShell, jadi skip saja.
    Info "Panel exe sudah di $panelExe, skip salin"
} else {
    Copy-Item $panelExeSrc.FullName $panelExe -Force
    Info "Panel exe: $($panelExeSrc.Name) -> $panelExe"
}

# --- 2. VC Redistributable (loud, sama seperti setup M0) -----------------------
if (-not (Test-Path "$env:SystemRoot\System32\vcruntime140.dll")) {
    Fail "vcruntime140.dll tidak ada - install dulu VC++ Redistributable 2015-2022 x64: https://aka.ms/vs/17/release/vc_redist.x64.exe lalu jalankan ulang script ini."
}

# --- Port Apache: dipilih di awal setting Apache ---------------------------
# Env APACHE_PORT menang (untuk jalan non-interaktif); kalau tidak di-set
# dan parameter -ApachePort tidak diisi, script bertanya (default 80).
# Port ini dipakai patch httpd.conf (Listen + ServerName), pesan fail
# service, dan URL verifikasi akhir. Port panel (8888) tidak boleh dipakai.
$portRaw = $null
if ($env:APACHE_PORT) { $portRaw = $env:APACHE_PORT }
elseif ($ApachePort -ne 0) { $portRaw = "$ApachePort" }
else {
    $portInput = Read-Host "Port Apache [80]"
    if ([string]::IsNullOrWhiteSpace($portInput)) { $portRaw = "80" } else { $portRaw = $portInput }
}
if ($portRaw -notmatch '^\d+$') { Fail "Port Apache harus angka, dapat: '$portRaw'" }
$ApachePort = [int]$portRaw
if (($ApachePort -lt 1) -or ($ApachePort -gt 65535)) { Fail "Port Apache di luar rentang 1-65535: $ApachePort" }
if ($ApachePort -eq 8888) { Fail "Port Apache tidak boleh sama dengan port panel (8888)" }
if ($ApachePort -eq 80) { $apacheBase = "http://127.0.0.1" } else { $apacheBase = "http://127.0.0.1:$ApachePort" }
Info "Port Apache: $ApachePort"

# --- 3. Apache + mod_fcgid (URL VS18 terverifikasi 2026-10-23 & 2026-10-25) ----
# Apache Lounge sudah pindah dari VS17 ke VS18; keduanya harus sama-sama VS18
# (mod_fcgid yang VS-nya beda tidak mau di-load Apache).
$ApacheUrl = "https://www.apachelounge.com/download/VS18/binaries/httpd-2.4.69-261002-Win64-VS18.zip"
$FcgidUrl  = "https://www.apachelounge.com/download/VS18/modules/mod_fcgid-2.3.10-win64-VS18.zip"
$dlDir = Join-Path $M0Dir "downloads"
if (-not (Test-Path $dlDir)) { New-Item -ItemType Directory -Path $dlDir -Force | Out-Null }
$httpd = Join-Path $M0Dir "Apache24\bin\httpd.exe"

if (-not (Test-Path $httpd)) {
    $zip = Join-Path $dlDir "httpd-2.4.69-261002-Win64-VS18.zip"
    Download-File $ApacheUrl $zip 1000000
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
    Info "Apache terpasang dari: $ApacheUrl"
} else { Info "Apache sudah ada, skip" }

$fcgidSo = Join-Path $M0Dir "Apache24\modules\mod_fcgid.so"
if (-not (Test-Path $fcgidSo)) {
    $zip = Join-Path $dlDir "mod_fcgid-2.3.10-win64-VS18.zip"
    Download-File $FcgidUrl $zip 10000
    $tmp = Join-Path $M0Dir "_fcgid_tmp"
    if (Test-Path $tmp) { Remove-Item $tmp -Recurse -Force }
    Expand-Archive -Path $zip -DestinationPath $tmp -Force
    $so = Find-ChildFile $tmp "mod_fcgid.so"
    if ($null -eq $so) { Fail "mod_fcgid.so tidak ditemukan di dalam ZIP mod_fcgid - laporkan ke Muse." }
    Copy-Item $so $fcgidSo -Force
    Remove-Item $tmp -Recurse -Force
    Info "mod_fcgid terpasang dari: $FcgidUrl"
} else { Info "mod_fcgid.so sudah ada, skip" }

# --- 4. PHP 8.2 + 8.3 NTS x64 (discovery php.net, fallback arsip pinned) -------
$pinned = @{
    "8.2" = @("php-8.2.28-nts-Win32-vs16-x64.zip", "php-8.2.15-nts-Win32-vs16-x64.zip")
    "8.3" = @("php-8.3.31-nts-Win32-vs16-x64.zip", "php-8.3.28-nts-Win32-vs16-x64.zip")
}
foreach ($branch in @("8.2", "8.3")) {
    $dest = Join-Path $M0Dir "php\$branch"
    if (Test-Path (Join-Path $dest "php.exe")) { Info "PHP $branch sudah ada, skip"; continue }
    if (-not (Test-Path $dest)) { New-Item -ItemType Directory -Path $dest -Force | Out-Null }
    $zip = $null
    Info "Cari link PHP $branch NTS x64 di php.net"
    try {
        $html = (Invoke-WebRequest -Uri "https://www.php.net/downloads.php?os=windows&version=$branch" -UseBasicParsing -ErrorAction Stop).Content
        $m = [regex]::Match($html, "php-$branch\.[0-9]+-nts-Win32-vs[0-9]+-x64\.zip")
        if ($m.Success) {
            $url = "https://windows.php.net/downloads/releases/$($m.Value)"
            $candidate = Join-Path $dlDir $m.Value
            if (Try-Download $url $candidate) { $zip = $candidate; Info "PHP $branch dari: $url" }
        }
    } catch {
        Info "Discovery PHP $branch gagal ($($_.Exception.Message)) - coba fallback arsip"
    }
    if ($null -eq $zip) {
        foreach ($name in $pinned[$branch]) {
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

# --- 5. Proyek dummy M0 (isi persis dari kit m0/, ditulis ulang tiap jalan) ---
Info "Tulis proyek dummy M0 ke $M0Dir\htdocs"
$htdocs = Join-Path $M0Dir "htdocs"
Write-TextFile (Join-Path $htdocs "app-php82\index.php") @'
<?php
// M0 dummy - plain project pinned to PHP 8.2
if (isset($_GET['check'])) {
    header('Content-Type: text/plain');
    echo 'project=app-php82 php=' . PHP_VERSION . ' sapi=' . php_sapi_name();
    exit;
}
phpinfo();
'@
Write-TextFile (Join-Path $htdocs "app-php83\index.php") @'
<?php
// M0 dummy - plain project pinned to PHP 8.3
if (isset($_GET['check'])) {
    header('Content-Type: text/plain');
    echo 'project=app-php83 php=' . PHP_VERSION . ' sapi=' . php_sapi_name();
    exit;
}
phpinfo();
'@
Write-TextFile (Join-Path $htdocs "app-laravel\public\index.php") @'
<?php
// M0 dummy Laravel front controller - lives in public/, like real Laravel.
if (isset($_GET['check'])) {
    header('Content-Type: text/plain');
    echo 'project=app-laravel php=' . PHP_VERSION . ' sapi=' . php_sapi_name() . ' front=public/index.php';
    exit;
}
header('Content-Type: text/html; charset=utf-8');
echo '<h1>M0 Laravel dummy</h1>';
echo '<p>Reached via <code>/app-laravel/</code> without <code>/public</code>.</p>';
echo '<p>PHP ' . htmlspecialchars(PHP_VERSION) . ' &middot; SAPI ' . htmlspecialchars(php_sapi_name()) . '</p>';
'@
Write-TextFile (Join-Path $htdocs "app-laravel\public\.htaccess") @'
<IfModule mod_rewrite.c>
    <IfModule mod_negotiation.c>
        Options -MultiViews -Indexes
    </IfModule>
    RewriteEngine On
    # Serve existing files/dirs directly, everything else to index.php
    RewriteCond %{REQUEST_FILENAME} !-d
    RewriteCond %{REQUEST_FILENAME} !-f
    RewriteRule ^ index.php [L]
</IfModule>
'@
Write-TextFile (Join-Path $htdocs "app-laravel\.env") @'
APP_NAME=M0LaravelDummy
APP_ENV=local
APP_URL=http://127.0.0.1/app-laravel
M0_SECRET_SHOULD_NOT_BE_PUBLIC=do-not-expose-this-value
'@
Write-TextFile (Join-Path $htdocs "app-laravel\artisan") @'
<?php
// M0 dummy artisan - NOT real Laravel. Proves K4: a CLI process runs with
// whichever php.exe invokes it (the panel supervisor will always invoke
// the php.exe of the project's registered PHP version).
if (php_sapi_name() !== 'cli') {
    http_response_code(403);
    exit('artisan is CLI only');
}
$cmd = $argv[1] ?? '';
if ($cmd === 'queue:work') {
    $line = sprintf(
        "[%s] queue dummy processed php=%s project=app-laravel\n",
        date('Y-m-d H:i:s'),
        PHP_VERSION
    );
    echo $line;
    $logDir = __DIR__ . '/storage/logs';
    if (!is_dir($logDir)) {
        mkdir($logDir, 0777, true);
    }
    file_put_contents($logDir . '/queue-dummy.log', $line, FILE_APPEND);
    exit(0);
}
echo "M0 dummy artisan. Usage: php artisan queue:work --once\n";
echo 'php=' . PHP_VERSION . "\n";
'@
Write-TextFile (Join-Path $htdocs "app-laravel\storage\logs\.gitkeep") ""

Write-TextFile (Join-Path $M0Dir "conf-m0-projects.conf") @'
# M0 - per-project mod_fcgid config (ditulis setup-panel-smoke.ps1).
# Principle (Plan D4/D5): NO mod_php anywhere. Each project directory pins
# its own php-cgi.exe via FcgidWrapper -> different wrapper path = different
# mod_fcgid process class = that folder's php.ini is used.

LoadModule fcgid_module modules/mod_fcgid.so

# Without this, a request to /app-php82/ serves a directory listing
# ("Index of /app-php82") instead of executing index.php - stock
# httpd.conf only has "DirectoryIndex index.html". (Bug found 2026-10-24.)
<IfModule dir_module>
    DirectoryIndex index.php index.html
</IfModule>

<IfModule fcgid_module>
    FcgidMaxProcesses 20
    FcgidMaxProcessesPerClass 10
    FcgidMaxRequestsPerProcess 1000
    FcgidIOTimeout 120
    FcgidBusyTimeout 300
    FcgidIdleTimeout 120
    FcgidInitialEnv PHP_FCGI_MAX_REQUESTS 1000
    FcgidInitialEnv SystemRoot "C:/Windows"
    FcgidInitialEnv SystemDrive "C:"
    FcgidInitialEnv TEMP "C:/Windows/Temp"
    FcgidInitialEnv TMP "C:/Windows/Temp"
    FcgidInitialEnv windir "C:/Windows"
</IfModule>

# --- app-php82 : plain project on PHP 8.2 -----------------------------------
<Directory "C:/m0/htdocs/app-php82">
    Options +ExecCGI
    AllowOverride All
    Require all granted
    <FilesMatch "\.php$">
        SetHandler fcgid-script
    </FilesMatch>
    FcgidWrapper "C:/m0/php/8.2/php-cgi.exe" .php
</Directory>

# --- app-php83 : plain project on PHP 8.3 -----------------------------------
<Directory "C:/m0/htdocs/app-php83">
    Options +ExecCGI
    AllowOverride All
    Require all granted
    <FilesMatch "\.php$">
        SetHandler fcgid-script
    </FilesMatch>
    FcgidWrapper "C:/m0/php/8.3/php-cgi.exe" .php
</Directory>

# --- app-laravel : Alias straight to public/ (Plan D5, /public disappears) --
Alias "/app-laravel" "C:/m0/htdocs/app-laravel/public"
<Directory "C:/m0/htdocs/app-laravel/public">
    Options +ExecCGI
    AllowOverride All
    Require all granted
    <FilesMatch "\.php$">
        SetHandler fcgid-script
    </FilesMatch>
    FcgidWrapper "C:/m0/php/8.2/php-cgi.exe" .php
</Directory>
# NOTE: the project root C:/m0/htdocs/app-laravel (with .env) is NOT aliased
# and NOT under DocumentRoot exposure for this path - a request to
# /app-laravel/.env resolves inside public/, where no .env exists -> 404.
'@

# --- 6. Patch httpd.conf (backup sekali, idempotent) ---------------------------
$conf = Join-Path $M0Dir "Apache24\conf\httpd.conf"
if (-not (Test-Path $conf)) { Fail "httpd.conf tidak ditemukan di $conf" }
if (-not (Test-Path "$conf.orig-m0")) { Copy-Item $conf "$conf.orig-m0" }
$text = [System.IO.File]::ReadAllText($conf)
$text = $text -replace '(?m)^Define SRVROOT .*', 'Define SRVROOT "C:/m0/Apache24"'
$text = $text -replace '(?m)^ServerRoot .*', 'ServerRoot "C:/m0/Apache24"'
$text = $text -replace '(?m)^#?\s*ServerName .*', "ServerName localhost:$ApachePort"
if ($text -notmatch '(?m)^ServerName ') { $text += "`r`nServerName localhost:$ApachePort`r`n" }
$text = $text -replace '(?m)^Listen .*', "Listen $ApachePort"
$text = $text -replace '(?m)^(\s*)DirectoryIndex .*', '$1DirectoryIndex index.php index.html'
$text = $text -replace '(?m)^DocumentRoot .*', 'DocumentRoot "C:/m0/htdocs"'
$text = $text -replace '\$\{SRVROOT\}/htdocs', 'C:/m0/htdocs'
$text = $text -replace 'C:/m0/Apache24/htdocs', 'C:/m0/htdocs'
$text = $text -replace '(?m)^\s*#\s*(LoadModule (rewrite|alias|authz_core|dir|mime)_module .*)$', '$1'
# Kalau baris rewrite tidak ada sama sekali (bukan cuma dikomen),
# uncomment di atas tidak menghasilkan apa-apa — tambahkan eksplisit.
# rewrite_module WAJIB aktif: .htaccess Laravel membungkus semua rule
# dalam <IfModule mod_rewrite.c>, jadi kalau modul ini mati, pretty URL
# (mis. /login) 404 diam-diam sementara / jalan via DirectoryIndex.
# Bug nyata 2026-10-28 di mesin max: httpd -M kosong untuk rewrite.
if ($text -notmatch '(?m)^\s*LoadModule rewrite_module ') {
    $text += "`r`nLoadModule rewrite_module modules/mod_rewrite.so`r`n"
    Info "LoadModule rewrite_module ditambahkan (barisnya tidak ada di httpd.conf)"
}
if ($text -match '(?m)^\s*LoadModule php') {
    Fail "httpd.conf masih memuat mod_php (LoadModule php...) - melanggar desain M0. Tempel baris itu ke Muse."
}
if ($text -notmatch 'conf-m0-projects\.conf') {
    $text += "`r`nInclude `"C:/m0/conf-m0-projects.conf`"`r`n"
}
$panelDataDir = ($PanelDir -replace '\\', '/')
# Include generated panel HARUS menunjuk <data_dir>/generated/apache.
# panel.json setup memakai data_dir = $PanelDir, jadi path-nya
# $panelDataDir/generated/apache. Path lama $panelDataDir/data/... adalah
# data_dir DEFAULT (bila panel.json tidak ada) — setup lama salah tulis
# path itu, sehingga config generated tidak pernah ter-load oleh Apache
# (gejala: proyek terdaftar tapi tetap listing/source PHP).
# Migrasi baris lama (plain maupun Optional) ke path yang benar.
$text = $text -replace '(?m)^Include(Optional)? "[^"]*data/generated/apache/\*\.conf"', "IncludeOptional `"$panelDataDir/generated/apache/*.conf`""
$panelInclude = "IncludeOptional `"$panelDataDir/generated/apache/*.conf`""
if ($text -notmatch [regex]::Escape("$panelDataDir/generated/apache/*.conf")) {
    $text += "`r`n$panelInclude`r`n"
}
[System.IO.File]::WriteAllText($conf, $text, (New-Object System.Text.UTF8Encoding $false))
Info "httpd.conf ter-patch (backup: httpd.conf.orig-m0)"

# --- 7. Bukti lokal: httpd -t harus Syntax OK ----------------------------------
# Direktori generated tetap dibuat dulu (panel membutuhkannya saat apply).
if (-not (Test-Path "$PanelDir\generated\apache")) {
    New-Item -ItemType Directory -Path "$PanelDir\generated\apache" -Force | Out-Null
}
$syntax = (& $httpd -t 2>&1) -join "`n"
Write-Host $syntax
if ($syntax -notmatch 'Syntax OK') {
    Fail "httpd -t TIDAK Syntax OK. Output persisnya di atas - kirim ke Muse, jangan paksa start Apache."
}
# Gate modul rewrite: httpd -t hanya cek sintaks, jadi modul yang mati
# tetap lolos setup dan baru ketahuan saat pretty URL 404 (bug 2026-10-28).
$mods = (& $httpd -M 2>&1) -join "`n"
if ($mods -notmatch 'rewrite_module') {
    Fail "rewrite_module TIDAK ke-load (httpd -M). Periksa LoadModule rewrite_module di httpd.conf dan file modules/mod_rewrite.so - jangan lanjutkan setup."
}
Info "rewrite_module ter-load (httpd -M)"
Info "Apache version: $((& $httpd -v | Select-Object -First 1))"

# --- 8. Service Apache (apache-spanel) ----------------------------------------
# Migrasi dari nama lama: service Apache2.4 peninggalan setup lama HANYA
# dihapus bila binary-nya dari $M0Dir. Service Apache2.4 milik aplikasi
# lain TIDAK disentuh - fail keras.
$oldSvc = Get-Service -Name "Apache2.4" -ErrorAction SilentlyContinue
if ($null -ne $oldSvc) {
    $old = Get-CimInstance Win32_Service -Filter "Name='Apache2.4'"
    if ($old.PathName -match [regex]::Escape($M0Dir)) {
        Info "Service lama Apache2.4 (milik setup ini) ditemukan, migrasi ke $ApacheServiceName"
        Stop-Service -Name "Apache2.4" -ErrorAction SilentlyContinue
        & $httpd -k uninstall -n "Apache2.4"
    } else {
        Fail "Service Apache2.4 sudah ada tapi bukan dari $M0Dir (milik XAMPP/aplikasi lain?). Stop/uninstall service itu dulu."
    }
}
$existing = Get-Service -Name $ApacheServiceName -ErrorAction SilentlyContinue
if ($null -ne $existing) {
    $svc = Get-CimInstance Win32_Service -Filter "Name='$ApacheServiceName'"
    if ($svc.PathName -notmatch [regex]::Escape($M0Dir)) {
        Fail "Service $ApacheServiceName sudah ada tapi bukan dari $M0Dir (milik XAMPP/aplikasi lain?). Stop/uninstall service itu dulu, atau ganti nama service lewat parameter -ApacheServiceName."
    }
    Info "Service $ApacheServiceName sudah ada (milik setup ini), skip install"
} else {
    Info "Install service Apache $ApacheServiceName"
    & $httpd -k install -n $ApacheServiceName
}
$svc = Get-Service -Name $ApacheServiceName
if ($svc.Status -ne "Running") { Start-Service -Name $ApacheServiceName }
Start-Sleep -Seconds 2
$svc = Get-Service -Name $ApacheServiceName
if ($svc.Status -ne "Running") {
    Fail "Service $ApacheServiceName gagal start. Kemungkinan port $ApachePort dipakai aplikasi lain - cek dengan: netstat -ano | findstr :$ApachePort"
}
Info "Service $ApacheServiceName running"

# --- 9. panel.json (backup sekali, hash dummy interaktif) ----------------------
$panelJson = Join-Path $PanelDir "panel.json"
if ((Test-Path $panelJson) -and -not (Test-Path "$panelJson.bak-smoke")) {
    Copy-Item $panelJson "$panelJson.bak-smoke"
    Info "panel.json lama dibackup ke panel.json.bak-smoke"
}
Write-Host ""
Write-Host "Sekarang buat hash AD dummy. Saat panel meminta 'Password untuk di-hash':"
Write-Host "  - untuk budi, ketik: budi-kuat-1"
Write-Host "  - untuk andi, ketik: andi-kuat-1"
Write-Host "(password dummy smoke test saja, bukan password asli siapa pun)"
$budiHash = (& $panelExe admin hash-password) -join ""
$andiHash = (& $panelExe admin hash-password) -join ""
if ([string]::IsNullOrWhiteSpace($budiHash) -or [string]::IsNullOrWhiteSpace($andiHash)) {
    Fail "Hash dummy kosong - perintah 'panel admin hash-password' tidak mengembalikan hash. Jalankan manual: $panelExe admin hash-password"
}
$config = [ordered]@{
    port           = 8888
    data_dir       = $PanelDir
    apache_service = $ApacheServiceName
    apache_home    = (($M0Dir -replace '\\', '/') + "/Apache24")
    ad_enabled     = $true
    ad_server      = "dummy"
    ad_domain      = "siws.local"
    ad_base_dn     = "DC=siws,DC=local"
    ad_group_dn    = "CN=IS,DC=siws,DC=local"
    ad_dummy_users = @(
        [ordered]@{ username = "budi"; password_hash = $budiHash; groups = @("IS") },
        [ordered]@{ username = "andi"; password_hash = $andiHash; groups = @("HRD") }
    )
}
$json = $config | ConvertTo-Json -Depth 4
Write-TextFile $panelJson $json
Info "panel.json ditulis (port 8888, data_dir $PanelDir, AD dummy siws.local/IS)"

# --- 10. Password admin (interaktif, pilihan lu sendiri) -----------------------
Write-Host ""
Write-Host "Sekarang set password admin panel (ketik 2x saat diminta):"
& $panelExe admin set-password

# --- 11. Service panel ---------------------------------------------------------
# panel.json baru ditulis di langkah 9, jadi service panel yang sudah jalan
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

# --- 12. Verifikasi akhir ------------------------------------------------------
Write-Host ""
& $panelExe version
& $panelExe doctor
Write-Host ""
Write-Host "SETUP SELESAI." -ForegroundColor Green
Write-Host "  Panel UI : http://127.0.0.1:8888 (login pakai password admin yang baru lu set)"
Write-Host "  Dummy web: $apacheBase/app-php82/?check  (harus 8.2)"
Write-Host "             $apacheBase/app-php83/?check  (harus 8.3)"
Write-Host "             $apacheBase/app-laravel/      (tanpa /public)"
Write-Host "  AD dummy : login Akun AD -> budi / budi-kuat-1 (lolos), andi / andi-kuat-1 (ditolak)"
Write-Host "  Lanjut smoke test urut: M1 (service/boot) -> M2 (CLI) -> M3 (UI) -> M4 (supervisor) -> M5 (auth) -> M5-AD."
Write-Host "  Catatan: uji boot M1 tetap manual - restart mesin, lalu buka UI tanpa start manual apa pun."
