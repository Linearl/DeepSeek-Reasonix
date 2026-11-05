package agent

import (
	"errors"
	"testing"

	"reasonix/internal/provider"
)

// TestSaveAdoptOverStaleDiskTakeoverCommits is the tl1409 acceptance shape:
// a second writer advanced the disk ledger and died (disk ahead of the local
// base, disk behind the live snapshot, no shared prefix). The takeover must
// persist the live transcript past the dead writer's revision with no second
// writer residue left behind.
func TestSaveAdoptOverStaleDiskTakeoverCommits(t *testing.T) {
	path := schemaOneSessionPath(t, "takeover.jsonl")
	live := NewSession("sys")
	bindSessionWriter(t, live, path)
	live.Add(provider.Message{Role: provider.RoleUser, Content: "m1"})
	live.Add(provider.Message{Role: provider.RoleAssistant, Content: "m2"})
	live.Add(provider.Message{Role: provider.RoleUser, Content: "m3"})
	if err := live.SaveSnapshot(path); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	baseRevision, _, err := sessionContentRevision(path)
	if err != nil {
		t.Fatalf("sessionContentRevision: %v", err)
	}

	// The second writer: loads the same file, adds its own tail, lands one
	// revision ahead, then stops forever.
	dead := func() *Session {
		loaded, err := LoadSession(path)
		if err != nil {
			t.Fatalf("LoadSession: %v", err)
		}
		return loaded
	}()
	dead.Add(provider.Message{Role: provider.RoleUser, Content: "dead-writer-tail"})
	if err := dead.SaveSnapshot(path); err != nil {
		t.Fatalf("dead writer save: %v", err)
	}
	diskRevision, _, err := sessionContentRevision(path)
	if err != nil {
		t.Fatalf("sessionContentRevision after dead writer: %v", err)
	}
	if diskRevision != baseRevision+1 {
		t.Fatalf("disk revision = %d, want %d", diskRevision, baseRevision+1)
	}

	// The live session keeps growing from its old baseline and hits the
	// permanent diverged refusal.
	live.Add(provider.Message{Role: provider.RoleUser, Content: "live4"})
	live.Add(provider.Message{Role: provider.RoleAssistant, Content: "live5"})
	err = live.SaveSnapshot(path)
	var conflict *SessionSnapshotConflictError
	if !errors.As(err, &conflict) || conflict == nil || conflict.Kind != SessionSnapshotConflictDiverged {
		t.Fatalf("live save err = %v, want diverged conflict", err)
	}
	if conflict.BaseRevision != baseRevision || conflict.DiskRevision != diskRevision {
		t.Fatalf("conflict revisions = (base %d, disk %d), want (%d, %d)", conflict.BaseRevision, conflict.DiskRevision, baseRevision, diskRevision)
	}

	if err := live.SaveAdoptOverStaleDisk(path, conflict); err != nil {
		t.Fatalf("SaveAdoptOverStaleDisk: %v", err)
	}

	// No second-writer residue: disk holds exactly the live transcript, the
	// ledger advanced past the dead revision, and the next save is a no-op.
	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("reload after takeover: %v", err)
	}
	// Snapshot includes the seeded system message at index 0.
	if got := loaded.Snapshot(); len(got) != 6 || got[5].Content != "live5" {
		t.Fatalf("reload = %d messages, want the live transcript (system + 5)", len(got))
	}
	finalRevision, ledgerDigest, err := sessionContentRevision(path)
	if err != nil {
		t.Fatalf("sessionContentRevision after takeover: %v", err)
	}
	if finalRevision != diskRevision+1 {
		t.Fatalf("ledger revision after takeover = %d, want %d", finalRevision, diskRevision+1)
	}
	digest, err := digestSessionMessages(live.Snapshot())
	if err != nil {
		t.Fatalf("digestSessionMessages: %v", err)
	}
	if ledgerDigest != digestString(digest) {
		t.Fatal("ledger digest after takeover does not describe the live transcript")
	}
	if live.snapshotUpToDate(path) {
		// The takeover must have advanced the live session's own persist
		// baseline too — a follow-up save reclassifies as a no-op instead of
		// re-litigating the conflict.
		return
	}
	if err := live.SaveSnapshot(path); err != nil {
		t.Fatalf("follow-up save: %v", err)
	}
}

// TestSaveAdoptOverStaleDiskRefusedWhenDiskAdvanced pins the disk-still-stale
// guard: between arming and committing, the "dead" writer wrote again. The
// takeover must refuse and leave the diverged conflict intact.
func TestSaveAdoptOverStaleDiskRefusedWhenDiskAdvanced(t *testing.T) {
	path := schemaOneSessionPath(t, "takeover-refused.jsonl")
	live := NewSession("sys")
	bindSessionWriter(t, live, path)
	live.Add(provider.Message{Role: provider.RoleUser, Content: "m1"})
	live.Add(provider.Message{Role: provider.RoleAssistant, Content: "m2"})
	if err := live.SaveSnapshot(path); err != nil {
		t.Fatalf("initial save: %v", err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	loaded.Add(provider.Message{Role: provider.RoleUser, Content: "dead-writer-tail"})
	if err := loaded.SaveSnapshot(path); err != nil {
		t.Fatalf("dead writer save: %v", err)
	}
	diskRevision, _, err := sessionContentRevision(path)
	if err != nil {
		t.Fatalf("sessionContentRevision: %v", err)
	}

	live.Add(provider.Message{Role: provider.RoleUser, Content: "live3"})
	live.Add(provider.Message{Role: provider.RoleAssistant, Content: "live4"})
	err = live.SaveSnapshot(path)
	var conflict *SessionSnapshotConflictError
	if !errors.As(err, &conflict) || conflict == nil {
		t.Fatalf("live save err = %v, want diverged conflict", err)
	}

	// The other writer wakes up and advances the disk past the fingerprint.
	loaded.Add(provider.Message{Role: provider.RoleUser, Content: "dead-writes-again"})
	if err := loaded.SaveSnapshot(path); err != nil {
		t.Fatalf("dead writer second save: %v", err)
	}

	if err := live.SaveAdoptOverStaleDisk(path, conflict); err == nil {
		t.Fatal("SaveAdoptOverStaleDisk succeeded past a moved disk, want refusal")
	}
	// The on-disk transcript still belongs to the other writer; nothing was
	// overwritten.
	finalRevision, _, err := sessionContentRevision(path)
	if err != nil {
		t.Fatalf("sessionContentRevision: %v", err)
	}
	if finalRevision != diskRevision+1 {
		t.Fatalf("disk revision = %d, want %d (untouched)", finalRevision, diskRevision+1)
	}
}

// TestSaveAdoptOverStaleDiskRefusedBadShapes pins the precondition gates:
// only a diverged conflict with the disk revision ahead and the live snapshot
// ahead on messages may arm a takeover.
func TestSaveAdoptOverStaleDiskRefusedBadShapes(t *testing.T) {
	path := schemaOneSessionPath(t, "takeover-shapes.jsonl")
	live := NewSession("sys")
	bindSessionWriter(t, live, path)
	live.Add(provider.Message{Role: provider.RoleUser, Content: "m1"})
	if err := live.SaveSnapshot(path); err != nil {
		t.Fatalf("initial save: %v", err)
	}

	cases := []struct {
		name     string
		conflict *SessionSnapshotConflictError
	}{
		{"nil conflict", nil},
		{"stale prefix kind", &SessionSnapshotConflictError{Path: path, Kind: SessionSnapshotConflictStalePrefix, BaseRevision: 1, DiskRevision: 2, ExistingMessages: 2, SnapshotMessages: 3}},
		{"disk not ahead", &SessionSnapshotConflictError{Path: path, Kind: SessionSnapshotConflictDiverged, BaseRevision: 2, DiskRevision: 2, ExistingMessages: 2, SnapshotMessages: 3}},
		{"snapshot not ahead", &SessionSnapshotConflictError{Path: path, Kind: SessionSnapshotConflictDiverged, BaseRevision: 1, DiskRevision: 2, ExistingMessages: 5, SnapshotMessages: 5}},
		{"different path", &SessionSnapshotConflictError{Path: "other-session.jsonl", Kind: SessionSnapshotConflictDiverged, BaseRevision: 1, DiskRevision: 2, ExistingMessages: 2, SnapshotMessages: 3}},
	}
	for _, tc := range cases {
		if err := live.SaveAdoptOverStaleDisk(path, tc.conflict); err == nil {
			t.Fatalf("%s: SaveAdoptOverStaleDisk succeeded, want precondition refusal", tc.name)
		}
	}
	// Nothing armed leaked into ordinary saves.
	if err := live.SaveSnapshot(path); err != nil {
		t.Fatalf("ordinary save after refused takeovers: %v", err)
	}
}
