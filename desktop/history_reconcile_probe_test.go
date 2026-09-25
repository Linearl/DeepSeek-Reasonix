//go:build probe

// History reconcile cost probe (task 123 tail, 2026-09-25).
//
// Run: go test -tags probe -run TestHistoryReconcileCostProbe -v . (in desktop/)
// Session path via RECONCILE_PROBE_SESSION (a *.jsonl transcript whose
// .events.jsonl sibling exceeds 50 MiB). The probe is build-tag isolated so
// the regular suite never sees it — no skips, no env dependence.
//
// It measures, against the real session, the exact fork points of
// coldHistorySlice: sidecar index load, ledger identity, Validate, full
// ScanSessionDisplayIndex, the event-log authoritative load (the 3s
// candidate), and the page conversion itself — so the fix targets the
// measured segment, not a guess.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/store"
)

func TestHistoryReconcileCostProbe(t *testing.T) {
	path := os.Getenv("RECONCILE_PROBE_SESSION")
	if path == "" {
		t.Fatal("RECONCIDLE_PROBE_SESSION not set — export the probe session path first")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat probe session: %v", err)
	}
	t.Logf("probe session: %s (transcript %d MiB)", path, info.Size()>>20)

	// 1. sidecar index load (the fast path's entry cost).
	t0 := time.Now()
	idx, loadErr := agent.LoadSessionDisplayIndex(store.SessionDisplayIndex(path))
	loadMs := time.Since(t0).Milliseconds()

	// 2. ledger identity (sidecar read).
	t0 = time.Now()
	identity, identityKnown, identityErr := agent.SessionContentIdentity(path)
	identMs := time.Since(t0).Milliseconds()

	// 3. Validate verdict — the fork between index page and rebuild.
	valid := false
	if idx != nil && loadErr == nil && identityKnown && identityErr == nil {
		valid = agent.ValidateSessionDisplayIndex(idx, identity.Revision, identity.RevisionKnown, identity.Digest, info.Size())
	}

	// 4. full ScanSessionDisplayIndex (the index-rebuild cost).
	t0 = time.Now()
	scanned, scanErr := agent.ScanSessionDisplayIndex(path)
	scanMs := time.Since(t0).Milliseconds()

	// 5. event-log authoritative load — the full-parse 3s candidate that runs
	// when the anchor scan digest disagrees with the ledger.
	fallbackMs := int64(-1)
	if scanned != nil && identityKnown && scanErr == nil && scanned.ContentDigest != identity.DigestHex {
		t0 = time.Now()
		_, _, _, loadErr2 := agent.LoadSessionDisplayMessages(path)
		fallbackMs = time.Since(t0).Milliseconds()
		if loadErr2 != nil {
			t.Logf("event-log load err: %v", loadErr2)
		}
	}

	app := &App{} // historyDerivedCache lazily inits its map on first miss
	t0 = time.Now()
	slice, pageErr := app.coldHistorySlice(filepath.Dir(path), path, HistorySliceRequest{Cursor: "", Turns: 12})
	pageMs := time.Since(t0).Milliseconds()

	src := "err"
	if pageErr == nil {
		src = slice.Source
	}
	fmt.Printf("PROBE transcript=%dMiB indexLoad=%dms identity=%dms valid=%v scan=%dms eventLogLoad=%dms coldSlice=%dms source=%s entries=%d err=%v\n",
		info.Size()>>20, loadMs, identMs, valid, scanMs, fallbackMs, pageMs, src,
		func() int {
			if slice.Entries != nil {
				return len(slice.Entries)
			}
			return 0
		}(), pageErr)
}
