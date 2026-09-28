package agent

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// captureDagDebug captures Debug-level slog for one test (these tests are not
// parallel): the dag-state line only guarantees its Debug form on small test
// logs (the Info form is gated on the 250ms replay floor).
func captureDagDebug(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

// Task 357 acceptance 3: two consecutive saves of one session must produce
// extended=false then extended=true — the reuse guard (same path, same
// generation, size at or beyond lastGoodEnd) is what the on-device readings
// kept missing behind reason=nil_cache. This pins the judge itself: with no
// cache eviction in between (one session, capacity >= 1), the second save
// MUST extend instead of replaying.
func TestDagStateForSaveExtendsOnSecondSave(t *testing.T) {
	path := dagTestSession(t)
	dagLinearLog(t, path)
	buf := captureDagDebug(t)

	s := &Session{}
	if _, err := s.dagStateForSave(context.Background(), path, time.Now().UTC()); err != nil {
		t.Fatalf("first save: %v", err)
	}
	buf.Reset()
	if _, err := s.dagStateForSave(context.Background(), path, time.Now().UTC()); err != nil {
		t.Fatalf("second save: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "dag state for save") {
		t.Fatalf("second save emitted no dag-state line; got: %s", out)
	}
	if !strings.Contains(out, "extended=true") {
		t.Fatalf("second consecutive save must extend, not replay (judge short-circuited); got: %s", out)
	}
	if strings.Contains(out, "reason=nil_cache") {
		t.Fatalf("second save of a fresh session must hit its own head state; got: %s", out)
	}
	// The attribution segments ride on the same line (task 357).
	for _, seg := range []string{"header_ms", "decide_ms", "replay_ms"} {
		if !strings.Contains(out, seg) {
			t.Fatalf("dag-state line missing %s segment; got: %s", seg, out)
		}
	}
}

// The millisecond segments the attribution rides on must ship on the Info
// line (the on-device grep surface): header/decide/replay next to the
// existing extended/reason/ms.
func TestSavePhasesAndDagSegmentsEmit(t *testing.T) {
	path := dagTestSession(t)
	dagLinearLog(t, path)
	buf := captureDagDebug(t)

	s := &Session{}
	// Force the segment line: the Info form needs >=250ms, so drive the
	// Debug form's presence here and assert the SAVE PHASES line via a real
	// locked save (phases is Info but has no size floor).
	if _, err := s.dagStateForSave(context.Background(), path, time.Now().UTC()); err != nil {
		t.Fatalf("dagStateForSave: %v", err)
	}
	saveErr := s.Save(path)
	t.Logf("saveErr=%v; buf=%s", saveErr, buf.String())
	if saveErr != nil {
		// The CAS refusal still passes through saveLocked, so the phases
		// line must exist even when the save itself rejects.
	}
	out := buf.String()
	if !strings.Contains(out, "session: save phases") {
		t.Fatalf("save must emit the phases line (task 357 attribution); got: %s", out)
	}
	for _, seg := range []string{"lock_wait_ms", "auth_ms", "snap_ms", "probe_ms", "locked_total_ms"} {
		if !strings.Contains(out, seg) {
			t.Fatalf("phases line missing %s; got: %s", seg, out)
		}
	}
}
