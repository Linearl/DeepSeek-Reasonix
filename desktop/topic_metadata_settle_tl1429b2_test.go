package main

// tl1429b-2 回归钉:归档元数据删除的进程内收敛链(侧栏残留③面)。包 79 现场:
// deleteTopic 输掉元数据文件锁后,marker 只能等**下次启动**的 reconcile——
// topic 在侧栏一直挂着直到用户手动再归档。修复:两连败后起后台有界重试链,
// 成功即清 marker + 发侧栏树事件,条目当轮消失;永久失败仍留 marker 给启动
// reconcile(原兜底不变)。钉:helper 三态语义 + 收敛清 marker + 永久失败保
// marker 的接线条件。

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/config"
)

func TestRetryTopicMetadataDeleteUntilSettledSemantics(t *testing.T) {
	// 前两次失败、第三次成功:收敛且只跑三次。
	var calls atomic.Int64
	delete := func(string) error {
		if calls.Add(1) < 3 {
			return errors.New("metadata lock contention")
		}
		return nil
	}
	if !retryTopicMetadataDeleteUntilSettled(delete, "topic_x", 4, time.Millisecond) {
		t.Fatal("a chain that eventually succeeds must settle")
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("attempts = %d, want exactly 3 (stop at first success)", got)
	}

	// 永久失败:false——marker 必须留给启动 reconcile。
	calls.Store(0)
	failing := func(string) error {
		calls.Add(1)
		return errors.New("permanent failure")
	}
	if retryTopicMetadataDeleteUntilSettled(failing, "topic_x", 3, time.Millisecond) {
		t.Fatal("a permanently failing chain must not report settled")
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("permanent-failure attempts = %d, want the bounded 3", got)
	}

	// 零次/nil delete:不跑、不收敛。
	if retryTopicMetadataDeleteUntilSettled(failing, "topic_x", 0, time.Millisecond) {
		t.Fatal("zero attempts must not settle")
	}
	if retryTopicMetadataDeleteUntilSettled(nil, "topic_x", 3, time.Millisecond) {
		t.Fatal("nil delete must not settle")
	}
}

func settleTestTopic(t *testing.T, topicID string) {
	t.Helper()
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	targets := []topicTrashTarget{{
		dir:         dir,
		sessionPath: filepath.Join(dir, "settle-fixture.jsonl"),
		key:         "settle-fixture.jsonl",
	}}
	if err := markTopicArchiveMetadataPending(topicID, targets); err != nil {
		t.Fatalf("mark metadata pending: %v", err)
	}
	t.Cleanup(func() { _ = clearTopicArchiveMetadataPending(topicID) })

	prevAttempts, prevInterval := topicArchiveMetadataDeleteSettleAttempts, topicArchiveMetadataDeleteSettleInterval
	topicArchiveMetadataDeleteSettleAttempts, topicArchiveMetadataDeleteSettleInterval = 2, time.Millisecond
	t.Cleanup(func() {
		topicArchiveMetadataDeleteSettleAttempts, topicArchiveMetadataDeleteSettleInterval = prevAttempts, prevInterval
	})
}

func TestSettleTopicArchiveMetadataDeleteClearsMarker(t *testing.T) {
	const topicID = "topic_settle_tl1429b2"
	settleTestTopic(t, topicID)
	app := NewApp()

	// 收敛链(真 deleteTopic 对未注册 topic 幂等成功)→ marker 当轮被清。
	app.settleTopicArchiveMetadataDelete(topicID)

	if _, err := os.Stat(topicArchiveMetadataPendingPath(topicID)); !os.IsNotExist(err) {
		t.Fatalf("metadata-pending marker survived a settled delete (err=%v)", err)
	}
}

func TestSettleTopicArchiveMetadataDeleteKeepsMarkerOnPermanentFailure(t *testing.T) {
	const topicID = "topic_settle_stuck_tl1429b2"
	settleTestTopic(t, topicID)
	app := NewApp()

	// 永久失败:marker 保留(启动 reconcile 兜底)——「只有 settled 才清」。
	app.settleTopicArchiveMetadataDeleteWith(func(string) error { return errors.New("stuck") }, topicID)

	if _, err := os.Stat(topicArchiveMetadataPendingPath(topicID)); os.IsNotExist(err) {
		t.Fatal("a permanently failing settle must keep the metadata-pending marker")
	}
}
