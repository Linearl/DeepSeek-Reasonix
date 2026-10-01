package session

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// Task 398 (upstream #11006 → #10893): a message/complete whose stable id is
// already durable must be refused by the writer (with an error log), while a
// log that already carries the duplicate keeps its first occurrence and stays
// openable — readers must never refuse the whole store for it.

// captureLogHandler records records so a test can assert the refusal logged.
type captureLogHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureLogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureLogHandler) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, record)
	return nil
}

func (h *captureLogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureLogHandler) WithGroup(string) slog.Handler      { return h }

// find returns the first record whose message contains substr and whose
// attribute key carries want.
func (h *captureLogHandler) find(substr, attrKey, attrValue string) (slog.Record, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, record := range h.records {
		if !strings.Contains(record.Message, substr) {
			continue
		}
		if attrKey == "" {
			return record, true
		}
		found := false
		record.Attrs(func(attr slog.Attr) bool {
			if attr.Key == attrKey && attr.Value.String() == attrValue {
				found = true
				return false
			}
			return true
		})
		if found {
			return record, true
		}
	}
	return slog.Record{}, false
}

func captureLogs(t *testing.T) *captureLogHandler {
	t.Helper()
	handler := &captureLogHandler{}
	previous := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return handler
}

func appendUserComplete(t *testing.T, session *Session, operationID, id, content string) error {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"message": provider.Message{ID: id, Role: provider.RoleUser, Content: content}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.Append(t.Context(), Batch{OperationID: operationID, Events: []Event{{Kind: "message/complete", Payload: payload}}})
	return err
}

// TestMessageCompleteRefusesDurableIDReuse covers the writer side: the second
// completion of a durable id is refused with ErrDuplicateMessageID, the
// refusal is logged, no state moves, and later writes still succeed.
func TestMessageCompleteRefusesDurableIDReuse(t *testing.T) {
	t.Run("same runtime", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "same-runtime")
		store, err := Open(dir, "same-runtime")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close(context.Background()) })
		logs := captureLogs(t)

		if err := appendUserComplete(t, store, "first", "dup", "alpha content"); err != nil {
			t.Fatalf("first completion: %v", err)
		}
		before := store.Snapshot()
		err = appendUserComplete(t, store, "second", "dup", "beta content")
		if !errors.Is(err, ErrDuplicateMessageID) {
			t.Fatalf("duplicate completion error = %v, want ErrDuplicateMessageID", err)
		}
		if !strings.Contains(err.Error(), "dup") {
			t.Fatalf("refusal does not name the id: %v", err)
		}
		after := store.Snapshot()
		if after.EventSequence != before.EventSequence {
			t.Fatalf("refused commit advanced sequence: before=%d after=%d", before.EventSequence, after.EventSequence)
		}
		if len(after.Projection.Messages) != 1 || after.Projection.Messages[0].Content != "alpha content" {
			t.Fatalf("refused commit mutated projection: %#v", after.Projection.Messages)
		}
		if _, ok := logs.find("refused message completion", "messageId", "dup"); !ok {
			t.Fatal("refusal did not log the reused durable message id")
		}
		// The refusal must not wedge the writer.
		if err := appendUserComplete(t, store, "third", "fresh", "gamma content"); err != nil {
			t.Fatalf("write after refusal: %v", err)
		}
	})

	// The blind spot the durable set exists for: an external-history session
	// drops projection.Messages, so a projection-only duplicate check sees an
	// empty set and would accept the reuse.
	t.Run("after projection history is dropped", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "externalized")
		store, err := Open(dir, "externalized")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close(context.Background()) })
		logs := captureLogs(t)

		if err := appendUserComplete(t, store, "first", "dup", "alpha content"); err != nil {
			t.Fatalf("first completion: %v", err)
		}
		store.externalizeDurableHistory()
		store.mu.Lock()
		liveBodies := len(store.projection.Messages)
		store.mu.Unlock()
		if liveBodies != 0 {
			t.Fatalf("externalize kept %d projection messages, want 0 (blind-spot precondition)", liveBodies)
		}
		err = appendUserComplete(t, store, "second", "dup", "beta content")
		if !errors.Is(err, ErrDuplicateMessageID) {
			t.Fatalf("blind-spot duplicate error = %v, want ErrDuplicateMessageID", err)
		}
		if _, ok := logs.find("refused message completion", "messageId", "dup"); !ok {
			t.Fatal("blind-spot refusal did not log the reused durable message id")
		}
	})

	// Reopen through the production external-history path: the bounded open
	// leaves projection.Messages empty, so only the seeded id set can refuse.
	t.Run("after external history reopen", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "reopen-blind")
		store, err := Open(dir, "reopen-blind")
		if err != nil {
			t.Fatal(err)
		}
		if err := appendUserComplete(t, store, "first", "dup", "alpha content"); err != nil {
			t.Fatalf("first completion: %v", err)
		}
		if _, err := store.Flush(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := store.Close(context.Background()); err != nil {
			t.Fatal(err)
		}

		reopened, err := OpenWithOptions(dir, "reopen-blind", OpenOptions{ExternalHistory: true})
		if err != nil {
			t.Fatalf("external history reopen: %v", err)
		}
		t.Cleanup(func() { _ = reopened.Close(context.Background()) })
		logs := captureLogs(t)
		reopened.mu.Lock()
		liveBodies := len(reopened.projection.Messages)
		reopened.mu.Unlock()
		if liveBodies != 0 {
			t.Fatalf("bounded open kept %d projection messages, want 0 (blind-spot precondition)", liveBodies)
		}
		err = appendUserComplete(t, reopened, "second", "dup", "beta content")
		if !errors.Is(err, ErrDuplicateMessageID) {
			t.Fatalf("reopen duplicate error = %v, want ErrDuplicateMessageID", err)
		}
		if _, ok := logs.find("refused message completion", "messageId", "dup"); !ok {
			t.Fatal("reopen refusal did not log the reused durable message id")
		}
	})
}

// searchReady polls the async index build until the search page is ready.
func searchReady(t *testing.T, query *Query, ref SessionRef, text string) SearchHistoryPage {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		page, err := query.SearchHistory(t.Context(), ref, text, "", 10)
		if err != nil {
			t.Fatalf("search %q: %v", text, err)
		}
		if page.Status == "ready" {
			return page
		}
		if page.Status != "preparing" {
			t.Fatalf("search %q page = %+v", text, page)
		}
		if time.Now().After(deadline) {
			t.Fatalf("search %q stayed preparing", text)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestDuplicateCompleteOnDiskKeepsFirstOccurrence constructs a log that holds
// two message/complete events with the same id (the pre-fix writer accepted
// those) and proves every reader still opens it: snapshot, history index and
// search index all keep the first occurrence.
func TestDuplicateCompleteOnDiskKeepsFirstOccurrence(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions-v4")
	service, err := NewService("local", NewFilesystemPersistence(root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
	runtime, err := service.Create(t.Context(), CreateOptions{SessionID: "damaged"})
	if err != nil {
		t.Fatal(err)
	}
	sess := runtime.Session()
	if err := appendUserComplete(t, sess, "first", "dup", "alpha unique token"); err != nil {
		t.Fatalf("first completion: %v", err)
	}
	// Reproduce the pre-fix writer: drop the id from the writer's live set so
	// the durable-id refusal cannot see it. The projection keeps its first
	// occurrence, so the commit is accepted and the duplicate lands on disk.
	sess.mu.Lock()
	delete(sess.messageIDs, "dup")
	sess.mu.Unlock()
	if err := appendUserComplete(t, sess, "second", "dup", "beta unique token"); err != nil {
		t.Fatalf("fixture append (pre-fix writer): %v", err)
	}
	ref := runtime.Ref()
	if err := service.Close(t.Context(), ref); err != nil {
		t.Fatal(err)
	}

	// Production open path: external history, bounded load.
	reopened, err := NewService("local", NewFilesystemPersistence(root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.CloseAll(context.Background()) })
	binding, err := reopened.Open(t.Context(), ref)
	if err != nil {
		t.Fatalf("external history open of duplicate log: %v", err)
	}
	snapshot := binding.Runtime().Session().Snapshot()
	if len(snapshot.Projection.Messages) != 1 || snapshot.Projection.Messages[0].Content != "alpha unique token" {
		t.Fatalf("snapshot kept = %#v, want first occurrence only", snapshot.Projection.Messages)
	}

	// History index build must survive the duplicate and keep the first id row.
	page := historyPageReady(t, reopened.Query(), ref, "", 10)
	if len(page.Messages) != 1 || page.Messages[0].MessageID != "dup" {
		t.Fatalf("history index rows = %+v, want one row for the first occurrence", page.Messages)
	}

	// Search index build must survive too: first text indexed, repeat skipped.
	first := searchReady(t, reopened.Query(), ref, "alpha")
	if len(first.Hits) != 1 {
		t.Fatalf("search for first text = %+v, want 1 hit", first.Hits)
	}
	second := searchReady(t, reopened.Query(), ref, "beta")
	if len(second.Hits) != 0 {
		t.Fatalf("search for repeated text = %+v, want the repeat skipped (0 hits)", second.Hits)
	}
	if err := binding.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(t.Context(), ref); err != nil {
		t.Fatal(err)
	}

	// Compatibility full-scan open (non-external) must also keep the first.
	// Drop the recovery checkpoint first: a checkpoint carries
	// projection.Messages = nil by design, and reusing it would bypass the
	// full replay this sub-check exists to cover.
	if err := os.RemoveAll(recoveryCacheDir(filepath.Join(root, ref.SessionID))); err != nil {
		t.Fatal(err)
	}
	full, err := Open(filepath.Join(root, ref.SessionID), ref.SessionID)
	if err != nil {
		t.Fatalf("full scan open of duplicate log: %v", err)
	}
	t.Cleanup(func() { _ = full.Close(context.Background()) })
	fullSnapshot := full.Snapshot()
	if len(fullSnapshot.Projection.Messages) != 1 || fullSnapshot.Projection.Messages[0].Content != "alpha unique token" {
		t.Fatalf("full scan kept = %#v, want first occurrence only", fullSnapshot.Projection.Messages)
	}
	// The full-scan open must also seed the writer's durable-id set from the
	// replayed log, so a third reuse is refused rather than appended again.
	if err := appendUserComplete(t, full, "third", "dup", "gamma unique token"); !errors.Is(err, ErrDuplicateMessageID) {
		t.Fatalf("duplicate after full-scan open = %v, want ErrDuplicateMessageID", err)
	}
}

// TestRecoveryCheckpointCarriesMessageIDs pins the checkpoint contract: the
// live id set rides in the checkpoint (projection.Messages is emptied there),
// and the projection version bump forces pre-fix checkpoints — which lack the
// field — to rebuild from the log instead of seeding an empty set.
func TestRecoveryCheckpointCarriesMessageIDs(t *testing.T) {
	projection, _ := Project(nil)
	state := &startupSessionState{
		projection: projection,
		messageIDs: identitiesOf([]string{"alpha-id", "beta-id"}),
	}
	checkpoint := checkpointFromStartup(Manifest{SessionID: "carry"}, storageIdentity{}, state)
	got := map[string]bool{}
	for _, id := range checkpoint.MessageIDs {
		got[id] = true
	}
	if !got["alpha-id"] || !got["beta-id"] || len(checkpoint.MessageIDs) != 2 {
		t.Fatalf("checkpoint MessageIDs = %v, want exactly the live set", checkpoint.MessageIDs)
	}
	if checkpoint.ProjectionVersion != recoveryProjectionVersion || recoveryProjectionVersion != 3 {
		t.Fatalf("projection version = %d (const %d), want 3 so pre-fix checkpoints rebuild once",
			checkpoint.ProjectionVersion, recoveryProjectionVersion)
	}
}
