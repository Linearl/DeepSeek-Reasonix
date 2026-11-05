package agent

import (
	"errors"
	"os"
	"testing"

	"reasonix/internal/provider"
)

// TestSnapshotConflictCarriesBothSidesIdentity pins the tl1443 evidence on a
// genuine diverged conflict: the snapshot digest is the live transcript, the
// base digest is the live session's own last save, the disk digest/ledger are
// the second writer's commit, and the ledger writer is this process.
func TestSnapshotConflictCarriesBothSidesIdentity(t *testing.T) {
	path := schemaOneSessionPath(t, "identity.jsonl")
	live := NewSession("sys")
	live.Add(provider.Message{Role: provider.RoleUser, Content: "m1"})
	if err := live.SaveSnapshot(path); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	baseDigest, err := live.ContentDigest()
	if err != nil {
		t.Fatal(err)
	}

	other, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	other.Add(provider.Message{Role: provider.RoleUser, Content: "other-tail"})
	if err := other.SaveSnapshot(path); err != nil {
		t.Fatalf("second writer save: %v", err)
	}
	diskDigest, err := other.ContentDigest()
	if err != nil {
		t.Fatal(err)
	}

	live.Add(provider.Message{Role: provider.RoleUser, Content: "live-tail"})
	err = live.SaveSnapshot(path)
	var conflict *SessionSnapshotConflictError
	if !errors.As(err, &conflict) || conflict == nil || conflict.Kind != SessionSnapshotConflictDiverged {
		t.Fatalf("live save err = %v, want diverged conflict", err)
	}
	snapshotDigest, err := live.ContentDigest()
	if err != nil {
		t.Fatal(err)
	}
	got := conflict.Identity
	if got.SnapshotDigest != snapshotDigest {
		t.Fatalf("SnapshotDigest = %q, want live transcript digest %q", got.SnapshotDigest, snapshotDigest)
	}
	if got.BaseDigest != baseDigest {
		t.Fatalf("BaseDigest = %q, want the live session's baseline %q", got.BaseDigest, baseDigest)
	}
	if got.DiskDigest != diskDigest {
		t.Fatalf("DiskDigest = %q, want the second writer's transcript %q", got.DiskDigest, diskDigest)
	}
	if got.LedgerDigest != diskDigest {
		t.Fatalf("LedgerDigest = %q, want the committed disk digest %q", got.LedgerDigest, diskDigest)
	}
	if got.LedgerWriter != SessionWriterID() {
		t.Fatalf("LedgerWriter = %q, want this process %q", got.LedgerWriter, SessionWriterID())
	}
	if origin, pid := LedgerWriterOrigin(got.LedgerWriter); origin != LedgerWriterSelf || pid != os.Getpid() {
		t.Fatalf("LedgerWriterOrigin = (%s, %d), want (%s, %d)", origin, pid, LedgerWriterSelf, os.Getpid())
	}
	if conflict.Error() == "" || conflict.BaseRevision >= conflict.DiskRevision {
		t.Fatalf("conflict ledger = base %d disk %d", conflict.BaseRevision, conflict.DiskRevision)
	}
}

// TestStalePrefixConflictCarriesIdentity covers the second construction site:
// a live session that fell behind a pure append on disk.
func TestStalePrefixConflictCarriesIdentity(t *testing.T) {
	path := schemaOneSessionPath(t, "stale.jsonl")
	live := NewSession("sys")
	live.Add(provider.Message{Role: provider.RoleUser, Content: "m1"})
	if err := live.SaveSnapshot(path); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	other, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	other.Add(provider.Message{Role: provider.RoleUser, Content: "m2"})
	if err := other.SaveSnapshot(path); err != nil {
		t.Fatalf("second writer save: %v", err)
	}
	live.Replace(append([]provider.Message(nil), live.Snapshot()[:1]...))
	err = live.SaveRewrite(path)
	var conflict *SessionSnapshotConflictError
	if !errors.As(err, &conflict) || conflict == nil || conflict.Kind != SessionSnapshotConflictStalePrefix {
		t.Fatalf("stale save err = %v, want stale-prefix conflict", err)
	}
	if conflict.Identity.SnapshotDigest == "" || conflict.Identity.DiskDigest == "" || conflict.Identity.LedgerWriter == "" {
		t.Fatalf("stale-prefix identity incomplete: %+v", conflict.Identity)
	}
	if conflict.Identity.SnapshotDigest == conflict.Identity.DiskDigest {
		t.Fatalf("stale-prefix sides must differ: %+v", conflict.Identity)
	}
}

func TestLedgerWriterOriginClassifiesWriterIDs(t *testing.T) {
	selfHost, _, ok := splitSessionWriterID(SessionWriterID())
	if !ok {
		t.Fatalf("own writer id %q does not parse", SessionWriterID())
	}
	cases := []struct {
		name   string
		id     string
		origin string
		pid    int
	}{
		{"empty", "", LedgerWriterUnknown, 0},
		{"self", SessionWriterID(), LedgerWriterSelf, os.Getpid()},
		{"same host other pid", selfHost + "-424242-0011223344556677", LedgerWriterOtherProcess, 424242},
		{"dashed foreign host", "build-box-07-31337-deadbeefdeadbeef", LedgerWriterOtherHost, 31337},
		{"no pid", "garbage", LedgerWriterUnknown, 0},
		{"bad pid", selfHost + "-notapid-00", LedgerWriterUnknown, 0},
	}
	for _, tc := range cases {
		origin, pid := LedgerWriterOrigin(tc.id)
		if origin != tc.origin || pid != tc.pid {
			t.Fatalf("%s: LedgerWriterOrigin(%q) = (%s, %d), want (%s, %d)", tc.name, tc.id, origin, pid, tc.origin, tc.pid)
		}
	}
	if got := ShortDigest("0123456789abcdef0123"); got != "0123456789ab" {
		t.Fatalf("ShortDigest = %q", got)
	}
	if got := ShortDigest("abc"); got != "abc" {
		t.Fatalf("ShortDigest(short) = %q", got)
	}
}
