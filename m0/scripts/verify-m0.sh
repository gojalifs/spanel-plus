#!/usr/bin/env bash
# M0 verification (bash) - run in Git Bash after Apache is up (setup-m0.sh done).
#   bash verify-m0.sh [base_url]   # default http://127.0.0.1
set -uo pipefail
BASE="${1:-http://127.0.0.1}"
if [ -d /c/Windows/System32 ]; then M0="/c/m0"; elif [ -d /mnt/c/Windows/System32 ]; then M0="/mnt/c/m0"; else echo "Harus di Git Bash/WSL Windows"; exit 1; fi

pass=0; failc=0
record() { # id ok detail
  if [ "$2" = "1" ]; then echo "$1 : PASS - $3"; pass=$((pass+1)); else echo "$1 : FAIL - $3"; failc=$((failc+1)); fi
}
get() { # path -> "CODE|BODY"
  local body code
  body="$(curl -s -w $'\n%{http_code}' "$BASE$1" 2>/dev/null || echo -e "\n000")"
  code="$(printf '%s' "$body" | tail -n1)"
  body="$(printf '%s' "$body" | sed '$d')"
  echo "$code|$body"
}

echo "== M0 verify ==  BaseUrl=$BASE  M0Root=$M0"
echo "httpd: $("$M0/Apache24/bin/httpd.exe" -v 2>&1 | head -n1 || echo 'httpd.exe NOT FOUND')"
echo "php82: $("$M0/php/8.2/php.exe" -v 2>&1 | head -n1 || echo 'php 8.2 NOT FOUND')"
echo "php83: $("$M0/php/8.3/php.exe" -v 2>&1 | head -n1 || echo 'php 8.3 NOT FOUND')"
[ -f "$M0/Apache24/modules/mod_fcgid.so" ] && echo "mod_fcgid.so exists: true" || echo "mod_fcgid.so exists: FALSE"
syntax="$("$M0/Apache24/bin/httpd.exe" -t 2>&1)"; echo "httpd -t: $syntax"

a="$(get '/app-php82/?check')"; b="$(get '/app-php83/?check')"
echo "app-php82 -> $a"; echo "app-php83 -> $b"
if [ "${a%%|*}" = "200" ] && [ "${b%%|*}" = "200" ] && printf '%s' "$a" | grep -q 'php=8\.2\.' && printf '%s' "$b" | grep -q 'php=8\.3\.'; then k1=1; else k1=0; fi
record K1 "$k1" "dua versi PHP beda dari satu Apache"

c="$(get '/app-laravel/?check')"
if [ "${c%%|*}" = "200" ] && printf '%s' "$c" | grep -q 'project=app-laravel'; then k2=1; else k2=0; fi
record K2 "$k2" "status=${c%%|*} body=${c#*|}"

d="$(get '/app-laravel/.env')"
if [ "${d%%|*}" = "404" ] && ! printf '%s' "$d" | grep -q 'M0_SECRET'; then k3=1; else k3=0; fi
record K3 "$k3" "status=${d%%|*} (expected 404)"

k4=0; k4detail=""
if [ -f "$M0/htdocs/app-laravel/artisan" ]; then
  out="$(cd "$M0/htdocs/app-laravel" && "$M0/php/8.2/php.exe" artisan queue:work --once 2>&1)"
  k4detail="$out"
  if printf '%s' "$out" | grep -q 'queue dummy processed' && printf '%s' "$out" | grep -q 'php=8\.2\.'; then k4=1; fi
fi
record K4 "$k4" "$k4detail"

if [ -f "$M0/Apache24/modules/mod_fcgid.so" ] && printf '%s' "$syntax" | grep -q 'Syntax OK'; then k5=1; else k5=0; fi
record K5 "$k5" "mod_fcgid + httpd -t (catat versi persisnya di M0-RESULT.md)"

mkdir -p "$M0/results"
stamp="$(date +%Y%m%d-%H%M%S)"
echo "Kriteria PASS=$pass FAIL=$failc (detail di console)" > "$M0/results/verify-$stamp.txt"
echo ""
if [ "$failc" = "0" ]; then echo "M0: PASS (semua kriteria)"; else echo "M0: FAIL - kirim output ini ke Muse"; fi
