package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/store"
)

func TestReconcileSessionSidecarsRemovesUnlockedArtifacts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, []byte(`{"role":"user","content":"hello"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, sidecar := range []string{path + ".lock", path + ".lease.lock", path + ".lease.json"} {
		if err := os.WriteFile(sidecar, []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", sidecar, err)
		}
	}

	if err := ReconcileSessionSidecars(dir); err != nil {
		t.Fatalf("ReconcileSessionSidecars: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("session transcript removed: %v", err)
	}
	for _, sidecar := range []string{path + ".lock", path + ".lease.lock", path + ".lease.json"} {
		if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
			t.Fatalf("%s exists after sidecar cleanup (err=%v)", sidecar, err)
		}
	}
}


func TestReconcileSessionSidecarsKeepsLiveLocks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, []byte(`{"role":"user","content":"hello"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockSessionFile(path)
	if err != nil {
		t.Fatalf("lockSessionFile: %v", err)
	}
	defer unlock()
	lease, err := TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("TryAcquireSessionLease: %v", err)
	}
	defer lease.Release()

	if err := ReconcileSessionSidecars(dir); err != nil {
		t.Fatalf("ReconcileSessionSidecars: %v", err)
	}
	for _, sidecar := range []string{path + ".lock", path + ".lease.lock"} {
		if _, err := os.Stat(sidecar); err != nil {
			t.Fatalf("%s missing while lock is live: %v", sidecar, err)
		}
	}
}


// TestReconcileSessionSidecarsKeepsFlockOnlyLocks proves the file lock alone
// protects a writer from cleanup: CLI-style savers hold the .lock flock while
// writing without ever taking a session lease.
func TestReconcileSessionSidecarsKeepsFlockOnlyLocks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, []byte(`{"role":"user","content":"hello"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockSessionFile(path)
	if err != nil {
		t.Fatalf("lockSessionFile: %v", err)
	}
	if err := ReconcileSessionSidecars(dir); err != nil {
		t.Fatalf("ReconcileSessionSidecars: %v", err)
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Fatalf(".lock removed while a lock-only writer holds it: %v", err)
	}
	unlock()
	if err := ReconcileSessionSidecars(dir); err != nil {
		t.Fatalf("ReconcileSessionSidecars after unlock: %v", err)
	}
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Fatalf(".lock survived cleanup after release (err=%v)", err)
	}
}


// TestReconcileSessionSidecarsRenamesOverlongSessionFiles covers the
// migration for transcripts left behind by the unbounded recovery cascade
// (#5923): names so long their lock/lease sidecars could not be created. The
// conversation bytes must survive under a bounded name, branch meta must move
// with its ID rewritten, and children must be re-parented onto the new ID.
func TestReconcileSessionSidecarsRenamesOverlongSessionFiles(t *testing.T) {
	dir := t.TempDir()
	longID := strings.Repeat("p", 240) // 246-byte basename: .lock fits, .lease.lock does not
	hugeID := strings.Repeat("q", 248) // 254-byte basename: no sidecar fits at all
	oldLong := filepath.Join(dir, longID+".jsonl")
	oldHuge := filepath.Join(dir, hugeID+".jsonl")
	content := `{"role":"system","content":"sys"}` + "\n" + `{"role":"user","content":"hello"}` + "\n"
	for _, p := range []string{oldLong, oldHuge} {
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(oldLong+".lock", []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveBranchMeta(oldLong, BranchMeta{Name: "长会话", ParentID: "root-branch"}); err != nil {
		t.Fatalf("SaveBranchMeta: %v", err)
	}
	childPath := filepath.Join(dir, "child.jsonl")
	if err := os.WriteFile(childPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveBranchMeta(childPath, BranchMeta{Name: "child", ParentID: longID}); err != nil {
		t.Fatalf("SaveBranchMeta child: %v", err)
	}

	if err := ReconcileSessionSidecars(dir); err != nil {
		t.Fatalf("ReconcileSessionSidecars: %v", err)
	}

	for _, gone := range []string{oldLong, oldHuge, oldLong + ".lock", oldLong + ".meta"} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s still present after rename (err=%v)", filepath.Base(gone), err)
		}
	}
	newLongID := recoveryParentStem(longID)
	newLong := filepath.Join(dir, newLongID+".jsonl")
	newHuge := filepath.Join(dir, recoveryParentStem(hugeID)+".jsonl")
	for _, p := range []string{newLong, newHuge} {
		if base := filepath.Base(p); len(base) > maxSessionBasenameBytes {
			t.Fatalf("renamed basename %q length %d exceeds bound %d", base, len(base), maxSessionBasenameBytes)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read renamed transcript: %v", err)
		}
		if string(b) != content {
			t.Fatalf("transcript content changed by rename: %q", b)
		}
	}
	meta, ok, err := LoadBranchMeta(newLong)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta renamed ok=%v err=%v", ok, err)
	}
	if meta.ID != newLongID {
		t.Fatalf("migrated meta ID = %q, want %q", meta.ID, newLongID)
	}
	if meta.Name != "长会话" || meta.ParentID != "root-branch" {
		t.Fatalf("migrated meta lost fields: %+v", meta)
	}
	childMeta, ok, err := LoadBranchMeta(childPath)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta child ok=%v err=%v", ok, err)
	}
	if childMeta.ParentID != newLongID {
		t.Fatalf("child ParentID = %q, want re-parented %q", childMeta.ParentID, newLongID)
	}

	before, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ReconcileSessionSidecars(dir); err != nil {
		t.Fatalf("ReconcileSessionSidecars rerun: %v", err)
	}
	after, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("rerun changed the directory: before=%d entries, after=%d", len(before), len(after))
	}
}


// TestReconcileOverlongRenameStillReparentsWhenSidecarMigrationFails pins the
// point-of-no-return contract: once the transcript rename lands, the mapping
// must be committed — children re-parented, error surfaced as a warning —
// because the old name is gone and no later run can reconstruct it.
func TestReconcileOverlongRenameStillReparentsWhenSidecarMigrationFails(t *testing.T) {
	dir := t.TempDir()
	longID := strings.Repeat("m", 240)
	oldPath := filepath.Join(dir, longID+".jsonl")
	content := `{"role":"user","content":"hello"}` + "\n"
	if err := os.WriteFile(oldPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveBranchMeta(oldPath, BranchMeta{Name: "keep", ParentID: "root"}); err != nil {
		t.Fatalf("SaveBranchMeta: %v", err)
	}
	childPath := filepath.Join(dir, "child.jsonl")
	if err := os.WriteFile(childPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveBranchMeta(childPath, BranchMeta{Name: "child", ParentID: longID}); err != nil {
		t.Fatalf("SaveBranchMeta child: %v", err)
	}

	newID := recoveryParentStem(longID)
	newPath := filepath.Join(dir, newID+".jsonl")
	// Sabotage the meta migration: its destination path is a directory.
	if err := os.Mkdir(newPath+".meta", 0o755); err != nil {
		t.Fatal(err)
	}

	if err := ReconcileSessionSidecars(dir); err == nil {
		t.Fatal("expected the sabotaged meta migration to surface an error")
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("renamed transcript missing after partial failure: %v", err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old transcript still present (err=%v)", err)
	}
	childMeta, ok, err := LoadBranchMeta(childPath)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta child ok=%v err=%v", ok, err)
	}
	if childMeta.ParentID != newID {
		t.Fatalf("child ParentID = %q, want %q despite sidecar failure", childMeta.ParentID, newID)
	}
	// The old meta stays behind as the durable copy of the un-migrated fields.
	if _, err := os.Stat(oldPath + ".meta"); err != nil {
		t.Fatalf("old meta lost though its migration failed: %v", err)
	}
}


// TestReconcileOverlongMigratesDiagnosticSidecars pins two halves of one fix:
// overlong-name reconciliation must skip .events.jsonl / .conflicts.jsonl
// sidecars instead of renaming each into a fake session with fabricated meta,
// and the owning transcript's rename must carry those sidecars along instead
// of orphaning them under the retired stem.
func TestReconcileOverlongMigratesDiagnosticSidecars(t *testing.T) {
	dir := t.TempDir()
	// 236-byte transcript name: past the 224 reconcile bound, while its
	// .events.jsonl (243) and .conflicts.jsonl (246) still fit under 255 —
	// exactly the window where the old suffix filter mistook them for
	// overlong sessions.
	id := strings.Repeat("s", 230)
	oldPath := filepath.Join(dir, id+".jsonl")
	content := `{"role":"system","content":"sys"}` + "\n" + `{"role":"user","content":"hello"}` + "\n"
	if err := os.WriteFile(oldPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.SessionEventLog(oldPath),
		[]byte(`{"schema_version":1,"type":"replace","messages":[{"role":"user","content":"hello"}]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.SessionConflictLog(oldPath),
		[]byte(`{"outcome":"forked_recovery_branch"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.SessionRecoveryState(oldPath),
		[]byte(`{"tasks":{"root":{"phase":"diagnosing"}}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The salvage sidecar holds raw session bytes; orphaning it under the
	// retired stem would leave unreachable transcript content behind (#6613
	// review follow-up).
	if err := os.WriteFile(store.SessionEventLogDamaged(oldPath),
		[]byte(`{"damaged_tail":true}`+"\ntorn bytes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ReconcileSessionSidecars(dir); err != nil {
		t.Fatalf("ReconcileSessionSidecars: %v", err)
	}

	newPath := filepath.Join(dir, recoveryParentStem(id)+".jsonl")
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("renamed transcript missing: %v", err)
	}
	if _, err := os.Stat(store.SessionEventLog(newPath)); err != nil {
		t.Fatalf("event log not migrated with the rename: %v", err)
	}
	if _, err := os.Stat(store.SessionEventLogDamaged(newPath)); err != nil {
		t.Fatalf("damaged salvage sidecar not migrated with the rename: %v", err)
	}
	if _, err := os.Stat(store.SessionConflictLog(newPath)); err != nil {
		t.Fatalf("conflict log not migrated with the rename: %v", err)
	}
	if _, err := os.Stat(store.SessionRecoveryState(newPath)); err != nil {
		t.Fatalf("recovery state not migrated with the rename: %v", err)
	}
	for _, gone := range []string{oldPath, store.SessionEventLog(oldPath), store.SessionEventLogDamaged(oldPath), store.SessionConflictLog(oldPath), store.SessionRecoveryState(oldPath)} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s still present under the retired stem (err=%v)", filepath.Base(gone), err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if store.IsSessionTranscriptName(e.Name()) && e.Name() != filepath.Base(newPath) {
			t.Fatalf("reconcile fabricated an extra session from a sidecar: %s", e.Name())
		}
	}
}

