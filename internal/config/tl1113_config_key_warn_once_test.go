package config

import (
	"strings"
	"testing"
)

// tl 1113: the unknown-key WARN must not flood the log. A long-lived desktop
// process re-loads the same config file on every settings read / heartbeat
// tick / session rebuild; before this fix each load re-warned every
// unrecognized key (209,197 lines, 52.5% of all log lines in 25 rotated
// desktop logs). The unknown-key branch now warns once per key per process;
// deprecated keys keep their per-load WARN because each carries a migration
// hint.

func TestTL1113UnknownKeyWarnedOncePerProcess(t *testing.T) {
	resetUnknownKeyWarnDedupeForTest()
	body := "[agent]\nfuture_upstream_key = 1\n"
	path := task321WriteConfig(t, body)

	logs := task321CaptureWarnings(t)
	task321LoadReadOnly(t, path)
	if out := logs.String(); strings.Count(out, "unknown key") != 1 {
		t.Fatalf("first load must warn exactly once for the unknown key, got %d lines:\n%s",
			strings.Count(out, "unknown key"), out)
	}

	// A second load of the same file (same key) must be silent.
	task321LoadReadOnly(t, path)
	if out := logs.String(); strings.Count(out, "unknown key") != 1 {
		t.Fatalf("repeat load must not re-warn the same unknown key, got %d lines:\n%s",
			strings.Count(out, "unknown key"), out)
	}
}

func TestTL1113NewUnknownKeyStillWarns(t *testing.T) {
	resetUnknownKeyWarnDedupeForTest()
	first := task321WriteConfig(t, "[agent]\nfuture_upstream_key = 1\n")
	second := task321WriteConfig(t, "[agent]\nfuture_upstream_key = 1\nanother_new_key = 2\n")

	logs := task321CaptureWarnings(t)
	task321LoadReadOnly(t, first)
	task321LoadReadOnly(t, second)

	out := logs.String()
	if !strings.Contains(out, "future_upstream_key") {
		t.Fatalf("first-seen key must be warned, got:\n%s", out)
	}
	if !strings.Contains(out, "another_new_key") {
		t.Fatalf("a genuinely new unknown key must still be warned after dedupe, got:\n%s", out)
	}
}

func TestTL1113DeprecatedKeyStillWarnsEveryLoad(t *testing.T) {
	resetUnknownKeyWarnDedupeForTest()
	body := "[agent]\nauto_plan = \"off\"\n"
	path := task321WriteConfig(t, body)

	logs := task321CaptureWarnings(t)
	task321LoadReadOnly(t, path)
	task321LoadReadOnly(t, path)

	out := logs.String()
	if got := strings.Count(out, "deprecated key"); got != 2 {
		t.Fatalf("deprecated keys keep the per-load WARN (migration hint stays visible), got %d lines:\n%s", got, out)
	}
}
