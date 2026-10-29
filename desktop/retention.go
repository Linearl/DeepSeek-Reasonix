package main

import (
	"log/slog"
	"os"

	"reasonix/internal/installlayout"
)

// 任务835: the updater-side versions retention trigger.
//
// The rule itself lives in installlayout.EnforceVersionRetention (keep the
// active tree plus at most three historical trees, sweep update debris older
// than a day: *.replaced- shells, .staging- scratch, quarantined backup
// nodes). This file owns the "update confirmed" half of the contract: the
// pass runs from the fresh process's own startup, after the local services
// are standing — a process that gets this far is a new version that booted
// successfully, so a failed update never loses its rollback source. Every
// launch runs it (idempotent), which is what bounds versions/ after any
// number of consecutive updates.
//
// The build-side prune (tools/prune-versions keep=5) and the signed-helper
// chain's own GC (update-helper's RetainPreviousVersions active+1) stay
// untouched: switching time keeps active+1 for rollback, startup time
// converges to the four-tree ceiling.

// retentionInstallRoot is the seam the tests point at a scratch install root;
// production resolves the real versioned install root from the executable.
var retentionInstallRoot = func() string {
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	root, err := installlayout.ResolveInstallRoot(executable)
	if err != nil {
		return ""
	}
	return root
}

// enforceInstallRetention applies the 任务835 rule once. A portable build
// (no versioned layout) is a no-op — there is no versions/ to retain.
func (a *App) enforceInstallRetention() {
	root := ""
	if a != nil {
		root = retentionInstallRoot()
	}
	if root == "" {
		slog.Debug("desktop: install retention skipped (no versioned install root)")
		return
	}
	report := installlayout.EnforceVersionRetention(root)
	if !report.Empty() {
		slog.Info("desktop: install retention applied at startup",
			"root", root,
			"prunedTrees", report.PrunedTrees,
			"replacedShells", report.RemovedReplaced,
			"stagingScratch", report.RemovedStaging,
			"quarantinedNodes", report.RemovedQuarantined,
			// Task 1259: trees kept because a live instance runs from them —
			// surfaced here so a persistent occupied tree stays visible in the
			// startup log (the escalated ones already warned inside the pass).
			"retainedOccupied", installlayout.DescribeRetained(report.RetainedOccupied))
	}
}
