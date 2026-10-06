// Package filelock provides bounded, cross-process advisory file locks.
package filelock

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// 跨进程锁重试节奏（task 474 / X8 方案 B）：阶梯退避 + 抖动 + 预算自适应封顶。
// 前 backoffFloorRuns 次保持 20ms 现状节奏——短临界区（<100ms，占绝大多数）行为
// 与固定轮询完全一致，锁释放后 ≤20ms 内被发现（零回归）；随后阶梯爬升
// （50ms×2 → 100ms×2），封顶 backoffCap，长等待稳态探测率较固定 20ms 网格降约
// 5-10 倍，±25% 抖动打散多进程固定网格的同步惊群。锁原语、预算分层
// （DefaultWaitTimeout + externalTimeout + ctx 三层）、错误类型与取消语义不动。
const (
	backoffFloor     = 20 * time.Millisecond
	backoffFloorRuns = 5
	backoffStep2     = 50 * time.Millisecond
	backoffStep2Runs = 2
	backoffStep3     = 100 * time.Millisecond
	backoffStep3Runs = 2
	backoffCap       = 200 * time.Millisecond

	// 预算自适应封顶：单次退避不超过本段等待预算的 1/8（进入重试循环时一次
	// 测得，取 ctx 与 externalTimeout 的更早 deadline），任何预算下保证约 8 次
	// 以上尝试机会（750ms sidecar 路径封顶 ≈94ms，5s 路径 200ms）。按段预算
	// 而非逐次剩余预算计算——后者会产生几何衰减尾部，长等待末段反而放大
	// 尝试次数（实测探针证伪后修正，见交付报告）。
	backoffBudgetDivisor = 8
	// 抖动幅度 ±25%（对齐 opencode ±30% 抖动先例）。
	backoffJitterFraction = 0.25
)

// jitterNext is the randomness source for retry jitter; tests replace it for
// determinism.
var jitterNext = rand.Float64

// backoffBase returns the nominal (pre-jitter) sleep after the attempt-th
// (0-based) failed try: 20ms×5 → 50ms×2 → 100ms×2 → 200ms.
func backoffBase(attempt int) time.Duration {
	switch {
	case attempt < backoffFloorRuns:
		return backoffFloor
	case attempt < backoffFloorRuns+backoffStep2Runs:
		return backoffStep2
	case attempt < backoffFloorRuns+backoffStep2Runs+backoffStep3Runs:
		return backoffStep3
	default:
		return backoffCap
	}
}

// nextRetryDelay caps the ladder step at min(backoffCap, budget/
// backoffBudgetDivisor), applies ±backoffJitterFraction jitter, and clamps the
// result back under the cap so a single sleep never exceeds one eighth of the
// phase budget. budget is measured once when the retry loop starts.
func nextRetryDelay(attempt int, budget time.Duration) time.Duration {
	limit := backoffCap
	if budget > 0 {
		if budgetLimit := budget / backoffBudgetDivisor; budgetLimit < limit {
			limit = budgetLimit
		}
	}
	d := backoffBase(attempt)
	if d > limit {
		d = limit
	}
	d = applyJitter(d)
	if d > limit {
		d = limit
	}
	if d < 0 {
		d = 0
	}
	return d
}

// applyJitter scales d into [d*(1-f), d*(1+f)] with f = backoffJitterFraction.
func applyJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	factor := 1 + (2*jitterNext()-1)*backoffJitterFraction
	return time.Duration(float64(d) * factor)
}

// phaseWaitBudget reports the retry-phase budget: the time left until the
// earliest deadline among ctxs, measured once when the retry loop starts
// (math.MaxInt64 when none has one; acquire() always installs a deadline so
// this is a defensive fallback only).
func phaseWaitBudget(ctxs ...context.Context) time.Duration {
	budget := time.Duration(math.MaxInt64)
	for _, c := range ctxs {
		if c == nil {
			continue
		}
		if deadline, ok := c.Deadline(); ok {
			if left := time.Until(deadline); left < budget {
				budget = left
			}
		}
	}
	return budget
}

// DefaultWaitTimeout bounds every lock wait whose caller supplied neither a
// deadline nor an explicit external budget (task 461 P1). 设计原则：可用性 >
// 锁完整性——锁等待宁可超时返错，不可无限挂起。All Acquire/AcquireMode calls
// are therefore bounded no matter what context they pass, including
// context.Background(); callers that need a different budget use
// AcquireWithExternalTimeout or pass their own deadline.
const DefaultWaitTimeout = 5 * time.Second

// ErrHeld reports that another file descriptor currently owns the lock.
// Callers normally see their context error after Acquire's bounded retry loop.
var ErrHeld = errors.New("file lock held")

// localLock is a process-local reader-writer lock for one canonical path.
// refs counts acquirers currently between registry entry and release/timeout
// so the registry can reclaim entries when no one is waiting or holding —
// important for short-lived paths such as session-temp owner locks.
type localLock struct {
	mu             sync.Mutex
	cond           *sync.Cond
	exclusive      bool
	readers        int
	waitingWriters int
	refs           int
}

var localRegistry = struct {
	sync.Mutex
	locks map[string]*localLock
}{locks: map[string]*localLock{}}

// Acquire obtains an exclusive lock on path until the returned release
// function is called. It serializes both goroutines in this process and other
// Reasonix processes. The wait is always bounded: it ends at ctx's deadline,
// ctx cancellation, or DefaultWaitTimeout when ctx has neither — it never
// hangs forever (task 461 P1).
func Acquire(ctx context.Context, path string) (func(), error) {
	return acquire(ctx, path, 0, ModeExclusive)
}

// AcquireMode obtains a lock in exclusive or shared mode, under the same
// bounded-wait contract as Acquire.
func AcquireMode(ctx context.Context, path string, mode Mode) (func(), error) {
	return acquire(ctx, path, 0, mode)
}

// AcquireWithExternalTimeout obtains an exclusive lock while keeping the
// in-process queue and cross-process file-lock budgets separate. ctx bounds
// the wait for another goroutine in this process (and its cancellation ends
// the whole acquire immediately); externalTimeout starts after that queue is
// acquired and bounds retries against other processes.
func AcquireWithExternalTimeout(ctx context.Context, path string, externalTimeout time.Duration) (func(), error) {
	if externalTimeout <= 0 {
		return nil, errors.New("external file lock timeout must be positive")
	}
	return acquire(ctx, path, externalTimeout, ModeExclusive)
}

func acquire(ctx context.Context, path string, externalTimeout time.Duration, mode Mode) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	lockPath, err := canonicalLockPath(path)
	if err != nil {
		return nil, err
	}
	// 可用性 > 锁完整性（task 461 P1）：任何锁等待都必须有界。调用方 ctx 没有
	// deadline 时统一套 DefaultWaitTimeout——包括 context.Background()，否则
	// 本地排队阶段仍可能无限等。已有 deadline 的 ctx 尊重调用方（更紧的）预算。
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultWaitTimeout)
		defer cancel()
	}
	key := localRegistryKey(lockPath)
	releaseLocal, err := acquireLocal(ctx, key, mode)
	if err != nil {
		return nil, err
	}
	fileCtx := ctx
	cancel := func() {}
	if externalTimeout > 0 {
		fileCtx, cancel = context.WithTimeout(context.Background(), externalTimeout)
	}
	defer cancel()

	// 阶梯退避 + 抖动（task 474）：短等待维持 20ms 节奏，长等待降频探测；
	// 睡眠期间 fileCtx/ctx 任一到点或取消仍经下方 select 即时退出，
	// 461-P1 的 ≤1s 停止 SLA 不变。段预算循环入口一次测得。
	budget := phaseWaitBudget(ctx, fileCtx)
	for attempt := 0; ; attempt++ {
		releaseFile, err := tryLockFileMode(lockPath, mode)
		if err == nil {
			var once sync.Once
			return func() {
				once.Do(func() {
					releaseFile()
					releaseLocal()
				})
			}, nil
		}
		if !errors.Is(err, ErrHeld) {
			releaseLocal()
			return nil, fmt.Errorf("acquire file lock: %w", err)
		}
		timer := time.NewTimer(nextRetryDelay(attempt, budget))
		select {
		case <-timer.C:
		case <-fileCtx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			releaseLocal()
			return nil, fmt.Errorf("acquire file lock: %w", fileCtx.Err())
		case <-ctx.Done():
			// 终止随时生效（task 461 P1）：即使外部超时预算未用完，调用方
			// ctx 一旦取消（用户点停止）也必须立即返回，不能继续等锁。
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			releaseLocal()
			return nil, fmt.Errorf("acquire file lock: %w", ctx.Err())
		}
	}
}

// TryAcquire attempts a non-blocking exclusive lock. It returns ErrHeld when
// another holder (in this process or another) currently owns the lock.
func TryAcquire(path string) (func(), error) {
	return TryAcquireMode(path, ModeExclusive)
}

// TryAcquireMode attempts a non-blocking lock in exclusive or shared mode.
func TryAcquireMode(path string, mode Mode) (func(), error) {
	lockPath, err := canonicalLockPath(path)
	if err != nil {
		return nil, err
	}
	key := localRegistryKey(lockPath)
	releaseLocal, ok := tryAcquireLocal(key, mode)
	if !ok {
		return nil, ErrHeld
	}

	releaseFile, err := tryLockFileMode(lockPath, mode)
	if err != nil {
		releaseLocal()
		if errors.Is(err, ErrHeld) {
			return nil, ErrHeld
		}
		return nil, fmt.Errorf("try acquire file lock: %w", err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			releaseFile()
			releaseLocal()
		})
	}, nil
}

func lookupLocal(key string) *localLock {
	local := localRegistry.locks[key]
	if local == nil {
		local = &localLock{}
		local.cond = sync.NewCond(&local.mu)
		localRegistry.locks[key] = local
	}
	local.refs++
	return local
}

func acquireLocal(ctx context.Context, key string, mode Mode) (func(), error) {
	localRegistry.Lock()
	local := lookupLocal(key)
	localRegistry.Unlock()

	stop := context.AfterFunc(ctx, func() {
		local.mu.Lock()
		local.cond.Broadcast()
		local.mu.Unlock()
	})
	defer stop()

	local.mu.Lock()
	writer := mode == ModeExclusive
	if writer {
		local.waitingWriters++
	}
	for {
		if ctx.Err() != nil {
			if writer {
				local.waitingWriters--
				local.cond.Broadcast()
			}
			local.mu.Unlock()
			dropLocalRef(key, local)
			return nil, fmt.Errorf("acquire file lock: %w", ctx.Err())
		}
		if mode == ModeShared {
			if !local.exclusive && local.waitingWriters == 0 {
				local.readers++
				local.mu.Unlock()
				return releaseLocalFunc(key, local, mode), nil
			}
		} else if !local.exclusive && local.readers == 0 {
			local.waitingWriters--
			local.exclusive = true
			local.mu.Unlock()
			return releaseLocalFunc(key, local, mode), nil
		}
		local.cond.Wait()
	}
}

func tryAcquireLocal(key string, mode Mode) (func(), bool) {
	localRegistry.Lock()
	local := lookupLocal(key)
	localRegistry.Unlock()

	local.mu.Lock()
	if mode == ModeShared {
		if local.exclusive || local.waitingWriters > 0 {
			local.mu.Unlock()
			dropLocalRef(key, local)
			return nil, false
		}
		local.readers++
		local.mu.Unlock()
		return releaseLocalFunc(key, local, mode), true
	}
	if local.exclusive || local.readers > 0 {
		local.mu.Unlock()
		dropLocalRef(key, local)
		return nil, false
	}
	local.exclusive = true
	local.mu.Unlock()
	return releaseLocalFunc(key, local, mode), true
}

func dropLocalRef(key string, local *localLock) {
	localRegistry.Lock()
	local.refs--
	if local.refs <= 0 {
		local.refs = 0
		delete(localRegistry.locks, key)
	}
	localRegistry.Unlock()
}

func releaseLocalFunc(key string, local *localLock, mode Mode) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			local.mu.Lock()
			if mode == ModeShared {
				if local.readers > 0 {
					local.readers--
				}
			} else {
				local.exclusive = false
			}
			local.cond.Broadcast()
			local.mu.Unlock()
			dropLocalRef(key, local)
		})
	}
}

// RegistrySizeForTest returns the number of live local-lock entries (tests).
func RegistrySizeForTest() int {
	localRegistry.Lock()
	defer localRegistry.Unlock()
	return len(localRegistry.locks)
}

func canonicalLockPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("file lock path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve file lock path: %w", err)
	}
	abs = filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		abs = strings.ToLower(filepath.ToSlash(abs))
	}
	return abs, nil
}

func localRegistryKey(path string) string {
	if runtime.GOOS == "darwin" {
		return strings.ToLower(filepath.ToSlash(path))
	}
	return path
}
