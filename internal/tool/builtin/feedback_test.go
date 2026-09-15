package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSubmitFeedbackRoundTrip(t *testing.T) {
	dir := t.TempDir()
	SetFeedbackHome(dir)
	t.Cleanup(func() { SetFeedbackHome("") })

	tool := submitFeedback{}
	if tool.Name() != "submit_feedback" {
		t.Fatalf("Name = %q", tool.Name())
	}
	if !tool.ReadOnly() || !tool.PlanModeSafe() {
		t.Fatal("submit_feedback must be read-only and plan-mode safe")
	}

	args, _ := json.Marshal(map[string]any{
		"kind": "idea",
		"text": "Add a keyboard shortcut for the feedback panel.",
		"tags": []string{"ui", "shortcut"},
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == "" {
		t.Fatal("Execute returned empty result")
	}

	entries, err := ListFeedbackEntries(0)
	if err != nil {
		t.Fatalf("ListFeedbackEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].Kind != "idea" || entries[0].Text == "" {
		t.Fatalf("entry = %+v", entries[0])
	}
	if len(entries[0].Tags) != 2 {
		t.Fatalf("tags = %v", entries[0].Tags)
	}
	if entries[0].At == "" {
		t.Fatal("entry missing timestamp")
	}

	// A second append grows the file; clear empties it.
	args2, _ := json.Marshal(map[string]any{"kind": "bug", "text": "Panel does not open."})
	if _, err := tool.Execute(context.Background(), args2); err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	if entries, _ := ListFeedbackEntries(0); len(entries) != 2 {
		t.Fatalf("after append entries = %d, want 2", len(entries))
	}
	if entries, _ := ListFeedbackEntries(1); len(entries) != 1 || entries[0].Kind != "bug" {
		t.Fatalf("limit=1 entries = %+v", entries)
	}
	if err := ClearFeedbackEntries(); err != nil {
		t.Fatalf("ClearFeedbackEntries: %v", err)
	}
	if entries, _ := ListFeedbackEntries(0); len(entries) != 0 {
		t.Fatalf("after clear entries = %d, want 0", len(entries))
	}

	// Missing file is an empty list, not an error.
	if err := os.Remove(filepath.Join(dir, "entries.jsonl")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if entries, err := ListFeedbackEntries(0); err != nil || len(entries) != 0 {
		t.Fatalf("missing file entries=%v err=%v", entries, err)
	}
}

func TestSubmitFeedbackValidation(t *testing.T) {
	dir := t.TempDir()
	SetFeedbackHome(dir)
	t.Cleanup(func() { SetFeedbackHome("") })

	tool := submitFeedback{}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"kind":"idea","text":""}`)); err == nil {
		t.Fatal("empty text must fail")
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"kind":"nope","text":"x"}`)); err == nil {
		t.Fatal("unknown kind must fail")
	}
	if _, err := AppendFeedbackEntry(FeedbackEntry{Kind: "bug", Text: "  "}); err == nil {
		t.Fatal("blank text must fail in AppendFeedbackEntry")
	}
}
