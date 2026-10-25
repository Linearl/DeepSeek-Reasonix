package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/store"
)

// tl 1150: the prompt-history panel and the transcript read the same session
// files, but the tape cached per-session scans forever — a user turn appended
// after the tape was built never showed up in the panel (missing entries even
// after "load more"), while the transcript eventually caught up. These tests
// pin the revalidation contract: a grown backing file must rescan, an unchanged
// one must keep the cache.

func promptHistoryGrowthSeedMeta(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := agent.SaveBranchMetaPreserveUpdated(path, agent.BranchMeta{
		CreatedAt: at,
		UpdatedAt: at,
	}); err != nil {
		t.Fatal(err)
	}
}

func appendJSONLUserPrompt(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(f, "{\"role\":\"user\",\"content\":%q}\n", text); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func appendEventLogUserPrompt(t *testing.T, path string, messageIndex int, text string, at time.Time) {
	t.Helper()
	rec := map[string]any{
		"schema_version": 1,
		"type":           "append",
		"revision":       messageIndex + 1,
		"base_revision":  messageIndex,
		"message_index":  messageIndex,
		"messages":       []map[string]string{{"role": "user", "content": text}},
		"created_at":     at.UTC().Format(time.RFC3339Nano),
	}
	buf, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(store.SessionEventLog(path), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(append(buf, '\n')); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPromptHistoryTapeRescansJSONLSessionAfterAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "current.jsonl")
	if err := os.WriteFile(path, []byte("{\"role\":\"user\",\"content\":\"first\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	promptHistoryGrowthSeedMeta(t, path, time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC))

	tape, err := newPromptHistoryTape(dir, path)
	if err != nil {
		t.Fatal(err)
	}
	first := tape.readOlder("", 10)
	if len(first.Entries) != 1 || first.Entries[0].Text != "first" {
		t.Fatalf("first read = %+v, want [first]", first.Entries)
	}

	appendJSONLUserPrompt(t, path, "second")

	second := tape.readOlder("", 10)
	if len(second.Entries) != 2 {
		t.Fatalf("read after append = %+v, want 2 entries (panel went stale)", second.Entries)
	}
	if second.Entries[0].Text != "second" || second.Entries[1].Text != "first" {
		t.Fatalf("read after append = %+v, want [second first]", second.Entries)
	}
}

func TestPromptHistoryTapeKeepsCacheWhenFileUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "current.jsonl")
	if err := os.WriteFile(path, []byte("{\"role\":\"user\",\"content\":\"only\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	promptHistoryGrowthSeedMeta(t, path, time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC))

	tape, err := newPromptHistoryTape(dir, path)
	if err != nil {
		t.Fatal(err)
	}
	if page := tape.readOlder("", 10); len(page.Entries) != 1 {
		t.Fatalf("first read = %+v, want [only]", page.Entries)
	}
	cached, ok := tape.loaded[path]
	if !ok || cached == nil || len(cached.entries) != 1 {
		t.Fatalf("expected a populated cache entry, got ok=%v", ok)
	}

	tape.readOlder("", 10)

	if tape.loaded[path] != cached {
		t.Fatal("unchanged session file must keep the cached scan (no rescan)")
	}
}

func TestPromptHistoryTapeRescansEventLogSessionAfterAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "current.jsonl")
	// The checkpoint carries the replace snapshot; the event log is the live
	// transcript the panel must follow.
	if err := os.WriteFile(path, []byte("{\"role\":\"user\",\"content\":\"old question\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 25, 0, 38, 0, 0, time.UTC)
	promptHistoryGrowthSeedMeta(t, path, base)
	replaceRec := map[string]any{
		"schema_version": 1,
		"type":           "replace",
		"revision":       1,
		"messages":       []map[string]string{{"role": "user", "content": "old question"}},
		"created_at":     base.UTC().Format(time.RFC3339Nano),
	}
	buf, err := json.Marshal(replaceRec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.SessionEventLog(path), append(buf, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	tape, err := newPromptHistoryTape(dir, path)
	if err != nil {
		t.Fatal(err)
	}
	first := tape.readOlder("", 10)
	if len(first.Entries) != 1 || first.Entries[0].Text != "old question" {
		t.Fatalf("first read = %+v, want [old question]", first.Entries)
	}

	appendEventLogUserPrompt(t, path, 1, "new question", base.Add(time.Minute))

	second := tape.readOlder("", 10)
	if len(second.Entries) != 2 {
		t.Fatalf("read after event-log append = %+v, want 2 entries", second.Entries)
	}
	if second.Entries[0].Text != "new question" || second.Entries[1].Text != "old question" {
		t.Fatalf("read after event-log append = %+v, want [new question old question]", second.Entries)
	}
}

// TestScanPromptHistorySeesUserTurnAfterTranscriptAppend walks the full App
// path the picker uses: submit (append) between two scans must surface the new
// prompt, and both surfaces must agree on it (SSOT assertion).
func TestScanPromptHistorySeesUserTurnAfterTranscriptAppend(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "current.jsonl")
	if err := os.WriteFile(current, []byte("{\"role\":\"user\",\"content\":\"earlier\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	promptHistoryGrowthSeedMeta(t, current, time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC))

	app := NewApp()
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: current, Label: "test"})
	defer ctrl.Close()
	app.setTestCtrl(ctrl, "")

	if result, err := app.ScanPromptHistory(""); err != nil {
		t.Fatal(err)
	} else if len(result.Entries) != 1 || result.Entries[0].Text != "earlier" {
		t.Fatalf("first scan = %+v, want [earlier]", result.Entries)
	}

	appendJSONLUserPrompt(t, current, "later")

	result, err := app.ScanPromptHistory("")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 2 || result.Entries[0].Text != "later" {
		t.Fatalf("scan after append = %+v, want [later earlier]", result.Entries)
	}

	// SSOT assertion: the transcript-side reader (LoadSessionUserMessages, the
	// same source the transcript replays) and the panel-side tape must agree
	// on the user-authored prompts of this session.
	users, err := agent.LoadSessionUserMessages(current)
	if err != nil {
		t.Fatal(err)
	}
	tapeTexts := map[string]bool{}
	for _, entry := range result.Entries {
		tapeTexts[entry.Text] = true
	}
	for _, user := range users {
		text := sessionUserPromptText(user.Message, identityPromptDisplay)
		if text == "" {
			continue
		}
		if !tapeTexts[text] {
			t.Fatalf("transcript sees user prompt %q that the panel scan is missing (SSOT violated)", text)
		}
	}
}

// TestPromptHistoryOlderCursorStillBoundedAfterAppend pins the paginaton
// contract after a rescan: an older cursor keeps serving the remaining older
// entries without duplicating what earlier pages already returned.
func TestPromptHistoryOlderCursorStillBoundedAfterAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "current.jsonl")
	body := ""
	for i := 0; i < 3; i++ {
		body += fmt.Sprintf("{\"role\":\"user\",\"content\":\"p%d\"}\n", i)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	promptHistoryGrowthSeedMeta(t, path, time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC))

	tape, err := newPromptHistoryTape(dir, path)
	if err != nil {
		t.Fatal(err)
	}
	page1 := tape.readOlder("", 2)
	if len(page1.Entries) != 2 || page1.Entries[0].Text != "p2" {
		t.Fatalf("page1 = %+v, want [p2 p1]", page1.Entries)
	}

	appendJSONLUserPrompt(t, path, "p3")

	page2 := tape.readOlder(page1.OlderCursor, 10)
	if len(page2.Entries) != 1 || page2.Entries[0].Text != "p0" {
		t.Fatalf("page2 after append = %+v, want [p0] (old entries must not duplicate)", page2.Entries)
	}
}
