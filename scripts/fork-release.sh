#!/usr/bin/env bash
# fork-release.sh — build Reasonix fork release artifacts locally.
# Usage: bash scripts/fork-release.sh [version]
#   version defaults to `git describe --tags --always`.
#
# This builds the CLI for all supported platforms (unsigned).
# Desktop (Wails) builds require the official release-desktop.yml pipeline
# or a local Wails toolchain; see FORK.md for the fork release notes.

set -euo pipefail

cd "$(dirname "$0")/.."

VERSION="${1:-$(git describe --tags --always 2>/dev/null || echo dev)}"
echo "==> Building Reasonix fork CLI version: ${VERSION}"

mkdir -p dist

for p in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do
  os="${p%/*}"
  arch="${p#*/}"
  ext=""
  if [ "$os" = "windows" ]; then ext=".exe"; fi
  out="dist/reasonix-${os}-${arch}${ext}"
  echo "==> ${os}/${arch} -> ${out}"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o "$out" ./cmd/reasonix
done

echo "==> Done. Artifacts in dist/:"
ls -lh dist/
