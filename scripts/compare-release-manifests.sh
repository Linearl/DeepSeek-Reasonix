#!/usr/bin/env bash
# Compare two release manifests (candidate vs existing) for one product.
# tl1626 档A: merged from compare-cli-release-manifests.sh + compare-desktop-release-manifests.sh
# (two 21-line copies whose only semantic difference was the product word in the
# error text). The original paths remain as 1-line wrappers passing CLI / Desktop.
#
# Usage: compare-release-manifests.sh <PRODUCT> <candidate-manifest> <existing-manifest>
#   PRODUCT is interpolated verbatim into the error message, preserving the
#   historical per-entrypoint wording ("<PRODUCT> release manifest does not exist: ...").
set -euo pipefail

product="${1:-}"
candidate="${2:-}"
existing="${3:-}"

for manifest in "$candidate" "$existing"; do
	if [ ! -f "$manifest" ]; then
		echo "${product} release manifest does not exist: $manifest" >&2
		exit 1
	fi
done

# Immutable manifests published before release_notes_url existed remain valid.
# Normalize only that missing field from the strict candidate; every other
# semantic difference must still fail the immutable recovery check.
jq -e -s '
	.[0] as $candidate |
	.[1] as $existing |
	($existing | .release_notes_url = (.release_notes_url // $candidate.release_notes_url)) == $candidate
' "$candidate" "$existing" >/dev/null
