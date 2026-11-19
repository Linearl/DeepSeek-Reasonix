package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// failOnCallN wraps fakeProvider and fails the Nth Stream call, so a test can
// script "the first summary succeeds, the refresh attempt dies mid-stream".
type failOnCallN struct {
	*fakeProvider
	calls int
	n     int
	err   error
}

func (f *failOnCallN) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	f.calls++
	if f.calls == f.n {
		ch := make(chan provider.Chunk, 1)
		ch <- provider.Chunk{Type: provider.ChunkError, Err: f.err}
		close(ch)
		return ch, nil
	}
	return f.fakeProvider.Stream(ctx, req)
}

// Task 303/633: when an earlier ladder round already installed the fold, a
// later round's mid-stream summary failure is a refresh problem on a kept
// compaction — not the compaction failing. The receipt and the wire event must
// both carry fold_installed, so the frontend says "short view kept" instead of
// reading as a give-up.
// tl1713: the pressure lane's second ladder round is exactly the digest-rewrite
// shape the low-yield gate declines (fold = the digest it just wrote), so the
// refresh-failure contract rides the manual over-ceiling lane: the ladder runs
// up to 4 rounds while the view sits at or above the hard ceiling and the gate
// stays bypassed (manual/mustFree) — the marker contract itself is unchanged.
// A 12000-word digest keeps the post-round-one view above that ceiling, so the
// scripted stream error lands on a kept fold in round two.
func TestFoldKeptRefreshFailureCarriesFoldInstalled(t *testing.T) {
	sess := foldableSessionOverForce(12)
	prov := &failOnCallN{
		fakeProvider: &fakeProvider{reply: strings.Repeat("word ", 4500)},
		n:            2, err: errors.New("provider down"),
	}
	a := agentOverForce(t, prov, sess)
	if est, hard := canonicalEstimate(a, sess), a.hardInputCeiling(); est < hard {
		t.Fatalf("fixture estimates %d tokens, below the ceiling %d; add turns", est, hard)
	}
	var seen *event.ContextMaintenance
	a.svc.sink = event.FuncSink(func(e event.Event) {
		if e.Kind == event.ContextMaintenanceEvent && e.Maintenance != nil && e.Maintenance.Status == "failed" {
			seen = e.Maintenance
		}
	})

	// tl1713: the manual lane surfaces a refresh failure to its CALLER (the
	// /compact result) instead of letting the turn proceed like the pressure
	// lane — the kept-fold receipt and wire event below are the contract under
	// test, and the error itself is the manual compact's honest outcome.
	if err := prepareContext(context.Background(), a, CompactionTriggerManual); err == nil {
		t.Fatal("manual compact reported success despite the scripted refresh failure")
	} else if !strings.Contains(err.Error(), "provider down") {
		t.Fatalf("prepare err = %v, want the scripted provider failure", err)
	}
	if prov.calls < 2 {
		t.Fatalf("summarizer used %d calls; the fixture never reached a second ladder round", prov.calls)
	}
	if a.currentProjectionVersion() == 0 {
		t.Fatal("round one installed no projection; the fold was not kept")
	}
	r := a.sess.compactionState.LastReceipt
	if r == nil || r.Status != "failed" || !r.FoldInstalled {
		t.Fatalf("receipt = %+v, want failed with fold_installed", r)
	}
	if !strings.Contains(r.Reason, "compaction kept") {
		t.Fatalf("reason = %q, want the compaction-kept wording", r.Reason)
	}
	if seen == nil || !seen.FoldInstalled {
		t.Fatalf("event = %+v, want fold_installed on the wire event", seen)
	}
}

// The blocked path never carries the fold marker: without an installed fold
// the copy must stay "no short view yet" — task 633's third state.
func TestBlockedReceiptHasNoFoldMarker(t *testing.T) {
	sess := foldableSessionOverForce(6)
	a := agentOverForce(t, &fakeProvider{reply: "digest"}, sess)
	var seen *event.ContextMaintenance
	a.svc.sink = event.FuncSink(func(e event.Event) {
		if e.Kind == event.ContextMaintenanceEvent && e.Maintenance != nil && e.Maintenance.Status == "blocked" {
			seen = e.Maintenance
		}
	})

	a.recordContextMaintenanceBlocked("", CompactionTriggerPressure, "summary", "context changed during summary")

	r := a.sess.compactionState.LastReceipt
	if r == nil || r.Status != "blocked" || r.FoldInstalled {
		t.Fatalf("receipt = %+v, want blocked without fold_installed", r)
	}
	if seen == nil || seen.FoldInstalled {
		t.Fatalf("event = %+v, want no fold_installed on the blocked event", seen)
	}
}
