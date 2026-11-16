#!/usr/bin/env bash
# tl1626 档A wrapper: the implementation lives in compare-release-manifests.sh.
# This entry path is preserved so release-desktop.yml (which invokes it as
# release-control/scripts/...) and release-workflows.test.sh need no change.
# Output is byte-identical to the pre-merge Desktop script.
exec "$(dirname "$0")/compare-release-manifests.sh" Desktop "$@"
