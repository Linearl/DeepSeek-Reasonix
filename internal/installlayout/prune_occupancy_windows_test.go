//go:build windows

package installlayout

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Task 1259 acceptance at the prune face: a tree a live process executes from
// is skipped (kept, reported, ledger-counted), and once the process is gone
// the tree prunes normally. This is the incident scenario — pruning while an
// instance runs — replayed against a real child process.

func writeOccupiedPruneFixture(t *testing.T, active string) (root, versionsDir, occupiedVersion string) {
	t.Helper()
	root = t.TempDir()
	versionsDir = filepath.Join(root, VersionsDirName)
	for _, v := range []string{"v1.38.0", "v1.38.1", "v1.38.2", "v1.38.3-20261029-0900"} {
		dir := filepath.Join(versionsDir, v)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	occupiedVersion = "v1.38.1" // beyond keep=2, not the pointer target
	if active != "" {
		ptr := CurrentPointer{SchemaVersion: CurrentSchemaVersion, ActiveVersion: active, ActiveDir: VersionDirRelative(active)}
		body, err := json.Marshal(ptr)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, CurrentFileName), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, versionsDir, occupiedVersion
}

func TestPruneVersionTreesKeepsOccupiedTreeAndEscalates(t *testing.T) {
	root, versionsDir, occupiedVersion := writeOccupiedPruneFixture(t, "v1.38.0")
	pid := spawnTreeInstance(t, versionsDir, occupiedVersion)
	treeDir := filepath.Join(versionsDir, occupiedVersion)

	// Three consecutive prune passes = the escalation ladder. keep=2 keeps the
	// two newest trees; v1.38.1 is beyond it and occupied by the child.
	for round := 1; round <= OccupiedEscalateAfterRounds; round++ {
		pruned, retained, err := PruneVersionTrees(root, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(pruned) != 0 {
			t.Fatalf("round %d: occupied tree must not prune, got pruned=%v", round, pruned)
		}
		if len(retained) != 1 || retained[0].Version != occupiedVersion {
			t.Fatalf("round %d: retained=%v want exactly %s", round, retained, occupiedVersion)
		}
		if retained[0].Rounds != round {
			t.Fatalf("round %d: ledger rounds=%d want %d", round, retained[0].Rounds, round)
		}
		if retained[0].Escalated != (round == OccupiedEscalateAfterRounds) {
			t.Fatalf("round %d: escalated=%v want %v", round, retained[0].Escalated, round == OccupiedEscalateAfterRounds)
		}
		if round == OccupiedEscalateAfterRounds {
			if len(retained[0].PIDs) != 1 || retained[0].PIDs[0] != pid {
				t.Fatalf("escalated entry must attribute the live pid %d, got %v", pid, retained[0].PIDs)
			}
		}
		// The anti-ghost guarantee: the tree and its executable stay intact
		// while the process lives on them.
		if _, err := os.Stat(filepath.Join(treeDir, DesktopBinaryName())); err != nil {
			t.Fatalf("round %d: occupied tree's exe must survive untouched: %v", round, err)
		}
	}

	// The ledger persisted the escalation state.
	data, err := os.ReadFile(filepath.Join(versionsDir, occupancyLedgerName))
	if err != nil {
		t.Fatalf("occupied rounds must persist in the ledger: %v", err)
	}
	var ledger occupancyLedgerFile
	if err := json.Unmarshal(data, &ledger); err != nil {
		t.Fatal(err)
	}
	if ledger.Rounds[occupiedVersion] != OccupiedEscalateAfterRounds {
		t.Fatalf("ledger rounds=%d want %d", ledger.Rounds[occupiedVersion], OccupiedEscalateAfterRounds)
	}

	// The instance dies: the next pass prunes the tree normally and the
	// ledger residue disappears with it.
	if err := killPID(pid); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(probeVersionTreeProcesses(versionsDir, occupiedVersion)) > 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	pruned, retained, err := PruneVersionTrees(root, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(pruned) != 1 || pruned[0] != occupiedVersion || len(retained) != 0 {
		t.Fatalf("after exit: pruned=%v retained=%v want exactly %s pruned", pruned, retained, occupiedVersion)
	}
	if _, err := os.Stat(treeDir); !os.IsNotExist(err) {
		t.Fatalf("released tree must be gone, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(versionsDir, occupancyLedgerName)); !os.IsNotExist(err) {
		t.Fatalf("ledger must be removed once nothing is occupied, got %v", err)
	}
}

func TestPruneVersionTreesOccupancyLedgerReset(t *testing.T) {
	root, versionsDir, _ := writeOccupiedPruneFixture(t, "v1.38.0")
	// A stale ledger naming a tree that no longer exists must not survive a
	// pass (escalation state is per tree, not global).
	ledger := occupancyLedgerFile{SchemaVersion: occupancyLedgerSchema, Rounds: map[string]int{"v1.38.1": OccupiedEscalateAfterRounds}}
	if err := os.WriteFile(filepath.Join(versionsDir, occupancyLedgerName), mustJSON(t, ledger), 0o644); err != nil {
		t.Fatal(err)
	}
	pruned, retained, err := PruneVersionTrees(root, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(pruned) != 1 || pruned[0] != "v1.38.1" || len(retained) != 0 {
		t.Fatalf("no live process: the tree must prune and the stale ledger drop, got pruned=%v retained=%v", pruned, retained)
	}
	if _, err := os.Stat(filepath.Join(versionsDir, occupancyLedgerName)); !os.IsNotExist(err) {
		t.Fatalf("stale-only ledger must be removed, got %v", err)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
