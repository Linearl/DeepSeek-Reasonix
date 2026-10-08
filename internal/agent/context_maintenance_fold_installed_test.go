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
func TestFoldKeptRefreshFailureCarriesFoldInstalled(t *testing.T) {
	sess := foldableSessionOverForce(6)
	// A 2000-word digest keeps the folded view above the pressure fold trigger
	// but below the hard ceiling, so the ladder takes a second round and the
	// scripted stream error lands on a kept fold.
	prov := &failOnCallN{
		fakeProvider: &fakeProvider{reply: strings.Repeat("word ", 2000)},
		n:            2, err: errors.New("provider down"),
	}
	a := agentOverForce(t, prov, sess)
	var seen *event.ContextMaintenance
	a.svc.sink = event.FuncSink(func(e event.Event) {
		if e.Kind == event.ContextMaintenanceEvent && e.Maintenance != nil && e.Maintenance.Status == "failed" {
			seen = e.Maintenance
		}
	})

	if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
		t.Fatalf("prepare = %v, want a kept fold to stay recoverable", err)
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
