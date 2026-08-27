package agent

import (
	"strings"
	"testing"
)

func TestIsHighSpeedModel(t *testing.T) {
	cases := []struct {
		ref  string
		want bool
	}{
		{"mimo-v2.5-pro-ultraspeed", true},
		{"mimo-v2.5-pro-ultra-speed", true},
		{"MIMO-V2.5-PRO-ULTRASPEED", true},
		{"deepseek-v4-flash", true},
		{"provider:mimo-v2.5-pro-ultraspeed@route", true},
		{"mimo-v2.5-pro", false},
		{"deepseek-v4", false},
		{"", false},
		{"   ", false},
	}
	for _, c := range cases {
		if got := isHighSpeedModel(c.ref); got != c.want {
			t.Errorf("isHighSpeedModel(%q) = %v, want %v", c.ref, got, c.want)
		}
	}
}

func TestWithExecSpeedMode(t *testing.T) {
	// Normal model: no block, content unchanged.
	if got := WithExecSpeedMode("hello", "mimo-v2.5-pro"); got != "hello" {
		t.Fatalf("normal model should not inject; got %q", got)
	}
	// High-speed model: leading transient block injected.
	got := WithExecSpeedMode("hello", "mimo-v2.5-pro-ultraspeed")
	if !strings.HasPrefix(got, "<exec-speed-mode>high</exec-speed-mode>") {
		t.Fatalf("high-speed model should inject exec-speed-mode block; got %q", got)
	}
	if !strings.Contains(got, "run_in_background") {
		t.Fatalf("block should carry the run_in_background strategy; got %q", got)
	}
	if !strings.HasSuffix(got, "hello") {
		t.Fatalf("block should prefix, not replace, content; got %q", got)
	}
	// Already-injected: must not double-inject.
	already := WithExecSpeedMode("hello", "mimo-v2.5-pro-ultraspeed")
	doubled := WithExecSpeedMode(already, "mimo-v2.5-pro-ultraspeed")
	if strings.Count(doubled, "<exec-speed-mode>") != 1 {
		t.Fatalf("must not double-inject; got %q", doubled)
	}
}

func TestExecSpeedModeBlockEmptyForNormal(t *testing.T) {
	if ExecSpeedModeBlock("deepseek-v4") != "" {
		t.Fatalf("ExecSpeedModeBlock should be empty for normal model")
	}
	if ExecSpeedModeBlock("") != "" {
		t.Fatalf("ExecSpeedModeBlock should be empty for empty ref")
	}
}
