package collabinbox

// 任务511 修二（sweep 节流闸）验收：距上次成功 sweep 不足窗口时跳过全库
// 重写，窗口过后恢复；跳过是健康快路径，绝不打 Degraded；失败不盖时间戳，
// 下一次读重试（排查报告 §4 缺口 2）。

import (
	"context"
	"testing"

	"reasonix/internal/sessioncollab"
)

func TestSweepThrottleSkipsWithinWindow(t *testing.T) {
	store, mail := fixtureStore(t)
	deliver(t, mail, sessioncollab.MailMessage{From: "sc_peer", To: "sc_me", Body: "keep me"})

	base := store.now()
	sweeps := 0
	realSweep := store.sweep
	store.sweep = func(ctx context.Context) (int, error) {
		sweeps++
		return realSweep(ctx)
	}
	listAt := func(atMs int64) Snapshot {
		t.Helper()
		store.now = func() int64 { return atMs }
		snap, err := store.List(context.Background(), Query{}, true)
		if err != nil {
			t.Fatalf("list at %d: %v", atMs, err)
		}
		return snap
	}

	// 第 1 次：从未 sweep 过（lastSweepAt 缺省）→ 执行并盖时间戳。
	s1 := listAt(base)
	if sweeps != 1 {
		t.Fatalf("the first panel read must sweep, got %d sweeps", sweeps)
	}
	if st := store.loadState(); st.LastSweepAt != base {
		t.Fatalf("a successful sweep must stamp lastSweepAt=%d, got %d", base, st.LastSweepAt)
	}

	// 窗口内连点（排查实证的连点桶自碰撞现场）：全部跳过，不再全库重写。
	listAt(base + 60_000)
	listAt(base + 3*60_000)
	listAt(base + sweepThrottleWindow.Milliseconds() - 1)
	if sweeps != 1 {
		t.Fatalf("reads within the throttle window must skip the sweep, got %d sweeps", sweeps)
	}

	// 窗口过后 → 恢复执行（低频维护而非每次读都重写）。
	s2 := listAt(base + sweepThrottleWindow.Milliseconds())
	if sweeps != 2 {
		t.Fatalf("a read past the window must sweep again, got %d sweeps", sweeps)
	}
	if s1.Degraded || s2.Degraded {
		t.Fatal("the throttled skip is a healthy fast path — it must never mark the snapshot Degraded")
	}
}

// sweep 失败不盖时间戳：下一次面板读会重试维护（可用性语义下的自愈）。
func TestSweepFailureDoesNotStampThrottle(t *testing.T) {
	store, _ := fixtureStore(t)
	if err := presetRetentionForever(store); err != nil {
		t.Fatal(err)
	}
	base := store.now()
	store.now = func() int64 { return base }
	store.sweep = func(context.Context) (int, error) { return 0, context.DeadlineExceeded }
	if _, err := store.List(context.Background(), Query{}, true); err != nil {
		t.Fatal(err)
	}
	if st := store.loadState(); st.LastSweepAt != 0 {
		t.Fatalf("a failed sweep must not stamp lastSweepAt, got %d", st.LastSweepAt)
	}
	// sweep 恢复健康后的下一次读：重试并盖时间戳。
	store.sweep = store.ApplyRetention
	if _, err := store.List(context.Background(), Query{}, true); err != nil {
		t.Fatal(err)
	}
	if st := store.loadState(); st.LastSweepAt != base {
		t.Fatalf("the retried sweep must stamp lastSweepAt=%d, got %d", base, st.LastSweepAt)
	}
}

// presetRetentionForever 预置 retention=forever，让 sweep 无事可做
// （失败注入不被保留期剪枝干扰）。
func presetRetentionForever(store *Store) error {
	st := store.loadState()
	st.Retention = RetentionForever
	return store.saveState(st)
}
