// tl1733 Block1 acceptance tests for the usage ledger (item 5): recall and
// view events persist outside the compaction refresh chain, across restarts,
// and never touch MEMORY.md.
package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// D1: recall and view events land in usage-counts.json with (memory, session)
// granularity; MEMORY.md stays clean of ledger data.
func TestUsageRecordedAndPersisted(t *testing.T) {
	store := recallTestStore(t)
	RecordUsageRecall(store, "sess-a", []string{"id-1", "id-2"})
	RecordUsageView(store, "sess-a", "id-1")
	RecordUsageRecall(store, "sess-b", []string{"id-1"})

	path := UsageLedgerPath(store)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ledger must persist on record: %v", err)
	}
	var data usageFile
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if data.StartedAt.IsZero() {
		t.Fatal("StartedAt must anchor on the first event")
	}
	id1 := data.Memories["id-1"]
	if id1 == nil || id1.RecallTotal != 2 || id1.ViewTotal != 1 {
		t.Fatalf("id-1 counters = %+v, want recall 2 view 1", id1)
	}
	if id1.Sessions["sess-a"].Recall != 1 || id1.Sessions["sess-b"].Recall != 1 || id1.Sessions["sess-a"].View != 1 {
		t.Fatalf("per-session counters = %+v", id1.Sessions)
	}
	if data.Memories["id-2"] == nil || data.Memories["id-2"].RecallTotal != 1 {
		t.Fatalf("id-2 counters = %+v", data.Memories["id-2"])
	}

	// Position out of the compaction chain: the ledger is its own file — the
	// MEMORY.md index never carries usage rows.
	if _, err := os.Stat(filepath.Join(store.Dir, "MEMORY.md")); err == nil {
		raw, err := os.ReadFile(filepath.Join(store.Dir, "MEMORY.md"))
		if err == nil && len(raw) > 0 && strings.Contains(string(raw), "recall_total") {
			t.Fatalf("MEMORY.md must not carry ledger data:\n%s", raw)
		}
	}
}

// D2: compaction and restarts must not lose counts. The compaction refresh
// chain (system-prompt memory region rebuild + ReloadFromDisk → Load) runs
// three times; the ledger counters stay continuous throughout, and a fresh
// process-resident handle (restart simulation) reads the same numbers.
func TestUsageSurvivesCompaction(t *testing.T) {
	store := recallTestStore(t)
	RecordUsageRecall(store, "sess-a", []string{"id-1"})
	RecordUsageView(store, "sess-a", "id-1")

	// Three compaction passes: each re-derives the memory snapshot from disk
	// (the Load half of the refresh chain). None of them may touch the ledger.
	for i := range 3 {
		reloaded := Load(Options{CWD: t.TempDir(), UserDir: filepath.Dir(store.Dir)})
		_ = reloaded
		path := UsageLedgerPath(store)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("pass %d: ledger must survive compaction: %v", i, err)
		}
		var data usageFile
		if err := json.Unmarshal(raw, &data); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
		if data.Memories["id-1"].RecallTotal != 1 || data.Memories["id-1"].ViewTotal != 1 {
			t.Fatalf("pass %d: counters drifted: %+v", i, data.Memories["id-1"])
		}
	}

	// Restart simulation: a fresh handle over the same path reads the same
	// ledger (the registry shares state per path, so drop the cache flag —
	// production does this implicitly when a new process re-reads the file).
	if u := usageFor(store); u != nil {
		u.mu.Lock()
		u.loaded = false
		u.data = usageFile{}
		u.mu.Unlock()
	}
	if !store.UsageRecalledWithin("id-1", time.Now().UTC(), 24*time.Hour) {
		t.Fatal("recency stamp must survive restart")
	}
}

// Session ledger bounding: per-memory session rows evict stalest-first once
// the cap is crossed, so the file stays bounded on long-lived stores.
func TestUsageSessionsBounded(t *testing.T) {
	store := recallTestStore(t)
	for i := range usageSessionsPerMemory + 8 {
		RecordUsageRecall(store, fmtSession(i), []string{"id-x"})
	}
	path := UsageLedgerPath(store)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var data usageFile
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	e := data.Memories["id-x"]
	if len(e.Sessions) > usageSessionsPerMemory {
		t.Fatalf("session ledger = %d rows, want ≤%d", len(e.Sessions), usageSessionsPerMemory)
	}
	if e.RecallTotal != usageSessionsPerMemory+8 {
		t.Fatalf("memory-level total must keep every event: %d, want %d", e.RecallTotal, usageSessionsPerMemory+8)
	}
}

func fmtSession(i int) string {
	return "sess-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
}
