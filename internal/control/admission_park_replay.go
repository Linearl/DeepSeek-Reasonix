package control

// tl1357b 必做1：parkedTurns 内存 FIFO 的持久重放层（中等方案）。
//
// 机制（tl1357 主件报告建议原文：park 时落一份 manifest 重放记录 + body 启动
// 核销）：忙时 park 的用户提交闭包只活在内存里，进程退出即丢且无任何持久层
// 可检索。本文件给 park 面补上持久副本：
//
//	park（四个 park 位点）→ manifest 重放行（blob+manifest 双提交，EnqueueInbox）
//	body 启动（FIFO drain 后闭包真正开跑）→ 核销该行（防双投）
//	进程退出而 body 未启动 → 行留队列，重启后按普通 queued 行派发（可检索可重放）
//
// 两种行形态（park 时一律停靠 uncertain，绝不以 queued 形态伴随活体 parked
// body——queued 行会被 461-P9 工具间隙泵注入运行中的轮、或在任何开门瞬间被派
// 发为重复 turn；uncertain 行对所有派发路径不可见，仅shelf 可检索可重发）：
//   - turn 形态（ref turns / /mcp__ / docs slash / custom command / skill
//     slash）：IntentFollowup 行。body 启动时核销（uncertain→running→
//     AckDequeue）；核销失败降级回 uncertain（可检索、不自动重投，866/263
//     结算面按 transcript 证据裁决）。进程退出而 body 未启动 → 行留 shelf
//     （ParkedSubmit 停靠原因），用户 Retry 即重放为 turn。
//   - dock 形态（steer fallback，#7045 语义）：IntentSteer 行 park 即停靠
//     uncertain（SteerUnapplied 停靠原因，永不自动派发——历史引导不得自动
//     升格为任务），仅作为可重发的持久副本；body 启动后行保留。
//
// 与 tl1357 主件 settled-receipt 防重投机制的衔接（撞面声明，详见交付报告）：
// steer_accepted 持久面（形态①）与本机制互斥——TrySteer 持久受理成功即返回，
// 不进 submitSteerFallback；已受理行的防重投由 MarkSteerConsumed/settled 探针
// 与本文件无关。park 重放行与 steer_accepted 行不存在同一消息双行路径。

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"

	"reasonix/internal/sessioninbox"
)

// parkReplayKind distinguishes the two durable row shapes a parked body can
// carry (see the file comment).
type parkReplayKind int

const (
	// parkReplayTurn rows replay as a queued follow-up turn; the body's start
	// writes the row off (核销) so a normally drained submit never double-fires.
	parkReplayTurn parkReplayKind = iota
	// parkReplayDock rows dock as uncertain steer guidance (never auto-run).
	parkReplayDock
)

// parkedTurnReplay is the durable replay record attached to one parked body.
// The park site persists it right after releasing c.mu; the body wrapper
// (built by runGuardedOrParkReplay) waits for that write before running, then
// writes the row off (turn kind) or hands its id to the unapplied-steer face
// (dock kind) — so the row and the live body can never deliver the same words
// twice.
type parkedTurnReplay struct {
	kind   parkReplayKind
	text   string
	source string
	// display is the user-visible row label; empty falls back to text.
	display string

	mu      sync.Mutex
	rowID   string
	written chan struct{} // closed once the park-side write attempt completed
}

func newTurnParkReplay(submit, display, source string) *parkedTurnReplay {
	return &parkedTurnReplay{kind: parkReplayTurn, text: submit, display: display, source: source, written: make(chan struct{})}
}

func newDockParkReplay(text, source string) *parkedTurnReplay {
	return &parkedTurnReplay{kind: parkReplayDock, text: text, source: source, written: make(chan struct{})}
}

// dockedRowID returns the manifest row written at park time; empty when the
// write failed or the body never parked. Only valid after <-written.
func (r *parkedTurnReplay) dockedRowID() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rowID
}

// abandon releases a wrapper whose body started without parking (gate-open
// admission) or whose admission was rejected: nothing is persisted and the
// settle wait must not block.
func (r *parkedTurnReplay) abandon() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.written != nil {
		close(r.written)
		r.written = nil
	}
}

// parkedTurnEntry is one parkedTurns FIFO slot: the body plus its optional
// replay record (nil for admissions without a replay payload — runGuarded
// compatibility bodies and durable-inbox bodies, which already own a row).
type parkedTurnEntry struct {
	run    func(ctx context.Context) error
	replay *parkedTurnReplay
}

// runGuardedOrParkReplay is runGuardedOrPark with a durable replay record for
// the parked window. The wrapper makes the body wait for the park-side row
// write before running, then settles it — the FIFO drain may start the body
// while the park goroutine is still committing the manifest row, and starting
// without that ordering would reopen the loss window the row exists to close.
func (c *Controller) runGuardedOrParkReplay(body func(ctx context.Context) error, replay *parkedTurnReplay) admissionResult {
	if replay == nil {
		return c.runGuardedOrPark(body)
	}
	wrapped := func(ctx context.Context) error {
		c.settleParkReplay(replay)
		return body(ctx)
	}
	return c.admitGuardedTurn(wrapped, true, true, nil, replay)
}

// persistParkReplay writes the manifest row at park time. Runs on the submit
// goroutine right after the park site released c.mu (never under a lock): the
// caller has already been told turnParked semantics by the FIFO append, and
// the row must exist before any restart can lose the in-memory body. A failed
// write (no session path, store error, capacity) is best-effort: the park
// face degrades to its pre-tl1357b memory-only behavior, the wrapper proceeds,
// and the loss window is logged.
func (c *Controller) persistParkReplay(r *parkedTurnReplay) {
	if r == nil {
		return
	}
	defer r.abandon() // written closes even on failure — the wrapper must not block
	if _, err := c.ensureInbox(); err != nil {
		slog.Warn("control: parked submit replay row not persisted (no durable inbox)",
			"source", r.source, "err", err)
		return
	}
	submit := strings.TrimSpace(r.text)
	if submit == "" {
		return
	}
	req := InboxRequest{
		Intent:  sessioninbox.IntentFollowup,
		Display: firstNonEmptyStr(r.display, submit),
		Raw:     submit,
		Submit:  submit,
		Source:  r.source,
		// Born parked in the enqueue's own commit (see InitialParkReason).
		InitialParkReason: r.blockReason(),
	}
	if r.kind == parkReplayDock {
		req.Intent = sessioninbox.IntentSteer
	}
	rec, err := c.EnqueueInbox(req)
	if err != nil {
		slog.Warn("control: parked submit replay row persist failed",
			"source", r.source, "err", err)
		return
	}
	r.mu.Lock()
	r.rowID = rec.ItemID
	r.mu.Unlock()
	slog.Info("control: parked submit persisted a replay row",
		"id", rec.ItemID, "source", r.source, "kind", r.kind)
}

// blockReason names the park face for the shelf: dock rows are guidance that
// never reached a provider request (the established steer-unapplied wording);
// turn rows are real submissions whose body may not have run.
func (r *parkedTurnReplay) blockReason() string {
	if r.kind == parkReplayDock {
		return sessioninbox.BlockReasonSteerUnapplied
	}
	return sessioninbox.BlockReasonParkedSubmit
}

// settleParkReplay runs at body start (the FIFO drain handed the parked body
// to a turn goroutine). It first waits for the park-side write so the row and
// the live body cannot disagree about who owns the delivery, then writes the
// row off: the body IS the delivery now. Turn-kind rows must never survive
// beside a running body — a later dispatch or shelf retry would deliver the
// same words twice — so every write-off failure parks the row back to
// uncertain (visible, never auto-dispatched, settled later against the
// transcript by the 866/263 passes) instead of leaving it queued.
func (c *Controller) settleParkReplay(r *parkedTurnReplay) {
	if r == nil {
		return
	}
	r.mu.Lock()
	written := r.written
	r.mu.Unlock()
	if written != nil {
		<-written
	}
	if r.kind != parkReplayTurn {
		return
	}
	id := r.dockedRowID()
	if id == "" {
		return
	}
	st, err := c.ensureInbox()
	if err != nil {
		slog.Warn("control: parked replay row write-off skipped (no durable inbox)",
			"id", id, "err", err)
		return
	}
	if err := st.SetState(id, sessioninbox.StateRunning, ""); err != nil {
		if isRowGoneErr(err) {
			return
		}
		slog.Warn("control: parked replay row write-off failed; parking uncertain",
			"id", id, "err", err)
		_ = st.SetState(id, sessioninbox.StateUncertain, "parked replay write-off failed")
		return
	}
	if err := st.AckDequeue(id); err != nil {
		if isRowGoneErr(err) {
			return
		}
		slog.Warn("control: parked replay row write-off failed; parking uncertain",
			"id", id, "err", err)
		_ = st.SetState(id, sessioninbox.StateUncertain, "parked replay write-off failed")
	}
}

func isRowGoneErr(err error) bool {
	return err == nil || errors.Is(err, sessioninbox.ErrNotFound)
}
