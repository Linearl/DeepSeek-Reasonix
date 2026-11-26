#!/usr/bin/env bash
# fork-release.sh — build Reasonix fork release artifacts locally.
# Usage: bash scripts/fork-release.sh [version]
#          [--verify-baseline <prev build point>]
#          [--verify-branches feat-a,feat-b | --verify-branches-file branches.txt]
#   version defaults to `git describe --tags --always`.
#
# tl1760 ancestry gate (2026-11-22): when branch claims are provided, every
# claimed branch/tl-commit is verified to be contained in the commit being
# built BEFORE any artifact is produced (cherry patch-id primary, ancestor
# auxiliary, rev-list baseline-window fallback). Any MISSING/EMPTY/ERROR
# aborts the release. No claims -> gate skipped, flow unchanged.
#
# This builds the CLI for all supported platforms (unsigned).
# Desktop (Wails) builds require the official release-desktop.yml pipeline
# or a local Wails toolchain; see FORK.md for the fork release notes.

set -euo pipefail

cd "$(dirname "$0")/.."

VERSION=""
VERIFY_BASELINE=""
VERIFY_BRANCHES=""
VERIFY_BRANCHES_FILE=""
while [ $# -gt 0 ]; do
  case "$1" in
    --verify-baseline) VERIFY_BASELINE="${2:?--verify-baseline needs a value}"; shift 2 ;;
    --verify-branches) VERIFY_BRANCHES="${2:?--verify-branches needs a value}"; shift 2 ;;
    --verify-branches-file) VERIFY_BRANCHES_FILE="${2:?--verify-branches-file needs a value}"; shift 2 ;;
    --*) echo "fork-release.sh: unknown option: $1" >&2; exit 2 ;;
    *) if [ -z "$VERSION" ]; then VERSION="$1"; else echo "fork-release.sh: unexpected extra argument: $1" >&2; exit 2; fi; shift ;;
  esac
done
VERSION="${VERSION:-$(git describe --tags --always 2>/dev/null || echo dev)}"

# ---- tl1760 ancestry gate (before any build; fail fast, no wasted artifacts) ----
RELEASE_COMMIT="$(git rev-parse HEAD)"
if [ -n "$VERIFY_BRANCHES" ] || [ -n "$VERIFY_BRANCHES_FILE" ]; then
  if [ -z "$VERIFY_BASELINE" ]; then
    echo "fork-release.sh: --verify-branches/-file requires --verify-baseline <previous package build point>" >&2
    exit 2
  fi
  echo "==> Ancestry gate: build point ${RELEASE_COMMIT}, baseline ${VERIFY_BASELINE}"
  GATE_ARGS=(--build-point "$RELEASE_COMMIT" --baseline "$VERIFY_BASELINE" --repo "$(pwd)")
  [ -n "$VERIFY_BRANCHES_FILE" ] && GATE_ARGS+=(--branches-file "$VERIFY_BRANCHES_FILE")
  [ -n "$VERIFY_BRANCHES" ] && GATE_ARGS+=(--branches "$VERIFY_BRANCHES")
  gate_rc=0
  node scripts/verify-release-ancestry.mjs "${GATE_ARGS[@]}" || gate_rc=$?
  if [ "$gate_rc" -ne 0 ]; then
    echo "==> Ancestry gate FAILED (exit ${gate_rc}) — release aborted: fix notes or roll-up before building" >&2
    exit "$gate_rc"
  fi
  echo "==> Ancestry gate passed."
else
  echo "==> Ancestry gate: no branch claims provided (skipped; flow unchanged)"
fi

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

# tl1820: repo hygiene warning at end of packaging. Interrupted/concurrent git
# operations leave tmp_pack_* garbage in .git/objects/pack; a large loose count
# bloats the repo. Non-zero warnings mean: run `git gc --prune=now` (after
# confirming no concurrent git processes) before the next build.
GC_STATS="$(git count-objects -v)"
GARBAGE="$(printf '%s\n' "$GC_STATS" | sed -n 's/^garbage: *//p')"
LOOSE="$(printf '%s\n' "$GC_STATS" | sed -n 's/^count: *//p')"
if [ "${GARBAGE:-0}" -gt 0 ]; then
  echo "==> WARNING: git count-objects reports ${GARBAGE} garbage entries (likely tmp_pack_* from interrupted git ops) — run git gc --prune=now (first confirm no concurrent git processes)" >&2
fi
if [ "${LOOSE:-0}" -gt 10000 ]; then
  echo "==> WARNING: git count-objects reports ${LOOSE} loose objects — consider git gc before next build" >&2
fi
