#!/usr/bin/env bash
# setup-panel-smoke-v2.sh - Setup lingkungan smoke test SPanel+ dari NOL,
# versi Git Bash (pasangan dari setup-panel-smoke.ps1 v2).
#
# Jalankan di Git Bash sebagai Administrator, dari folder yang sama dengan
# file panel exe yang sudah di-download:
#   cd /c/Panel            # atau folder mana pun tempat script + exe berada
#   bash setup-panel-smoke-v2.sh
#
# Alternatif tanpa exe lokal: set PANEL_EXE_URL ke URL asset release GitHub
# (mis. https://github.com/gojalifs/spanel-plus/releases/download/v0.6.2-m5/
# panel-v0.6.2-m5.exe) - script mengunduhnya sendiri. Tanpa keduanya, script
# berhenti keras di langkah 1 (sengaja, bukan jalan setengah-setengah).
#
# Yang script kerjakan (urut, fail-loud di tiap langkah):
#   1. Cari panel exe di folder script (diutamakan panel-v*.exe terbaru),
#      salin ke C:\Panel\panel.exe (di-skip kalau sumbernya sudah file itu).
#   2. Cek VC++ Redistributable (vcruntime140.dll) - berhenti keras kalau
#      belum ada, seperti setup M0.
#   3. Pilih port Apache (default 80) lalu download Apache 2.4.69 VS18 +
#      mod_fcgid VS18 yang cocok (URL terverifikasi 2026-10-23 & 2026-10-25),
#      ekstrak ke C:\m0\Apache24.
#   4. Download PHP 8.2 & 8.3 NTS x64 (discovery php.net + fallback arsip
#      pinned), ekstrak ke C:\m0\php\8.2 dan 8.3, buat php.ini.
#   5. Tulis proyek dummy M0 persis dari kit (app-php82/83, app-laravel,
#      conf-m0-projects.conf) ke C:\m0\htdocs.
#   6. Patch httpd.conf (backup sekali, idempotent) + baris Include M0 dan
#      baris Include generated panel; httpd -t harus Syntax OK.
#   7. Install + start service Apache (apache-spanel).
#   8. Tulis C:\Panel\panel.json (port 8888, AD dummy siws.local/IS;
#      hash dibuat non-interaktif dari password dummy smoke test).
#   9. panel admin set-password (interaktif - password pilihan lu, ketik 2x).
#  10. Install + start service panel SPanelPlus, lalu panel version + doctor.
#
# Idempotent: komponen yang sudah ada di-skip, baris Include tidak
# diduplikasi, backup dibuat sekali. Yang tetap manual sesudah script:
# uji boot M1 (restart mesin, buka UI tanpa start manual apa pun).
set -euo pipefail

fail() { echo "" >&2; echo "SETUP FAIL: $*" >&2; echo "Berhenti di sini - jangan lanjut ke smoke test. Kirim pesan fail ini ke Muse." >&2; exit 1; }
info() { echo "==> $*"; }

# --- Detect environment: Git Bash (/c/...) or WSL (/mnt/c/...) ---------------
if [ -d /c/Windows/System32 ]; then
  WIN_C="/c"; SYS32="/c/Windows/System32"
elif [ -d /mnt/c/Windows/System32 ]; then
  WIN_C="/mnt/c"; SYS32="/mnt/c/Windows/System32"
else
  fail "Script ini harus jalan di Git Bash atau WSL di mesin Windows (folder C:\\ tidak ditemukan)."
fi
PANEL_DIR="$WIN_C/Panel"
M0="$WIN_C/m0"
PANELW="C:/Panel"

command -v curl  >/dev/null || fail "curl tidak ditemukan di bash ini."
command -v unzip >/dev/null || fail "unzip tidak ditemukan di bash ini (Git Bash membawanya; kalau WSL: sudo apt install unzip)."

download() { # url dest min_bytes
  local url="$1" dest="$2" min="${3:-100000}"
  info "Download $url"
  curl -L -f --retry 2 -o "$dest" "$url" || fail "Download gagal dari $url (curl exit $?)"
  local size; size=$(stat -c%s "$dest" 2>/dev/null || stat -f%z "$dest")
  [ "$size" -ge "$min" ] || fail "File hasil download mencurigakan (size=$size): $dest dari $url"
}
try_download() { # url dest -> 0/1, no fail
  curl -L -f -s -o "$2" "$1" && [ "$(stat -c%s "$2" 2>/dev/null || echo 0)" -gt 1000000 ]
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# --- 1. Panel exe: cari di folder script, salin ke C:\Panel -------------------
# Preferensi: panel-v*.exe terbaru dulu, baru panel.exe polos.
# (ls dibungkus || true: tanpa itu, "tidak ada file yang cocok" mematikan
# script di bawah set -e sebelum fallback sempat jalan.)
panel_src=""
ls_out="$(ls -t "$SCRIPT_DIR"/panel-v*.exe 2>/dev/null || true)"
if [ -n "$ls_out" ]; then panel_src="$(printf '%s' "$ls_out" | head -n1)"; fi
if [ -z "$panel_src" ]; then
  panel_src="$(ls "$SCRIPT_DIR"/panel*.exe 2>/dev/null | head -n1 || true)"
fi
if [ -z "$panel_src" ] && [ -n "${PANEL_EXE_URL:-}" ]; then
  # Tidak ada exe lokal tapi ada URL release: unduh langsung ke tujuan.
  info "Tidak ada panel exe lokal - download dari PANEL_EXE_URL"
  mkdir -p "$PANEL_DIR"
  download "$PANEL_EXE_URL" "$PANEL_DIR/panel.exe" 1000000
  panel_src="$PANEL_DIR/panel.exe"
fi
[ -n "$panel_src" ] || fail "Tidak ada file panel*.exe di folder script ($SCRIPT_DIR) dan PANEL_EXE_URL tidak di-set. Download dulu panel-v0.6.2-m5.exe dan taruh sebelahan dengan script ini, atau set PANEL_EXE_URL ke URL asset release GitHub."
mkdir -p "$PANEL_DIR"
# Stop service panel lama dulu kalau jalan (file exe terkunci saat jalan).
if sc query SPanelPlus >/dev/null 2>&1; then
  if sc query SPanelPlus 2>/dev/null | grep -q RUNNING; then
    info "Stop service panel lama dulu (file exe terkunci saat service jalan)"
    net stop SPanelPlus >/dev/null 2>&1 || true
  fi
fi
panel_exe="$PANEL_DIR/panel.exe"
src_abs="$(readlink -f "$panel_src" 2>/dev/null || echo "$panel_src")"
dst_abs="$(readlink -f "$panel_exe" 2>/dev/null || echo "$panel_exe")"
if [ "$src_abs" = "$dst_abs" ]; then
  # Script + exe sudah di C:\Panel dan exe sudah bernama panel.exe:
  # menyalin file ke dirinya sendiri pasti gagal, jadi skip saja.
  info "Panel exe sudah di $panel_exe, skip salin"
else
  cp "$panel_src" "$panel_exe" || fail "Gagal menyalin $(basename "$panel_src") ke $panel_exe (file terkunci? service panel masih jalan?)"
  info "Panel exe: $(basename "$panel_src") -> $panel_exe"
fi

# --- 2. VC Redistributable (loud, sama seperti setup M0) -----------------------
[ -f "$SYS32/vcruntime140.dll" ] || fail "vcruntime140.dll tidak ada - install dulu VC++ Redistributable 2015-2022 x64: https://aka.ms/vs/17/release/vc_redist.x64.exe lalu jalankan ulang."

# --- Port Apache: dipilih di awal setting Apache ---------------------------
# Env APACHE_PORT menang (untuk jalan non-interaktif); kalau tidak di-set,
# script bertanya (default 80, Enter saja untuk pakai default). Port ini
# dipakai patch httpd.conf (Listen + ServerName), pesan fail service, dan
# URL verifikasi akhir. Port panel (8888) tidak boleh dipakai di sini.
if [ -z "${APACHE_PORT:-}" ]; then
  printf 'Port Apache [80]: '
  read -r APACHE_PORT || true
  if [ -z "$APACHE_PORT" ]; then APACHE_PORT=80; fi
fi
case "$APACHE_PORT" in
  ''|*[!0-9]*) fail "Port Apache harus angka, dapat: '$APACHE_PORT'" ;;
esac
if [ "$APACHE_PORT" -lt 1 ] || [ "$APACHE_PORT" -gt 65535 ]; then
  fail "Port Apache di luar rentang 1-65535: $APACHE_PORT"
fi
if [ "$APACHE_PORT" -eq 8888 ]; then
  fail "Port Apache tidak boleh sama dengan port panel (8888)"
fi
if [ "$APACHE_PORT" -eq 80 ]; then
  APACHE_BASE="http://127.0.0.1"
else
  APACHE_BASE="http://127.0.0.1:$APACHE_PORT"
fi
info "Port Apache: $APACHE_PORT"

# --- 3. Apache + mod_fcgid (URL VS18 terverifikasi) ---------------------------
# Apache Lounge sudah pindah dari VS17 ke VS18; keduanya harus sama-sama
# VS18 (mod_fcgid yang VS-nya beda tidak mau di-load Apache).
APACHE_URL="https://www.apachelounge.com/download/VS18/binaries/httpd-2.4.69-261002-Win64-VS18.zip"
FCGID_URL="https://www.apachelounge.com/download/VS18/modules/mod_fcgid-2.3.10-win64-VS18.zip"
mkdir -p "$M0/downloads" "$M0/php/8.2" "$M0/php/8.3" "$M0/htdocs"

if [ ! -f "$M0/Apache24/bin/httpd.exe" ]; then
  download "$APACHE_URL" "$M0/downloads/$(basename "$APACHE_URL")"
  rm -rf "$M0/_apache_tmp"; mkdir -p "$M0/_apache_tmp"
  unzip -q -o "$M0/downloads/$(basename "$APACHE_URL")" -d "$M0/_apache_tmp"
  inner="$(find "$M0/_apache_tmp" -name httpd.exe -path '*/bin/*' | head -n1)"
  [ -n "$inner" ] || fail "httpd.exe tidak ditemukan di dalam ZIP Apache - struktur ZIP berubah, laporkan ke Muse."
  # Sisa run sebelumnya yang rusak (folder ada, httpd.exe hilang): singkirkan
  # dulu supaya mv tidak menumpuk Apache24 di dalamnya.
  [ -d "$M0/Apache24" ] && rm -rf "$M0/Apache24"
  mv "$(dirname "$(dirname "$inner")")" "$M0/Apache24"
  rm -rf "$M0/_apache_tmp"
  info "Apache terpasang dari: $APACHE_URL"
else info "Apache sudah ada, skip"; fi

if [ ! -f "$M0/Apache24/modules/mod_fcgid.so" ]; then
  download "$FCGID_URL" "$M0/downloads/$(basename "$FCGID_URL")" 10000
  rm -rf "$M0/_fcgid_tmp"; mkdir -p "$M0/_fcgid_tmp"
  unzip -q -o "$M0/downloads/$(basename "$FCGID_URL")" -d "$M0/_fcgid_tmp"
  so="$(find "$M0/_fcgid_tmp" -name mod_fcgid.so | head -n1)"
  [ -n "$so" ] || fail "mod_fcgid.so tidak ditemukan di dalam ZIP mod_fcgid - laporkan ke Muse."
  cp "$so" "$M0/Apache24/modules/mod_fcgid.so"
  rm -rf "$M0/_fcgid_tmp"
  info "mod_fcgid terpasang dari: $FCGID_URL"
else info "mod_fcgid.so sudah ada, skip"; fi

# --- 4. PHP 8.2 + 8.3 NTS x64 (discovery + fallback pinned, seperti setup M0) --
declare -A PINNED=(
  ["8.2"]="php-8.2.28-nts-Win32-vs16-x64.zip php-8.2.15-nts-Win32-vs16-x64.zip"
  ["8.3"]="php-8.3.31-nts-Win32-vs16-x64.zip php-8.3.28-nts-Win32-vs16-x64.zip"
)
for branch in 8.2 8.3; do
  dest="$M0/php/$branch"
  if [ -f "$dest/php.exe" ]; then info "PHP $branch sudah ada, skip"; continue; fi
  zip=""
  info "Cari link PHP $branch NTS x64 di php.net"
  html="$(curl -L -f -s "https://www.php.net/downloads.php?os=windows&version=$branch" || true)"
  file="$(printf '%s' "$html" | grep -oE "php-${branch}\.[0-9]+-nts-Win32-vs[0-9]+-x64\.zip" | head -n1 || true)"
  if [ -n "${file:-}" ]; then
    url="https://windows.php.net/downloads/releases/$file"
    if try_download "$url" "$M0/downloads/$file"; then zip="$M0/downloads/$file"; echo "PHP $branch dari: $url"; fi
  fi
  if [ -z "$zip" ]; then
    echo "Discovery PHP $branch tidak berhasil - coba fallback arsip pinned..."
    for name in ${PINNED[$branch]}; do
      for folder in releases releases/archives; do
        url="https://windows.php.net/downloads/$folder/$name"
        if try_download "$url" "$M0/downloads/$name"; then zip="$M0/downloads/$name"; echo "PHP $branch dari arsip: $url"; break 2; fi
        echo "  bukan di $url"
      done
    done
  fi
  [ -n "$zip" ] || fail "Semua jalur download PHP $branch NTS x64 gagal (discovery + arsip pinned). Laporkan output ini ke Muse."
  unzip -q -o "$zip" -d "$dest"
  [ -f "$dest/php-cgi.exe" ] || fail "php-cgi.exe tidak ada setelah ekstrak PHP $branch dari $zip - bukan build NTS yang benar."
  [ -f "$dest/php.ini" ] || cp "$dest/php.ini-development" "$dest/php.ini"
  info "PHP $branch siap: $("$dest/php.exe" -v | head -n1)"
done

# --- 5. Proyek dummy M0 (isi persis dari kit m0/) ------------------------------
# Heredoc dikutip ('EOF') supaya $ di kode PHP tidak di-expand bash.
info "Tulis proyek dummy M0 ke $M0/htdocs"
# BUGFIX v3: subfolder proyek harus dibuat dulu - `cat >` tidak membuat
# folder induk, dan run pertama gagal di sini (app-php82 belum ada).
mkdir -p "$M0/htdocs/app-php82" "$M0/htdocs/app-php83" "$M0/htdocs/app-laravel/public" "$M0/htdocs/app-laravel/storage/logs"
cat > "$M0/htdocs/app-php82/index.php" <<'EOF'
<?php
// M0 dummy - plain project pinned to PHP 8.2
if (isset($_GET['check'])) {
    header('Content-Type: text/plain');
    echo 'project=app-php82 php=' . PHP_VERSION . ' sapi=' . php_sapi_name();
    exit;
}
phpinfo();
EOF
cat > "$M0/htdocs/app-php83/index.php" <<'EOF'
<?php
// M0 dummy - plain project pinned to PHP 8.3
if (isset($_GET['check'])) {
    header('Content-Type: text/plain');
    echo 'project=app-php83 php=' . PHP_VERSION . ' sapi=' . php_sapi_name();
    exit;
}
phpinfo();
EOF
mkdir -p "$M0/htdocs/app-laravel/public" "$M0/htdocs/app-laravel/storage/logs"
cat > "$M0/htdocs/app-laravel/public/index.php" <<'EOF'
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
EOF
cat > "$M0/htdocs/app-laravel/public/.htaccess" <<'EOF'
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
EOF
cat > "$M0/htdocs/app-laravel/.env" <<'EOF'
APP_NAME=M0LaravelDummy
APP_ENV=local
APP_URL=http://127.0.0.1/app-laravel
M0_SECRET_SHOULD_NOT_BE_PUBLIC=do-not-expose-this-value
EOF
cat > "$M0/htdocs/app-laravel/artisan" <<'EOF'
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
EOF
touch "$M0/htdocs/app-laravel/storage/logs/.gitkeep"
cp "$SCRIPT_DIR/../m0/apache/conf-m0-projects.conf" "$M0/conf-m0-projects.conf" 2>/dev/null || true
if [ ! -f "$M0/conf-m0-projects.conf" ]; then
  # Script di-download tanpa kit repo: tulis conf dari salinan embedded.
  cat > "$M0/conf-m0-projects.conf" <<'EOF'
# M0 - per-project mod_fcgid config (ditulis setup-panel-smoke-v2.sh).
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
EOF
fi

# --- 6. Patch httpd.conf (backup sekali, idempotent, seperti setup M0) --------
CONF="$M0/Apache24/conf/httpd.conf"
[ -f "$CONF" ] || fail "httpd.conf tidak ditemukan di $CONF"
[ -f "$CONF.orig-m0" ] || cp "$CONF" "$CONF.orig-m0"
# Apache Lounge stock conf uses: Define SRVROOT "C:/Apache24"
sed -i -E 's|^Define SRVROOT .*|Define SRVROOT "C:/m0/Apache24"|' "$CONF"
sed -i -E 's|^ServerRoot .*|ServerRoot "C:/m0/Apache24"|' "$CONF"
sed -i -E "s|^#?[[:space:]]*ServerName .*|ServerName localhost:$APACHE_PORT|" "$CONF"
grep -q '^ServerName ' "$CONF" || echo "ServerName localhost:$APACHE_PORT" >> "$CONF"
sed -i -E "s|^Listen .*|Listen $APACHE_PORT|" "$CONF"
sed -i -E 's|^([[:space:]]*)DirectoryIndex .*|\1DirectoryIndex index.php index.html|' "$CONF"
sed -i -E 's|^DocumentRoot .*|DocumentRoot "C:/m0/htdocs"|' "$CONF"
sed -i -E 's|<Directory "\$\{SRVROOT\}/htdocs">|<Directory "C:/m0/htdocs">|' "$CONF"
sed -i -E 's|<Directory "C:/m0/Apache24/htdocs">|<Directory "C:/m0/htdocs">|' "$CONF"
# Uncomment modules we need (delimiter @ on purpose: the pattern itself
# contains | for alternation, so | cannot be the s delimiter - that was
# the "char 32: unknown option to s" bug, reproduced & fixed 2026-10-23)
# Toleran spasi di depan # dan sesudahnya: baris "# LoadModule ..."
# atau "  #LoadModule ..." harus ikut terbuka, bukan cuma "#LoadModule".
sed -i -E 's@^[[:space:]]*#[[:space:]]*(LoadModule (rewrite|alias|authz_core|dir|mime)_module .*)$@\1@' "$CONF"
# Kalau baris rewrite tidak ada sama sekali (bukan cuma dikomen),
# uncomment di atas tidak menghasilkan apa-apa — tambahkan eksplisit.
# rewrite_module WAJIB aktif: .htaccess Laravel membungkus semua rule
# dalam <IfModule mod_rewrite.c>, jadi kalau modul ini mati, pretty URL
# (mis. /login) 404 diam-diam sementara / jalan via DirectoryIndex.
# Bug nyata 2026-10-28 di mesin max: httpd -M kosong untuk rewrite.
if ! grep -qE '^[[:space:]]*LoadModule rewrite_module ' "$CONF"; then
  printf '\r\nLoadModule rewrite_module modules/mod_rewrite.so\r\n' >> "$CONF"
  info "LoadModule rewrite_module ditambahkan (barisnya tidak ada di httpd.conf)"
fi
if grep -qE '^[[:space:]]*LoadModule php' "$CONF"; then
  fail "httpd.conf masih memuat mod_php (LoadModule php...) - melanggar desain M0. Tempel baris itu ke Muse."
fi
grep -q 'conf-m0-projects\.conf' "$CONF" || printf '\r\nInclude "C:/m0/conf-m0-projects.conf"\r\n' >> "$CONF"
# Include generated panel HARUS menunjuk <data_dir>/generated/apache.
# panel.json setup memakai data_dir C:/Panel, jadi path-nya
# C:/Panel/generated/apache. Path lama C:/Panel/data/generated/apache
# adalah data_dir DEFAULT (bila panel.json tidak ada) — setup lama salah
# tulis path itu, sehingga config generated tidak pernah ter-load oleh
# Apache (gejala: proyek terdaftar tapi tetap listing/source PHP).
# Migrasi baris lama (plain maupun Optional) ke path yang benar.
sed -i -E 's|^Include(Optional)? "C:/Panel/data/generated/apache/\*.conf"|IncludeOptional "C:/Panel/generated/apache/*.conf"|' "$CONF"
grep -q 'C:/Panel/generated/apache' "$CONF" || printf '\r\nIncludeOptional "C:/Panel/generated/apache/*.conf"\r\n' >> "$CONF"
info "httpd.conf ter-patch (backup: httpd.conf.orig-m0)"
grep -E '^(Define SRVROOT|DocumentRoot|Listen|ServerName)' "$CONF" || true

# --- 7. Bukti lokal: httpd -t harus Syntax OK ----------------------------------
# BUGFIX v5 (koreksi atas v4): mkdir saja TIDAK cukup. Include biasa
# menuntut wildcard cocok >= 1 file - direktori kosong tetap gagal
# ("No matches for the wildcard"). Yang benar: IncludeOptional untuk
# direktori generated panel, yang boleh kosong sebelum apply pertama.
# (v4 hanya mengatasi error "Could not open directory" saat direktori
# belum ada sama sekali.)
mkdir -p "$PANEL_DIR/generated/apache"
# (|| true: tanpa itu, httpd -t yang gagal mematikan script sebelum pesan
# fail di bawah sempat tampil.)
info "Test konfigurasi Apache"
syntax="$("$M0/Apache24/bin/httpd.exe" -t 2>&1 || true)"
echo "$syntax"
printf '%s' "$syntax" | grep -q 'Syntax OK' || fail "httpd -t TIDAK Syntax OK. Output persisnya di atas - kirim ke Muse, jangan paksa start Apache."
# Gate modul rewrite: httpd -t hanya cek sintaks, jadi modul yang mati
# tetap lolos setup dan baru ketahuan saat pretty URL 404 (bug 2026-10-28).
mods="$("$M0/Apache24/bin/httpd.exe" -M 2>&1 || true)"
printf '%s' "$mods" | grep -q 'rewrite_module' || fail "rewrite_module TIDAK ke-load (httpd -M). Periksa LoadModule rewrite_module di httpd.conf dan file modules/mod_rewrite.so - jangan lanjutkan setup."
info "rewrite_module ter-load (httpd -M)"
"$M0/Apache24/bin/httpd.exe" -v

# --- 8. Service Apache (apache-spanel) ----------------------------------------
# Migrasi dari nama lama: service Apache2.4 peninggalan setup lama HANYA
# dihapus bila binary-nya dari C:\m0 (milik setup ini). Service Apache2.4
# milik aplikasi lain (bukan dari m0) TIDAK disentuh - fail keras.
if sc query Apache2.4 >/dev/null 2>&1; then
  if sc qc Apache2.4 2>/dev/null | grep -qi 'm0'; then
    info "Service lama Apache2.4 (milik setup ini) ditemukan, migrasi ke apache-spanel"
    net stop Apache2.4 >/dev/null 2>&1 || true
    "$M0/Apache24/bin/httpd.exe" -k uninstall -n Apache2.4 || true
  else
    fail "Service Apache2.4 sudah ada tapi bukan dari C:\\m0 (milik XAMPP/aplikasi lain?). Stop/uninstall service itu dulu."
  fi
fi
if sc query apache-spanel >/dev/null 2>&1; then
  if ! sc qc apache-spanel 2>/dev/null | grep -qi 'm0'; then
    fail "Service apache-spanel sudah ada tapi bukan dari C:\\m0 (milik aplikasi lain?). Stop/uninstall service itu dulu."
  fi
  info "Service apache-spanel sudah ada (milik setup ini), skip install"
else
  info "Install service Apache apache-spanel"
  "$M0/Apache24/bin/httpd.exe" -k install -n apache-spanel || true
fi
if ! sc query apache-spanel 2>/dev/null | grep -q RUNNING; then
  net start apache-spanel || true
  sleep 2
fi
sc query apache-spanel 2>/dev/null | grep -q RUNNING || fail "Service apache-spanel gagal start. Kemungkinan port $APACHE_PORT dipakai aplikasi lain - cek dengan: netstat -ano | findstr :$APACHE_PORT"
info "Service apache-spanel running"

# --- 9. panel.json (backup sekali, hash dummy non-interaktif) ------------------
PANEL_JSON="$PANEL_DIR/panel.json"
if [ -f "$PANEL_JSON" ] && [ ! -f "$PANEL_JSON.bak-smoke" ]; then
  cp "$PANEL_JSON" "$PANEL_JSON.bak-smoke"
  info "panel.json lama dibackup ke panel.json.bak-smoke"
fi
# Password dummy smoke test saja (bukan password asli siapa pun). Hash
# dibuat lewat panel.exe sendiri; passwordnya tidak pernah ditulis ke file.
# Heredoc di bawah TIDAK dikutip supaya variabel hash ter-expand SEKALI -
# nilai hash yang sudah jadi tidak di-expand ulang oleh bash.
info "Buat hash AD dummy (budi/andi) lewat panel.exe"
budi_hash="$(printf '%s\n' 'budi-kuat-1' | "$panel_exe" admin hash-password || true)"
andi_hash="$(printf '%s\n' 'andi-kuat-1' | "$panel_exe" admin hash-password || true)"
[ -n "$budi_hash" ] && [ -n "$andi_hash" ] || fail "Hash dummy kosong - perintah 'panel admin hash-password' tidak mengembalikan hash. Jalankan manual: $panel_exe admin hash-password"
cat > "$PANEL_JSON" <<EOF
{
  "port": 8888,
  "data_dir": "C:/Panel",
  "apache_service": "apache-spanel",
  "apache_home": "C:/m0/Apache24",
  "ad_enabled": true,
  "ad_server": "dummy",
  "ad_domain": "siws.local",
  "ad_base_dn": "DC=siws,DC=local",
  "ad_group_dn": "CN=IS,DC=siws,DC=local",
  "ad_dummy_users": [
    {
      "username": "budi",
      "password_hash": "$budi_hash",
      "groups": ["IS"]
    },
    {
      "username": "andi",
      "password_hash": "$andi_hash",
      "groups": ["HRD"]
    }
  ]
}
EOF
info "panel.json ditulis (port 8888, data_dir C:/Panel, apache_service apache-spanel, apache_home C:/m0/Apache24, AD dummy siws.local/IS)"

# --- 10. Password admin (interaktif, pilihan lu sendiri) -----------------------
echo ""
echo "Sekarang set password admin panel (ketik 2x saat diminta):"
"$panel_exe" admin set-password

# --- 11. Service panel ---------------------------------------------------------
# panel.json baru ditulis di langkah 9, jadi service panel yang sudah jalan
# HARUS di-restart supaya config baru (termasuk apache_service) ter-load;
# kalau tidak, panel tetap memegang config lama di memori.
if sc query SPanelPlus >/dev/null 2>&1; then
  info "Service SPanelPlus sudah ada, skip install"
  if sc query SPanelPlus 2>/dev/null | grep -q RUNNING; then
    info "Restart service SPanelPlus supaya panel.json baru ter-load"
    net stop SPanelPlus >/dev/null 2>&1 || true
    sleep 1
  fi
else
  info "Install service panel SPanelPlus"
  "$panel_exe" service install || true
fi
if ! sc query SPanelPlus 2>/dev/null | grep -q RUNNING; then
  "$panel_exe" service start || true
  sleep 2
fi
sc query SPanelPlus 2>/dev/null | grep -q RUNNING || fail "Service SPanelPlus gagal start. Jalankan manual: $panel_exe service start lalu kirim outputnya ke Muse."
info "Service SPanelPlus running"

# --- 12. Verifikasi akhir ------------------------------------------------------
# (doctor dibiarkan gagal tanpa mematikan script: outputnya tetap tampil
# untuk dibaca, dan WARN panel-port memang by design.)
echo ""
"$panel_exe" version
"$panel_exe" doctor || true
echo ""
echo "SETUP SELESAI."
echo "  Panel UI : http://127.0.0.1:8888 (login pakai password admin yang baru lu set)"
echo "  Dummy web: $APACHE_BASE/app-php82/?check  (harus 8.2)"
echo "             $APACHE_BASE/app-php83/?check  (harus 8.3)"
echo "             $APACHE_BASE/app-laravel/      (tanpa /public)"
echo "  AD dummy : login Akun AD -> budi / budi-kuat-1 (lolos), andi / andi-kuat-1 (ditolak)"
echo "  Lanjut smoke test urut: M1 (service/boot) -> M2 (CLI) -> M3 (UI) -> M4 (supervisor) -> M5 (auth) -> M5-AD."
echo "  Catatan: uji boot M1 tetap manual - restart mesin, lalu buka UI tanpa start manual apa pun."
