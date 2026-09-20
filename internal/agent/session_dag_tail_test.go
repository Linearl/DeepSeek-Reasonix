package agent

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// dagWideLog writes a log whose entries are large enough that a small window can
// be cut out of it, which is what the trailing-window replay exists for.
func dagWideLog(t *testing.T, sessionPath string, count int) (ids []string, base time.Time) {
	t.Helper()
	base = time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC)
	entries := []sessionDAGEntry{{Type: sessionDAGTypeLog, Generation: 1, At: base}}
	parent := ""
	for i := 0; i < count; i++ {
		role := provider.RoleUser
		if i%2 == 1 {
			role = provider.RoleAssistant
		}
		id := "M" + strconv.Itoa(i)
		// Pad so each entry is a few hundred bytes: the window must be able to
		// start in the middle of the log rather than at its first entry.
		content := "m" + strconv.Itoa(i) + ":" + strings.Repeat("x", 400)
		m := dagMsg(role, content, id)
		entries = append(entries, dagMessageEntry(t, SessionMainHead, parent, "t1", m, base.Add(time.Duration(i)*time.Second)))
		parent = id
		ids = append(ids, id)
	}
	dagAppend(t, sessionPath, entries...)
	return ids, base
}

// TestDAGTailReplayMatchesFullReplayTail is the correctness property the whole
// optimisation rests on: a windowed replay must produce exactly the same tail as
// the full replay, because that tail is what the reader paints.
func TestDAGTailReplayMatchesFullReplayTail(t *testing.T) {
	path := dagTestSession(t)
	_, _ = dagWideLog(t, path, 40)

	full := dagReplay(t, path)
	fullMsgs, _ := full.materialize(full.selectedHead())
	if len(fullMsgs) < 10 {
		t.Fatalf("fixture is too small to window: %d messages", len(fullMsgs))
	}

	st, err := replaySessionDAGTail(context.Background(), store.SessionEventLog(path), 4096, defaultSessionReplayLimits)
	if err != nil {
		if errors.Is(err, errSessionDAGTailUnavailable) {
			t.Fatalf("window replay unavailable for a %d-message log, the window never engages", len(fullMsgs))
		}
		t.Fatal(err)
	}
	if !st.tailTruncated || st.windowStart <= 0 {
		t.Fatalf("windowed state not marked: truncated=%v windowStart=%d", st.tailTruncated, st.windowStart)
	}
	tailMsgs, _ := st.materialize(st.selectedHead())
	if len(tailMsgs) == 0 || len(tailMsgs) >= len(fullMsgs) {
		t.Fatalf("windowed replay returned %d of %d messages", len(tailMsgs), len(fullMsgs))
	}
	got, want := dagContents(tailMsgs), dagContents(fullMsgs)
	wantTail := want[len(want)-len(got):]
	if strings.Join(got, ",") != strings.Join(wantTail, ",") {
		t.Fatalf("windowed tail %v does not match the full replay tail %v", got, wantTail)
	}
}

// TestDAGTailReplayRefusesWhenLogFits keeps the fallback honest: a log that is not
// larger than the window must not be replayed as a window, so the caller can fall
// back to the full replay instead of showing a partial transcript as the whole one.
func TestDAGTailReplayRefusesWhenLogFits(t *testing.T) {
	path := dagTestSession(t)
	dagLinearLog(t, path)
	if _, err := replaySessionDAGTail(context.Background(), store.SessionEventLog(path), 64<<20, defaultSessionReplayLimits); !errors.Is(err, errSessionDAGTailUnavailable) {
		t.Fatalf("expected errSessionDAGTailUnavailable for a small log, got %v", err)
	}
}

// TestSessionDAGTailWindowStartSnapsToLineBoundary pins the alignment rule: the
// restart offset must follow a newline, because entries are one JSON object per
// line and a mid-line restart cannot decode.
func TestSessionDAGTailWindowStartSnapsToLineBoundary(t *testing.T) {
	path := dagTestSession(t)
	_, _ = dagWideLog(t, path, 40)
	logPath := store.SessionEventLog(path)
	start, ok := sessionDAGTailWindowStart(logPath, 4096)
	if !ok {
		t.Fatal("window start unavailable for a log far larger than the window")
	}
	if start <= 0 {
		t.Fatalf("window start = %d, want a forward cut inside the log", start)
	}
	st, err := replaySessionDAGTail(context.Background(), logPath, 4096, defaultSessionReplayLimits)
	if err != nil {
		t.Fatalf("replay from the computed window: %v", err)
	}
	if st.damaged {
		t.Fatal("windowed replay of an aligned offset came back damaged")
	}
	if len(st.nodes) == 0 {
		t.Fatal("windowed replay decoded no messages")
	}
}

// TestLoadSessionTranscriptTailLeavesSmallSessionsAlone is the no-regression
// property: below the threshold the first-paint entry point is byte-for-byte the
// ordinary load.
func TestLoadSessionTranscriptTailLeavesSmallSessionsAlone(t *testing.T) {
	path := dagTestSession(t)
	_, _ = dagWideLog(t, path, 8)

	plain, err := loadSessionTranscript(context.Background(), path, defaultSessionReplayLimits, nil)
	if err != nil {
		t.Fatal(err)
	}
	tail, err := loadSessionTranscriptTail(context.Background(), path, defaultSessionReplayLimits, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tail.tailTruncated {
		t.Fatal("a small session must not be reported as truncated")
	}
	if strings.Join(dagContents(tail.msgs), ",") != strings.Join(dagContents(plain.msgs), ",") {
		t.Fatalf("small-session load changed: %v vs %v", dagContents(tail.msgs), dagContents(plain.msgs))
	}
}
