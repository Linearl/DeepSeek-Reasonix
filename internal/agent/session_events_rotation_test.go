package agent

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// legacyOversized is the pre-task-333 judgment, kept verbatim as the
// comparison oracle: the default (manual) mode must stay bit-for-bit identical
// so the new gate adds zero behavior to existing installs.
func legacyOversized(logSize, contentBytes int64) bool {
	limit := sessionEventLogCompactFloor
	if scaled := contentBytes * sessionEventLogCompactFactor; scaled > limit {
		limit = scaled
	}
	return logSize > limit
}

// gateJudges calls the mode-aware gate with a stable attributed path (the
// off-mode WARN carries it, review finding 2026-09-28).
func gateJudges(log, content int64) bool {
	return sessionEventLogOversized("test.events.jsonl", log, content)
}

// resetEventsRotation restores the zero-value default for the next test.
func resetEventsRotation(t *testing.T) {
	t.Helper()
	eventsRotationStore.Store(nil)
	t.Cleanup(func() { eventsRotationStore.Store(nil) })
}

// TestEventsRotationDefaultMatchesLegacy pins acceptance c3: before any push
// (and after an explicit manual push) the gate judges exactly like the old
// implementation across the interesting range.
func TestEventsRotationDefaultMatchesLegacy(t *testing.T) {
	resetEventsRotation(t)
	cases := []struct{ log, content int64 }{
		{0, 0},
		{sessionEventLogCompactFloor, 1024},
		{sessionEventLogCompactFloor + 1, 1024},
		{4 << 20, 1 << 20},       // exactly 4x: not over
		{(4 << 20) + 1, 1 << 20}, // 4x + 1 byte: over
		{sessionEventLogCompactFactor * 10_000, 10_000},
		{1 << 30, 100 << 20},
	}
	for _, tc := range cases {
		if got, want := gateJudges(tc.log, tc.content), legacyOversized(tc.log, tc.content); got != want {
			t.Fatalf("default gate(%d, %d) = %v, want legacy %v", tc.log, tc.content, got, want)
		}
	}
	// Explicit manual push behaves the same (config default = manual).
	SetEventsAutoRotation("manual", 4, 0)
	for _, tc := range cases {
		if got, want := gateJudges(tc.log, tc.content), legacyOversized(tc.log, tc.content); got != want {
			t.Fatalf("manual gate(%d, %d) = %v, want legacy %v", tc.log, tc.content, got, want)
		}
	}
}

// TestEventsRotationOffSkipsGateAndWarns pins acceptance c1 (off): an
// oversized log is never rotated, the skip is greppable, and the WARN names
// the session path so the message is attributable per review finding.
func TestEventsRotationOffSkipsGateAndWarns(t *testing.T) {
	resetEventsRotation(t)
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	SetEventsAutoRotation("off", 4, 0)
	if gateJudges((4<<20)+1, 1<<20) {
		t.Fatal("off mode must skip the oversized gate")
	}
	warn := buf.String()
	if !strings.Contains(warn, "left in place (events auto rotation off)") {
		t.Fatalf("off-mode skip must leave a greppable WARN, log=%q", warn)
	}
	if !strings.Contains(warn, "path=test.events.jsonl") {
		t.Fatalf("off-mode WARN must attribute the session path, log=%q", warn)
	}
	// A log under the limit must not warn (the WARN marks real overflow only).
	buf.Reset()
	if gateJudges(1<<10, 1<<20) {
		t.Fatal("under-limit log must not be oversized")
	}
	if buf.Len() != 0 {
		t.Fatalf("no WARN expected under the limit, got %q", buf.String())
	}
}

// TestEventsRotationAutoThresholds pins acceptance c1/c2 (auto): factor and
// cap OR together, and a threshold push takes effect on the next judgment —
// no restart, same call sequence the save path uses.
func TestEventsRotationAutoThresholds(t *testing.T) {
	resetEventsRotation(t)
	const content int64 = 1 << 20 // 1 MiB live content
	over2x, over4x := int64(2<<20)+1, int64(4<<20)

	// Factor 2: over-2x rotates, the same log did not rotate under manual 4x.
	SetEventsAutoRotation("manual", 4, 0)
	if gateJudges(over2x, content) {
		t.Fatal("manual 4x must not rotate at 2x+1")
	}
	SetEventsAutoRotation("auto", 2, 0) // immediate effect: next judgment flips
	if !gateJudges(over2x, content) {
		t.Fatal("auto factor 2 must rotate at 2x+1")
	}
	if gateJudges(2<<20, content) {
		t.Fatal("auto factor 2 must not rotate exactly at 2x")
	}

	// Cap alone (factor too high to fire): a 200 KiB-content session gives the
	// 16x factor 3.1 MiB of headroom, so only the 1 MiB cap can rotate the
	// 1.5 MiB log — thresholds OR together.
	SetEventsAutoRotation("auto", 16, 1)
	if !gateJudges(3<<19, 200<<10) { // 1.5 MiB log
		t.Fatal("auto cap 1 MiB must rotate a 1.5 MiB log (thresholds OR)")
	}
	if gateJudges(900<<10, 200<<10) { // 900 KiB: under the cap
		t.Fatal("auto cap 1 MiB must not rotate below the cap")
	}

	// Pushing thresholds back re-judges immediately.
	SetEventsAutoRotation("manual", 4, 0)
	if gateJudges(over4x, content) {
		t.Fatal("exactly 4x must not rotate under manual")
	}
	if gateJudges(over4x+1, content) != legacyOversized(over4x+1, content) {
		t.Fatal("return to manual must restore the legacy judgment")
	}
}

// TestEventsRotationSetterClamps pins the backstop semantics: bad inputs land
// on the safe default instead of being rejected silently mid-flight. The cap
// clamp keeps capMB<<20 inside int64 — an unbounded cap would wrap the shift
// negative and mark every log over (review finding, 2026-09-28).
func TestEventsRotationSetterClamps(t *testing.T) {
	resetEventsRotation(t)
	SetEventsAutoRotation("weird", 99, -5)
	cfg := currentEventsRotation()
	if cfg.mode != "manual" {
		t.Fatalf("unknown mode clamped to %q, want manual", cfg.mode)
	}
	if cfg.factor != 16 {
		t.Fatalf("factor clamped to %v, want 16", cfg.factor)
	}
	if cfg.capMB != 0 {
		t.Fatalf("negative cap clamped to %d, want 0", cfg.capMB)
	}
	SetEventsAutoRotation("auto", 1, 0)
	if cfg := currentEventsRotation(); cfg.factor != 2 {
		t.Fatalf("factor clamped up to %v, want 2", cfg.factor)
	}
	SetEventsAutoRotation("auto", 4, 1<<40) // way past the bound
	if cfg := currentEventsRotation(); cfg.capMB != eventsRotationCapMBMax {
		t.Fatalf("oversized cap clamped to %d, want %d", cfg.capMB, eventsRotationCapMBMax)
	}
}

// TestEventsLogAboveThresholdClampsRawCap: the exported judgment is reachable
// with caller-supplied values, so its own clamp is what keeps an absurd cap
// from overflowing the shift and marking a tiny log over the threshold.
func TestEventsLogAboveThresholdClampsRawCap(t *testing.T) {
	if EventsLogAboveThreshold(1<<10, 1<<20, 4, 1<<40) {
		t.Fatal("raw cap beyond the bound must clamp, not overflow into always-true")
	}
	if !EventsLogAboveThreshold((2<<20)+1, 1<<20, 2, 0) {
		t.Fatal("factor threshold must still fire through the exported judgment")
	}
}
