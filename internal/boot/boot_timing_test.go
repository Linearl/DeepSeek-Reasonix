package boot

import (
	"strings"
	"testing"
	"time"
)

func TestBootTimingSummary(t *testing.T) {
	timing := newBootTiming()
	if timing == nil {
		t.Fatal("newBootTiming returned nil")
	}
	if timing.summary() == "" {
		t.Fatal("empty timing still renders a summary")
	}
	if !strings.Contains(timing.summary(), "total=") {
		t.Fatalf("summary missing total: %q", timing.summary())
	}

	timing.mark("config")
	timing.mark("extensions")
	// Simulate a little work so stages are non-zero when the clock is coarse.
	time.Sleep(2 * time.Millisecond)
	timing.mark("provider")
	summary := timing.summary()
	for _, want := range []string{"total=", "config=", "extensions=", "provider="} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary %q missing %q", summary, want)
		}
	}

	// mark on a nil receiver must not panic (defensive for optional timing).
	var nilTiming *bootTiming
	nilTiming.mark("noop")
	if nilTiming.summary() != "" {
		t.Fatalf("nil timing summary = %q, want empty", nilTiming.summary())
	}
}
