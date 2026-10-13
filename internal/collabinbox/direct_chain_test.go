package collabinbox

// 任务 856 ③ — the direct-chain (对话链) read-port contracts:
//
//   聚合口径 (v2 返工钦定): ONLY session↔session point-to-point mail
//   aggregates — channel mail belongs to its group view, and human↔session
//   mail stays in the mail inbox view (the user manages it directly there).
//   PairID is the stable unordered-pair key; ReadDirectChain replays the
//   bidirectional timeline oldest-first with per-entry origins intact.

import (
	"context"
	"testing"

	"reasonix/internal/sessioncollab"
)

func TestListDirectChainsAggregation(t *testing.T) {
	store, mail := fixtureStore(t)
	ctx := context.Background()
	base := store.now()
	// Session↔session point-to-point pairs — these aggregate.
	deliver(t, mail, sessioncollab.MailMessage{ID: "s1", From: "sc_main", To: "sc_dev", Body: "dispatch the slice", At: base - 3*dayMs})
	deliver(t, mail, sessioncollab.MailMessage{ID: "s2", From: "sc_dev", To: "sc_main", Body: "ack, slice committed", At: base})
	// Reverse direction must fold into the SAME unordered pair.
	deliver(t, mail, sessioncollab.MailMessage{ID: "s3", From: "sc_dev", To: "sc_merge", Body: "branch kept, not merged", At: base - dayMs})
	// Human↔session — NEVER aggregates (the user manages it in the inbox).
	deliver(t, mail, sessioncollab.MailMessage{ID: "h1", From: HumanLocalContact, To: "sc_dev", Body: "human dispatch", At: base})
	deliver(t, mail, sessioncollab.MailMessage{ID: "h2", From: "sc_dev", To: HumanLocalContact, Body: "to the human", At: base})
	// Channel mail — belongs to the group view, never to a direct chain.
	deliver(t, mail, sessioncollab.MailMessage{ID: "c1", From: "sc_main", To: "sc_dev", Channel: "release", Body: "fan-out copy", At: base})
	// Dismissed session↔session mail — excluded (chains-view parity).
	deliver(t, mail, sessioncollab.MailMessage{ID: "d1", From: "sc_main", To: "sc_audit", Body: "dismissed dispatch", At: base})
	if _, err := store.Dismiss(ctx, []string{"d1"}); err != nil {
		t.Fatal(err)
	}

	snap, err := store.ListDirectChains(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Total != 2 {
		t.Fatalf("pair count = %d, want 2 (session↔session only): %+v", snap.Total, snap.Pairs)
	}
	first := snap.Pairs[0]
	if first.PairID == "" || first.Count != 2 || first.A != "sc_dev" || first.B != "sc_main" {
		t.Fatalf("newest pair = %+v, want the sc_main↔sc_dev pair with count 2", first)
	}
	if first.Preview != "ack, slice committed" {
		t.Fatalf("preview = %q, want the newest entry's preview", first.Preview)
	}
	for _, p := range snap.Pairs {
		if p.A == HumanLocalContact || p.B == HumanLocalContact {
			t.Fatalf("a human endpoint leaked into the pairs: %+v", p)
		}
	}
}

func TestReadDirectChainReplaysBidirectionally(t *testing.T) {
	store, mail := fixtureStore(t)
	ctx := context.Background()
	base := store.now()
	deliver(t, mail, sessioncollab.MailMessage{ID: "r1", From: "sc_main", To: "sc_dev", Body: "dispatch", At: base - 2*dayMs})
	deliver(t, mail, sessioncollab.MailMessage{ID: "r2", From: "sc_dev", To: "sc_main", Body: "ack", At: base - dayMs})
	deliver(t, mail, sessioncollab.MailMessage{ID: "r3", From: "sc_main", To: "sc_dev", Body: "receipt", At: base})
	// Noise from other parties must not leak into the replay.
	deliver(t, mail, sessioncollab.MailMessage{ID: "r4", From: "sc_main", To: "sc_merge", Body: "other pair", At: base})
	deliver(t, mail, sessioncollab.MailMessage{ID: "r5", From: HumanLocalContact, To: "sc_main", Body: "human line", At: base})

	snap, err := store.ListDirectChains(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	view, err := store.ReadDirectChain(ctx, snap.Pairs[0].PairID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if view.A != "sc_dev" || view.B != "sc_main" {
		t.Fatalf("parties = %s/%s, want the sorted unordered pair", view.A, view.B)
	}
	if len(view.Entries) != 3 {
		t.Fatalf("replay has %d entries, want exactly the pair's 3", len(view.Entries))
	}
	for i, want := range []string{"r1", "r2", "r3"} {
		if view.Entries[i].ID != want {
			t.Fatalf("replay[%d] = %s, want %s (oldest first)", i, view.Entries[i].ID, want)
		}
	}
	// Both directions present — the timeline is bidirectional with origins.
	if view.Entries[0].From != "sc_main" || view.Entries[1].From != "sc_dev" {
		t.Fatal("the replay lost the per-entry origin (from)")
	}

	// The tail limit keeps the NEWEST lines (a playback, not a head-trim).
	limited, err := store.ReadDirectChain(ctx, snap.Pairs[0].PairID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited.Entries) != 2 || limited.Entries[0].ID != "r2" || limited.Entries[1].ID != "r3" {
		t.Fatalf("limited replay = %v, want the newest tail [r2 r3]", limited.Entries)
	}

	// A malformed pair id is an explicit error, never an empty replay.
	if _, err := store.ReadDirectChain(ctx, "garbage", 0); err == nil {
		t.Fatal("a malformed pair id must error")
	}
}
