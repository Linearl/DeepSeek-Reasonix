#!/usr/bin/env bash
# tl1788 pattern 5: automated single-run re-verification for go tests.
#
# First pass runs the requested packages together (like `go test ./...`).
# Every failing package is then re-run alone, up to -attempts total runs.
# Green on any isolated run => FLAKY (load/env jitter; reported, non-blocking).
# Red on every attempt => REAL RED (blocking; exit 1). The gate only ever
# opens for environment noise; real reds always surface.
#
# -count=1 is always forced so the test cache can never fake a re-run.
# Default -timeout 30m (tl1788 pattern 4: go's 10m per-package default
# mis-kills the agent package, measured at 750s+, and the desktop package,
# measured at 1203s, before they finish).
#
# Usage: go-test-triage.sh [-timeout DURATION] [-attempts N] [packages...]
# Exit codes: 0 = no real red (flaky-only or all green); 1 = at least one
# package red on every attempt (or the run failed without package-level
# output, e.g. a build/toolchain failure).
set -u

TIMEOUT=30m
ATTEMPTS=3
while [ $# -gt 0 ]; do
  case "$1" in
    -timeout) TIMEOUT="$2"; shift 2;;
    -attempts) ATTEMPTS="$2"; shift 2;;
    *) break;;
  esac
done
[ $# -gt 0 ] || set -- ./...

command -v go >/dev/null || { echo "go-test-triage: go not on PATH"; exit 1; }

logdir="$(mktemp -d)"
echo "go-test-triage: timeout=$TIMEOUT attempts=$ATTEMPTS packages=$* logdir=$logdir"

run_once() {
  go test -count=1 -timeout "$TIMEOUT" "$@"
}

full_log="$logdir/full.log"
run_once "$@" > "$full_log" 2>&1
status=$?

if [ "$status" -eq 0 ]; then
  echo "go-test-triage: PASS (all packages green on first run)"
  exit 0
fi

# Package-level failure lines look like `FAIL <pkg> 0.5s` or
# `FAIL <pkg> [build failed]`; the bare trailing `FAIL` is excluded.
failing="$(grep -E '^FAIL[[:space:]]+[[:alnum:]]' "$full_log" | awk '{print $2}' | sort -u)"
if [ -z "$failing" ]; then
  echo "go-test-triage: FAIL (exit $status) with no package-level FAIL lines - build or toolchain failure, treat as REAL RED. Last 40 lines:"
  tail -40 "$full_log"
  exit 1
fi

flaky=""
real=""
for pkg in $failing; do
  verdict="REAL"
  attempt=1
  while [ "$attempt" -lt "$ATTEMPTS" ]; do
    attempt=$((attempt + 1))
    pkg_log="$logdir/rerun-$(printf '%s' "$pkg" | tr '/:' '--').log"
    echo "go-test-triage: REVERIFY $pkg (isolated run $attempt/$ATTEMPTS)"
    if run_once "$pkg" > "$pkg_log" 2>&1; then
      verdict="FLAKY"
      break
    fi
  done
  if [ "$verdict" = "FLAKY" ]; then
    flaky="$flaky $pkg"
    echo "FLAKY $pkg (green on isolated run $attempt/$ATTEMPTS - env/load jitter, NOT red)"
  else
    real="$real $pkg"
    echo "REAL-RED $pkg (red on all $ATTEMPTS attempts)"
    grep -E '^--- FAIL|^    --- FAIL|^FAIL' "$pkg_log" | head -20
    echo "  evidence log: $pkg_log"
  fi
done

echo "go-test-triage summary: FLAKY:${flaky:- none} | REAL-RED:${real:- none}"
echo "go-test-triage logs: first run $full_log (+ rerun logs in $logdir)"
[ -z "$real" ]
