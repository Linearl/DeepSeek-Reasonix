// tl1733 Block1 item 5: the recall-injection side of the usage ledger. A hit
// injection records (memory, session) recall counts into usage-counts.json —
// outside the compaction refresh chain; suppressed recalls record nothing.
package control

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/memory"
)

func TestRecallInjectionRecordsUsage(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "work")
	store := memory.StoreFor(root, workDir)
	if err := os.MkdirAll(store.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fact := "---\nid: mem-probe\nname: usage-probe\ntitle: Usage probe\ndescription: probes the usage ledger\nkeywords: probe\nmetadata:\n  type: project\n  scope: project\ntriggers: tl1733probe\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(store.Dir, "usage-probe.md"), []byte(fact), 0o644); err != nil {
		t.Fatal(err)
	}

	mm := newMemoryManager(memory.Load(memory.Options{CWD: workDir, UserDir: root}))
	result := mm.recall("sess-tl1733", "tl1733probe")
	if len(result.Hits) == 0 {
		t.Fatalf("precondition: the probe fact must be recalled, suppressed=%q", result.Suppressed)
	}

	raw, err := os.ReadFile(memory.UsageLedgerPath(store))
	if err != nil {
		t.Fatalf("usage ledger must persist on recall injection: %v", err)
	}
	var data struct {
		Memories map[string]struct {
			RecallTotal int `json:"recall_total"`
			Sessions    map[string]struct {
				Recall int `json:"recall"`
			} `json:"sessions"`
		} `json:"memories"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	entry, ok := data.Memories["mem-probe"]
	if !ok || entry.RecallTotal != 1 {
		t.Fatalf("recall count missing: %+v", data.Memories)
	}
	if entry.Sessions["sess-tl1733"].Recall != 1 {
		t.Fatalf("(memory, session) pair not recorded: %+v", entry.Sessions)
	}

	// Suppressed recall: no hits → no ledger writes.
	before := string(raw)
	suppressed := mm.recall("sess-tl1733", "继续")
	if len(suppressed.Hits) != 0 && suppressed.Suppressed == "" {
		t.Fatalf("unexpected second hit changed the test premise: %+v", suppressed.Hits)
	}
	after, err := os.ReadFile(memory.UsageLedgerPath(store))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != before {
		t.Fatalf("suppressed recall must not write the ledger")
	}
}
