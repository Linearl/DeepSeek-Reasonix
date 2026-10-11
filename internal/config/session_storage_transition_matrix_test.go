package config

import "testing"

// Task 772: the mode ladder is the rollback contract for every session the
// desktop owns, so the transition and restart decisions are pinned as
// exhaustive 4x4 tables — one row per (from, to) pair, no sampling. A new mode
// or a flipped rule fails here with the exact cell named.

// TestSessionStorageTransitionMatrixExhaustive walks all 16 (from, to) cells.
// Rules under test (task 155, unchanged): downgrades are always allowed; an
// upgrade may move one stage at a time; a bigger jump is refused unless a
// dual-write stage is in effect or ran before — being IN a dual-write mode
// counts (the mirror is running, which is the safety precondition; the
// one-stage cadence is UX, not a data gate).
func TestSessionStorageTransitionMatrixExhaustive(t *testing.T) {
	all := SessionStorageModes
	for _, from := range all {
		for _, to := range all {
			fromIndex, toIndex := SessionStorageModeIndex(from), SessionStorageModeIndex(to)
			downgrade := toIndex < fromIndex
			oneStage := toIndex-fromIndex <= 1
			staged := isDualStage(from)
			wantErr := !downgrade && !oneStage && !staged

			if err := ValidateSessionStorageTransition(from, to, nil); (err != nil) != wantErr {
				t.Fatalf("transition %s -> %s: err=%v, wantErr=%v", from, to, err, wantErr)
			}
			// With any dual-write stage in the history every upgrade is legal.
			history := []string{SessionStorageDualWriteReadV3, SessionStorageDualWriteReadV4}
			if err := ValidateSessionStorageTransition(from, to, history); err != nil {
				t.Fatalf("transition %s -> %s with staged history refused: %v", from, to, err)
			}
		}
	}
	// The cold skip is refused exactly once: v3_only straight to v4_only.
	if err := ValidateSessionStorageTransition(SessionStorageV3Only, SessionStorageV4Only, nil); err == nil {
		t.Fatal("v3_only -> v4_only allowed without any dual-write stage")
	}
}

// TestSessionStorageNeedsRestartMatrixExhaustive walks all 16 (from, to) cells.
// Only the two dual-write modes share a write-side boot contract, so that pair
// (and identity) is the complete set of restart-free moves.
func TestSessionStorageNeedsRestartMatrixExhaustive(t *testing.T) {
	for _, from := range SessionStorageModes {
		for _, to := range SessionStorageModes {
			want := from != to && !(isDualStage(from) && isDualStage(to))
			if got := SessionStorageNeedsRestart(from, to); got != want {
				t.Fatalf("restart(%s -> %s) = %v, want %v", from, to, got, want)
			}
		}
	}
}

func isDualStage(mode string) bool {
	return mode == SessionStorageDualWriteReadV3 || mode == SessionStorageDualWriteReadV4
}

// TestSessionStorageLadderEndToEnd replays the exact journey the settings UI
// permits against a clean state home: every hop is validated against the
// growing audit trail, the clamping guard lets a hand-edited v4_only through
// only once the trail shows a dual-write stage, and the ledger records each
// hop oldest-first.
func TestSessionStorageLadderEndToEnd(t *testing.T) {
	t.Setenv("REASONIX_STATE_HOME", t.TempDir())

	current := SessionStorageV3Only
	ladder := []string{
		SessionStorageDualWriteReadV3,
		SessionStorageDualWriteReadV4,
		SessionStorageV4Only,
		SessionStorageDualWriteReadV3,
		SessionStorageV3Only,
	}
	for i, to := range ladder {
		if err := ValidateSessionStorageTransition(current, to, SessionStorageHistoryModes()); err != nil {
			t.Fatalf("hop %d (%s -> %s) refused: %v", i, current, to, err)
		}
		if err := AppendSessionStorageChange(SessionStorageChange{
			From: current, To: to, Source: "settings-ui", Restart: SessionStorageNeedsRestart(current, to),
		}); err != nil {
			t.Fatalf("hop %d audit: %v", i, err)
		}
		current = to
	}
	changes, err := ReadSessionStorageChanges()
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if len(changes) != len(ladder) {
		t.Fatalf("ledger has %d entries, want %d", len(changes), len(ladder))
	}
	for i := 1; i < len(changes); i++ {
		if changes[i-1].At.After(changes[i].At) {
			t.Fatalf("ledger out of order at entry %d", i)
		}
		if changes[i].From != changes[i-1].To {
			t.Fatalf("ledger chain broken at entry %d: %s after %s", i, changes[i].From, changes[i-1].To)
		}
	}

	// With the dual-write stage in the ledger, a hand-edited v4_only is no
	// longer clamped at boot.
	if mode, adjusted := ResolveSafeSessionStorageMode(SessionStorageV4Only, SessionStorageHistoryModes()); adjusted || mode != SessionStorageV4Only {
		t.Fatalf("v4_only after a staged ladder = %q adjusted=%v, want unchanged", mode, adjusted)
	}
	// And the direct skip stays refused on a clean history even though earlier
	// hops existed — the guard reads the ledger, not the ladder.
	t.Setenv("REASONIX_STATE_HOME", t.TempDir())
	if err := ValidateSessionStorageTransition(SessionStorageV3Only, SessionStorageV4Only, nil); err == nil {
		t.Fatal("v3_only -> v4_only allowed without any dual-write stage")
	}
}
