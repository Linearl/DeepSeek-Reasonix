package provider

// 任务 tl1327 ② 断网熔断的时序测试：连续拨号失败判定网络不可达 → 暂停全部
// provider 拨号 → 冷却到期仅放一条金丝雀 → 成功统一放行 / 失败续闭。时序读数
// （熔断触发点、暂停期快速失败延迟）打印到测试输出供交付报告引用。

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var errDialRefused = errors.New("dial tcp: connection refused (simulated outage)")

func refusedDialer(fail *atomic.Bool) DialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if fail.Load() {
			return nil, errDialRefused
		}
		return nil, nil // 网络恢复：拨号成功
	}
}

func TestDialBreakerOpensAfterConsecutiveFailuresAndDeniesFast(t *testing.T) {
	fail := atomic.Bool{}
	fail.Store(true)
	g, restoreGate := dialGateForTest(DialGateSettings{MaxConcurrent: 8}, nil)
	defer restoreGate()

	// 阈值前：失败照常返回原始错误（现状行为，熔断不改变错误面）。
	for i := 0; i < 2; i++ {
		_, err := g.dial(context.Background(), refusedDialer(&fail), "tcp", "x:443")
		if !errors.Is(err, errDialRefused) {
			t.Fatalf("pre-threshold dial %d: got %v, want raw dial error", i, err)
		}
	}
	openedAt := time.Now()
	_, err := g.dial(context.Background(), refusedDialer(&fail), "tcp", "x:443") // 第 3 连败→熔断
	if !errors.Is(err, errDialRefused) {
		t.Fatalf("threshold dial: got %v, want raw dial error", err)
	}
	openLatency := time.Since(openedAt)

	// 暂停期：快速失败 + 哨兵可识别，绝不再碰网络（碰了=SYN_SENT 风暴）。
	var denyLatencySum time.Duration
	for i := 0; i < 5; i++ {
		start := time.Now()
		_, err := g.dial(context.Background(), refusedDialer(&fail), "tcp", "x:443")
		denyLatencySum += time.Since(start)
		if !IsDialPaused(err) {
			t.Fatalf("paused dial %d: got %v (%T), want DialPausedError", i, err, err)
		}
	}
	snap := DialGateSnapshotNow()
	t.Logf("时序读数: 第3次连败触发熔断(阈值3,触发点即最后一次真实拨号返回,附加耗时=%s); 暂停期5次快速失败平均=%s; openUntil=+%.1fs",
		openLatency.Round(time.Microsecond), (denyLatencySum / 5).Round(time.Microsecond), time.Until(snap.BreakerOpenUntil).Seconds())
	if snap.ConsecutiveFails != 3 {
		t.Fatalf("consecutive failures %d, want 3 (denied dials must not add evidence)", snap.ConsecutiveFails)
	}
	if snap.BreakerOpenUntil.IsZero() {
		t.Fatal("breaker did not open after threshold failures")
	}
}

func TestDialBreakerCanaryExclusiveThenRelease(t *testing.T) {
	// 冷却到期：并发 16 请求争抢，恰好 1 条金丝雀过闸（probing CAS 单飞），
	// 其余 15 条快速失败；金丝雀成功→熔断关闭→统一放行。
	fail := atomic.Bool{}
	fail.Store(true)
	base := time.Now()
	var mu sync.Mutex
	now := base
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	g, restoreGate := dialGateForTest(DialGateSettings{Cooldown: 45 * time.Second}, clock)
	defer restoreGate()

	for i := 0; i < 3; i++ { // 触发熔断
		_, _ = g.dial(context.Background(), refusedDialer(&fail), "tcp", "x:443")
	}
	if _, err := g.dial(context.Background(), refusedDialer(&fail), "tcp", "x:443"); !IsDialPaused(err) {
		t.Fatalf("during cooldown: got %v, want pause", err)
	}

	mu.Lock()
	now = base.Add(46 * time.Second)
	mu.Unlock()
	var canaries, paused atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := g.enter(context.Background())
			switch {
			case err == nil:
				canaries.Add(1) // 金丝雀：probing 置位，模拟「拨号在飞」直到 record
			case IsDialPaused(err):
				paused.Add(1)
			default:
				t.Errorf("unexpected enter error: %v", err)
			}
		}()
	}
	wg.Wait()
	if canaries.Load() != 1 {
		t.Fatalf("canaries admitted %d, want exactly 1 (single-flight probe)", canaries.Load())
	}
	if paused.Load() != 15 {
		t.Fatalf("paused while canary in flight %d, want 15", paused.Load())
	}

	// 金丝雀成功 → 熔断关闭 → 统一放行（不再有暂停）。
	fail.Store(false)
	g.record(nil)
	snap := DialGateSnapshotNow()
	if !snap.BreakerOpenUntil.IsZero() || snap.ConsecutiveFails != 0 {
		t.Fatalf("breaker not closed after canary success: %+v", snap)
	}
	if err := g.enter(context.Background()); err != nil {
		t.Fatalf("post-release enter denied: %v", err)
	}
	conn, err := g.dial(context.Background(), refusedDialer(&fail), "tcp", "x:443")
	if err != nil || conn != nil {
		t.Fatalf("post-release dial: conn=%v err=%v", conn, err)
	}
}

func TestDialBreakerCanaryFailureReopensCooldown(t *testing.T) {
	// 金丝雀失败 → 续闭一个完整冷却期，且期间继续快速失败。
	fail := atomic.Bool{}
	fail.Store(true)
	base := time.Now()
	var mu sync.Mutex
	now := base
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	g, restoreGate := dialGateForTest(DialGateSettings{Cooldown: 45 * time.Second}, clock)
	defer restoreGate()

	for i := 0; i < 3; i++ {
		_, _ = g.dial(context.Background(), refusedDialer(&fail), "tcp", "x:443")
	}
	mu.Lock()
	now = base.Add(46 * time.Second)
	mu.Unlock()
	_, err := g.dial(context.Background(), refusedDialer(&fail), "tcp", "x:443") // 金丝雀（失败）
	if !errors.Is(err, errDialRefused) {
		t.Fatalf("canary: got %v, want raw dial error", err)
	}
	mu.Lock()
	openUntil := time.Unix(0, g.openUntil.Load())
	mu.Unlock()
	wantReopen := base.Add(46*time.Second + 45*time.Second)
	if d := openUntil.Sub(wantReopen); d > time.Millisecond || d < -time.Millisecond {
		t.Fatalf("canary failure reopen = %s, want %s (full cooldown re-armed)", openUntil.Format("15:04:05.000"), wantReopen.Format("15:04:05.000"))
	}
	if _, err := g.dial(context.Background(), refusedDialer(&fail), "tcp", "x:443"); !IsDialPaused(err) {
		t.Fatalf("after canary failure: got %v, want pause", err)
	}
}

func TestDialBreakerAnySuccessReleasesImmediately(t *testing.T) {
	// 恢复即放行：熔断打开期间，任何一条在途真实拨号成功（含 pre-open 在飞
	// 的）都立即关闭熔断——网络恢复不需要等满冷却。
	fail := atomic.Bool{}
	fail.Store(true)
	g, restoreGate := dialGateForTest(DialGateSettings{}, nil)
	defer restoreGate()

	blocked := make(chan struct{})
	controlled := func(ctx context.Context, network, addr string) (net.Conn, error) {
		<-blocked // 在飞：enter 已过、拨号未归
		if fail.Load() {
			return nil, errDialRefused
		}
		return nil, nil
	}
	inFlightDone := make(chan struct{})
	go func() {
		defer close(inFlightDone)
		_, _ = g.dial(context.Background(), controlled, "tcp", "x:443")
	}()
	for int64(len(g.slots)) < 1 {
		time.Sleep(time.Millisecond)
	}
	// 三连败打开熔断。
	for i := 0; i < 3; i++ {
		_, _ = g.dial(context.Background(), refusedDialer(&fail), "tcp", "x:443")
	}
	if snap := DialGateSnapshotNow(); snap.BreakerOpenUntil.IsZero() {
		t.Fatal("precondition: breaker should be open")
	}
	// 在途拨号成功归队 → 熔断立即关闭。
	fail.Store(false)
	close(blocked)
	<-inFlightDone
	if snap := DialGateSnapshotNow(); !snap.BreakerOpenUntil.IsZero() {
		t.Fatalf("success during open did not close breaker: openUntil=%s", snap.BreakerOpenUntil.Format("15:04:05.000"))
	}
	if err := g.enter(context.Background()); err != nil {
		t.Fatalf("post-recovery enter denied: %v", err)
	}
}

func TestDialBreakerCallerCancelIsNotNetworkEvidence(t *testing.T) {
	// 用户取消回合 ≠ 网络断：ctx 取消的拨号不得计入熔断证据。
	g, restoreGate := dialGateForTest(DialGateSettings{}, nil)
	defer restoreGate()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 10; i++ {
		_, _ = g.dial(ctx, func(ctx context.Context, network, addr string) (net.Conn, error) {
			return nil, context.Canceled
		}, "tcp", "x:443")
	}
	if snap := DialGateSnapshotNow(); snap.ConsecutiveFails != 0 || !snap.BreakerOpenUntil.IsZero() {
		t.Fatalf("cancels counted as network evidence: %+v", snap)
	}
}

func TestDialGateSnapshotObservability(t *testing.T) {
	// ④ 基线/事故排查用的观测面：容量、在途、排队、峰值。
	g, restoreGate := dialGateForTest(DialGateSettings{MaxConcurrent: 2}, nil)
	defer restoreGate()

	release := make(chan struct{})
	blocking := func(ctx context.Context, network, addr string) (net.Conn, error) {
		<-release
		return nil, nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = g.dial(context.Background(), blocking, "tcp", "x:443")
		}()
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if s := DialGateSnapshotNow(); s.InFlight == 2 && s.Waiting >= 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	snap := DialGateSnapshotNow()
	t.Logf("观测读数: capacity=%d inFlight=%d waiting=%d peak=%d disabled=%v",
		snap.Capacity, snap.InFlight, snap.Waiting, snap.PeakInFlight, snap.Disabled)
	if snap.Capacity != 2 || snap.InFlight != 2 || snap.Waiting < 2 {
		t.Fatalf("snapshot mismatch: %+v", snap)
	}
	close(release)
	wg.Wait()
}
