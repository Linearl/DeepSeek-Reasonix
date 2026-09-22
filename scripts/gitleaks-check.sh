#!/usr/bin/env bash
# Task 219 P0 (2026-09-22): local gitleaks secret scan.
#
#   scripts/gitleaks-check.sh            # staged changes (run before every commit)
#   scripts/gitleaks-check.sh full       # whole history incl. all branches in HEAD's past
#
# Exit 0 clean, 1 leaks found, 2 gitleaks unavailable.
#
# Setup (once): install the gitleaks 8.x binary (e.g.
#   https://github.com/gitleaks/gitleaks/releases — single executable) and put
#   it on PATH, or point GITLEAKS_BIN at it. The repo-root .gitleaks.toml
#   carries the inspected allowlist from the 2026-09-22 baseline scan
#   (9701 commits, no leaks found) — extend that file only with entries backed
#   by an inspected example, never to silence a rule wholesale.
#
# Optional CI integration is planned but not enabled yet — see
# docs/GITLEAKS-CI.md for the workflow draft and the confirmation gates.

set -euo pipefail

GITLEAKS="${GITLEAKS_BIN:-gitleaks}"
if ! command -v "$GITLEAKS" >/dev/null 2>&1; then
  echo "gitleaks not found on PATH (set GITLEAKS_BIN to the binary)." >&2
  echo "Install: https://github.com/gitleaks/gitleaks/releases" >&2
  exit 2
fi

mode="${1:-staged}"
case "$mode" in
  staged)
    # `protect` reads staged changes only: cheap enough for every commit.
    exec "$GITLEAKS" protect --staged --redact --verbose
    ;;
  full)
    exec "$GITLEAKS" detect --source . --redact
    ;;
  *)
    echo "usage: scripts/gitleaks-check.sh [staged|full]" >&2
    exit 2
    ;;
esac
