package sessioninbox

// tl1288: a cross-process recovery pass with nothing to recover (recovered==0,
// items empty or consumed-residue only) used to leave the OLD RunID on disk —
// the in-memory adoption was never committed — so every later
// loadOrInitLocked re-entered the recovery branch: one cross_process_recovery
// pass + one snapshot line every ~4s for as long as the process lived
// (production desktop.log 20261031: the loop ran all night against a manifest
// whose runId was pinned to a process that no longer existed). The fix adopts
// the fresh RunID durably even when the scan recovers nothing, making the
// pass once-per-process instead of once-per-tick.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readManifestRunID(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var man struct {
		RunID string `json:"runId"`
	}
	if err := json.Unmarshal(data, &man); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return man.RunID
}

func foreignRunIDManifest(t *testing.T, st *Store) {
	t.Helper()
	manPath := filepath.Join(st.Dir(), "manifest.json")
	data, err := os.ReadFile(manPath)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), st.runID, "foreign-run-id-0000", 1))
	if err := os.WriteFile(manPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestRunIDAdoptionCommitsWithoutRecovery: a foreign RunID with only consumed
// residue (recovered==0) must still be adopted durably — the manifest on disk
// carries the fresh RunID after the first pass, and later passes are no-ops.
func TestRunIDAdoptionCommitsWithoutRecovery(t *testing.T) {
	snapshotPath := withRecoverySnapshotPath(t)
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")

	st, err := Open(session, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	// One consumed steer: durable manifest, but P15 keeps it out of recovered.
	rec, err := st.Enqueue(EnqueueRequest{Envelope: PromptEnvelope{SubmitText: "already delivered"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetState(rec.ItemID, StateSteerConsumed, ""); err != nil {
		t.Fatal(err)
	}
	st.Close()

	foreignRunIDManifest(t, st)
	CloseRecoverySnapshot()
	s2, err := Open(session, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	// The adoption is durable: the manifest on disk now carries THIS process's
	// RunID even though nothing was recovered.
	if got := readManifestRunID(t, s2.Dir()); got != s2.runID {
		t.Fatalf("manifest runId = %q, want the fresh run id adopted durably (got the foreign value back — the loop would re-trigger every pass)", got)
	}

	// Convergence: repeated snapshots re-open the manifest, but the RunID
	// matches now, so no further recovery passes fire and no further snapshot
	// lines appear.
	for i := 0; i < 3; i++ {
		_ = s2.Snapshot()
	}
	lines := readRecoverySnapshotLines(t, snapshotPath)
	if len(lines) != 1 {
		t.Fatalf("snapshot lines = %d, want exactly 1 (one pass, then convergence; >1 means the RunID loop is back)", len(lines))
	}
	if got := lines[0]["recovered"].(float64); got != 0 {
		t.Fatalf("recovered = %v, want 0", got)
	}

	// Consumed residue must still not pause the inbox (缺陷A 场景① unchanged).
	snap := s2.Snapshot()
	if snap.Paused || snap.Recovered {
		t.Fatalf("consumed-residue-only adoption must not pause, got %+v", snap)
	}
}
