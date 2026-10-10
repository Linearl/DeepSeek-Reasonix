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
	suppressed := 0
	for _, lifecycle := range a.lifecycle.previousRuns {
		if a.lifecycle.noiseGate && lifecycleNoiseBenign(lifecycle.Phase) {
			suppressed++
			continue
		}
		_ = writePendingReport(desktopLifecycleReport(lifecycle), true)
		if m := a.metrics.Load(); m != nil {
			m.inc("desktop_exit", "abnormal")
			m.inc("desktop_exit_phase", metricBucket(lifecycle.Phase))
			m.persist()
		}
	}
	if suppressed > 0 {
		slog.Info("desktop: lifecycle noise gate suppressed clean-shutdown residue",
			"suppressed", suppressed, "phases", "shutting_down/healthy")
		if m := a.metrics.Load(); m != nil {
			m.inc("desktop_exit_phase", "noise_gate_suppressed")
			m.persist()
		}
	}
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
