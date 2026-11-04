package turnevent

import (
	"os"
	"runtime"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/store"
)

// tl1390：活跃 turn 期间账本写手长期持有 *.turns.jsonl 的 O_APPEND 句柄，
// Windows 上 Go 的 os.Open 共享模式不含 FILE_SHARE_DELETE，句柄未关的文件
// 无法 rename/replace（MoveFileEx 拿不到 DELETE 权限 → "being used by
// another process"）。2026-11-04 转正事故的永久 declined 即此机制。本文件
// 钉住 WithWriterQuiesced 的三面契约：quiesce 期间句柄确实关闭（文件可搬）、
// fn 返回后 append 经 ensureWriterLocked 按路径重开且零丢失、未 quiesce 时
// 裸 rename 确实会输给打开的写手（仅 Windows 可观测）。
func TestLedgerWriterQuiesceReleasesHandleForSidecarMove(t *testing.T) {
	sessionPath := testSessionPath(t)
	l := openTestLedger(t, sessionPath, "session")
	if _, err := l.Begin(); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, ok, err := l.Append(event.Event{Kind: event.UserInput, Text: "quiesce 前的记录"}, event.TurnInProgress); err != nil || !ok {
		t.Fatalf("append before quiesce: ok=%v err=%v", ok, err)
	}
	turnsPath := store.SessionTurnEventLog(sessionPath)

	// 事故前提腿（仅 Windows 可观测）：写手开着时裸 rename 必败。
	if runtime.GOOS == "windows" {
		if err := os.Rename(turnsPath, turnsPath+".probe"); err == nil {
			_ = os.Rename(turnsPath+".probe", turnsPath)
			t.Fatal("expected rename to fail while the ledger writer holds the file open")
		}
	} else {
		if err := os.Rename(turnsPath, turnsPath+".probe"); err == nil {
			_ = os.Rename(turnsPath+".probe", turnsPath)
		}
	}

	// 契约一：quiesce 期间句柄关闭，文件可搬（转正搬运的前置条件）。
	// 注意 fn 在 l.mu 保护下执行——不能用锁内读 l.writer 断言，只能用行为
	// 断言（Windows 上句柄未关则 rename 必败，rename 成功即句柄已关的铁证）。
	movedDuringQuiesce := false
	if err := l.WithWriterQuiesced(func() error {
		if err := os.Rename(turnsPath, turnsPath+".moved"); err != nil {
			t.Fatalf("rename during quiesce: %v", err)
		}
		movedDuringQuiesce = true
		return os.Rename(turnsPath+".moved", turnsPath)
	}); err != nil {
		t.Fatalf("WithWriterQuiesced: %v", err)
	}
	if !movedDuringQuiesce {
		t.Fatal("quiesce body did not run")
	}

	// 契约二：fn 返回后 append 按路径重开写手，内容零丢失。
	if _, ok, err := l.Append(event.Event{Kind: event.UserInput, Text: "quiesce 后的记录"}, event.TurnInProgress); err != nil || !ok {
		t.Fatalf("append after quiesce: ok=%v err=%v", ok, err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened, err := Open(sessionPath, "session")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("Close reopened ledger: %v", err)
		}
	})
	view, err := reopened.Replay(0)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	found := map[string]bool{}
	for i := range view.Events {
		found[view.Events[i].Event.Text] = true
	}
	if !found["quiesce 前的记录"] || !found["quiesce 后的记录"] {
		t.Fatalf("replay lost records across quiesce: %v", found)
	}
}

func TestLedgerWriterQuiesceNilLedgerRunsBody(t *testing.T) {
	ran := false
	if err := (*Ledger)(nil).WithWriterQuiesced(func() error {
		ran = true
		return nil
	}); err != nil {
		t.Fatalf("nil ledger quiesce: %v", err)
	}
	if !ran {
		t.Fatal("nil-ledger quiesce did not run the body")
	}
}
