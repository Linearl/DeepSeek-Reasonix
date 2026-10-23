package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

// Task 1013 replaced the bolt checkpoint cache with full-log replay. These
// tests pin the replacement contract: the open path never reads or writes the
// removed cache, the replay restores the durable prefix exactly, and the
// bounded recent baseline is still built in memory.

func TestRecoveryOpenReplaysLogWithoutCache(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions-v4", "fast")
	s, err := CreateWithOptions(dir, "fast", OpenOptions{ExternalHistory: true})
	if err != nil {
		t.Fatal(err)
	}

	for i := range 80 {
		message := provider.Message{ID: "message-" + strings.Repeat("x", 8) + strconv.Itoa(i), Role: provider.RoleUser, Content: strings.Repeat("history", 256)}
		payload, marshalErr := json.Marshal(map[string]any{"message": message})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if _, err := s.Append(context.Background(), Batch{OperationID: "operation-" + string(rune(0x100+i)), Events: []Event{{Kind: "message/complete", Payload: payload}}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	wantModel, err := json.Marshal(s.DeriveMessages())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	// Every query projection is disposable. Removing it must not change the
	// replay result or recent-message access.
	if err := os.RemoveAll(filepath.Join(filepath.Dir(dir), ".query-cache", "fast")); err != nil {
		t.Fatal(err)
	}
	var stats RecoveryOpenStats
	reopened, err := OpenWithOptions(dir, "fast", OpenOptions{
		ExternalHistory: true,
		ObserveRecovery: func(got RecoveryOpenStats) { stats = got },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close(context.Background()) })
	if stats.UsedCheckpoint {
		t.Fatalf("recovery stats = %+v, want full replay (no checkpoint cache)", stats)
	}
	if stats.LogBytesTotal == 0 || stats.LogBytesRead != stats.LogBytesTotal {
		t.Fatalf("recovery stats = %+v, want the full log read", stats)
	}
	gotModel, err := json.Marshal(reopened.DeriveMessages())
	if err != nil {
		t.Fatal(err)
	}
	if string(gotModel) != string(wantModel) {
		t.Fatal("provider-visible model projection changed across replay")
	}
	recent := reopened.RecentSnapshot()
	if recent.StorageGeneration == "" || recent.DurableSequence != reopened.EventSequence() || len(recent.Entries) == 0 || len(recent.Entries) > RecentMessageLimit {
		t.Fatalf("recent snapshot = %+v", recent)
	}
	if info, statErr := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(dir)), ".recovery-cache")); statErr == nil {
		t.Fatalf(".recovery-cache must stay absent (found %v)", info)
	}

	// A live session still refuses a reused operation key with different
	// content (in-memory table), and — post-removal — the replayed log is the
	// only durable source, so replaying a batch from scratch would append a
	// new sequence rather than dedupe against a checkpoint. Both behaviours are
	// visible here: the conflict is refused, the id reuse is refused by the
	// durable-id guard.
	firstPayload, _ := json.Marshal(map[string]any{"message": provider.Message{ID: "fresh", Role: provider.RoleUser, Content: "fresh content"}})
	first, err := reopened.Append(t.Context(), Batch{OperationID: "operation-" + string(rune(0x100)), Events: []Event{{Kind: "message/complete", Payload: firstPayload}}})
	if err != nil {
		t.Fatal(err)
	}
	if first.FirstSequence != 81 {
		t.Fatalf("commit starts at %d, want 81 (80 durable)", first.FirstSequence)
	}
	conflictPayload, _ := json.Marshal(map[string]any{"message": provider.Message{ID: "conflict", Role: provider.RoleUser, Content: "different"}})
	if _, err := reopened.Append(t.Context(), Batch{OperationID: "operation-" + string(rune(0x100)), Events: []Event{{Kind: "message/complete", Payload: conflictPayload}}}); err == nil {
		t.Fatal("same operation key with different content unexpectedly succeeded")
	}
}

func TestRecentEntriesKeepAbsoluteVisibleTurns(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "session")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	messages := []provider.Message{
		{ID: "eight", Role: provider.RoleUser, Content: "eight"},
		{ID: "assistant", Role: provider.RoleAssistant, Content: "answer"},
		{ID: "nine", Role: provider.RoleUser, Content: "nine"},
		{ID: "ten", Role: provider.RoleUser, Content: "ten"},
	}
	entries, err := buildRecentEntries(t.Context(), dir, messages, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []int{8, 8, 9, 10}
	for index := range entries {
		if entries[index].VisibleTurn != want[index] {
			t.Fatalf("entry %d visible turn=%d, want %d", index, entries[index].VisibleTurn, want[index])
		}
	}
}

// A session opened read-only through the FilesystemPersistence reports the
// removed on-disk snapshot as "preparing" instead of failing: Query.Recent's
// fallback (live runtime or paging) takes over, which is what a v4-mode cold
// reader used to get from recent-v1.json.
func TestRecentSnapshotOnDiskAlwaysReportsPreparing(t *testing.T) {
	root := t.TempDir()
	persistence := NewFilesystemPersistence(filepath.Join(root, "sessions-v4"))
	s, err := persistence.Create(CreateOptions{SessionID: "cold"})
	if err != nil {
		t.Fatal(err)
	}
	appendRecoveryTestMessage(t, s, "one", "first")
	if _, err := s.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(persistence.Root, "cold")
	manifest, err := readStoredManifest(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	identity, err := readStorageIdentity(dir, manifest)
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	if _, err := readRecentSnapshot(dir, identity); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("readRecentSnapshot = %v, want os.ErrNotExist", err)
	}
}

func appendRecoveryTestMessage(t *testing.T, s *Session, id, content string) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"message": provider.Message{ID: id, Role: provider.RoleUser, Content: content}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(t.Context(), Batch{OperationID: "append-" + id, Events: []Event{{Kind: "message/complete", Payload: payload}}}); err != nil {
		t.Fatal(err)
	}
}
