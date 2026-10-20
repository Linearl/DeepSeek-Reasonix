package main

import (
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"time"

	"reasonix/internal/repair"
)

func (a *App) recordPreviousRunDiagnostics() {
	previous := a.lifecycle.previousRun
	if previous.Abnormal {
		report := previousRunReport(previous)
		_ = writePendingReport(report, true)
		if m := a.metrics.Load(); m != nil {
			m.inc("desktop_legacy_exit", "abnormal")
			m.inc("desktop_legacy_exit_phase", metricBucket(previous.Phase))
			m.persist()
		}
	}
	// Task 377: the noise gate (experimental, default off) skips the report for
	// phases that prove a clean shutdown was already underway — the clean()/exit
	// race C-20260920-02 documented, not crash evidence. Suppression stays
	// observable: one summary line + a metrics bucket, never a silent drop.
	// Task 847 (763 复发#2, 20261011 装机实测): an update restart is a PLANNED
	// exit — the exiting process wrote the update-restart marker right before
	// quitting, so a residue in a clean-shutdown phase from that same PID is
	// the marker-writer's clean()/exit race, not a crash. The marker vouches
	// for exactly that record (fresh window + PID match), independent of the
	// gate: gate=false installs (the field default) must not re-surface the
	// 0911 false "上次运行崩溃" banner on every update restart.
	//
	// Task 873: the marker check must precede the phase gate. A forced update
	// restart (grace window expired → forced quit) can leave the record in a
	// mid-shutdown phase (closing_*, waiting_runtime_locks): that phase is not
	// "benign" in the task-377 sense, but the exit was still PLANNED (the
	// marker writer is this exact PID handing off to an update relaunch).
	//
	// Task 1018 (20261020 装机实测): the planned-exit dispositions are read-and-
	// clear at the source — a marker-vouched update restart or a benign-phase
	// clean shutdown never writes a crash report (a wedged record had been
	// re-surfacing the 「上次运行遗留的崩溃报告」 banner every launch). Forensics
	// stay observable (1545): per-record log + summary + metrics + the 736
	// launcher death record as the death-moment witness.
	//
	// Task 1018-fix2 (20261020 协调线定案): 1018 over-shot — it silenced
	// abnormal_phase too, but wedged/unknown have NO other sink (go.fatal and
	// recoverToPending need a live process, which a force-kill never leaves).
	// Restore abnormal-phase reports (删保护机制前必须有替代 — none here); the
	// 1002 stale layer + 847/873 marker vouch still quiet the false banner.
	//
	// Task 1002 并集（847 家族新形态）：非直接前任留下的记录不算本次「上次运行」
	// 证据，单独退役计数（staleRetired）。归属判定（1002）与处置动作（计划退出
	// 读后即清、异常相位写报告）正交，见下方循环内的前置分支。
	updateRestart, cleanShutdown, abnormalPhase := 0, 0, 0
	staleRetired := 0
	for _, lifecycle := range a.lifecycle.previousRuns {
		disposition := "abnormal_phase"
		switch {
		case updateRestartMarkerFreshForLifecycle(lifecycle.PID):
			disposition = "update_restart"
			updateRestart++
		case lifecycleNoiseBenign(lifecycle.Phase):
			disposition = "clean_shutdown"
			cleanShutdown++
		default:
			abnormalPhase++
		}
		// Task 1002 (847 家族新形态, 20261019/20 装机实锤): the marker vouch only
		// covers the exiting process's OWN record — an EARLIER abnormal death's
		// record survives its successor's boot and is claimed boots later, when
		// the marker belongs to an unrelated process (PID cross-check refuses).
		// The consume-pass stamp settles attribution; this pre-branch rules
		// attribution only, never disposal.
		if lifecycleRecordPredatesPass(lifecycle.UpdatedAt, a.lifecycle.priorPassAt) {
			staleRetired++
			continue
		}
		if disposition == "abnormal_phase" {
			// Task 1018-fix2: wedged/unknown have no other sink — report it.
			_ = writePendingReport(desktopLifecycleReport(lifecycle), true)
		}
		slog.Info("desktop: lifecycle residue dispositioned (task 1018-fix2)",
			"pid", lifecycle.PID, "phase", lifecycle.Phase, "disposition", disposition,
			"reported", disposition == "abnormal_phase",
			"exit_code", lifecycle.ExitCode, "death_at", lifecycle.DeathAt,
			"task_1027_wedge_point", wedgePointBucket(lifecycle.WedgePoint))
		if m := a.metrics.Load(); m != nil {
			m.inc("desktop_exit_phase", metricBucket(lifecycle.Phase))
			// Task 1027 (issue #60): the crash-report surface carries the
			// wedge point as a payload field (task 1018-fix2 restored the
			// abnormal-phase report); the metrics stream is the aggregatable
			// companion — a wedged exit gets its own named bucket instead of
			// folding into the bare "wedged" phase count, so remote cuts can
			// slice by wedge point without parsing report text. Planned exits
			// carry no wedge point and never touch this signal.
			if strings.EqualFold(strings.TrimSpace(lifecycle.Phase), "wedged") {
				m.inc("desktop_exit_wedge_point", wedgePointBucket(lifecycle.WedgePoint))
			}
			switch disposition {
			case "update_restart":
				m.inc("desktop_exit_phase", "update_restart_suppressed")
			case "clean_shutdown":
				if a.lifecycle.noiseGate {
					m.inc("desktop_exit_phase", "noise_gate_suppressed")
				}
			default:
				m.inc("desktop_exit_phase", "abnormal_phase_reported")
				m.inc("desktop_exit", "residue_reported")
			}
		}
	}
	if total := updateRestart + cleanShutdown + abnormalPhase; total > 0 {
		slog.Info("desktop: lifecycle residue dispositioned",
			"total", total, "update_restart_suppressed", updateRestart,
			"clean_shutdown", cleanShutdown, "abnormal_phase_reported", abnormalPhase)
		if m := a.metrics.Load(); m != nil {
			m.persist()
		}
	}
	// Task 1002: stale retirements get their own named line — they are not a
	// vouch (nothing "approves" the death) but an attribution ruling: an
	// earlier boot already had its chance at this evidence.
	if staleRetired > 0 {
		slog.Info("desktop: stale lifecycle residue retired without a crash report",
			"count", staleRetired,
			"priorPassAt", a.lifecycle.priorPassAt.Format(time.RFC3339),
			"rule", "an earlier boot's consume pass already ran after the record's last phase write")
		if m := a.metrics.Load(); m != nil {
			m.inc("desktop_exit_phase", "stale_record_retired")
			m.persist()
		}
	}
}

// lifecycleRecordPredatesPass reports whether a claimed record's last phase
// write happened before a PREVIOUS boot's consume pass — i.e. that pass
// already saw this record (or an identical file), so the current boot is a
// late claimant rather than the record's direct successor and the record
// cannot honestly be surfaced as "上次运行" evidence here. Fail-open on
// purpose: a missing or unparseable timestamp keeps the report-always
// behavior (task 1002 — the false-banner fix must never eat a real crash).
func lifecycleRecordPredatesPass(updatedAt string, priorPassAt time.Time) bool {
	if priorPassAt.IsZero() {
		return false
	}
	at, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(updatedAt))
	if err != nil {
		return false
	}
	return at.Before(priorPassAt)
}

// lifecycleNoiseBenign reports whether the phase proves the previous run was
// already in (or finished) its clean shutdown. wedged (shutdown watchdog forced
// exit — a real abnormal termination) and unknown phases are never benign.
func lifecycleNoiseBenign(phase string) bool {
	switch strings.ToLower(strings.TrimSpace(phase)) {
	case "shutting_down", "healthy":
		return true
	}
	return false
}

func previousRunReport(previous repair.PreviousRunObservation) crashReport {
	phase := metricBucket(previous.Phase)
	uptime := metricBucket(previous.UptimeBucket)
	profile := metricBucket(previous.InstallProfile)
	update := "none"
	if previous.UpdateTo != "" {
		update = sanitizeCrashField(previous.UpdateFrom+" -> "+previous.UpdateTo, 128)
	}
	message := fmt.Sprintf(`[desktop.legacy_abnormal_exit]

Reasonix consumed a legacy v1.18-v1.19 startup record whose owner was no longer running.

--- lifecycle context ---
phase: %s
uptime bucket: %s
install profile: %s
previous version: %s
update transition: %s`,
		phase,
		uptime,
		profile,
		sanitizeCrashField(previous.Version, 64),
		update,
	)
	report := baseCrashReport("crash")
	report.SchemaVersion = 2
	report.Source = "native.lifecycle.legacy"
	report.Label = "desktop.legacy_abnormal_exit"
	report.ErrorType = "LegacyAbnormalDesktopExit"
	report.ErrorMessage = "A legacy startup record was consumed once after its owner stopped."
	report.TopFrame = "desktop.lifecycle.legacy." + phase
	report.FingerprintHint = "desktop.legacy_abnormal_exit." + runtime.GOOS + "." + phase
	report.OccurredAt = time.Now().UTC().Format(time.RFC3339)
	report.Message = sanitizeCrashText(message, maxCrashDetailBytes)
	return report
}

// wedgePointEnum collapses a raw wedge point to its aggregatable stage name:
// the per-tab teardown stage stamps a live tab ID (closing_tab:<id>), which
// would fan the remote metric into unbounded cardinality — only the stage
// prefix is kept. All other stage names pass through unchanged.
func wedgePointEnum(point string) string {
	if i := strings.IndexByte(point, ':'); i >= 0 {
		return point[:i]
	}
	return point
}

// wedgePointBucket is the enumerated wedge point for remote surfaces (metrics
// bucket, report payload field): the stage name normalized through
// metricBucket, or the explicit "unknown" when the record carries none — the
// task-1027 acceptance forbids a missing/blank value where the field is
// emitted.
func wedgePointBucket(point string) string {
	if strings.TrimSpace(point) == "" {
		return "unknown"
	}
	return metricBucket(wedgePointEnum(point))
}

// desktopLifecycleReport builds the desktop.abnormal_exit.v2 report shape.
// Task 1018 silenced every disposition; task 1018-fix2 restored the call for
// the abnormal_phase disposition only (wedged/unknown have no other crash
// sink), planned exits staying read-and-clear. It remains the canonical schema
// for pre-1018 backlog and the task-736 attribution field-mapping doc.
func desktopLifecycleReport(previous desktopLifecycleObservation) crashReport {
	phase := metricBucket(previous.Phase)
	// Task 736 (issue #39): occurredAt used to be the record's last phase
	// write — "healthy" stamped seconds after startup, hours before the death
	// it claimed to timestamp. The launcher-observed death moment is the
	// actual value; without it, the discovery moment is the honest bound
	// (death happened no later than this) and the message says so.
	occurredAt := previous.DeathAt
	deathLine := ""
	if occurredAt == "" {
		occurredAt = previous.DetectedAt
	} else {
		deathLine = fmt.Sprintf("\ndeath observed: %s (launcher, exit code %d)", previous.DeathAt, previous.ExitCode)
	}
	// Task 1027 (issue #60): the wedge point names the teardown stage the
	// shutdown watchdog was waiting on. Enumerated for remote aggregation:
	// the per-tab stage carries a live tab ID (closing_tab:<id>), which
	// would fan out into unbounded buckets, so the ID suffix is dropped
	// before metricBucket normalizes the name. A wedged exit WITHOUT a
	// wedge point (a pre-1027 record) reports the explicit "unknown" — the
	// acceptance forbids a missing field. A non-wedged exit has no wedge
	// point at all, and the omitempty wire field stays absent rather than
	// emitting noise.
	wedgePoint := ""
	wedgeLine := ""
	if strings.EqualFold(strings.TrimSpace(previous.Phase), "wedged") {
		wedgePoint = wedgePointBucket(previous.WedgePoint)
		wedgeLine = fmt.Sprintf("\nwedge point: %s (shutdown watchdog forced exit)", wedgePoint)
	}
	message := fmt.Sprintf(`[desktop.abnormal_exit.v2]

Reasonix found a per-process lifecycle record whose desktop process was no longer running.

--- lifecycle context ---
phase: %s
pid: %d%s%s
previous version: %s
previous channel: %s
started at: %s
last phase update: %s
discovered at: %s`,
		phase,
		previous.PID,
		deathLine,
		wedgeLine,
		sanitizeCrashField(previous.Version, 64),
		sanitizeCrashField(previous.Channel, 32),
		sanitizeCrashField(previous.StartedAt, 64),
		sanitizeCrashField(previous.UpdatedAt, 64),
		sanitizeCrashField(previous.DetectedAt, 64),
	)
	report := baseCrashReport("crash")
	report.SchemaVersion = 3
	report.Source = "native.lifecycle"
	report.Label = "desktop.abnormal_exit.v2"
	report.ErrorType = "AbnormalDesktopExit"
	report.ErrorMessage = "A per-process lifecycle record remained after its desktop process stopped."
	report.TopFrame = "desktop.lifecycle.v2." + phase
	report.FingerprintHint = "desktop.abnormal_exit.v2." + runtime.GOOS + "." + phase
	report.OccurredAt = sanitizeCrashField(occurredAt, 64)
	report.ProcessPID = previous.PID
	report.ExitPhase = previous.Phase
	report.WedgePoint = wedgePoint
	report.Message = sanitizeCrashText(message, maxCrashDetailBytes)
	return report
}
