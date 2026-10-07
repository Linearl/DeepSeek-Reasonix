package control

// 任务585 验收：turn 完成结算 vs 快照失败的重入缺口。
//
// 实测（2026-10-07 desktop.log）：每次更新重启窗口，收尾中的 inbox turn 的
// SnapshotActivity 以 session-write-authority-stale 失败（transcript 多已落盘，
// 见 16:21:40 「turn transcript saved before metadata update failed」），旧逻辑
// 把该轮全部 inbox item 一律 park 成 Uncertain+paused——已处理消息借这套残留
// 在重启后留在队列货架，任何重试/续跑都会再次注入。
//
// 修复语义（与 finishInFlightTurn 同契约）：
//   - transcript 已落盘（durable=true）即使 metadata 失败 → item 已应用，
//     正常 AckDequeue 结算，重启后零残留；
//   - transcript 未落盘（durable=false）→ 维持 Uncertain+paused（可见残留、
//     可人工恢复），绝不静默丢。

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/sessioninbox"
)

// newTurnDoneFixture 建一个带单条 running item 的控制器，item 已在活动集合
// （模拟该 item 已被本轮消费）。返回控制器与其 inbox store。
func newTurnDoneFixture(t *testing.T) (*Controller, *sessioninbox.Store, string) {
	t.Helper()
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(session, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := New(Options{SessionPath: session, SessionDir: dir, Sink: event.Discard})
	rec, err := c.EnqueueInbox(InboxRequest{Intent: sessioninbox.IntentFollowup, Submit: "585: already applied guidance"})
	if err != nil {
		t.Fatal(err)
	}
	st, err := c.ensureInbox()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetState(rec.ItemID, sessioninbox.StateRunning, ""); err != nil {
		t.Fatal(err)
	}
	c.inbox.mu.Lock()
	c.inbox.trackActive(rec.ItemID)
	c.inbox.mu.Unlock()
	return c, st, rec.ItemID
}

// 验收①：transcript 已落盘 + metadata 写失败（重启窗实况）→ item 正常结算，
// 队列零残留；恢复链（RecoverOrphanedInFlightOwnedBy）无可重放对象。
func TestInboxTurnDoneSettlesAppliedItemsWhenTranscriptDurable(t *testing.T) {
	c, st, id := newTurnDoneFixture(t)
	c.inbox.mu.Lock()
	c.inbox.snapshotCompletion = func() (bool, error) {
		// 生产实况形态：transcript 落盘成功，随后的 metadata 更新撞上
		// session-write-authority-stale（重启窗必然）。
		return true, errors.New("session write authority stale")
	}
	c.inbox.mu.Unlock()

	c.onInboxTurnDone()

	snap := st.Snapshot()
	if snap.Paused {
		t.Fatal("a durable-completed turn must not pause the inbox")
	}
	if len(snap.Items) != 0 {
		t.Fatalf("applied items must be settled (deleted), got %+v", snap.Items)
	}
	if _, _, err := st.ReadItem(id); !errors.Is(err, sessioninbox.ErrNotFound) {
		t.Fatalf("settled item must be gone, got err=%v", err)
	}

	// 恢复链幂等复核：重开恢复（重启等价）后无 uncertain/无重放对象。
	if n, err := st.RecoverOrphanedInFlightOwnedBy(func(string) bool { return false }, nil); err != nil || n != 0 {
		t.Fatalf("recovery after settled completion recovered %d items (err=%v), want 0", n, err)
	}
	if snap2 := st.Snapshot(); len(snap2.Items) != 0 || snap2.Paused {
		t.Fatalf("recovery resurrected residue: %+v paused=%v", snap2.Items, snap2.Paused)
	}
}

// 验收②保守面：transcript 未落盘（真写失败）→ 维持 Uncertain+paused 可见残留，
// 绝不静默丢（宁可重入可见不可丢消息）。
func TestInboxTurnDoneKeepsUncertainWhenTranscriptNotDurable(t *testing.T) {
	c, st, id := newTurnDoneFixture(t)
	c.inbox.mu.Lock()
	c.inbox.snapshotCompletion = func() (bool, error) {
		return false, errors.New("session log append failed")
	}
	c.inbox.mu.Unlock()

	c.onInboxTurnDone()

	snap := st.Snapshot()
	if !snap.Paused {
		t.Fatal("an undurable completion must pause the inbox for review")
	}
	if len(snap.Items) != 1 {
		t.Fatalf("the item must be kept as recoverable work, got %+v", snap.Items)
	}
	item := snap.Items[0]
	if item.ID != id || item.State != sessioninbox.StateUncertain {
		t.Fatalf("item = %+v, want id=%s state=uncertain", item, id)
	}
	if item.BlockReason != "turn completed but transcript snapshot failed" {
		t.Fatalf("blockReason = %q, want the snapshot-failure reason", item.BlockReason)
	}
}
