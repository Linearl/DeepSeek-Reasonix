package main

import (
	"log/slog"

	"reasonix/internal/repair"
)

// tl1827: startup consumption of the update-apply-failure marker.
//
// The write side has always existed: the Windows update helper records
// repair.MarkUpdateApplyFailedExact after a failed NSIS run and relaunches,
// and the Linux apply path writes the same intent marker before publishing
// (desktop/updater.go applyLinux) expecting "startup correlates the exact
// transaction and rolls the release unit back". The read side did not: no
// production caller ever invoked repair.RecoverFailedInstall, so a recorded
// installer failure survived forever and no rollback ever ran.
//
// This file owns the missing half of the contract, aligning with the
// apply_failure.go semantics (read marker → correlate with the pending
// transaction → roll back → clear; on rollback errors both the marker and the
// transaction are retained so the next launch retries):
//
//   - runs once per launch from the local-services startup stage, only for
//     the primary desktop process (remote web-window children return before
//     startupStartLocalServices);
//   - runs in the background, kicked before install retention so a backup a
//     rollback still needs is never swept first;
//   - is observable: every non-zero-action outcome carries the
//     "[update-recover]" anchor log (tl1795 "[autopilot-restore]" precedent);
//     a launch without a marker stays silent at info level (zero action).

// repairRecoverFailedInstall is the repair-entry seam the e2e tests stub.
var repairRecoverFailedInstall = repair.RecoverFailedInstall

// runUpdateApplyFailureRecovery performs one startup recovery pass. The repair
// layer serializes marker correlation, rollback, and marker cleanup into one
// transition under the pending-update lock; this wrapper only makes the
// outcome observable.
func runUpdateApplyFailureRecovery() {
	result, failure, err := repairRecoverFailedInstall()
	if err != nil {
		// The repair layer intentionally keeps both the marker and the pending
		// transaction here; the next launch retries the same transition.
		if result.MixedInstall {
			// A failed rollback that could not compensate mixes binaries from
			// two releases — the state launchers must never silently continue
			// from. Shout so the next diagnostic starts here.
			slog.Error("[update-recover] failed-update rollback errored; install is mixed",
				"toVersion", failure.ToVersion,
				"reason", failure.Reason,
				"err", err)
			return
		}
		slog.Warn("[update-recover] failed-update rollback errored; marker retained for retry",
			"toVersion", failure.ToVersion,
			"reason", failure.Reason,
			"err", err)
		return
	}
	if failure == nil {
		// No marker: the common path. Zero action, zero info-level noise.
		return
	}
	if !result.RolledBack {
		// The marker did not correlate with the pending transaction (stale,
		// foreign, or the transaction already resolved). The repair layer
		// cleared it without touching binaries.
		slog.Info("[update-recover] stale failure marker cleared without rollback",
			"toVersion", failure.ToVersion,
			"reason", failure.Reason)
		return
	}
	// UpdateRollbackResult mirrors the rollback direction: FromVersion is the
	// failed target being left behind, ToVersion is the release restored.
	slog.Info("[update-recover] rolled back failed update at startup",
		"failedVersion", result.FromVersion,
		"restoredVersion", result.ToVersion,
		"target", result.TargetPath,
		"reason", failure.Reason)
}

// startupRecoverFailedInstall kicks the recovery pass in the background. A
// method (not an inline goSafe call site) so tests can pin that the startup
// chain actually invokes the recovery.
func (a *App) startupRecoverFailedInstall() {
	a.goSafe("recoverFailedInstall", runUpdateApplyFailureRecovery)
}
