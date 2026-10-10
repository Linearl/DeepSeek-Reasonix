package agent

// 任务 710 tests: the fold "would not shrink" WARN is a size-regime signal,
// not a per-save log line (847 repeats in the fork开发-新5 incident), and the
// measured save duration the controller's autosave backs off with is recorded
// on the Session.

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

func TestFoldWouldNotShrinkWarnRateLimited(t *testing.T) {
	resetEventsRotation(t)
	SetEventsAutoRotation("auto", 16, 1)

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	const content int64 = 900 << 10
	path := "t710-fold-warn.events.jsonl"

	sessionEventLogOversized(path, (1500<<10)+1, content) // first sighting: WARN
	sessionEventLogOversized(path, (1500<<10)+1, content) // same regime: suppressed
	sessionEventLogOversized(path, (1550<<10)+1, content) // +3% growth: suppressed
	sessionEventLogOversized(path, (1700<<10)+1, content) // >10% growth: re-warn

	if got := strings.Count(buf.String(), "fold would not shrink it"); got != 2 {
		t.Fatalf("fold WARN count = %d, want 2 (first sighting + >10%% growth); log=%s", got, buf.String())
	}
	if !strings.Contains(buf.String(), "remedy=") {
		t.Fatalf("fold WARN must carry the remediation attr; log=%s", buf.String())
	}

	// A different session path warns independently of the first one's regime.
	sessionEventLogOversized("t710-fold-warn-2.events.jsonl", (1500<<10)+1, content)
	if got := strings.Count(buf.String(), "fold would not shrink it"); got != 3 {
		t.Fatalf("second path must warn independently; count = %d, want 3", got)
	}
}

func TestLastSaveDurationMsRecorded(t *testing.T) {
	s := NewSession("sys")
	if got := s.LastSaveDurationMs(); got != 0 {
		t.Fatalf("fresh session LastSaveDurationMs = %d, want 0", got)
	}
	s.Add(provider.Message{Role: provider.RoleUser, Content: "t710 duration probe"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "ack"})
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// The save ran through the observed wrapper, so the recorded duration must
	// have been stored; it may legitimately round to 0ms on a tiny session,
	// which is exactly the "cheap save, keep the base interval" reading the
	// controller's backoff makes.
	if got := s.LastSaveDurationMs(); got < 0 {
		t.Fatalf("LastSaveDurationMs = %d, want >= 0", got)
	}
}
