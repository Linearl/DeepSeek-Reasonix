package reminder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/store"
)

func testSession(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestSetDurableAndSurvivesRestart covers the hard cross-restart promise at
// the storage layer: Set commits to disk before returning, and the queue file
// is self-contained JSON that a fresh process (no shared memory) reads back
// with every field intact.
func TestSetDurableAndSurvivesRestart(t *testing.T) {
	session := testSession(t)
	now := time.Now()
	r, err := Set(session, "check the oven", now.Add(10*time.Minute), now)
	if err != nil {
		t.Fatal(err)
	}
	if r.ID == "" || r.Status != StatusPending {
		t.Fatalf("set returned %+v", r)
	}

	// The durable file exists at <stem>.reminders/reminders.json and decodes
	// standalone — this is what a restarted process reads.
	dir := store.SessionRemindersDir(session)
	if dir == "" || !strings.HasSuffix(filepath.ToSlash(dir), "s.reminders") {
		t.Fatalf("reminders dir = %q", dir)
	}
	data, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatalf("durable file missing after Set: %v", err)
	}
	var q queue
	if err := json.Unmarshal(data, &q); err != nil {
		t.Fatalf("durable file does not decode: %v", err)
	}
	if len(q.Entries) != 1 || q.Entries[0].ID != r.ID || q.Entries[0].Message != "check the oven" {
		t.Fatalf("durable entries = %+v", q.Entries)
	}
	if q.Entries[0].DueAt != r.DueAt {
		t.Fatalf("dueAt mismatch: file %d vs returned %d", q.Entries[0].DueAt, r.DueAt)
	}

	// Fresh read through the API equals a restart scan.
	entries, err := List(session)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != r.ID {
		t.Fatalf("restart scan = %+v", entries)
	}
}

func TestValidateReminder(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name    string
		due     time.Time
		message string
		wantErr string
	}{
		{name: "empty message", due: now.Add(time.Minute), message: "  ", wantErr: "message is required"},
		{name: "in the past", due: now.Add(-time.Second), message: "m", wantErr: "in the future"},
		{name: "too long", due: now.Add(8 * 24 * time.Hour), message: "m", wantErr: "at most"},
		{name: "oversized body", due: now.Add(time.Minute), message: strings.Repeat("x", MaxMessageBytes+1), wantErr: "byte limit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateReminder(tc.due, tc.message, now)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want contains %q", err, tc.wantErr)
			}
		})
	}
	if err := ValidateReminder(now.Add(time.Minute), "ok", now); err != nil {
		t.Fatalf("valid reminder rejected: %v", err)
	}
}

func TestCancelAndRemove(t *testing.T) {
	session := testSession(t)
	now := time.Now()
	r1, _ := Set(session, "one", now.Add(time.Minute), now)
	r2, _ := Set(session, "two", now.Add(2*time.Minute), now)

	found, err := Cancel(session, r1.ID)
	if err != nil || !found {
		t.Fatalf("cancel = %v %v", found, err)
	}
	// Unknown ids (already fired or never existed) report false, not an error.
	found, err = Cancel(session, r1.ID)
	if err != nil || found {
		t.Fatalf("double cancel = %v %v", found, err)
	}
	entries, _ := List(session)
	if len(entries) != 1 || entries[0].ID != r2.ID {
		t.Fatalf("after cancel entries = %+v", entries)
	}

	if err := Remove(session, r2.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := Remove(session, "never-existed"); err != nil {
		t.Fatalf("remove unknown id must be a no-op, got %v", err)
	}
	entries, _ = List(session)
	if len(entries) != 0 {
		t.Fatalf("entries after remove = %+v", entries)
	}
}

func TestListSortedByDueAt(t *testing.T) {
	session := testSession(t)
	now := time.Now()
	Set(session, "later", now.Add(3*time.Minute), now)
	Set(session, "earlier", now.Add(time.Minute), now)
	Set(session, "middle", now.Add(2*time.Minute), now)
	entries, err := List(session)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"earlier", "middle", "later"}
	for i, w := range want {
		if entries[i].Message != w {
			t.Fatalf("entry %d = %q, want %q", i, entries[i].Message, w)
		}
	}
}

func TestSetQueueFull(t *testing.T) {
	session := testSession(t)
	now := time.Now()
	for i := range MaxPending {
		if _, err := Set(session, "m", now.Add(time.Duration(i+1)*time.Minute), now); err != nil {
			t.Fatalf("set %d: %v", i, err)
		}
	}
	if _, err := Set(session, "over", now.Add(time.Hour), now); err == nil || !strings.Contains(err.Error(), "queue full") {
		t.Fatalf("err = %v, want queue full", err)
	}
}

// TestCorruptQueueFailsLoudly pins the durability stance: a corrupt file is an
// error, never a silent empty queue (alarms must not silently vanish).
func TestCorruptQueueFailsLoudly(t *testing.T) {
	session := testSession(t)
	dir := store.SessionRemindersDir(session)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := List(session); err == nil {
		t.Fatal("List on corrupt file returned nil error")
	}
}
