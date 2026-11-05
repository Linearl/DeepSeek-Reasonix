#!/usr/bin/env bash
set -euo pipefail

# Self-test for scripts/check-compress-cache-floor.sh: trigger / no-trigger /
# usage / env-file input / filter coverage.

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
check="$repo_root/scripts/check-compress-cache-floor.sh"

# The -run filter the gate uses. Keep in sync with check-compress-cache-floor.sh.
filter='TestPrefixCacheHitRate|TestCheckCompressCacheRedline|TestRedlineReadsScorerUnits|TestFullCompressionCache|TestFullCompressionExemptFromAbsoluteLine|TestPartialCompressionAbsoluteLine'

# 1. Non-compression files: gate not needed, exit 0 without running go test.
out="$("$check" docs/GUIDE.md README.md)"
if [[ "$out" != *"not needed"* ]]; then
  echo "non-compression change unexpectedly triggered the gate: $out" >&2
  exit 1
fi

# 2. Usage passthrough.
"$check" -h >/dev/null

# 3. Compression-chain file via argv: gate runs the real red-line suite.
out="$("$check" internal/agent/compact_projection.go)"
if [[ "$out" != *"Prefix-cache floor gate passed."* ]]; then
  echo "compression-chain change did not pass the gate: $out" >&2
  exit 1
fi

# 4. Env-file input path and the curate/fold/chunked surfaces (task 1378's
#    domain rides the same gate).
tmp="$(mktemp)"
printf '%s\n' internal/agent/curate_context.go internal/agent/session_extract.go internal/tool/compress.go > "$tmp"
out="$(COMPRESS_FLOOR_CHANGED_FILES_FILE="$tmp" "$check")"
rm -f "$tmp"
if [[ "$out" != *"Prefix-cache floor gate passed."* ]]; then
  echo "env-file input did not pass the gate: $out" >&2
  exit 1
fi

# 5. Filter coverage: the gate's -run filter must match the whole three-state
#    suite (11 named tests). A renamed/typo'd filter would silently run zero
#    tests and stay green — this check is the guard against that.
listed="$(cd "$repo_root" && go test ./internal/agent -list "$filter" | grep -c '^Test' || true)"
if [[ "$listed" -lt 11 ]]; then
  echo "gate -run filter matched only $listed tests (want >= 11); the three-state suite would not fully run under the gate" >&2
  exit 1
fi

echo "compress-cache-floor contract tests: PASS (filter matched $listed tests)"
