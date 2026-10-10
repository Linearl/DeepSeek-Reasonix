package agent

// 任务 710 synthetic-sample benchmark: a session whose event log is heavy
// must keep append-shaped saves O(delta) and redundant saves at ~zero IO.
// The incident sample (fork开发-新5) is 96MB of log with 55MB of live content
// on a disk where each full-rewrite save cost 0.7-1.3s; a full-scale replica
// would blow the suite time budget, so this benchmark scales the shape down
// (thousands of messages, MB-scale log) and reports the per-save wall-clock
// ratios that the fix's guarantees rest on. Extrapolation to 96MB is linear
// in bytes and is documented in the delivery report, marked as derived.

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/provider"
)

func t710SyntheticMessages(n int) []provider.Message {
	msgs := make([]provider.Message, 0, n+1)
	msgs = append(msgs, provider.Message{Role: provider.RoleSystem, Content: "sys"})
	for i := 0; i < n; i++ {
		role := provider.RoleUser
		content := fmt.Sprintf("t710 user turn %d: %s", i, string(make([]byte, 200)))
		if i%2 == 1 {
			role = provider.RoleAssistant
			content = fmt.Sprintf("t710 assistant turn %d: %s", i, string(make([]byte, 800)))
		}
		msgs = append(msgs, provider.Message{Role: role, Content: content})
	}
	return msgs
}

func TestSyntheticLargeSessionSaveCostShape(t *testing.T) {
	s := NewSession("sys")
	msgs := t710SyntheticMessages(4000)
	s.AddBatch(msgs...)
	path := filepath.Join(t.TempDir(), "session.jsonl")
	// Production desktop shape: the controller binds a session write
	// authority, which arms the writer-tail CAS and keeps append-shaped
	// classification off the full-log replay.
	bindSessionWriter(t, s, path)

	start := time.Now()
	if err := s.Save(path); err != nil {
		t.Fatalf("initial full save: %v", err)
	}
	fullWrite := time.Since(start)

	// Append-shaped progress save: two more messages on a completed tail.
	start = time.Now()
	s.AddBatch(t710SyntheticMessages(2)...)
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("append save: %v", err)
	}
	appendSave := time.Since(start)

	// Redundant save with nothing new: the snapshot no-op fast path keeps it
	// off the disk entirely (this is the shape the controller gate coalesces
	// in front of).
	start = time.Now()
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("redundant save: %v", err)
	}
	redundantSave := time.Since(start)

	t.Logf("t710 synthetic: full=%v append=%v redundant=%v (append/full=%.3f)",
		fullWrite, appendSave, redundantSave, float64(appendSave.Nanoseconds())/float64(fullWrite.Nanoseconds()))

	if appendSave > fullWrite/2 {
		t.Fatalf("append-shaped save (%v) must stay well under half the full write (%v)", appendSave, fullWrite)
	}
	if redundantSave > fullWrite/10 {
		t.Fatalf("redundant save (%v) must be near-free vs the full write (%v)", redundantSave, fullWrite)
	}
}
