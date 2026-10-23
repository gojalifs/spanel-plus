#!/usr/bin/env bash
# M0 auto-setup (bash version) - run in Git Bash as Administrator, or WSL:
#   cd /e/panel/m0-kit/scripts   # sesuaikan drive/lokasi kit lu
#   bash setup-m0.sh
#
# Downloads & installs everything verify-m0.sh expects. Fail-loud throughout:
# set -e + explicit checks - any failed step stops with the exact URL/error.
# Uses only tools Git Bash ships: curl, unzip, sed, grep, find.
set -euo pipefail

fail() { echo "" >&2; echo "SETUP FAIL: $*" >&2; echo "Berhenti di sini - jangan lanjut verify. Kirim pesan fail ini ke Muse." >&2; exit 1; }
info() { echo "==> $*"; }

# --- Detect environment: Git Bash (/c/...) or WSL (/mnt/c/...) ---------------
if [ -d /c/Windows/System32 ]; then
  WIN_C="/c"; SYS32="/c/Windows/System32"
elif [ -d /mnt/c/Windows/System32 ]; then
  WIN_C="/mnt/c"; SYS32="/mnt/c/Windows/System32"
else
  fail "Script ini harus jalan di Git Bash atau WSL di mesin Windows (folder C:\\ tidak ditemukan)."
fi
M0="$WIN_C/m0"                 # filesystem path for bash tools
M0W="C:/m0"                    # path written into Apache config (Windows form)

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

# --- 0. Layout ---------------------------------------------------------------
info "Siapkan layout $M0 (Git Bash/WSL path) = C:\\m0"
mkdir -p "$M0/php/8.2" "$M0/php/8.3" "$M0/results" "$M0/downloads" "$M0/htdocs"
KIT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
[ -d "$KIT_ROOT/htdocs" ] || fail "Folder htdocs kit tidak ditemukan di $KIT_ROOT - ekstrak kit utuh dulu."

# --- 1. VC Redistributable check (loud) ---------------------------------------
[ -f "$SYS32/vcruntime140.dll" ] || fail "vcruntime140.dll tidak ada - install dulu VC++ Redistributable 2015-2022 x64: https://aka.ms/vs/17/release/vc_redist.x64.exe lalu jalankan ulang."

# --- 2. Apache + mod_fcgid (pinned verified URLs, both VS18) ------------------
# Verified on https://www.apachelounge.com/download/ on 2026-10-23.
APACHE_URL="https://www.apachelounge.com/download/VS18/binaries/httpd-2.4.69-261002-Win64-VS18.zip"
FCGID_URL="https://www.apachelounge.com/download/VS18/modules/mod_fcgid-2.3.10-win64-VS18.zip"

if [ ! -f "$M0/Apache24/bin/httpd.exe" ]; then
  download "$APACHE_URL" "$M0/downloads/$(basename "$APACHE_URL")"
  rm -rf "$M0/_apache_tmp"; mkdir -p "$M0/_apache_tmp"
  unzip -q -o "$M0/downloads/$(basename "$APACHE_URL")" -d "$M0/_apache_tmp"
  inner="$(find "$M0/_apache_tmp" -name httpd.exe -path '*/bin/*' | head -n1)"
  [ -n "$inner" ] || fail "httpd.exe tidak ditemukan di dalam ZIP Apache - struktur ZIP berubah, laporkan ke Muse."
  mv "$(dirname "$(dirname "$inner")")" "$M0/Apache24"
  rm -rf "$M0/_apache_tmp"
  echo "Apache terpasang dari: $APACHE_URL"
else info "Apache sudah ada, skip"; fi

if [ ! -f "$M0/Apache24/modules/mod_fcgid.so" ]; then
  download "$FCGID_URL" "$M0/downloads/$(basename "$FCGID_URL")" 10000
  rm -rf "$M0/_fcgid_tmp"; mkdir -p "$M0/_fcgid_tmp"
  unzip -q -o "$M0/downloads/$(basename "$FCGID_URL")" -d "$M0/_fcgid_tmp"
  so="$(find "$M0/_fcgid_tmp" -name mod_fcgid.so | head -n1)"
  [ -n "$so" ] || fail "mod_fcgid.so tidak ditemukan di dalam ZIP mod_fcgid - laporkan ke Muse."
  cp "$so" "$M0/Apache24/modules/mod_fcgid.so"
  rm -rf "$M0/_fcgid_tmp"
  echo "mod_fcgid terpasang dari: $FCGID_URL"
else info "mod_fcgid.so sudah ada, skip"; fi

# --- 3. PHP 8.2 + 8.3 NTS x64 (discovery via curl, fallback pinned archives) --
declare -A PINNED=(
  ["8.2"]="php-8.2.28-nts-Win32-vs16-x64.zip php-8.2.15-nts-Win32-vs16-x64.zip"
  ["8.3"]="php-8.3.31-nts-Win32-vs16-x64.zip php-8.3.28-nts-Win32-vs16-x64.zip"
)
for branch in 8.2 8.3; do
  dest="$M0/php/$branch"
  if [ -f "$dest/php.exe" ]; then info "PHP $branch sudah ada, skip"; continue; fi
  zip=""
  # Discovery: parse the php.net Windows download page for this branch
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
  cp "$dest/php.ini-development" "$dest/php.ini"
  info "PHP $branch siap: $("$dest/php.exe" -v | head -n1)"
done

# --- 4. Copy kit payload --------------------------------------------------------
info "Salin htdocs + config M0 dari kit"
cp -r "$KIT_ROOT/htdocs/." "$M0/htdocs/"
cp "$KIT_ROOT/apache/conf-m0-projects.conf" "$M0/conf-m0-projects.conf"

# --- 5. Patch httpd.conf (backup first, idempotent) ----------------------------
CONF="$M0/Apache24/conf/httpd.conf"
[ -f "$CONF" ] || fail "httpd.conf tidak ditemukan di $CONF"
[ -f "$CONF.orig-m0" ] || cp "$CONF" "$CONF.orig-m0"
# Apache Lounge stock conf uses: Define SRVROOT "C:/Apache24"
sed -i -E 's|^Define SRVROOT .*|Define SRVROOT "C:/m0/Apache24"|' "$CONF"
sed -i -E 's|^ServerRoot .*|ServerRoot "C:/m0/Apache24"|' "$CONF"
sed -i -E 's|^#?[[:space:]]*ServerName .*|ServerName localhost:80|' "$CONF"
grep -q '^ServerName ' "$CONF" || echo 'ServerName localhost:80' >> "$CONF"
sed -i -E 's|^Listen .*|Listen 80|' "$CONF"
sed -i -E 's|^([[:space:]]*)DirectoryIndex .*|\1DirectoryIndex index.php index.html|' "$CONF"
sed -i -E 's|^DocumentRoot .*|DocumentRoot "C:/m0/htdocs"|' "$CONF"
sed -i -E 's|<Directory "\$\{SRVROOT\}/htdocs">|<Directory "C:/m0/htdocs">|' "$CONF"
sed -i -E 's|<Directory "C:/m0/Apache24/htdocs">|<Directory "C:/m0/htdocs">|' "$CONF"
# Uncomment modules we need (delimiter @ on purpose: the pattern itself
# contains | for alternation, so | cannot be the s delimiter - that was
# the "char 32: unknown option to s" bug, reproduced & fixed 2026-10-23)
sed -i -E 's@^#(LoadModule (rewrite|alias|authz_core|dir|mime)_module .*)$@\1@' "$CONF"
if grep -qE '^[[:space:]]*LoadModule php' "$CONF"; then
  fail "httpd.conf masih memuat mod_php (LoadModule php...) - melanggar desain M0. Tempel baris itu ke Muse."
fi
grep -q 'conf-m0-projects\.conf' "$CONF" || printf '\r\nInclude "C:/m0/conf-m0-projects.conf"\r\n' >> "$CONF"
info "httpd.conf ter-patch (backup: httpd.conf.orig-m0)"
grep -E '^(Define SRVROOT|DocumentRoot|Listen|ServerName)' "$CONF" || true

# --- 6. Prove it locally ---------------------------------------------------------
info "Test konfigurasi Apache"
syntax="$("$M0/Apache24/bin/httpd.exe" -t 2>&1)"
echo "$syntax"
printf '%s' "$syntax" | grep -q 'Syntax OK' || fail "httpd -t TIDAK Syntax OK. Output persisnya di atas - kirim ke Muse, jangan paksa start Apache."
"$M0/Apache24/bin/httpd.exe" -v
"$M0/php/8.2/php.exe" -v | head -n1
"$M0/php/8.3/php.exe" -v | head -n1
echo ""
echo "SETUP SELESAI. Langkah berikutnya:"
echo "  1. Jalankan Apache (di Git Bash Administrator, biarkan jendela ini terbuka):"
echo "       /c/m0/Apache24/bin/httpd.exe -w"
echo "  2. Di jendela Git Bash lain, dari folder scripts kit:  bash verify-m0.sh"
echo "  3. Kirim output verify ke Muse."
