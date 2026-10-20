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
	// gate: gate=false installs (the field default, and every existing config
	// renders the key explicitly false) must not re-surface the 0911 false
	// "上次运行崩溃" banner on every update restart.
	gateSuppressed, markerSuppressed, staleRetired := 0, 0, 0
	for _, lifecycle := range a.lifecycle.previousRuns {
		// Task 873: the marker check must precede the phase gate. A forced
		// update restart (grace window expired → forced quit) can leave the
		// record in a mid-shutdown phase (closing_*, waiting_runtime_locks):
		// that phase is not "benign" in the task-377 sense, but the exit was
		// still PLANNED (the marker writer is this exact PID handing off to an
		// update relaunch). Task 847 only suppressed clean phases, so every
		// forced-through install surfaced the "上次运行遗留的崩溃报告" banner.
		// The PID-match + fresh-window vouching stays the same — a marker from
		// an unrelated PID or an old window suppresses nothing.
		if updateRestartMarkerFreshForLifecycle(lifecycle.PID) {
			markerSuppressed++
			continue
		}
		// Task 1002 (847 家族新形态, 20261019/20 装机实锤): the marker vouch
		// only covers the exiting process's OWN record. A record left by an
		// EARLIER abnormal death can survive its natural successor's boot (a
		// lost Windows claim race, or a briefly-alive reused PID) and get
		// claimed several boots later — by then the on-disk marker belongs to
		// an unrelated process, the PID cross-check correctly refuses, and the
		// stale record surfaced as "上次运行遗留的崩溃报告" right after an
		// update the user read as the update's own crash. The consume-pass
		// stamp settles the attribution: if an intermediate boot already ran
		// its consume pass after this record's last phase write, THIS boot is
		// not the record's direct successor — the record is not "上次运行"
		// evidence here. Retire it (claim already removed it from disk) with a
		// named log + metrics bucket, never silently (1545).
		if lifecycleRecordPredatesPass(lifecycle.UpdatedAt, a.lifecycle.priorPassAt) {
			staleRetired++
			continue
		}
		if !lifecycleNoiseBenign(lifecycle.Phase) {
			_ = writePendingReport(desktopLifecycleReport(lifecycle), true)
			if m := a.metrics.Load(); m != nil {
				m.inc("desktop_exit", "abnormal")
				m.inc("desktop_exit_phase", metricBucket(lifecycle.Phase))
				m.persist()
			}
			continue
		}
		if a.lifecycle.noiseGate {
			gateSuppressed++
			continue
		}
		_ = writePendingReport(desktopLifecycleReport(lifecycle), true)
		if m := a.metrics.Load(); m != nil {
			m.inc("desktop_exit", "abnormal")
			m.inc("desktop_exit_phase", metricBucket(lifecycle.Phase))
			m.persist()
		}
	}
	if gateSuppressed > 0 || markerSuppressed > 0 {
		slog.Info("desktop: clean-shutdown lifecycle residue suppressed",
			"gate_suppressed", gateSuppressed, "update_restart_suppressed", markerSuppressed,
			"phases", "shutting_down/healthy")
		if m := a.metrics.Load(); m != nil {
			if gateSuppressed > 0 {
				m.inc("desktop_exit_phase", "noise_gate_suppressed")
			}
			if markerSuppressed > 0 {
				m.inc("desktop_exit_phase", "update_restart_suppressed")
			}
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
	message := fmt.Sprintf(`[desktop.abnormal_exit.v2]

Reasonix found a per-process lifecycle record whose desktop process was no longer running.

--- lifecycle context ---
phase: %s
pid: %d%s
previous version: %s
previous channel: %s
started at: %s
last phase update: %s
discovered at: %s`,
		phase,
		previous.PID,
		deathLine,
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
	report.Message = sanitizeCrashText(message, maxCrashDetailBytes)
	return report
}
