package control

// tl1357b 必做1 · 复现钉：parkedTurns 内存 FIFO 的丢失面。
//
// 验收口径（对齐 tl1357 主件「受理即落盘」）：用户提交在忙时 park 的消息必须
// 在任一持久层可检索可重放。修前（红）park 只把闭包塞进内存 parkedTurns，进程
// 退出即丢、无任何 manifest 行；修后（绿）park 时 manifest 有行可回读，body
// 启动即核销（防双投），进程退出（Close/崩溃）后行留队列可重放。
//
// 三个钉：
//  1. TestParkReplayRowPersistsAtParkTime   —— park 即落行 + body 启动核销
//  2. TestParkReplayRowSurvivesProcessExit —— park 后进程退出（Close），行留
//     队列，重启（新 controller）后可检索且可重放（派发为下一轮输入）
//  3. TestSteerFallbackParkDocksReplayRow  —— steer fallback 面的 dock 形态：
//     行停靠为 uncertain（永不自动派发，#7045 语义），body 启动后行仍在
//  4. TestParkReplaySteerAcceptedRowNoDoubleDelivery —— 撞面核对钉：走
//     steer_accepted 持久面的消息（tl1357 形态①）不再产生 parked 重放行
//     （TrySteer 持久受理直接返回，不经 submitSteerFallback）。

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/sessioninbox"
)

// tl1357b harness: 与 tl1357 主件 harness 同构（真实持久 store + 可控阻塞轮），
// 复用 inboxDispatchRunner 作为回合执行器与派发观察窗。
type tl1357bHarness struct {
	c        *Controller
	runner   *inboxDispatchRunner
	entered  chan struct{}
	release  chan struct{}
	released sync.Once
	done     <-chan struct{}
	dir      string
}

func newTL1357bHarness(t *testing.T) *tl1357bHarness {
	t.Helper()
	dir := t.TempDir()
	h := &tl1357bHarness{
		runner:  &inboxDispatchRunner{inputs: make(chan string, 8)},
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
		dir:     dir,
	}
	done := make(chan struct{}, 8)
	h.done = done
	h.c = New(Options{
		Runner: h.runner,
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.TurnDone {
				done <- struct{}{}
			}
		}),
		SessionDir:  dir,
		SessionPath: filepath.Join(dir, "session.jsonl"),
	})
	t.Cleanup(func() {
		h.c.Close()
		h.c.autosaveWG.Wait()
	})
	return h
}

// startBlockingTurn admits one raw turn whose body parks on h.release — the
// running-gate backdrop every repro needs.
func (h *tl1357bHarness) startBlockingTurn(t *testing.T) {
	t.Helper()
	h.c.runGuarded(func(context.Context) error {
		h.entered <- struct{}{}
		<-h.release
		return nil
	})
	select {
	case <-h.entered:
	case <-time.After(inboxDispatchTestTimeout):
		t.Fatal("blocking turn never started")
	}
	if st := h.c.RuntimeStatus(); !st.Running {
		t.Fatalf("precondition: turn must be running, got %+v", st)
	}
}

func (h *tl1357bHarness) releaseTurn() { h.released.Do(func() { close(h.release) }) }

func (h *tl1357bHarness) waitTurnDone(t *testing.T) {
	t.Helper()
	select {
	case <-h.done:
	case <-time.After(inboxDispatchTestTimeout):
		failInboxDispatchWait(t, h.c, "turn completion")
	}
}

func (h *tl1357bHarness) waitDispatch(t *testing.T, want string) {
	t.Helper()
	select {
	case got := <-h.runner.inputs:
		if !strings.Contains(got, want) {
			t.Fatalf("replayed turn input = %q, want it to contain %q", got, want)
		}
	case <-time.After(inboxDispatchTestTimeout):
		failInboxDispatchWait(t, h.c, "replay dispatch of "+want)
	}
}

func (h *tl1357bHarness) assertNoDispatchWithin(t *testing.T) {
	t.Helper()
	select {
	case got := <-h.runner.inputs:
		t.Fatalf("dock row must never auto-dispatch, but runner received: %q", got)
	case <-time.After(500 * time.Millisecond):
	}
}

// manifestItems reads the durable manifest through the controller store.
func (h *tl1357bHarness) manifestItems(t *testing.T) []sessioninbox.InboxItemMeta {
	t.Helper()
	st, err := h.c.ensureInbox()
	if err != nil {
		t.Fatal(err)
	}
	return st.Snapshot().Items
}

// itemBody reads one item's durable envelope straight from the store
// (retrievability = the acceptance criterion's "manifest 可检索").
func (h *tl1357bHarness) itemBody(t *testing.T, id string) sessioninbox.PromptEnvelope {
	t.Helper()
	st, err := h.c.ensureInbox()
	if err != nil {
		t.Fatal(err)
	}
	_, env, err := st.ReadItem(id)
	if err != nil {
		t.Fatalf("durable body must be retrievable: %v", err)
	}
	return env
}

// 钉1：running 门 park 即落 manifest 重放行；body 启动（FIFO drain）即核销。
// 修前：park 后 manifest 零行（红）。
func TestParkReplayRowPersistsAtParkTime(t *testing.T) {
	h := newTL1357bHarness(t)
	h.startBlockingTurn(t)

	const text = "tl1357b钉1：running 态提交必须 park 且落重放行"
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.c.SubmitDisplay(text, text)
	}()

	// park 已发生（running 门 + 中断请求），行必须在 park 即落盘。
	deadline := time.Now().Add(10 * time.Second)
	var items []sessioninbox.InboxItemMeta
	for {
		items = h.manifestItems(t)
		if len(items) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("park 即落盘失败：park 后 manifest 行数 = %d, want 1（修前红：park 只进内存）", len(items))
		}
		time.Sleep(10 * time.Millisecond)
	}
	item := items[0]
	// 行必须停靠 uncertain（对所有派发路径不可见——queued 形态会被 461-P9 泵
	// 注入运行中的轮，形成双投），body 启动时才核销。
	if item.State != sessioninbox.StateUncertain {
		t.Fatalf("重放行状态 = %s, want uncertain（不可派发的停靠形态）", item.State)
	}
	if item.Intent != sessioninbox.IntentFollowup {
		t.Fatalf("重放行意图 = %s, want followup（重放为正式 turn）", item.Intent)
	}
	if item.BlockReason != sessioninbox.BlockReasonParkedSubmit {
		t.Fatalf("重放行停靠原因 = %q, want %q", item.BlockReason, sessioninbox.BlockReasonParkedSubmit)
	}
	if got := h.itemBody(t, item.ID).SubmitText; !strings.Contains(got, text) {
		t.Fatalf("持久层正文回读 = %q, want it to contain %q", got, text)
	}

	// body 启动核销：当前轮让位 → FIFO drain 起 parked body → 行核销（防双投）。
	h.releaseTurn()
	<-done
	h.waitTurnDone(t)
	deadline = time.Now().Add(10 * time.Second)
	for {
		if len(h.manifestItems(t)) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("body 启动后重放行必须核销，残留 = %+v", h.manifestItems(t))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// 钉2：park 后进程退出（Close，parked body 随进程消亡），manifest 行留队列；
// 重启（新 controller 同 session）后行可检索且可重放（派发为下一轮输入）。
func TestParkReplayRowSurvivesProcessExit(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	runner := &inboxDispatchRunner{inputs: make(chan string, 8)}
	newController := func() *Controller {
		done := make(chan struct{}, 8)
		return New(Options{
			Runner: runner,
			Sink: event.FuncSink(func(e event.Event) {
				if e.Kind == event.TurnDone {
					done <- struct{}{}
				}
			}),
			SessionDir:  dir,
			SessionPath: sessionPath,
		})
	}

	c := newController()
	t.Cleanup(func() { c.Close(); c.autosaveWG.Wait() })

	started := make(chan struct{})
	block := make(chan struct{})
	c.runGuarded(func(context.Context) error {
		close(started)
		<-block
		return nil
	})
	<-started

	const text = "tl1357b钉2：进程退出后 park 行必须可重放"
	go func() {
		c.SubmitDisplay(text, text)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		c.mu.Lock()
		parked := len(c.parkedTurns)
		c.mu.Unlock()
		if parked == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("precondition: submit was not parked (parked=%d)", parked)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 进程退出模拟：parked body 未跑、行已落盘 → Close 不核销也不删行。
	c.Close()
	c.autosaveWG.Wait()
	close(block) // 让旧 controller 的阻塞轮体退出，释放其 goroutine

	// 重启：新 controller 同 session。行以 uncertain 停靠（body 随进程消亡，
	// 未核销）——可检索、可重发。
	c2 := newController()
	t.Cleanup(func() { c2.Close(); c2.autosaveWG.Wait() })
	st, err := c2.ensureInbox()
	if err != nil {
		t.Fatal(err)
	}
	items := st.Snapshot().Items
	if len(items) != 1 || items[0].State != sessioninbox.StateUncertain {
		t.Fatalf("重启后重放行必须以 uncertain 停靠，items = %+v", items)
	}
	if _, env, err := st.ReadItem(items[0].ID); err != nil || !strings.Contains(env.SubmitText, text) {
		t.Fatalf("重启后正文必须可检索 (err=%v, env=%+v)", err, env)
	}

	// 可重放：RetryItem（shelf 重发）转回 queued，一次普通轮收尾的派发 kick
	// 把行送进执行器。
	if err := st.RetryItem(items[0].ID); err != nil {
		t.Fatalf("retry parked row: %v", err)
	}
	c2.runGuarded(func(context.Context) error { return nil })
	var got string
	select {
	case got = <-runner.inputs:
	case <-time.After(inboxDispatchTestTimeout):
		failInboxDispatchWait(t, c2, "replay dispatch after restart")
	}
	if !strings.Contains(got, text) {
		t.Fatalf("replayed input = %q, want it to contain %q", got, text)
	}
}

// 钉3：steer fallback 面的 dock 形态——行停靠为 uncertain（永不自动派发，
// #7045：历史引导不得自动升格为任务），body 启动后行仍在（resendable）。
func TestSteerFallbackParkDocksReplayRow(t *testing.T) {
	h := newTL1357bHarness(t)
	h.startBlockingTurn(t)

	const text = "tl1357b钉3：fallback 引导必须停靠可重发"
	// 白盒直入被测单元：TrySteer 持久路径失败/未走通时的内存兜底面。
	res := h.c.submitSteerFallback(text)
	if res != turnParked && res != turnStarted {
		t.Fatalf("steer fallback admission = %v, want parked", res)
	}

	deadline := time.Now().Add(10 * time.Second)
	var items []sessioninbox.InboxItemMeta
	for {
		items = h.manifestItems(t)
		if len(items) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dock 行未落盘：manifest 行数 = %d（修前红：无任何持久行）", len(items))
		}
		time.Sleep(10 * time.Millisecond)
	}
	item := items[0]
	if item.State != sessioninbox.StateUncertain {
		t.Fatalf("dock 行状态 = %s, want uncertain（不自动派发）", item.State)
	}
	if item.Intent != sessioninbox.IntentSteer {
		t.Fatalf("dock 行意图 = %s, want steer", item.Intent)
	}
	if got := h.itemBody(t, item.ID).SubmitText; !strings.Contains(got, text) {
		t.Fatalf("dock 行正文回读 = %q, want %q", got, text)
	}

	// body 启动后：dock 行保留（uncertain + resendable），且永不自动派发。
	h.releaseTurn()
	h.waitTurnDone(t)
	h.assertNoDispatchWithin(t)
	items = h.manifestItems(t)
	if len(items) != 1 || items[0].State != sessioninbox.StateUncertain {
		t.Fatalf("body 启动后 dock 行必须保留为 uncertain，items = %+v", items)
	}
}

// 钉4（撞面核对）：消息走 steer_accepted 持久面（tl1357 形态①）时不得再产生
// parked 重放行——TrySteer 持久受理返回 true，Steer 不再进 submitSteerFallback。
func TestParkReplaySteerAcceptedRowNoDoubleDelivery(t *testing.T) {
	h := newTL1357bHarness(t)
	h.startBlockingTurn(t)
	defer h.releaseTurn()

	const guidance = "tl1357b钉4：持久面受理不重复落行"
	if !h.c.TrySteer(guidance) {
		t.Fatal("TrySteer must accept while a turn is running")
	}
	items := h.manifestItems(t)
	if len(items) != 1 {
		t.Fatalf("持久面应恰好一行，got %d", len(items))
	}
	if items[0].State != sessioninbox.StateSteerAccepted && items[0].State != sessioninbox.StateQueued {
		t.Fatalf("持久面行状态 = %s", items[0].State)
	}
	// Steer 的 fallback 必须因 TrySteer 成功而不触发（无第二行）。
	h.c.Steer(guidance)
	items = h.manifestItems(t)
	if len(items) != 1 {
		t.Fatalf("撞面：持久受理后 fallback 不得再加行，行数 = %d", len(items))
	}
}
