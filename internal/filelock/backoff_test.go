package filelock

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func stubJitter(t *testing.T, next func() float64) {
	t.Helper()
	prev := jitterNext
	jitterNext = next
	t.Cleanup(func() { jitterNext = prev })
}

// TestBackoffBaseLadder pins the task 474 ladder: 20ms×5 → 50ms×2 → 100ms×2 →
// 200ms cap. The first five retries stay on the pre-474 fixed 20ms cadence so
// short critical sections keep their ≤20ms release-discovery latency.
func TestBackoffBaseLadder(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 20 * time.Millisecond},
		{4, 20 * time.Millisecond},
		{5, 50 * time.Millisecond},
		{6, 50 * time.Millisecond},
		{7, 100 * time.Millisecond},
		{8, 100 * time.Millisecond},
		{9, 200 * time.Millisecond},
		{100, 200 * time.Millisecond},
	}
	for _, tc := range cases {
		if got := backoffBase(tc.attempt); got != tc.want {
			t.Fatalf("backoffBase(%d) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}

// TestNextRetryDelayBudgetAdaptiveCap pins the budget-adaptive cap: with a
// large budget the ladder is untouched; a 750ms sidecar budget caps the deep
// steps at budget/8 (≈94ms); a tiny budget shrinks further so roughly eight
// chances always remain. The budget is the retry-phase budget, measured once
// when the loop starts (NOT recomputed from the shrinking remainder — that
// would add a geometric decay tail that inflates the attempt count).
func TestNextRetryDelayBudgetAdaptiveCap(t *testing.T) {
	stubJitter(t, func() float64 { return 0.5 }) // 无抖动

	// 5s 预算：阶梯全额生效，200ms 封顶。
	if got := nextRetryDelay(0, 5*time.Second); got != 20*time.Millisecond {
		t.Fatalf("floor step with 5s budget = %v, want 20ms", got)
	}
	if got := nextRetryDelay(9, 5*time.Second); got != 200*time.Millisecond {
		t.Fatalf("deep step with 5s budget = %v, want 200ms", got)
	}
	// 750ms sidecar 预算：地板段不受影响，深段封顶 750/8 ≈ 93.75ms。
	if got := nextRetryDelay(4, 750*time.Millisecond); got != 20*time.Millisecond {
		t.Fatalf("floor step with 750ms budget = %v, want 20ms", got)
	}
	if got := nextRetryDelay(9, 750*time.Millisecond); got != 750*time.Millisecond/8 {
		t.Fatalf("deep step with 750ms budget = %v, want %v (750/8)", got, 750*time.Millisecond/8)
	}
	// 预算极小：退避压到预算/8，保证还有后续尝试机会。
	if got := nextRetryDelay(9, 80*time.Millisecond); got != 10*time.Millisecond {
		t.Fatalf("deep step with 80ms budget = %v, want 10ms (80/8)", got)
	}
	if got := nextRetryDelay(9, time.Millisecond); got != time.Millisecond/8 {
		t.Fatalf("deep step with 1ms budget = %v, want %v (1/8)", got, time.Millisecond/8)
	}
	// 无 deadline（防御路径）：只受 200ms 常量封顶。
	if got := nextRetryDelay(9, 1<<62); got != 200*time.Millisecond {
		t.Fatalf("deep step without deadline = %v, want 200ms", got)
	}
}

// TestNextRetryDelayJitterBounds pins the ±25% jitter: mid-ladder steps jitter
// symmetrically, while the deep step is clamped back under the 200ms cap so
// the published release-discovery bound (X8 §2.1: 20ms → ~200ms) still holds
// at maximum jitter.
func TestNextRetryDelayJitterBounds(t *testing.T) {
	cases := []struct {
		f       float64
		attempt int
		want    time.Duration
	}{
		{0, 9, 150 * time.Millisecond},   // -25%
		{0.5, 9, 200 * time.Millisecond}, //  0%
		{1, 9, 200 * time.Millisecond},   // +25%，被 200ms cap 夹回
		{0, 5, 37500 * time.Microsecond}, // 50ms 地板段 -25%
		{1, 5, 62500 * time.Microsecond}, // 50ms 地板段 +25%（未触 cap，对称抖动）
	}
	for _, tc := range cases {
		stubJitter(t, func() float64 { return tc.f })
		if got := nextRetryDelay(tc.attempt, 5*time.Second); got != tc.want {
			t.Fatalf("jitterNext=%v attempt=%d: delay = %v, want %v", tc.f, tc.attempt, got, tc.want)
		}
	}
	// 最大抖动下仍不得突破段预算/8。
	stubJitter(t, func() float64 { return 1 })
	if got := nextRetryDelay(9, 750*time.Millisecond); got != 750*time.Millisecond/8 {
		t.Fatalf("max jitter must not exceed budget cap: got %v, want %v", got, 750*time.Millisecond/8)
	}
}

// TestRetryScheduleFitsAnyBudget pins the availability promise of the
// budget-adaptive cap: no matter how small the budget, the schedule always
// leaves room for roughly eight or more tries within the phase budget. The
// simulation mirrors the real loop: the budget is fixed when the loop starts
// and every step is capped at budget/8.
func TestRetryScheduleFitsAnyBudget(t *testing.T) {
	stubJitter(t, func() float64 { return 0.5 })
	for _, budget := range []time.Duration{
		60 * time.Millisecond, 200 * time.Millisecond, 750 * time.Millisecond,
		1500 * time.Millisecond, 5 * time.Second,
	} {
		spent := time.Duration(0)
		attempts := 0
		for spent <= budget {
			d := nextRetryDelay(attempts, budget)
			if d <= 0 {
				t.Fatalf("budget %v: zero delay at attempt %d", budget, attempts)
			}
			spent += d
			attempts++
			if attempts > 100000 {
				t.Fatalf("budget %v: schedule did not converge", budget)
			}
		}
		if attempts < 8 {
			t.Fatalf("budget %v: only %d attempts fit, want ≥8", budget, attempts)
		}
	}
}

// TestAcquireCancelDuringDeepBackoffReturnsFast pins the 461-P1 stop SLA in
// the deep-backoff phase: by 600ms the contended acquirer is sleeping in
// 100-200ms steps, and caller cancellation must still end the wait in under a
// second.
func TestAcquireCancelDuringDeepBackoffReturnsFast(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.lock")
	releaseExternal, err := tryLockFile(path)
	if err != nil {
		t.Fatalf("hold external file lock: %v", err)
	}
	defer releaseExternal()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(600*time.Millisecond, cancel)
	started := time.Now()
	_, err = Acquire(ctx, path)
	elapsed := time.Since(started)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("acquire error = %v, want context.Canceled", err)
	}
	if elapsed >= time.Second {
		t.Fatalf("acquire returned after %v, want <1s after caller cancel in deep backoff (task 461 P1 停止 SLA)", elapsed)
	}
}

// TestAcquireDiscoversReleaseWithinBackoffCap pins the discovery-latency cost
// of the ladder: a release during the floor phase is found on the 20ms
// cadence (zero regression for short critical sections), and a release during
// the deep-backoff phase is found within the 200ms cap plus slack.
func TestAcquireDiscoversReleaseWithinBackoffCap(t *testing.T) {
	t.Run("floor phase", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.lock")
		releaseExternal, err := tryLockFile(path)
		if err != nil {
			t.Fatalf("hold external file lock: %v", err)
		}
		acquired := make(chan struct{})
		go func() {
			release, acquireErr := Acquire(context.Background(), path)
			if acquireErr == nil {
				release()
			}
			close(acquired)
		}()
		time.Sleep(60 * time.Millisecond) // 仍在 20ms 地板段
		started := time.Now()
		releaseExternal()
		select {
		case <-acquired:
		case <-time.After(2 * time.Second):
			t.Fatal("acquire never completed after release")
		}
		if d := time.Since(started); d > 100*time.Millisecond {
			t.Fatalf("floor-phase discovery took %v after release, want ≤20ms+slack", d)
		}
	})
	t.Run("deep backoff phase", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.lock")
		releaseExternal, err := tryLockFile(path)
		if err != nil {
			t.Fatalf("hold external file lock: %v", err)
		}
		acquired := make(chan struct{})
		go func() {
			release, acquireErr := Acquire(context.Background(), path)
			if acquireErr == nil {
				release()
			}
			close(acquired)
		}()
		time.Sleep(1200 * time.Millisecond) // 已进入 200ms 封顶段
		started := time.Now()
		releaseExternal()
		select {
		case <-acquired:
		case <-time.After(2 * time.Second):
			t.Fatal("acquire never completed after release")
		}
		if d := time.Since(started); d > backoffCap+100*time.Millisecond {
			t.Fatalf("deep-phase discovery took %v after release, want ≤%v+slack", d, backoffCap)
		}
	})
}
