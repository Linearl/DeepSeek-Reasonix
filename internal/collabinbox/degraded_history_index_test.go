package collabinbox

// 任务511 修一的索引层验收：mail 锁繁忙 → History 空行集 + degraded →
// 快照 Degraded=true，前端据此显示「收件箱暂时不可用（锁繁忙）」而非空态
// 「暂无信件」（排查报告 §4 缺口 1）。

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/filelock"
	"reasonix/internal/sessioncollab"
)

func TestMailLockBusyMarksSnapshotDegraded(t *testing.T) {
	store, mail := fixtureStore(t)
	deliver(t, mail, sessioncollab.MailMessage{From: "sc_peer", To: "sc_me", Body: "real data on disk"})

	// 楔住 .mail.lock（独占）——收件箱锁本身健康，只有传输层读被楔住。
	wedge, err := filelock.TryAcquire(filepath.Join(store.mailDir, ".mail.lock"))
	if err != nil {
		t.Fatalf("wedge the mail lock: %v", err)
	}
	defer wedge()

	// applyRetention=false：隔离 History 这一个降级源（保留期另测）。
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	snap, err := store.List(ctx, Query{}, false)
	if err != nil {
		t.Fatalf("degraded read must not fail the list: %v", err)
	}
	if !snap.Degraded {
		t.Fatal("snapshot must be marked Degraded when the transport read was lock-busy")
	}
	if snap.Total != 0 {
		t.Fatalf("a lock-busy transport yields no rows, got total=%d", snap.Total)
	}
}

func TestHistoryHealthyKeepsSnapshotClean(t *testing.T) {
	store, mail := fixtureStore(t)
	deliver(t, mail, sessioncollab.MailMessage{From: "sc_peer", To: "sc_me", Body: "healthy path"})

	snap, err := store.List(context.Background(), Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Degraded {
		t.Fatal("a healthy read must never be marked Degraded")
	}
	if snap.Total != 1 {
		t.Fatalf("healthy read must carry the data, got total=%d", snap.Total)
	}
}
