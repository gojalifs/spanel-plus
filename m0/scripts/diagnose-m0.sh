#!/usr/bin/env bash
# M0 diagnose for K1/K2 404 - run in Git Bash, Apache supposedly running.
# Prints everything needed in one paste. No changes are made.
set -uo pipefail
if [ -d /c/Windows/System32 ]; then M0="/c/m0"; elif [ -d /mnt/c/Windows/System32 ]; then M0="/mnt/c/m0"; else echo "Harus di Git Bash/WSL Windows"; exit 1; fi

echo "===== 1. Files on disk (yang Apache harusnya baca) ====="
ls -la "$M0/htdocs/" 2>&1
ls -la "$M0/htdocs/app-php82/" 2>&1
ls -la "$M0/htdocs/app-laravel/public/" 2>&1
echo ""
echo "===== 2. httpd.conf efektif (baris kunci) ====="
grep -nE 'SRVROOT|ServerRoot|DocumentRoot|Listen|ServerName|conf-m0-projects|<Directory' "$M0/Apache24/conf/httpd.conf" 2>&1 | head -n 40
echo ""
echo "--- isi conf-m0-projects.conf yang ter-Include (kepala file) ---"
head -n 20 "$M0/conf-m0-projects.conf" 2>&1
echo ""
echo "===== 3. Siapa yang jawab di port 80? ====="
echo "--- GET / (headers + awal body) ---"
curl -s -i --max-time 10 http://127.0.0.1/ 2>&1 | head -n 25
echo ""
echo "--- GET /app-php82/?check ---"
curl -s -i --max-time 10 'http://127.0.0.1/app-php82/?check' 2>&1 | head -n 25
echo ""
echo "--- siapa yang LISTEN di port 80 + proses httpd yang jalan ---"
netstat -ano 2>&1 | grep -E '[:.]80[[:space:]]' | head -n 10 || echo "(netstat tidak terbaca)"
tasklist.exe //FI "IMAGENAME eq httpd.exe" 2>&1 || echo "(tidak ada httpd.exe di tasklist)"
echo ""
echo "===== 4. Apache error.log terakhir (PENENTU: path yang dicari Apache) ====="
tail -n 40 "$M0/Apache24/logs/error.log" 2>&1
echo ""
echo "===== SELESAI - tempel SEMUA output ini ke Muse ====="
