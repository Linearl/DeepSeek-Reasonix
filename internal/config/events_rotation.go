package config

import (
	"fmt"
	"math"
	"strings"
)

// Normalized spellings for the automatic event-log rotation gate (task 333).
const (
	EventsAutoRotationOff    = "off"
	EventsAutoRotationManual = "manual" // default: today's built-in behavior
	EventsAutoRotationAuto   = "auto"
)

// EventsAutoRotationModes is the user-facing list rendered in config comments
// and error messages.
var EventsAutoRotationModes = []string{EventsAutoRotationOff, EventsAutoRotationManual, EventsAutoRotationAuto}

// Rotation threshold bounds for auto mode (task 333): the factor stays near
// the built-in 4x default, the cap is an optional MiB ceiling (0 = disabled).
const (
	EventsRotationFactorDefault = 4.0
	EventsRotationFactorMin     = 2.0
	EventsRotationFactorMax     = 16.0
)

// NormalizeEventsAutoRotation maps a raw config value onto off|manual|auto.
// Unknown values are refused (ok=false): the mode decides whether the
// oversized gate runs, so a typo must not silently disable or enable rotation.
func NormalizeEventsAutoRotation(mode string) (string, bool) {
	switch normalized := strings.ToLower(strings.TrimSpace(mode)); normalized {
	case "":
		return EventsAutoRotationManual, true
	case EventsAutoRotationOff, EventsAutoRotationManual, EventsAutoRotationAuto:
		return normalized, true
	default:
		return "", false
	}
}

// EventsAutoRotationMode returns the effective gate mode. An invalid stored
// value falls back to "manual" (today's behavior) rather than guessing —
// reading a setting must never flip the gate on its own.
func EventsAutoRotationMode(cfg *Config) string {
	if cfg == nil {
		return EventsAutoRotationManual
	}
	if mode, ok := NormalizeEventsAutoRotation(cfg.EventsAutoRotation); ok {
		return mode
	}
	return EventsAutoRotationManual
}

// EventsRotationFactor returns the auto-mode multiple threshold, clamping
// out-of-range or non-finite stored values onto the 4x default.
func EventsRotationFactor(cfg *Config) float64 {
	if cfg == nil {
		return EventsRotationFactorDefault
	}
	factor := cfg.EventsRotationFactor
	if math.IsNaN(factor) || math.IsInf(factor, 0) ||
		factor < EventsRotationFactorMin || factor > EventsRotationFactorMax {
		return EventsRotationFactorDefault
	}
	return factor
}

// EventsRotationCapMB returns the auto-mode absolute ceiling in MiB; a
// negative stored value disables the cap (same as 0).
func EventsRotationCapMB(cfg *Config) int64 {
	if cfg == nil || cfg.EventsRotationCapMB < 0 {
		return 0
	}
	return cfg.EventsRotationCapMB
}

// SetEventsAutoRotation selects the automatic event-log rotation gate (task
// 333): off | manual | auto. Unknown modes are refused rather than written,
// matching SetSessionStorage — the value drives whether the oversized gate
// fires, so a silent fallback would hide a typo.
func (c *Config) SetEventsAutoRotation(mode string) error {
	normalized, ok := NormalizeEventsAutoRotation(mode)
	if !ok {
		return fmt.Errorf("events auto rotation: %q is not a mode (use %s)",
			mode, strings.Join(EventsAutoRotationModes, ", "))
	}
	c.EventsAutoRotation = normalized
	return nil
}

// SetEventsRotation stores the auto-mode thresholds: factor within 2-16, cap
// in MiB with 0 disabling it. Both are validated so a bad value fails in the
// setter instead of disabling rotation at save time.
func (c *Config) SetEventsRotation(factor float64, capMB int64) error {
	if math.IsNaN(factor) || math.IsInf(factor, 0) ||
		factor < EventsRotationFactorMin || factor > EventsRotationFactorMax {
		return fmt.Errorf("events rotation factor %v: must be between %.0f and %.0f",
			factor, EventsRotationFactorMin, EventsRotationFactorMax)
	}
	if capMB < 0 {
		return fmt.Errorf("events rotation cap %d MiB: must be >= 0 (0 disables the cap)", capMB)
	}
	c.EventsRotationFactor = factor
	c.EventsRotationCapMB = capMB
	return nil
}
