package control

import (
	"os"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
)

// P14 (SetActiveTab 移出锁等待) 的 control 层验收：Controller.SnapshotIfSavePathFree
// 在 save-path 锁空闲时完整落盘，被占时立即干净拒绝且不改文件字节。

func TestSnapshotIfSavePathFreePersistsWhenUncontended(t *testing.T) {
	c, sess, path := newSchemaTwoBranchController(t)
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "p14 third"})

	attempted, err := c.SnapshotIfSavePathFree()
	if err != nil {
		t.Fatalf("SnapshotIfSavePathFree: %v", err)
	}
	if !attempted {
		t.Fatal("attempted = false on an uncontended path, want true")
	}
	loaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if got := loaded.Snapshot(); len(got) != 4 {
		t.Fatalf("reloaded messages = %d, want 4 (system + fixture pair + p14 third)", len(got))
	}
}

func TestSnapshotIfSavePathFreeDeclinesWhenSavePathHeld(t *testing.T) {
	c, sess, path := newSchemaTwoBranchController(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read baseline transcript: %v", err)
	}
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "must not land yet"})

	release := agent.HoldSessionSavePathForTest(path)
	type attempt struct {
		attempted bool
		err       error
	}
	done := make(chan attempt, 1)
	go func() {
		a, err := c.SnapshotIfSavePathFree()
		done <- attempt{a, err}
	}()
	select {
	case r := <-done:
		if r.err != nil || r.attempted {
			t.Fatalf("SnapshotIfSavePathFree on held path = (%v, %v), want (false, nil)", r.attempted, r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SnapshotIfSavePathFree blocked on a held save path; want immediate decline")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read transcript after decline: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("declined snapshot modified the session file")
	}
	release()

	// The still-owed flush goes through once the path frees.
	attempted, err := c.SnapshotIfSavePathFree()
	if err != nil || !attempted {
		t.Fatalf("post-release SnapshotIfSavePathFree = (%v, %v), want (true, nil)", attempted, err)
	}
	loaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession after flush: %v", err)
	}
	msgs := loaded.Snapshot()
	if len(msgs) != 4 || msgs[3].Content != "must not land yet" {
		t.Fatalf("flushed transcript = %d msgs (last %q), want the owed message persisted", len(msgs), msgs[len(msgs)-1].Content)
	}
}
