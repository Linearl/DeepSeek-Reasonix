#!/bin/bash
# build-local-installer.sh — local Windows installer build (win-amd64).
#
# NOTE on icon/version resources (2026-09-07):
#   The main reasonix-desktop.exe gets its icon embedded by wails itself
#   (build/windows/icon.ico), so runtime/taskbar icons are correct.
#   CI stamps version resources via cmd/windows-resource, which is NOT
#   committed to the repo (upstream build.sh references an untracked tool),
#   so local builds carry no version resource — cosmetic only.
#   goversioninfo .syso embedding is NOT viable on go1.26 yet:
#   "unknown relocation type 7" at link time (retry after toolchain updates
#   or when upstream commits the stamper). Shortcuts in project.nsi now point
#   their icon at the main exe instead of the bare launcher for this reason.
#
# Usage:
#   scripts/build-local-installer.sh [VERSION]   # VERSION defaults to
#                                                 # desktop/wails.json productVersion
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CHANNEL="${REASONIX_CHANNEL:-stable}"

# --- version: $1 overrides desktop/wails.json productVersion ---
if [ -n "${1:-}" ]; then
	VER="$1"
else
	VER=$(grep -o '"productVersion": *"[^"]*"' "$ROOT/desktop/wails.json" | head -1 | cut -d '"' -f4)
fi
if [ -z "${VER:-}" ]; then
	echo "ERROR: could not resolve version (pass one: $0 1.34.0)" >&2
	exit 1
fi

export HTTP_PROXY="${HTTP_PROXY:-http://127.0.0.1:10808}"
export HTTPS_PROXY="${HTTPS_PROXY:-http://127.0.0.1:10808}"
export PATH="$(go env GOPATH)/bin:${LOCALAPPDATA:-$HOME/AppData/Local}/reasonix-nsis/nsis-3.12:$PATH"
export REASONIX_CHANNEL="$CHANNEL"

INS="$ROOT/desktop/build/windows/installer"
mkdir -p "$INS"

echo "==> [1/3] prebuilt helpers: guard / launcher / update-helper / cli (VER=$VER)"
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
	-ldflags="-s -w -X main.version=$VER" -o "$INS/reasonix-guard.exe" ./cmd/reasonix-legacy-migrator
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
	-ldflags="-s -w -H windowsgui -X main.version=$VER" -o "$INS/reasonix-launcher.exe" ./cmd/reasonix-launcher
(cd desktop && GOOS=windows GOARCH=amd64 go build -trimpath \
	-ldflags="-s -w" -o "$INS/reasonix-update-helper.exe" ./cmd/update-helper)
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
	-ldflags="-s -w -X main.version=$VER -X main.channel=$CHANNEL" -o "$INS/reasonix-cli.exe" ./cmd/reasonix

echo "==> [2/3] wails build (never pass -s: it skips the frontend build)"
cd "$ROOT/desktop"
wails build -clean -platform windows/amd64 -nsis -webview2 embed

echo "==> [3/3] artifacts"
ls -la build/bin/ | grep -Ei "installer|reasonix-desktop"
echo "DONE: desktop/build/bin/reasonix-desktop-amd64-installer.exe (v$VER)"
