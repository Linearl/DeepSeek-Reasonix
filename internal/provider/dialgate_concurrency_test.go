package provider

// 任务 tl1327 ① 拨号并发预算的测试。SYN_SENT 有界的进程内等价物：只有拿到
// gate 槽的拨号才允许进入「拨号中」状态（真实世界里这一步就是内核发 SYN、
// 挂 SYN_SENT），断言同时在拨号中的数量 ≤ 预算，其余排队。

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// blockedDialer tracks concurrency inside the dial itself — the offline
// stand-in for a SYN with no SYN-ACK (SYN_SENT).
type blockedDialer struct {
	mu       sync.Mutex
	inFlight int
	peak     int
	release  chan struct{}
	started  atomic.Int64
}

func (b *blockedDialer) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	b.mu.Lock()
	b.inFlight++
	if b.inFlight > b.peak {
		b.peak = b.inFlight
	}
	b.mu.Unlock()
	b.started.Add(1)
	<-b.release
	b.mu.Lock()
	b.inFlight--
	b.mu.Unlock()
	return nil, errors.New("blocked dialer has no conn")
}

func (b *blockedDialer) current() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.inFlight
}

func (b *blockedDialer) peakSeen() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.peak
}

// dialerFunc adapts a bare func to DialFunc.
func dialerFunc(f func(ctx context.Context, network, addr string) (net.Conn, error)) DialFunc {
	return f
}

func TestDialGateBoundsConcurrentDials(t *testing.T) {
	// 验收断言（tl1327）：拨号全挂场景下，进程同时在途的新建连接 ≤ 预算；
	// 其余必须排队而非失败；网络恢复（release）后全部放行。
	const budget = 4
	const callers = 32
	prevPatience := queuePatience
	queuePatience = 5 * time.Second
	defer func() { queuePatience = prevPatience }()

	inner := &blockedDialer{release: make(chan struct{})}
	// FailureThreshold 抬到不可能触发：本测试只验 ① 的并发界，熔断在②测。
	g, restoreGate := dialGateForTest(DialGateSettings{MaxConcurrent: budget, FailureThreshold: 1 << 20}, nil)
	defer restoreGate()

	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = g.dial(context.Background(), inner.dial, "tcp", "example.invalid:443")
		}()
	}
	// 等风暴饱和：预算槽全被占、且仍有排队者。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snap := DialGateSnapshotNow()
		if snap.InFlight == budget && snap.Waiting >= budget {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if got := inner.current(); got > budget {
		t.Fatalf("in-flight dials %d exceed budget %d (SYN_SENT unbounded)", got, budget)
	}
	if snap := DialGateSnapshotNow(); snap.Waiting == 0 {
		t.Fatal("no waiters queued behind the budget — callers must queue, not fail")
	}
	close(inner.release) // "网络恢复"：排队的拨号依次放行
	wg.Wait()
	if inner.started.Load() != callers {
		t.Fatalf("dials started %d, want %d — queued callers must all eventually dial", inner.started.Load(), callers)
	}
	if peak := inner.peakSeen(); peak > budget {
		t.Fatalf("peak in-flight %d exceed budget %d", peak, budget)
	}
	if snap := DialGateSnapshotNow(); snap.PeakInFlight > budget {
		t.Fatalf("gate peak in-flight %d exceed budget %d", snap.PeakInFlight, budget)
	}
	if snap := DialGateSnapshotNow(); snap.InFlight != 0 {
		t.Fatalf("slot leaked: %d still in flight after all dials returned", snap.InFlight)
	}
}

func TestDialGateQueuePatienceBounded(t *testing.T) {
	// 无界等待=可用性缺陷：排队有耐心上限；超时是类型化可重试错误且不吞槽。
	prevPatience := queuePatience
	queuePatience = 60 * time.Millisecond
	defer func() { queuePatience = prevPatience }()

	inner := &blockedDialer{release: make(chan struct{})}
	g, restoreGate := dialGateForTest(DialGateSettings{MaxConcurrent: 1, FailureThreshold: 1 << 20}, nil)
	defer restoreGate()

	// 占住唯一的槽。
	go func() { _, _ = g.dial(context.Background(), inner.dial, "tcp", "x:443") }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && inner.current() < 1 {
		time.Sleep(2 * time.Millisecond)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := g.dial(context.Background(), inner.dial, "tcp", "x:443")
		errCh <- err
	}()
	select {
	case err := <-errCh:
		var qerr *DialQueueTimeoutError
		if !errors.As(err, &qerr) {
			t.Fatalf("queued dial: got %v (%T), want DialQueueTimeoutError", err, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queue patience not enforced — unbounded wait")
	}
	close(inner.release)
}

func TestDialGateCtxCancelWhileQueued(t *testing.T) {
	inner := &blockedDialer{release: make(chan struct{})}
	g, restoreGate := dialGateForTest(DialGateSettings{MaxConcurrent: 1, FailureThreshold: 1 << 20}, nil)
	defer restoreGate()

	holder := make(chan struct{})
	go func() { _, _ = g.dial(context.Background(), inner.dial, "tcp", "x:443") }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && inner.current() < 1 {
		time.Sleep(2 * time.Millisecond)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := g.dial(ctx, inner.dial, "tcp", "x:443")
		errCh <- err
	}()
	time.Sleep(50 * time.Millisecond) // 让它进入排队
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued dial after cancel: got %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled waiter not freed — unbounded queue wait")
	}
	close(inner.release)
	close(holder)
}

func TestDialGateDisabledPassesThroughUnbounded(t *testing.T) {
	// 回退路径：disabled = true → 行为退回现状（无预算、无排队、无熔断）。
	inner := &blockedDialer{release: make(chan struct{})}
	disabled := true
	_, restoreGate := dialGateForTest(DialGateSettings{Disabled: &disabled, MaxConcurrent: 1}, nil)
	defer restoreGate()

	var inFlight, peak atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// 走真实链路 gatedDial（transport 实际调用的入口），而非内部 g.dial。
			_, _ = gatedDial(context.Background(), dialerFunc(func(ctx context.Context, network, addr string) (net.Conn, error) {
				cur := inFlight.Add(1)
				for {
					p := peak.Load()
					if cur <= p || peak.CompareAndSwap(p, cur) {
						break
					}
				}
				<-inner.release
				inFlight.Add(-1)
				return nil, errors.New("done")
			}), "tcp", "x:443")
		}()
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && inFlight.Load() < 8 {
		time.Sleep(2 * time.Millisecond)
	}
	close(inner.release)
	wg.Wait()
	if peak.Load() != 8 {
		t.Fatalf("disabled gate still bounds dials: peak %d, want unbounded 8", peak.Load())
	}
}

func TestGatedDialFailOpenOnGatePanic(t *testing.T) {
	// fail-open 契约：门自身故障（注入 nowFn panic）→ 落回原始拨号。
	g, restoreGate := dialGateForTest(DialGateSettings{}, nil)
	defer restoreGate()
	g.nowFn = func() time.Time { panic("gate bookkeeping bug") }

	done := make(chan error, 1)
	go func() {
		_, err := gatedDial(context.Background(), dialerFunc(func(ctx context.Context, network, addr string) (net.Conn, error) {
			return nil, nil
		}), "tcp", "x:443")
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("fail-open dial failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("gate panic did not fall back to plain dial — fail-open broken")
	}
}
