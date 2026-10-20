package control

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"reasonix/internal/event"
	"reasonix/internal/extension"
)

// turnCancelDiagnosis records the first external fire of a turn's stored
// cancel func along with the caller stack (任务1017 批B, acceptance ②).
//
// 现场（2026-10-19 17:31）：空闲开轮桥的 turn 跑了 13s 后死于 "context
// canceled"，日志只有结果没有来源——runSynchronousTurn 传入的 ctx 源自
// context.Background()，外部不可取消，唯一能关闭它的是存到 c.cancel 上的
// 那个 cancel。谁调用了它（用户停止 / CancelWithInboxItems / 关停路径）在
// 旧日志里完全不可见。fire() 在第一次外部触发时抓取调用方栈（截断到 40 行，
// 够定位入口、不至于淹没日志轮转）；describe() 把它交还给错误标注路径。
type turnCancelDiagnosis struct {
	once  sync.Once
	fired atomic.Bool
	stack atomic.Value // string
}

// cancelSourceStackLimit bounds the captured stack: the entry frames near the
// top identify the caller; the deep tail is provider internals nobody reads
// in a log line.
const cancelSourceStackLimit = 40

func (d *turnCancelDiagnosis) fire() {
	if d == nil {
		return
	}
	d.once.Do(func() {
		buf := make([]byte, 16<<10)
		n := runtime.Stack(buf, false)
		stack := string(buf[:n])
		if lines := strings.Count(stack, "\n") + 1; lines > cancelSourceStackLimit {
			stack = strings.Join(strings.SplitN(strings.TrimRight(stack, "\n"), "\n", cancelSourceStackLimit+1)[:cancelSourceStackLimit], "\n") + "\n…(truncated)"
		}
		d.stack.Store(stack)
		d.fired.Store(true)
	})
}

// describe returns the captured caller stack, or "" when the stored cancel
// was never fired externally.
func (d *turnCancelDiagnosis) describe() string {
	if d == nil || !d.fired.Load() {
		return ""
	}
	s, _ := d.stack.Load().(string)
	return s
}

// runSynchronousTurn owns the blocking transport lifecycle. Durable steer
// acknowledgement is intentionally shared with the asynchronous completion
// path, while follow-up dispatch remains owned by the synchronous frontend so
// its response sink stays bound for every queued turn.
func (c *Controller) runSynchronousTurn(
	ctx context.Context,
	onAdmitted func() error,
	run func(context.Context) error,
) error {
	if err := c.ensureWriteAuthorityReady(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(extension.ContextWithRuntimeOwner(ctx, c.RuntimeOwner()))
	// 任务1017 批B: external cancellations go through the STORED cancel (the
	// only handle outside callers hold), so the stored func is wrapped to
	// capture who fired it. finish() calls the raw cancel directly — a normal
	// completion is not a cancel source and must not stamp the diagnosis.
	diag := &turnCancelDiagnosis{}
	c.mu.Lock()
	// Finishing is part of the gate: TurnDone is still fanning out. Closed
	// seals a torn-down controller. Blocking callers get an error rather than
	// parking because they already own and enforce the request boundary.
	if c.running || c.finishing || c.rotating || c.closed {
		c.mu.Unlock()
		cancel()
		return ErrTurnRunning
	}
	if c.rejectDrainingGenerationLocked() {
		c.mu.Unlock()
		cancel()
		c.emitDrainingNotice()
		return ErrRuntimeDraining
	}
	c.cancel = func() {
		diag.fire()
		cancel()
	}
	c.running = true
	c.canceling = false
	c.mu.Unlock()
	c.refreshRuntimeState(event.Event{})
	finish := func() {
		c.mu.Lock()
		c.running = false
		c.cancel = nil
		c.canceling = false
		c.mu.Unlock()
		c.refreshRuntimeState(event.Event{})
		cancel()
	}
	if onAdmitted != nil {
		if err := onAdmitted(); err != nil {
			finish()
			return err
		}
	}
	defer event.RecordTurnCompletion(c.sink)
	defer func() {
		finish()
		c.onInboxTurnDone()
	}()
	err := run(ctx)
	// 任务1017 批B (acceptance ②): a context-canceled result must name its
	// source. Two honest shapes: the stored cancel fired externally (stack
	// attached), or the cancellation never crossed the stored handle (an
	// internal child ctx or an upstream ctx died) — both said out loud, the
	// silent bare "context canceled" is what made the 10-19 现场半天定位.
	if err != nil && errors.Is(err, context.Canceled) {
		if src := diag.describe(); src != "" {
			slog.Warn("control: synchronous turn canceled via stored cancel",
				"cancel_source_stack", src)
			err = fmt.Errorf("%w [cancel-source: external cancel() caller]\n%s", err, src)
		} else {
			err = fmt.Errorf("%w [cancel-source: stored cancel never fired — cancellation came from an internal child ctx or the caller's ctx]", err)
		}
	}
	return err
}
