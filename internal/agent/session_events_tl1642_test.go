package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// tl1642: an append record is the wall clock every message in its batch was
// persisted with. Replay must stamp that time onto messages predating
// per-message CreatedAt, so a later full-state rewrite (replace/fold/
// migration/recovery lanes all re-persist the replayed transcript) cannot
// launder per-turn times into "everything happened at the rewrite instant" —
// the 2026-11-16 装机重启修复 batch where every legacy prompt showed the
// repair moment (14:44) in the prompt-history panel.

// appendOnlyFixture saves a session whose turns ride real append records
// (the first save lands the "snapshot" replace; every later save appends),
// matching the shape legacy sessions accumulated before per-message stamps.
func appendOnlyFixture(t *testing.T, dir string) (string, []time.Time) {
	t.Helper()
	path := filepath.Join(dir, "session.jsonl")
	session := NewSession("system")
	if err := session.Save(path); err != nil {
		t.Fatal(err)
	}
	times := make([]time.Time, 0, 3)
	appendTurn := func(role provider.Role, content string) {
		t.Helper()
		session.Add(provider.Message{Role: role, Content: content})
		if err := session.Save(path); err != nil {
			t.Fatal(err)
		}
		replay, err := replaySessionEventLog(store.SessionEventLog(path))
		if err != nil {
			t.Fatal(err)
		}
		last := len(replay.times) - 1
		if last < 0 || replay.times[last].IsZero() {
			t.Fatalf("append record for %q carried no record time", content)
		}
		times = append(times, replay.times[last])
	}
	appendTurn(provider.RoleUser, "重启前的真实提问")
	appendTurn(provider.RoleAssistant, "回答")
	appendTurn(provider.RoleUser, "第二条真实提问")
	return path, times
}

// Replay fills CreatedAt=0 messages from their append record's CreatedAt.
// Replace-snapshot messages stay unstamped by design: a replace record's time
// is the rewrite instant, and stamping it onto collapsed history would write
// the rewrite clock into per-turn data.
func TestReplayStampsAppendRecordTimeOntoLegacyMessages(t *testing.T) {
	dir := t.TempDir()
	path, recordTimes := appendOnlyFixture(t, dir)

	replay, err := replaySessionEventLog(store.SessionEventLog(path))
	if err != nil {
		t.Fatal(err)
	}
	if replay.damaged {
		t.Fatal("baseline replay reported the log damaged")
	}
	// system(snapshot) + user + assistant + user(appends)
	if len(replay.msgs) != 4 {
		t.Fatalf("replay = %d messages, want 4", len(replay.msgs))
	}
	if replay.msgs[0].CreatedAt != 0 {
		t.Fatalf("snapshot-system CreatedAt=%d: replace-snapshot rows must stay unstamped (a replace record's time is the rewrite instant)", replay.msgs[0].CreatedAt)
	}
	for i, m := range replay.msgs[1:] {
		want := recordTimes[i]
		if m.CreatedAt == 0 {
			t.Fatalf("appended message %d (%s) still has no CreatedAt: replay must stamp the append record time onto legacy rows", i+1, m.Role)
		}
		if m.CreatedAt != want.UnixMilli() {
			t.Fatalf("appended message %d CreatedAt=%d != its record time %d", i+1, m.CreatedAt, want.UnixMilli())
		}
	}
	// A message that already carries its own CreatedAt keeps that value.
	stamped := NewSession("system")
	if err := stamped.Save(filepath.Join(dir, "stamped.jsonl")); err != nil {
		t.Fatal(err)
	}
	stamped.Add(provider.Message{Role: provider.RoleUser, Content: "mine", CreatedAt: 1700000000000})
	if err := stamped.Save(filepath.Join(dir, "stamped.jsonl")); err != nil {
		t.Fatal(err)
	}
	stampedReplay, err := replaySessionEventLog(store.SessionEventLog(filepath.Join(dir, "stamped.jsonl")))
	if err != nil {
		t.Fatal(err)
	}
	kept := false
	for _, m := range stampedReplay.msgs {
		if m.Content == "mine" {
			kept = true
			if m.CreatedAt != 1700000000000 {
				t.Fatalf("existing CreatedAt was overwritten: %d", m.CreatedAt)
			}
		}
	}
	if !kept {
		t.Fatal("pin lost the stamped message")
	}
}

// The form-① end-to-end pin: a rewrite (the restart-time repair lane) must
// preserve legacy per-turn times through the replace snapshot it writes.
// LoadSessionUserMessages — the prompt-history panel's data source — must see
// the original turn times, never the rewrite instant and never a zero time.
func TestRewritePreservesLegacyTurnTimesThroughReplaceSnapshot(t *testing.T) {
	dir := t.TempDir()
	path, _ := appendOnlyFixture(t, dir)
	logPath := store.SessionEventLog(path)

	before, err := LoadSessionUserMessages(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 2 {
		t.Fatalf("pre-rewrite user messages = %d, want 2", len(before))
	}
	originalTimes := make([]time.Time, 0, len(before))
	for _, u := range before {
		if u.At.IsZero() {
			t.Fatalf("pre-rewrite user message %q has no time; the stamping pin above should have caught this", u.Message.Content)
		}
		originalTimes = append(originalTimes, u.At)
	}

	// The rewrite the recovery/fold/migration lanes perform: compact the whole
	// log into one replace snapshot built from the replayed transcript.
	replay, err := replaySessionEventLog(logPath)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := digestSessionMessages(replay.msgs)
	if err != nil {
		t.Fatal(err)
	}
	if err := compactSessionEventLog(path, replay.msgs, digest, replay.lastRevision, "tl1642 rewrite pin"); err != nil {
		t.Fatal(err)
	}
	// The rewrite must actually have collapsed the log (single replace), or
	// this pin proves nothing.
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() >= replay.size {
		t.Fatalf("rewrite did not compact the log: %d bytes >= %d", info.Size(), replay.size)
	}

	after, err := LoadSessionUserMessages(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("post-rewrite user messages = %d, want %d", len(after), len(before))
	}
	for i, u := range after {
		if u.At != originalTimes[i] {
			t.Fatalf("user message %q time changed across the rewrite: %v -> %v (the replace snapshot laundered the legacy time)",
				u.Message.Content, originalTimes[i], u.At)
		}
	}
}
