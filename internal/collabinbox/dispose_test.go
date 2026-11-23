package collabinbox

// tl1768 — the filter-based batch dispose contracts (the shared底层 the agent
// mailbox action and the desktop panel build on):
//
//   PlanDispose (dry-run): lists exactly what a dispose with the same filters
//   would touch, newest first; truncated above the hard cap; never mutates;
//   refuses on a degraded read (a partial view must never authorize rows).
//   DisposeByFilter (execute): re-runs the filter under the exclusive lock;
//   archive = dismiss tombstone (recoverable via Undismiss, transport
//   untouched); delete = physical archive-only removal with fold expansion
//   and state GC; refused above the per-call cap; empty match is an honest
//   no-op.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"reasonix/internal/sessioncollab"
)

func TestValidDisposeMode(t *testing.T) {
	for _, mode := range []string{DisposeArchive, DisposeDelete} {
		if !ValidDisposeMode(mode) {
			t.Fatalf("mode %q must be valid", mode)
		}
	}
	if ValidDisposeMode("nuke") || ValidDisposeMode("") {
		t.Fatal("unknown/empty modes must be rejected")
	}
}

func TestPlanDisposeListsFilterMatchesWithoutMutating(t *testing.T) {
	store, mail := fixtureStore(t)
	ctx := context.Background()
	base := store.now()
	deliver(t, mail, sessioncollab.MailMessage{ID: "m_old", From: "sc_a", To: "sc_b", Body: "old handled", At: base - 3*dayMs})
	deliver(t, mail, sessioncollab.MailMessage{ID: "m_new", From: "sc_a", To: "sc_b", Body: "new pending", At: base})
	deliver(t, mail, sessioncollab.MailMessage{ID: "m_other", From: "sc_c", To: "sc_d", Body: "other mailbox", At: base})

	before, err := store.List(ctx, Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := store.PlanDispose(ctx, Query{To: "sc_b", Until: base - dayMs}, DisposeDelete)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != DisposeDelete || plan.Total != 1 || plan.Truncated {
		t.Fatalf("plan = mode %s total %d truncated %v, want delete/1/false", plan.Mode, plan.Total, plan.Truncated)
	}
	if len(plan.Entries) != 1 || plan.Entries[0].ID != "m_old" {
		t.Fatalf("plan must list exactly the until-capped match, got %+v", plan.Entries)
	}
	// Dry-run mutates nothing: same table before and after (revision included).
	after, err := store.List(ctx, Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision || after.Total != before.Total {
		t.Fatalf("plan mutated the view: %s → %s", before.Revision, after.Revision)
	}
	// Unknown mode refused.
	if _, err := store.PlanDispose(ctx, Query{}, "nuke"); err == nil || !strings.Contains(err.Error(), "archive|delete") {
		t.Fatalf("unknown plan mode must be refused: %v", err)
	}
}

func TestPlanDisposeTruncatesAboveCapAndReportsTotal(t *testing.T) {
	store, mail := fixtureStore(t)
	ctx := context.Background()
	base := store.now()
	for i := 0; i < maxLimit+5; i++ {
		// Unique bodies: identical from/to/body rows inside the 24h fold window
		// would cluster into ONE folded entry and defeat the cap fixtures.
		deliver(t, mail, sessioncollab.MailMessage{ID: fmt.Sprintf("m_bulk_%04d", i), From: "sc_a", To: "sc_b", Body: fmt.Sprintf("bulk %04d", i), At: base - int64(maxLimit+5-i)})
	}
	plan, err := store.PlanDispose(ctx, Query{To: "sc_b"}, DisposeArchive)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Total != maxLimit+5 || !plan.Truncated || len(plan.Entries) != maxLimit {
		t.Fatalf("plan must cap the listing at %d but report the full total: total=%d truncated=%v listed=%d", maxLimit, plan.Total, plan.Truncated, len(plan.Entries))
	}
	// Newest first: the newest bulk row leads the listing (fixture seeds
	// At = base-(maxLimit+5-i), i goes 0..maxLimit+4, so the newest is base-1).
	if plan.Entries[0].At != base-1 {
		t.Fatalf("plan listing must be newest first, got At=%d want %d", plan.Entries[0].At, base-1)
	}
}

func TestDisposeArchiveHidesAndUndismissRecovers(t *testing.T) {
	store, mail := fixtureStore(t)
	ctx := context.Background()
	base := store.now()
	deliver(t, mail, sessioncollab.MailMessage{ID: "m_1", From: "sc_a", To: "sc_b", Body: "handled one", At: base})
	deliver(t, mail, sessioncollab.MailMessage{ID: "m_2", From: "sc_a", To: "sc_b", Body: "handled two", At: base - dayMs})
	deliver(t, mail, sessioncollab.MailMessage{ID: "m_keep", From: "sc_c", To: "sc_d", Body: "unrelated", At: base})

	res, err := store.DisposeByFilter(ctx, Query{To: "sc_b"}, DisposeArchive)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Recoverable || res.Disposed != 2 || res.Removed != 0 {
		t.Fatalf("archive result = disposed %d removed %d recoverable %v, want 2/0/true", res.Disposed, res.Removed, res.Recoverable)
	}
	// Hidden from the default view; transport layer untouched (soft disposal).
	snap, err := store.List(ctx, Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Total != 1 || snap.Entries[0].ID != "m_keep" {
		t.Fatalf("archived rows must leave the default view: total=%d", snap.Total)
	}
	if _, found := mail.ParentThread("sc_b", "m_1"); !found {
		t.Fatal("archive must not touch the transport layer")
	}
	// The dismissed view still sees them (and recoverable via Undismiss).
	dismissed, err := store.List(ctx, Query{IncludeDismissed: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if dismissed.Total != 3 {
		t.Fatalf("dismissed rows stay in the dismissed view: total=%d", dismissed.Total)
	}
	if _, err := store.Undismiss(ctx, []string{"m_1", "m_2"}); err != nil {
		t.Fatal(err)
	}
	back, err := store.List(ctx, Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if back.Total != 3 {
		t.Fatalf("undismiss must restore archived rows: total=%d", back.Total)
	}
}

func TestDisposeDeletePhysicallyRemovesAndExpandsClusters(t *testing.T) {
	store, mail := fixtureStore(t)
	ctx := context.Background()
	base := store.now()
	// A duplicate cluster (three byte-identical copies within the fold window)
	// plus one distinct row outside the filter.
	inbox := mail.InboxPath("sc_b")
	var lines []string
	for i, id := range []string{"m_d0", "m_d1", "m_d2"} {
		line, err := json.Marshal(sessioncollab.MailMessage{ID: id, From: "sc_a", To: "sc_b", Body: "same body", At: base - int64(i)*60_000})
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(line))
	}
	keep, err := json.Marshal(sessioncollab.MailMessage{ID: "m_keep", From: "sc_c", To: "sc_b", Body: "keep", At: base - dayMs})
	if err != nil {
		t.Fatal(err)
	}
	lines = append(lines, string(keep))
	joiner := "\n"
	if err := os.WriteFile(inbox, []byte(strings.Join(lines, joiner)+joiner), 0o600); err != nil {
		t.Fatal(err)
	}
	// A dismissal on a cluster MEMBER (not the primary) must neither hide the
	// folded entry from the filter (the primary carries the view state) nor
	// survive the physical removal — dead keys are garbage-collected.
	if _, err := store.Dismiss(ctx, []string{"m_d1"}); err != nil {
		t.Fatal(err)
	}

	res, err := store.DisposeByFilter(ctx, Query{To: "sc_b", From: "sc_a"}, DisposeDelete)
	if err != nil {
		t.Fatal(err)
	}
	if res.Recoverable || res.Disposed != 1 {
		t.Fatalf("folded cluster shows as ONE entry: disposed=%d recoverable=%v", res.Disposed, res.Recoverable)
	}
	if res.Removed != 3 {
		t.Fatalf("physical removal must take the whole cluster: removed=%d want 3", res.Removed)
	}
	for _, id := range []string{"m_d0", "m_d1", "m_d2"} {
		if _, found := mail.ParentThread("sc_b", id); found {
			t.Fatalf("cluster member %s survived the filter delete", id)
		}
	}
	if _, found := mail.ParentThread("sc_b", "m_keep"); !found {
		t.Fatal("the delete touched mail outside the filter")
	}
	st := store.loadState()
	if len(st.Dismissed) != 0 {
		t.Fatalf("dead dismissed keys must be garbage-collected: %v", st.Dismissed)
	}
	if res.Snapshot.Total != 1 {
		t.Fatalf("result snapshot must show the survivor: total=%d", res.Snapshot.Total)
	}
}

func TestDisposeDeleteRefusesAboveCap(t *testing.T) {
	store, mail := fixtureStore(t)
	ctx := context.Background()
	base := store.now()
	for i := 0; i < maxLimit+1; i++ {
		deliver(t, mail, sessioncollab.MailMessage{ID: fmt.Sprintf("m_bulk_%04d", i), From: "sc_a", To: "sc_b", Body: fmt.Sprintf("bulk %04d", i), At: base - int64(maxLimit+1-i)})
	}
	res, err := store.DisposeByFilter(ctx, Query{To: "sc_b"}, DisposeDelete)
	if err == nil {
		t.Fatalf("a %d-row match must be refused, got result %+v", maxLimit+1, res)
	}
	for _, want := range []string{"narrow", "until"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal must name the next step %q: %v", want, err)
		}
	}
	// Nothing was removed by the refused call.
	after, err := store.List(ctx, Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if after.Total != maxLimit+1 {
		t.Fatalf("a refused dispose must not touch anything: total=%d", after.Total)
	}
}

func TestDisposeEmptyMatchIsHonestNoop(t *testing.T) {
	store, mail := fixtureStore(t)
	ctx := context.Background()
	deliver(t, mail, sessioncollab.MailMessage{ID: "m_1", From: "sc_a", To: "sc_b", Body: "one", At: store.now()})
	for _, mode := range []string{DisposeArchive, DisposeDelete} {
		res, err := store.DisposeByFilter(ctx, Query{To: "sc_nobody"}, mode)
		if err != nil {
			t.Fatal(err)
		}
		if res.Disposed != 0 || len(res.IDs) != 0 {
			t.Fatalf("empty match must dispose nothing: %+v", res)
		}
	}
	snap, err := store.List(ctx, Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Total != 1 {
		t.Fatalf("noop dispose must leave the table alone: total=%d", snap.Total)
	}
}

func TestDisposeUnknownModeRefused(t *testing.T) {
	store, _ := fixtureStore(t)
	if _, err := store.DisposeByFilter(context.Background(), Query{}, "shred"); err == nil || !strings.Contains(err.Error(), "archive|delete") {
		t.Fatalf("unknown execute mode must be refused: %v", err)
	}
}
