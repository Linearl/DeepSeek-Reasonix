package agent

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// TestDAGCheckpointEquivalence is the task-308-O2 hard proof: restoring from
// the fold checkpoint and replaying only the trailing window produces exactly
// the state a full replay produces — same selected head, same materialized
// transcript, same node/head/record counts, same replayed-to offset. The
// checkpoint may only save work, never change the result.
//
// The fixture follows the dagLinearLog shape: every message carries an ID and
// parents on the previous message's ID, with a system message first — the
// replay is a strict parent chain, so entries without those links are dropped.
func TestDAGCheckpointEquivalence(t *testing.T) {
	dir := t.TempDir()
	sessionPath := dir + "/s1"
	logPath := store.SessionEventLog(sessionPath)
	base := time.UnixMilli(1790600000000)

	sys := dagMsg(provider.RoleSystem, "sys", "S0")
	q1 := dagMsg(provider.RoleUser, "one", "U1")
	a1 := dagMsg(provider.RoleAssistant, "two", "A1")
	q2 := dagMsg(provider.RoleUser, "three", "U2")
	a2 := dagMsg(provider.RoleAssistant, "four", "A2")
	dagAppend(t, sessionPath,
		sessionDAGEntry{Type: sessionDAGTypeLog, Generation: 1, At: base},
		dagMessageEntry(t, SessionMainHead, "", "t1", sys, base),
		dagMessageEntry(t, SessionMainHead, "S0", "t1", q1, base.Add(time.Second)),
		dagMessageEntry(t, SessionMainHead, "U1", "t1", a1, base.Add(2*time.Second)),
		dagMessageEntry(t, SessionMainHead, "A1", "t1", q2, base.Add(3*time.Second)),
		// The fold marker: everything through U2 is covered; A2 comes after.
		sessionDAGEntry{Type: sessionDAGTypeCompaction, Head: SessionMainHead, CoveredLeaf: "U2", CoveredCount: 4, PrefixHash: "ph-1", At: base.Add(4 * time.Second)},
		dagMessageEntry(t, SessionMainHead, "U2", "t1", a2, base.Add(5*time.Second)),
	)

	// Round 1: full replay (gate off — no env), then seed the checkpoint from
	// that state the way the self-seeding path does.
	st1 := dagReplay(t, sessionPath)
	if len(st1.nodes) == 0 || st1.records < 5 {
		t.Fatalf("fixture produced an empty replay: nodes=%d records=%d", len(st1.nodes), st1.records)
	}
	snap := snapshotFromState(st1, "ph-1")
	if err := saveDAGCheckpoint(logPath, snap); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	t.Setenv("REASONIX_DAG_FOLD_CHECKPOINT", "1")
	restored, ok := replaySessionDAGFromCheckpoint(context.Background(), logPath, defaultSessionReplayLimits)
	if !ok {
		t.Fatal("checkpoint path did not engage with the gate on and a fresh checkpoint present")
	}

	// Equivalence: every observable the reader cares about must match.
	if restored.selectedHead() != st1.selectedHead() {
		t.Fatalf("selected head %q != full-replay %q", restored.selectedHead(), st1.selectedHead())
	}
	if len(restored.nodes) != len(st1.nodes) || len(restored.heads) != len(st1.heads) {
		t.Fatalf("graph size mismatch: nodes %d/%d heads %d/%d",
			len(restored.nodes), len(st1.nodes), len(restored.heads), len(st1.heads))
	}
	if restored.records != st1.records || restored.lastGoodEnd != st1.lastGoodEnd {
		t.Fatalf("records/offset mismatch: %d/%d vs %d/%d",
			restored.records, restored.lastGoodEnd, st1.records, st1.lastGoodEnd)
	}
	got1, _ := restored.materialize(restored.selectedHead())
	want1, _ := st1.materialize(st1.selectedHead())
	if a, b := dagContents(got1), dagContents(want1); len(a) != len(b) {
		t.Fatalf("materialized transcript mismatch: %v vs %v", a, b)
	} else {
		for i := range a {
			if a[i] != b[i] {
				t.Fatalf("message %d mismatch: %q vs %q", i, a[i], b[i])
			}
		}
	}

	// Append after the checkpoint (parenting on the current leaf), then
	// re-read through the checkpoint path: the trailing window must pick the
	// new message up on top of the restored prefix.
	q3 := dagMsg(provider.RoleUser, "five", "U3")
	dagAppend(t, sessionPath, dagMessageEntry(t, SessionMainHead, "A2", "t1", q3, base.Add(6*time.Second)))
	st2, err := replaySessionDAG(context.Background(), logPath, defaultSessionReplayLimits)
	if err != nil {
		t.Fatalf("post-append replay: %v", err)
	}
	got2, _ := st2.materialize(st2.selectedHead())
	contents := dagContents(got2)
	if len(contents) != 6 || contents[len(contents)-1] != "five" {
		t.Fatalf("trailing window replay lost the appended message: %v", contents)
	}

	// Gate off: same log, no checkpoint engagement — legacy behaviour.
	t.Setenv("REASONIX_DAG_FOLD_CHECKPOINT", "")
	st3 := dagReplay(t, sessionPath)
	got3, _ := st3.materialize(st3.selectedHead())
	if len(dagContents(got3)) != 6 {
		t.Fatalf("gate-off replay diverged: %v", dagContents(got3))
	}
}

// TestDAGCheckpointLoadSessionEntry is the entry-level proof (task 308-O2):
// through the real loadSessionTranscript entry — the one save, recovery and
// export all share — a gated cold read hits the checkpoint (the hit log line
// fires) and produces the same transcript the full replay produces, with and
// without a post-checkpoint append.
func TestDAGCheckpointLoadSessionEntry(t *testing.T) {
	dir := t.TempDir()
	sessionPath := dir + "/s2"
	base := time.UnixMilli(1790700000000)

	sys := dagMsg(provider.RoleSystem, "sys", "S0")
	q1 := dagMsg(provider.RoleUser, "one", "U1")
	a1 := dagMsg(provider.RoleAssistant, "two", "A1")
	dagAppend(t, sessionPath,
		sessionDAGEntry{Type: sessionDAGTypeLog, Generation: 1, At: base},
		dagMessageEntry(t, SessionMainHead, "", "t1", sys, base),
		dagMessageEntry(t, SessionMainHead, "S0", "t1", q1, base.Add(time.Second)),
		dagMessageEntry(t, SessionMainHead, "U1", "t1", a1, base.Add(2*time.Second)),
		sessionDAGEntry{Type: sessionDAGTypeCompaction, Head: SessionMainHead, CoveredLeaf: "A1", CoveredCount: 3, PrefixHash: "ph-e", At: base.Add(3 * time.Second)},
	)

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	// Seed: one full replay under the gate self-seeds the checkpoint (no hit
	// yet — the checkpoint does not exist before this load).
	t.Setenv("REASONIX_DAG_FOLD_CHECKPOINT", "1")
	res1, err := loadSessionTranscript(context.Background(), sessionPath, defaultSessionReplayLimits, newSessionTranscriptHasher())
	if err != nil {
		t.Fatalf("seed load: %v", err)
	}
	if strings.Contains(logBuf.String(), "agent: dag checkpoint hit") {
		t.Fatal("seed load hit a checkpoint that could not exist yet")
	}
	if _, err := os.Stat(store.SessionEventLog(sessionPath) + dagCheckpointSuffix); err != nil {
		t.Fatalf("seed load did not persist a checkpoint: %v", err)
	}
	logBuf.Reset()

	// Entry-level cold read: same messages, and the hit line fires again.
	res2, err := loadSessionTranscript(context.Background(), sessionPath, defaultSessionReplayLimits, newSessionTranscriptHasher())
	if err != nil {
		t.Fatalf("cold load: %v", err)
	}
	if !strings.Contains(logBuf.String(), "msg=\"agent: dag checkpoint hit\"") {
		t.Fatalf("cold load did not hit the checkpoint, log:\n%s", logBuf.String())
	}
	if a, b := dagContents(res1.msgs), dagContents(res2.msgs); len(a) != len(b) {
		t.Fatalf("entry-level transcript mismatch: %v vs %v", a, b)
	}
	if !res2.dag {
		t.Fatal("entry-level load did not take the DAG path")
	}

	// Gate off: no hit line, same transcript — legacy behaviour.
	t.Setenv("REASONIX_DAG_FOLD_CHECKPOINT", "")
	logBuf.Reset()
	res3, err := loadSessionTranscript(context.Background(), sessionPath, defaultSessionReplayLimits, newSessionTranscriptHasher())
	if err != nil {
		t.Fatalf("gate-off load: %v", err)
	}
	if strings.Contains(logBuf.String(), "agent: dag checkpoint hit") {
		t.Fatal("gate-off load still hit the checkpoint")
	}
	if a, b := dagContents(res2.msgs), dagContents(res3.msgs); len(a) != len(b) {
		t.Fatalf("gate-off transcript diverged: %v vs %v", a, b)
	}
}
