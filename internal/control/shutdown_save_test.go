package control

import (
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// TestSnapshotForShutdownKeepsSessionPathOnLockTimeout pins the tl1280
// shutdown contract: the schema-2 unlocked append (AppendForShutdownWithoutLock)
// left with the write-path slow lane, so a save that cannot take the file lock
// during shutdown always falls back to the recovery-copy path — the same one a
// schema-1 session always had. The tail is never silently dropped, the original
// transcript is never rewritten, and the recovery copy carries the shutdown
// notice so the caller can hand the file to the user.
func TestSnapshotForShutdownKeepsSessionPathOnLockTimeout(t *testing.T) {
	dir := t.TempDir()
	path := dir + string('/') + "session.jsonl"
	base := agent.NewSession("sys")
	base.Add(provider.Message{Role: provider.RoleUser, Content: "persisted"})
	if err := base.SaveSnapshot(path); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	current, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	current.Add(provider.Message{Role: provider.RoleAssistant, Content: "shutdown tail"})
	exec := agent.New(nil, nil, current, agent.Options{}, event.Discard)
	sink := &noticeSink{}
	var recovered []SessionRecoveryInfo
	c := New(Options{
		Executor: exec, SessionDir: dir, SessionPath: path, Label: "shutdown", Sink: sink,
		OnSessionRecovered: func(info SessionRecoveryInfo) error {
			recovered = append(recovered, info)
			return nil
		},
	})
	t.Cleanup(agent.SetSessionFileLockWaitForTest(40*time.Millisecond, 5*time.Millisecond))
	release, err := agent.HoldSessionFileLockForTest(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)

	if err := c.SnapshotForShutdown(); err != nil {
		t.Fatalf("SnapshotForShutdown: %v", err)
	}
	release()
	if len(recovered) != 1 {
		t.Fatalf("shutdown must hand the transcript to the recovery callback once, got %+v", recovered)
	}
	info := recovered[0]
	if info.OriginalPath != path {
		t.Fatalf("recovery original path = %q, want %q", info.OriginalPath, path)
	}
	if info.RecoveryPath == "" || info.RecoveryPath == path {
		t.Fatalf("recovery path = %q, want a distinct copy beside %q", info.RecoveryPath, path)
	}
	if notice, ok := sink.lastNotice(); !ok || notice.Code != event.NoticeCodeSessionShutdownRecoveryForked {
		t.Fatalf("shutdown notice = %+v ok=%v, want the recovery-copy notice", notice, ok)
	}
	// The original transcript is never rewritten under a held lock: the seed
	// bytes stay exactly as they were, and the tail lives only on the copy.
	reloaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession original: %v", err)
	}
	if got := reloaded.Snapshot(); len(got) != 2 || got[1].Content != "persisted" {
		t.Fatalf("original transcript = %+v, want the seed untouched", got)
	}
	copied, err := agent.LoadSession(info.RecoveryPath)
	if err != nil {
		t.Fatalf("LoadSession recovery copy: %v", err)
	}
	msgs := copied.Snapshot()
	if len(msgs) != 3 || msgs[2].Content != "shutdown tail" {
		t.Fatalf("recovery copy = %+v, want the unsaved tail preserved", msgs)
	}
	if heads, err := agent.ListSessionHeads(info.RecoveryPath); err != nil || len(heads) != 0 {
		t.Fatalf("ListSessionHeads shim on the recovery copy = %+v err=%v, want empty", heads, err)
	}
}
