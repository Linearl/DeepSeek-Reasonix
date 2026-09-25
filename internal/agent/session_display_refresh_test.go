package agent

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// Task 123 tail: the cold-path incremental index refresh. The field sample
// (2026-09-25, 7 sessions >50MiB) showed the exact stale shape this covers:
// the read model (jsonl) already carries the tail while the display index
// identity lags (warn-only refresh), so Validate failed and the request paid
// the authoritative full replay (measured 1290ms on a 15MiB transcript).

// writeLedger records revision+digest the way save's reservation handoff
// leaves it: a plain branch-meta write with SchemaVersion 0. The
// recordSessionContentRevision reservation contract is save-internal (the
// reservation must be pre-placed by the decision layer), so the test writes
// the resulting state directly.
func writeLedger(t *testing.T, path string, digest [sha256.Size]byte, revision int64) {
	t.Helper()
	m, _, err := LoadBranchMeta(path)
	if err != nil {
		t.Fatalf("load branch meta: %v", err)
	}
	m.Revision = revision
	m.ContentDigest = digestString(digest)
	m.SchemaVersion = 0
	if err := SaveBranchMeta(path, m); err != nil {
		t.Fatalf("save branch meta: %v", err)
	}
}

// staleIndexSession builds the field shape: read model + ledger + event index
// all advanced by an append, display index deliberately NOT refreshed.
func staleIndexSession(t *testing.T) (path string, old []provider.Message, tail []provider.Message) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "session.jsonl")
	// The event index writer no-ops (and removes a stale index) when the event
	// log does not exist — create it so writeSessionEventIndex actually lands.
	if err := os.WriteFile(store.SessionEventLog(path), nil, 0o644); err != nil {
		t.Fatalf("touch event log: %v", err)
	}
	all := displayIndexTestMessages()
	old = all[:4]
	tail = all[4:]

	if err := writeSessionMessages(path, old); err != nil {
		t.Fatalf("writeSessionMessages(old): %v", err)
	}
	digest1, err := digestSessionMessages(old)
	if err != nil {
		t.Fatalf("digest(old): %v", err)
	}
	writeLedger(t, path, digest1, 1)
	if err := WriteSessionDisplayIndex(store.SessionDisplayIndex(path), BuildSessionDisplayIndex(old, 1, true, digest1)); err != nil {
		t.Fatalf("write display index: %v", err)
	}
	if err := writeSessionEventIndex(path, old, digest1, 1); err != nil {
		t.Fatalf("write event index 1: %v", err)
	}

	// The append that advances everything except the display index.
	full := append(append([]provider.Message{}, old...), tail...)
	if err := writeSessionMessages(path, full); err != nil {
		t.Fatalf("writeSessionMessages(full): %v", err)
	}
	digest2, err := digestSessionMessages(full)
	if err != nil {
		t.Fatalf("digest(full): %v", err)
	}
	writeLedger(t, path, digest2, 2)
	if err := writeSessionEventIndex(path, full, digest2, 2); err != nil {
		t.Fatalf("write event index 2: %v", err)
	}
	return path, old, tail
}

func TestRefreshSessionDisplayIndexFromReadModelExtendsTail(t *testing.T) {
	path, old, tail := staleIndexSession(t)

	refreshed, err := RefreshSessionDisplayIndexFromReadModel(path)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !refreshed {
		t.Fatal("Refresh returned false on the canonical stale shape (read model ahead, index behind)")
	}

	idx, err := LoadSessionDisplayIndex(store.SessionDisplayIndex(path))
	if err != nil || idx == nil {
		t.Fatalf("Load after refresh: idx=%v err=%v", idx, err)
	}
	wantCount := len(old) + len(tail)
	if idx.MessageCount != wantCount {
		t.Fatalf("MessageCount = %d, want %d", idx.MessageCount, wantCount)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if idx.TranscriptSize != info.Size() {
		t.Fatalf("TranscriptSize = %d, want file size %d", idx.TranscriptSize, info.Size())
	}
	identity, known, err := SessionContentIdentity(path)
	if err != nil || !known {
		t.Fatalf("identity: known=%v err=%v", known, err)
	}
	if !ValidateSessionDisplayIndex(idx, identity.Revision, identity.RevisionKnown, identity.Digest, info.Size()) {
		t.Fatal("ValidateSessionDisplayIndex rejected the refreshed index — refresh stamped an identity that does not round-trip")
	}
	// Tail rows decode through the extended entries exactly like the prefix.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for k, want := range tail {
		e := idx.Entries[len(old)+k]
		line := raw[e.Offset : e.Offset+e.Length]
		var m provider.Message
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("tail entry %d does not decode: %v", k, err)
		}
		if m.Role != want.Role {
			t.Errorf("tail entry %d role = %q, want %q", k, m.Role, want.Role)
		}
		if len(want.Content) > 0 && !strings.HasPrefix(m.Content, want.Content[:min(len(want.Content), 16)]) {
			t.Errorf("tail entry %d content prefix mismatch", k)
		}
	}
}

func TestRefreshSessionDisplayIndexRejectsPartialTail(t *testing.T) {
	path, _, _ := staleIndexSession(t)
	// A save mid-write: half a JSON line at EOF must abort, not extend.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open append: %v", err)
	}
	if _, err := f.WriteString(`{"role":"assistant","content":"partial`); err != nil {
		f.Close()
		t.Fatalf("append partial: %v", err)
	}
	f.Close()

	refreshed, err := RefreshSessionDisplayIndexFromReadModel(path)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if refreshed {
		t.Fatal("Refresh extended over a partial trailing line — must refuse so the next request retries after the save lands")
	}
}

func TestRefreshSessionDisplayIndexRejectsCountMismatch(t *testing.T) {
	path, old, _ := staleIndexSession(t)
	// Event index claims the OLD message count: the tail does not provably
	// reach the authoritative end, so stamping the ledger identity could lie.
	digest, err := digestSessionMessages(old)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	identity, known, err := SessionContentIdentity(path)
	if err != nil || !known {
		t.Fatalf("identity: known=%v err=%v", known, err)
	}
	if err := writeSessionEventIndex(path, old, digest, identity.Revision); err != nil {
		t.Fatalf("rewrite event index with stale count: %v", err)
	}

	refreshed, err := RefreshSessionDisplayIndexFromReadModel(path)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if refreshed {
		t.Fatal("Refresh accepted an event index whose MessageCount does not cover the tail — alignment guard failed")
	}
}

func TestRefreshSessionDisplayIndexRejectsInconsistentEntries(t *testing.T) {
	path, _, _ := staleIndexSession(t)
	prev, err := LoadSessionDisplayIndex(store.SessionDisplayIndex(path))
	if err != nil || prev == nil {
		t.Fatalf("load: %v", err)
	}
	// Structural lie: entries no longer cover what MessageCount claims (drop
	// the tail entry, keep the count). The prefix self-consistency check
	// (len(Entries) != MessageCount) must refuse first.
	broken := *prev
	broken.Entries = append([]DisplayIndexEntry{}, prev.Entries[:len(prev.Entries)-1]...)
	if err := WriteSessionDisplayIndex(store.SessionDisplayIndex(path), &broken); err != nil {
		t.Fatalf("write broken index: %v", err)
	}
	refreshed, err := RefreshSessionDisplayIndexFromReadModel(path)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if refreshed {
		t.Fatal("Refresh extended an index whose entries are not self-consistent — prefix proof failed")
	}
}
