package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/store"
)

// tl1642 pins. Two forms, one root:
//   ① a replace rewrite (restart-time repair / fold / migration) collapses the
//     log into one snapshot whose replay times are zero — rows without a
//     per-message createdAt then had no time left but the rewrite instant, and
//     the panel showed the whole batch at that moment (实测 2026-11-16 14:44);
//   ② those approximate times are the file/meta clock and advance with every
//     save, so in a newest-first panel they pinned the rewritten batch ABOVE
//     genuinely newer prompts — the user's "最后一条用户消息缺席面板".
// Fix A (agent replay) stops the laundering; Fix B (this file) makes an
// approximate time never outrank a real one.

// The comparator: an approximate row must never beat a real-timestamped row
// regardless of clock values; buckets tie-break by clock, then session, turn.
func TestPromptHistoryApproxTimeNeverOutranksRealTime(t *testing.T) {
	real := PromptHistoryEntry{At: 100, atApprox: false}
	approx := PromptHistoryEntry{At: 999, atApprox: true}
	if !promptHistoryEntryNewer(real, approx) {
		t.Fatal("a real timestamp must outrank an approximate one even when the approximation's clock is larger")
	}
	if promptHistoryEntryNewer(approx, real) {
		t.Fatal("comparator inconsistency: approx vs real must lose in exactly one direction")
	}
	lateReal := PromptHistoryEntry{At: 200, atApprox: false}
	if !promptHistoryEntryNewer(lateReal, real) || promptHistoryEntryNewer(real, lateReal) {
		t.Fatal("within the real bucket clocks must still decide")
	}
	a1 := PromptHistoryEntry{At: 500, SessionPath: "a", Turn: 1, atApprox: true}
	a2 := PromptHistoryEntry{At: 500, SessionPath: "a", Turn: 2, atApprox: true}
	if !promptHistoryEntryNewer(a2, a1) {
		t.Fatal("within the approximate bucket the turn tie-break must hold")
	}
}

// Form ② at panel level: a session carrying already-laundered rows (single
// replace, no createdAt) plus a freshly sent message whose real createdAt is
// EARLIER than the meta clock the fallback would use. The real prompt must
// stay at the top; the approximated batch sorts below it, turn-newest first.
func TestPromptHistoryNewestRealPromptAboveApproximatedBatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "current.jsonl")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	rewriteAt := time.Date(2026, 11, 16, 14, 45, 0, 0, time.UTC)
	sendAt := time.Date(2026, 11, 16, 14, 46, 30, 0, time.UTC)
	flushAt := sendAt.Add(90 * time.Second) // batch flush after the send
	metaAt := flushAt.Add(3 * time.Minute)  // a later save bumped the meta clock
	replaceRec := map[string]any{
		"schema_version": 1,
		"type":           "replace",
		"revision":       7,
		"messages": []map[string]string{
			{"role": "user", "origin": "user", "content": "旧行A"},
			{"role": "user", "origin": "user", "content": "旧行B"},
		},
		"created_at": rewriteAt.UTC().Format(time.RFC3339Nano),
	}
	appendRec := map[string]any{
		"schema_version": 1,
		"type":           "append",
		"revision":       8,
		"base_revision":  7,
		"message_index":  2,
		"messages": []map[string]string{{
			"role": "user", "origin": "user", "content": "刚发的问题",
		}},
		"created_at": flushAt.UTC().Format(time.RFC3339Nano),
	}
	// The freshly sent row carries its real per-message time in the checkpoint
	// shape the writer persists (createdAt rides the message, not the record).
	appendRec["messages"] = []map[string]any{{
		"role": "user", "origin": "user", "content": "刚发的问题", "createdAt": sendAt.UnixMilli(),
	}}
	body := ""
	for _, rec := range []map[string]any{replaceRec, appendRec} {
		buf, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		body += string(buf) + "\n"
	}
	if err := os.WriteFile(store.SessionEventLog(path), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := agent.SaveBranchMetaPreserveUpdated(path, agent.BranchMeta{
		CreatedAt: rewriteAt,
		UpdatedAt: metaAt,
	}); err != nil {
		t.Fatal(err)
	}

	tape, err := newPromptHistoryTape(dir, path)
	if err != nil {
		t.Fatal(err)
	}
	entries := tape.readAll()

	if len(entries) != 3 {
		t.Fatalf("panel entries = %d, want 3", len(entries))
	}
	if entries[0].Text != "刚发的问题" {
		t.Fatalf("newest real prompt buried below the approximated batch: order = %q", textsOf(entries))
	}
	if entries[0].At != sendAt.UnixMilli() {
		t.Fatalf("real prompt At=%d, want its own send time %d", entries[0].At, sendAt.UnixMilli())
	}
	if entries[1].Text != "旧行B" || entries[2].Text != "旧行A" {
		t.Fatalf("approximated batch order = %q, want turn-newest first within the bucket", textsOf(entries))
	}
	for _, e := range entries[1:] {
		if e.atApprox {
			continue
		}
		t.Fatalf("laundered row %q not marked approximate; the sort bucket is doing nothing", e.Text)
	}
}

// Form ① at panel level: the post-fix repair output. A replace snapshot whose
// rows CARRY createdAt (what the agent replay stamp now persists through every
// rewrite) must surface its real per-turn times — never the rewrite instant,
// never the meta fallback.
func TestPromptHistoryStampedReplaceSnapshotKeepsRealTimes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "current.jsonl")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	turn1 := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	turn2 := time.Date(2026, 11, 16, 10, 5, 0, 0, time.UTC)
	rewriteAt := time.Date(2026, 11, 16, 14, 45, 0, 0, time.UTC)
	metaAt := rewriteAt.Add(5 * time.Minute)
	replaceRec := map[string]any{
		"schema_version": 1,
		"type":           "replace",
		"revision":       2,
		"messages": []map[string]any{
			{"role": "user", "origin": "user", "content": "重启前的第一问", "createdAt": turn1.UnixMilli()},
			{"role": "user", "origin": "user", "content": "重启前的第二问", "createdAt": turn2.UnixMilli()},
		},
		"created_at": rewriteAt.UTC().Format(time.RFC3339Nano),
	}
	buf, err := json.Marshal(replaceRec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.SessionEventLog(path), []byte(string(buf)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := agent.SaveBranchMetaPreserveUpdated(path, agent.BranchMeta{
		CreatedAt: rewriteAt,
		UpdatedAt: metaAt,
	}); err != nil {
		t.Fatal(err)
	}

	tape, err := newPromptHistoryTape(dir, path)
	if err != nil {
		t.Fatal(err)
	}
	entries := tape.readAll()

	if len(entries) != 2 {
		t.Fatalf("panel entries = %d, want 2", len(entries))
	}
	want := map[string]int64{
		"重启前的第一问": turn1.UnixMilli(),
		"重启前的第二问": turn2.UnixMilli(),
	}
	for _, e := range entries {
		if e.atApprox {
			t.Fatalf("row %q marked approximate: a snapshot row carrying createdAt must read as real time", e.Text)
		}
		if ms, ok := want[e.Text]; ok && e.At != ms {
			t.Fatalf("row %q At=%d, want its own turn time %d (the rewrite instant %d must not leak in)",
				e.Text, e.At, ms, rewriteAt.UnixMilli())
		}
	}
	if entries[0].Text != "重启前的第二问" || entries[1].Text != "重启前的第一问" {
		t.Fatalf("order = %q, want newest turn first", textsOf(entries))
	}
}

func textsOf(entries []PromptHistoryEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Text)
	}
	return out
}
