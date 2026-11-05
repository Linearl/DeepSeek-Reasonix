#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/check-compress-cache-floor.sh [changed-file ...]

tl1396 prefix-cache red line gate for the compression chain. When a change
touches compression-chain surfaces (compact / compress / curate / fold /
chunked / prefix-recovery), it runs the three-state red-line go tests:

  state 1  full compression: exempt from the 95% absolute line
  state 2  full compression: relative floor (hitAfter >= hitBefore x 0.05,
           the calibrated conservative lower bound)
  state 3  partial compression (tl1397 reservation): >=95% absolute line

Any compression change that breaks its scenario's line turns the gate red.

Inputs:
  COMPRESS_FLOOR_CHANGED_FILES            Newline-separated changed files.
  COMPRESS_FLOOR_CHANGED_FILES_FILE       File containing newline-separated changed files.
  COMPRESS_FLOOR_BASE_SHA / BASE_SHA      Diff base when files are not supplied.
  COMPRESS_FLOOR_HEAD_SHA / HEAD_SHA      Diff head when files are not supplied.
USAGE
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

changed_input=""
if [[ "$#" -gt 0 ]]; then
  changed_input="$(printf '%s\n' "$@")"
elif [[ -n "${COMPRESS_FLOOR_CHANGED_FILES_FILE:-}" ]]; then
  changed_input="$(cat "$COMPRESS_FLOOR_CHANGED_FILES_FILE")"
elif [[ -n "${COMPRESS_FLOOR_CHANGED_FILES:-}" ]]; then
  changed_input="$COMPRESS_FLOOR_CHANGED_FILES"
else
  base="${COMPRESS_FLOOR_BASE_SHA:-${BASE_SHA:-}}"
  head="${COMPRESS_FLOOR_HEAD_SHA:-${HEAD_SHA:-HEAD}}"
  if [[ -z "$base" ]]; then
    base="$(git merge-base origin/main-v2 "$head" 2>/dev/null || git merge-base main-v2 "$head")"
  fi
  diff_base="$base"
  if merge_base="$(git merge-base "$base" "$head" 2>/dev/null)"; then
    diff_base="$merge_base"
  fi
  changed_input="$(git diff --name-only "$diff_base" "$head")"
fi

# Compression-chain surfaces: every file whose change can move what the next
# request looks like after a fold. Kept deliberately wide (task 1378's curate
# increments live in the same domain); a false-positive run costs one go test.
compress_chain=()
for file in "${changed_input:-}"; do
  [[ -z "$file" ]] && continue
  case "$file" in
    internal/agent/compact*|\
    internal/agent/compress*|\
    internal/agent/curate*|\
    internal/agent/fold*|\
    internal/agent/prefix_cache*|\
    internal/agent/positional*|\
    internal/agent/recovery_prefix*|\
    internal/agent/session_extract*|\
    internal/agent/context_maintenance*|\
    internal/agent/storm_breaker.go|\
    internal/tool/compress*|\
    internal/tool/builtin/compress.go)
      compress_chain+=("$file")
      ;;
  esac
done

if [[ "${#compress_chain[@]}" -eq 0 ]]; then
  echo "No compression-chain files changed; prefix-cache floor gate not needed."
  exit 0
fi

echo "Compression-chain files changed:"
printf '  - %s\n' "${compress_chain[@]}"

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

# The red-line suite: scorer unit tests + the three-state judgment tests +
# the calibration bound. -count=1 so a stale test cache cannot green-light a
# new compression behavior.
go test ./internal/agent -count=1 -run 'TestPrefixCacheHitRate|TestCheckCompressCacheRedline|TestRedlineReadsScorerUnits|TestFullCompressionCache|TestFullCompressionExemptFromAbsoluteLine|TestPartialCompressionAbsoluteLine'

echo "Prefix-cache floor gate passed."
