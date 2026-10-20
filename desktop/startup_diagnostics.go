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
	//
	// Task 873: the marker check must precede the phase gate. A forced update
	// restart (grace window expired → forced quit) can leave the record in a
	// mid-shutdown phase (closing_*, waiting_runtime_locks): that phase is not
	// "benign" in the task-377 sense, but the exit was still PLANNED (the
	// marker writer is this exact PID handing off to an update relaunch).
	//
	// Task 1018 (20261020 装机实测): the record family is read-and-clear at
	// the source, for EVERY disposition — marker-vouched, clean-shutdown, and
	// abnormal phase alike. Nothing here writes a crash report anymore. The
	// 10-19/10-20 incidents: a wedged-phase record (shutdown watchdog forced
	// quit, desktop.log zero panic) sat in the crash queue and re-surfaced the
	// 「上次运行遗留的崩溃报告」 banner on every launch while uploads were
	// failing. A real panic never depended on this path — the crash-fatal sink
	// dump (go.fatal) and recoverToPending (go panic) own that surface — so
	// converting the scan to consume-and-clear cannot mask a real crash. The
	// forensics stay observable (1545: never a silent drop): one line per
	// record naming pid/phase/exit + the summary counters + metrics buckets;
	// the launcher's death record (task 736) remains the death-moment witness.
	//
	// Task 1002 并集（847 家族新形态）：先做一次归属时效判定——非直接前任留下的
	// 记录（中间某一轮 boot 的 consume pass 已晚于它的最后一次 phase 写入）不
	// 算本次的「上次运行」证据，单独退役计数（staleRetired，尾部分别命名日志 +
	// metrics 桶）。归属判定（1002）与处置动作（1018：读后即清、不写报告）正
	// 交，见下方循环内的前置分支。
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
		//
		// 并集说明（1018 × 1002）：本前置分支只做「归属」判定；退役后的命名日志与
		// metrics 桶在循环外的 staleRetired 汇总块统一出具。处置动作（不写崩溃
		// 报告、读后即清）仍由下方的 1018 主分支负责——两边不互相覆盖。
		if lifecycleRecordPredatesPass(lifecycle.UpdatedAt, a.lifecycle.priorPassAt) {
			staleRetired++
			continue
		}
		slog.Info("desktop: lifecycle residue consumed read-and-clear, no crash report (task 1018)",
			"pid", lifecycle.PID, "phase", lifecycle.Phase, "disposition", disposition,
			"exit_code", lifecycle.ExitCode, "death_at", lifecycle.DeathAt)
		if m := a.metrics.Load(); m != nil {
			m.inc("desktop_exit_phase", metricBucket(lifecycle.Phase))
			switch disposition {
			case "update_restart":
				m.inc("desktop_exit_phase", "update_restart_suppressed")
			case "clean_shutdown":
				if a.lifecycle.noiseGate {
					m.inc("desktop_exit_phase", "noise_gate_suppressed")
				}
			default:
				m.inc("desktop_exit_phase", "abnormal_phase_cleared")
				m.inc("desktop_exit", "residue_cleared")
			}
		}
	}
	if total := updateRestart + cleanShutdown + abnormalPhase; total > 0 {
		slog.Info("desktop: lifecycle residue cleared without a crash report",
			"total", total, "update_restart_suppressed", updateRestart,
			"clean_shutdown", cleanShutdown, "abnormal_phase_cleared", abnormalPhase)
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

// desktopLifecycleReport builds the desktop.abnormal_exit.v2 report shape.
// Since task 1018 the startup scan no longer writes these (read-and-clear at
// the source); the builder stays as the canonical schema of the pre-1018
// backlog files that sweepJudgedLifecyclePendingReports must recognize, and
// as the field-mapping documentation for the task-736 attribution contract.
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
