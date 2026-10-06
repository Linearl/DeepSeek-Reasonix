package checkpoint

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// MutationBarrier provides exclusive workspace mutation access for rewind
// transactions. It is intentionally separate from App.mu / Controller locks so
// file I/O never runs under those mutexes.
//
// Writers call EnterWrite / ExitWrite around mutations.
// Rewind holds EnterExclusive for the whole prepare+commit critical section.
type MutationBarrier struct {
	mu sync.Mutex
	// changed broadcasts state transitions. It replaces sync.Cond so waiters
	// can select on a budget or ctx cancellation (task 472 增补 / 474 切片 3：
	// cond.Wait 无法被打断，是无界等待面的实现载体). Closed and recreated
	// under mu on every broadcast.
	changed   chan struct{}
	writers   int
	exclusive bool
	// generation increments on every exclusive release so prepare tokens can
	// detect concurrent mutation without relying on wall-clock time.
	generation atomic.Uint64
	// closed rejects new enters after shutdown (optional).
	closed bool
}

// defaultEnterWriteBudget bounds EnterWrite when the caller supplies no
// deadline — task 472 增补（474 切片 3）。Same availability-over-completeness
// philosophy as filelock 461-P1: 可用性 > 锁完整性，等待宁可超时返错。Tests may
// shrink it.
var defaultEnterWriteBudget = 5 * time.Second

// barrierWaitLogThreshold is the wait above which an EnterWrite is worth a
// wait_ms log line (472 报告 §4.3 留痕，照 save-path「lock waited」先例的
// 250ms 阈值). Uncontended enters stay silent.
const barrierWaitLogThreshold = 250 * time.Millisecond

// NewMutationBarrier returns a ready barrier.
func NewMutationBarrier() *MutationBarrier {
	b := &MutationBarrier{changed: make(chan struct{})}
	return b
}

// broadcastLocked wakes every waiter. Callers hold mu.
func (b *MutationBarrier) broadcastLocked() {
	if b.changed != nil {
		close(b.changed)
	}
	b.changed = make(chan struct{})
}

// Generation returns the current exclusive-release generation.
func (b *MutationBarrier) Generation() uint64 {
	if b == nil {
		return 0
	}
	return b.generation.Load()
}

// EnterWrite blocks until exclusive access is free, then increments the writer
// count. The wait is bounded: it ends at defaultEnterWriteBudget when the
// caller has no other deadline (task 472: 无界等待面收口).
func (b *MutationBarrier) EnterWrite() error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultEnterWriteBudget)
	defer cancel()
	return b.EnterWriteContext(ctx)
}

// EnterWriteContext is EnterWrite under the caller's context: its deadline and
// cancellation end the wait immediately, and a context without deadline still
// gets defaultEnterWriteBudget.
func (b *MutationBarrier) EnterWriteContext(ctx context.Context) error {
	if b == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultEnterWriteBudget)
		defer cancel()
	}
	started := time.Now()
	// 472 留痕（defer 覆盖全部退出路径）：等待 ≥250ms 记一行 wait_ms，预算耗尽
	// 返错路径同样留痕；无争用进入保持静默。
	defer func() {
		if waited := time.Since(started); waited >= barrierWaitLogThreshold {
			slog.Warn("checkpoint: mutation barrier waited", "op", "enter_write", "wait_ms", waited.Milliseconds())
		}
	}()
	for {
		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			return fmt.Errorf("mutation barrier closed")
		}
		if !b.exclusive {
			b.writers++
			b.mu.Unlock()
			return nil
		}
		changed := b.changed
		b.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return fmt.Errorf("mutation barrier enter write: %w", ctx.Err())
		}
	}
}

// TryEnterWrite is a non-blocking EnterWrite.
func (b *MutationBarrier) TryEnterWrite() bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.exclusive || b.closed {
		return false
	}
	b.writers++
	return true
}

// ExitWrite decrements the writer count and advances the workspace generation.
// Plans prepared before a completed writer can therefore never authorize a
// later commit without a fresh preview.
func (b *MutationBarrier) ExitWrite() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.writers > 0 {
		b.writers--
		b.generation.Add(1)
	}
	if b.writers == 0 {
		b.broadcastLocked()
	}
}

// EnterExclusive waits until no writers hold the barrier, then takes exclusive.
// The wait is unbounded by design (rewind's prepare+commit critical section is
// user-initiated and rare); EnterWrite 是 472 点名的无界等待面，本切片只收口
// EnterWrite。
func (b *MutationBarrier) EnterExclusive() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for b.exclusive || b.writers > 0 || b.closed {
		if b.closed {
			return fmt.Errorf("mutation barrier closed")
		}
		changed := b.changed
		b.mu.Unlock()
		<-changed
		b.mu.Lock()
	}
	b.exclusive = true
	return nil
}

// TryEnterExclusive is a non-blocking EnterExclusive.
func (b *MutationBarrier) TryEnterExclusive() bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.exclusive || b.writers > 0 || b.closed {
		return false
	}
	b.exclusive = true
	return true
}

// ExitExclusive releases exclusive access and bumps generation.
func (b *MutationBarrier) ExitExclusive() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.exclusive = false
	b.generation.Add(1)
	b.broadcastLocked()
}

// Busy reports whether exclusive is held or writers are active.
func (b *MutationBarrier) Busy() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.exclusive || b.writers > 0
}

// Close rejects future enters (best-effort shutdown).
func (b *MutationBarrier) Close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.closed = true
	b.broadcastLocked()
	b.mu.Unlock()
}
