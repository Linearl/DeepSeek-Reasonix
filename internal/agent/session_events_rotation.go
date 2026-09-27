package agent

import (
	"math"
	"sync/atomic"
)

// eventsRotationConfig is the agent-side view of the task-333 rotation gate.
// The agent package must not import internal/config (layering), so the
// desktop host pushes the normalized settings here on boot and on every
// settings change — atomic.Pointer keeps the save path read cheap and makes a
// threshold change take effect on the very next save, with no restart.
type eventsRotationConfig struct {
	mode   string // "off" | "manual" (default) | "auto"
	factor float64
	capMB  int64
}

var eventsRotationStore atomic.Pointer[eventsRotationConfig]

// Events rotation modes (task 333). "manual" mirrors today's behavior: the
// built-in factor gate runs and slimming happens through the explicit UI/CLI
// entries.
const (
	eventsRotationOff    = "off"
	eventsRotationManual = "manual"
	eventsRotationAuto   = "auto"
)

// Auto-mode threshold bounds, kept in lockstep with internal/config
// (EventsRotationFactorMin/Max/Default, EventsRotationCapMBMax) — the desktop
// setter normalizes first, these bounds are the agent-side backstop. The cap
// bound keeps capMB<<20 inside int64 (1 TiB shifts to 1<<50): an unbounded
// cap would wrap the shift negative and rotate every log every save (review
// finding, 2026-09-28).
const (
	eventsRotationFactorMin     = 2.0
	eventsRotationFactorMax     = 16.0
	eventsRotationFactorDefault = 4.0
	eventsRotationCapMBMax      = int64(1) << 30
)

// SetEventsAutoRotation stores the rotation gate settings (task 333). Invalid
// values are clamped rather than refused: this is a backstop behind the
// config-layer setters, and a rejected update would silently leave the gate on
// the previous thresholds.
func SetEventsAutoRotation(mode string, factor float64, capMB int64) {
	normalized := eventsRotationManual
	switch mode {
	case eventsRotationOff, eventsRotationManual, eventsRotationAuto:
		normalized = mode
	}
	if math.IsNaN(factor) || math.IsInf(factor, 0) {
		factor = eventsRotationFactorDefault
	}
	if factor < eventsRotationFactorMin {
		factor = eventsRotationFactorMin
	}
	if factor > eventsRotationFactorMax {
		factor = eventsRotationFactorMax
	}
	if capMB < 0 {
		capMB = 0
	}
	if capMB > eventsRotationCapMBMax {
		capMB = eventsRotationCapMBMax
	}
	eventsRotationStore.Store(&eventsRotationConfig{mode: normalized, factor: factor, capMB: capMB})
}

// currentEventsRotation returns the active gate settings; before the desktop
// host's first push the zero value is exactly the default configuration
// (manual, 4x, no cap), so plain CLI/test runs behave as before task 333.
func currentEventsRotation() eventsRotationConfig {
	if cfg := eventsRotationStore.Load(); cfg != nil {
		return *cfg
	}
	return eventsRotationConfig{mode: eventsRotationManual, factor: eventsRotationFactorDefault}
}

// eventsLogAboveFactor is the oversized judgment factored out so all three
// modes share one expression: the log must exceed max(floor, content×factor).
func eventsLogAboveFactor(logSize, contentBytes int64, factor float64) bool {
	limit := sessionEventLogCompactFloor
	if scaled := int64(math.Round(float64(contentBytes) * factor)); scaled > limit {
		limit = scaled
	}
	return logSize > limit
}

// EventsLogAboveThreshold is the judgment with caller-supplied thresholds: the
// task-333 storage panel's inventory marks sessions over-limit through this
// one function so the UI's statistic card, the gate and the CLI stay on the
// same source of truth (the factor threshold and the optional MiB cap OR
// together, matching the auto-mode gate). Both inputs are defensively clamped:
// a raw caller-supplied cap beyond the bound would overflow capMB<<20 into a
// negative threshold and mark every log over (review finding, 2026-09-28).
func EventsLogAboveThreshold(logSize, contentBytes int64, factor float64, capMB int64) bool {
	if math.IsNaN(factor) || math.IsInf(factor, 0) {
		factor = eventsRotationFactorDefault
	}
	if capMB > eventsRotationCapMBMax {
		capMB = eventsRotationCapMBMax
	}
	return eventsLogAboveFactor(logSize, contentBytes, factor) ||
		(capMB > 0 && logSize > capMB<<20)
}

// EventsAutoRotationSnapshot reports the gate settings currently live in the
// save path. Host-side tests use it to verify a settings push landed; a nil
// store reads as the default configuration (manual, 4x, no cap).
func EventsAutoRotationSnapshot() (mode string, factor float64, capMB int64) {
	cfg := currentEventsRotation()
	return cfg.mode, cfg.factor, cfg.capMB
}
