package agent

import (
	"strings"
	"testing"
)

func TestIsHighSpeedConfigured(t *testing.T) {
	cases := []struct {
		ref    string
		marked []string
		want   bool
	}{
		{"mimo-v2.5-pro-ultraspeed", []string{"mimo-v2.5-pro-ultraspeed"}, true},
		{"provider/mimo-v2.5-pro-ultraspeed", []string{"mimo-v2.5-pro-ultraspeed"}, true},
		{"provider|mimo-v2.5-pro-ultraspeed", []string{"mimo-v2.5-pro-ultraspeed"}, true},
		{"provider/mimo", []string{"mimo"}, true},
		{"deepseek-v4", []string{"deepseek-v4"}, true},
		// Explicit marking wins even for ids that mention "flash" — never inferred.
		{"deepseek-v4-flash", []string{}, false},
		{"mimo-v2.5-pro-ultraspeed", []string{}, false},
		{"mimo", []string{"something-else"}, false},
		{"", []string{"mimo"}, false},
		{"mimo", nil, false},
	}
	for _, c := range cases {
		if got := isHighSpeedConfigured(c.ref, c.marked); got != c.want {
			t.Errorf("isHighSpeedConfigured(%q, %v) = %v, want %v", c.ref, c.marked, got, c.want)
		}
	}
}

func TestWithExecSpeedMode(t *testing.T) {
	// Unmarked model: no block, content unchanged.
	if got := WithExecSpeedMode("hello", "mimo-v2.5-pro", []string{}); got != "hello" {
		t.Fatalf("unmarked model should not inject; got %q", got)
	}
	// Marked model: leading transient block injected.
	got := WithExecSpeedMode("hello", "mimo-v2.5-pro-ultraspeed", []string{"mimo-v2.5-pro-ultraspeed"})
	if !strings.HasPrefix(got, "<exec-speed-mode>high</exec-speed-mode>") {
		t.Fatalf("marked model should inject exec-speed-mode block; got %q", got)
	}
	if !strings.Contains(got, "run_in_background") {
		t.Fatalf("block should carry the run_in_background strategy; got %q", got)
	}
	if !strings.HasSuffix(got, "hello") {
		t.Fatalf("block should prefix, not replace, content; got %q", got)
	}
	// Already-injected: must not double-inject.
	already := WithExecSpeedMode("hello", "mimo", []string{"mimo"})
	doubled := WithExecSpeedMode(already, "mimo", []string{"mimo"})
	if strings.Count(doubled, "<exec-speed-mode>") != 1 {
		t.Fatalf("must not double-inject; got %q", doubled)
	}
}

func TestExecSpeedModeBlockEmptyWhenUnmarked(t *testing.T) {
	if ExecSpeedModeBlock("deepseek-v4", []string{}) != "" {
		t.Fatal("ExecSpeedModeBlock should be empty for an unmarked model")
	}
	if ExecSpeedModeBlock("", []string{"deepseek-v4"}) != "" {
		t.Fatal("ExecSpeedModeBlock should be empty for an empty model ref")
	}
}
