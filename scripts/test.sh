#!/usr/bin/env bash
# SPanel+ — local equivalent of CI (.github/workflows/ci.yml).
# Run from the repo root, in Git Bash on Windows or any Linux shell:
#   bash scripts/test.sh
# Fails loud on the first red stage, same as CI.
set -euo pipefail
cd "$(dirname "$0")/.."

echo "== gofmt check =="
files=$(gofmt -l cmd internal)
if [ -n "$files" ]; then
  echo "Files not gofmt-clean:"
  echo "$files"
  exit 1
fi

echo "== go vet =="
go vet ./...

echo "== unit tests =="
go test ./...

echo "== build (host) =="
mkdir -p dist
go build -o dist/panel ./cmd/panel

if [ "$(go env GOOS)" != "windows" ]; then
  echo "== build (windows cross-compile check) =="
  GOOS=windows GOARCH=amd64 go build -o dist/panel.exe ./cmd/panel
fi

echo "ALL GREEN"
