package config

import (
	"math"
	"strings"
	"testing"
)

// TestNormalizeEventsAutoRotation covers the raw-value mapping: empty is the
// default, the three modes pass through, everything else is refused so a typo
// can never flip the gate silently.
func TestNormalizeEventsAutoRotation(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"", EventsAutoRotationManual, true},
		{" ", EventsAutoRotationManual, true},
		{"off", EventsAutoRotationOff, true},
		{"manual", EventsAutoRotationManual, true},
		{"auto", EventsAutoRotationAuto, true},
		{"Off ", EventsAutoRotationOff, true},
		{"yes", "", false},
		{"manual2", "", false},
	}
	for _, tc := range cases {
		got, ok := NormalizeEventsAutoRotation(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("NormalizeEventsAutoRotation(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// TestEventsRotationHelpersClamp pins the read-side fallbacks: an invalid
// stored value reads back as today's behavior, never as a guessed mode.
func TestEventsRotationHelpersClamp(t *testing.T) {
	if got := EventsAutoRotationMode(nil); got != EventsAutoRotationManual {
		t.Fatalf("nil config mode = %q, want manual", got)
	}
	bad := &Config{EventsAutoRotation: "bogus", EventsRotationFactor: 99, EventsRotationCapMB: -1}
	if got := EventsAutoRotationMode(bad); got != EventsAutoRotationManual {
		t.Fatalf("invalid stored mode reads as %q, want manual", got)
	}
	if got := EventsRotationFactor(bad); got != EventsRotationFactorDefault {
		t.Fatalf("out-of-range factor reads as %v, want %v", got, EventsRotationFactorDefault)
	}
	nan := &Config{EventsRotationFactor: math.NaN()}
	if got := EventsRotationFactor(nan); got != EventsRotationFactorDefault {
		t.Fatalf("NaN factor reads as %v, want %v", got, EventsRotationFactorDefault)
	}
	if got := EventsRotationCapMB(bad); got != 0 {
		t.Fatalf("negative cap reads as %d, want 0", got)
	}
}

// TestSetEventsRotationValidation: the setters refuse bad input rather than
// storing it for a later surprise.
func TestSetEventsRotationValidation(t *testing.T) {
	c := &Config{}
	if err := c.SetEventsAutoRotation("nope"); err == nil {
		t.Fatal("unknown mode must be refused")
	}
	if err := c.SetEventsAutoRotation("auto"); err != nil {
		t.Fatalf("auto mode: %v", err)
	}
	if c.EventsAutoRotation != EventsAutoRotationAuto {
		t.Fatalf("stored mode = %q, want auto", c.EventsAutoRotation)
	}
	if err := c.SetEventsRotation(1.5, 0); err == nil {
		t.Fatal("factor below 2 must be refused")
	}
	if err := c.SetEventsRotation(17, 0); err == nil {
		t.Fatal("factor above 16 must be refused")
	}
	if err := c.SetEventsRotation(math.NaN(), 0); err == nil {
		t.Fatal("NaN factor must be refused")
	}
	if err := c.SetEventsRotation(8, -1); err == nil {
		t.Fatal("negative cap must be refused")
	}
	if err := c.SetEventsRotation(8, 512); err != nil {
		t.Fatalf("valid thresholds: %v", err)
	}
	if c.EventsRotationFactor != 8 || c.EventsRotationCapMB != 512 {
		t.Fatalf("stored thresholds = (%v, %d), want (8, 512)", c.EventsRotationFactor, c.EventsRotationCapMB)
	}
}

// TestRenderEventsRotationLines pins the render table (81/123 lost-line
// lesson): the main renderer carries all three lines unconditionally — even at
// defaults — so a hand-edited value survives the next settings save, while the
// project delta stays sparse.
func TestRenderEventsRotationLines(t *testing.T) {
	out := RenderTOML(&Config{}) // defaults
	for _, want := range []string{
		"events_auto_rotation = \"manual\"",
		"events_rotation_factor = 4.0",
		"events_rotation_cap_mb = 0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("RenderTOML(defaults) missing %q", want)
		}
	}
	// Defaults add nothing to the project delta (sparse semantics).
	delta := RenderTOMLProjectDelta(&Config{})
	if strings.Contains(delta, "events_auto_rotation") || strings.Contains(delta, "events_rotation_factor") || strings.Contains(delta, "events_rotation_cap_mb") {
		t.Errorf("default project delta must stay sparse, got:\n%s", delta)
	}
	// A non-default override shows up.
	custom := &Config{EventsAutoRotation: EventsAutoRotationAuto, EventsRotationFactor: 8, EventsRotationCapMB: 256}
	delta = RenderTOMLProjectDelta(custom)
	for _, want := range []string{`events_auto_rotation = "auto"`, "events_rotation_factor = 8.0", "events_rotation_cap_mb = 256"} {
		if !strings.Contains(delta, want) {
			t.Errorf("project delta missing %q, got:\n%s", want, delta)
		}
	}
}
