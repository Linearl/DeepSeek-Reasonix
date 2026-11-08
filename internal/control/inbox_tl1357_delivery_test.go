package control

// tl1357 用户消息投递丢失三形态 · 复现钉（形态①，control 层）。
//
// 验收标准（用户定）：三形态各有复现用例，消息在任一持久层可检索
// （jsonl/events/inbox/manifest 至少一处）。
//
// 形态①（受理即落盘）：Controller.TrySteer 受理的用户消息必须在返回受理前
// 落 sessioninbox manifest。修前（红）文本只进 agent 内存 steerQueue，无任何
// 持久行；修后（绿）manifest 有行、正文可回读、回合收尾后自动重放。
//
// 形态② 的复现钉在同目录 inbox_tl1357_cancel_requeue_test.go；形态③（重启
// 后引导消息与会话不同步）的复现钉在 desktop restart_inbox_gate_test.go
// （跨进程重启门是桌面策略层）。

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/sessioninbox"
)

// tl1357 harness: a controller with a real durable store whose first turn
// blocks until released; inbox dispatches afterwards run through the same
// runner (the replay face).
type tl1357Harness struct {
	c       *Controller
	runner  *inboxDispatchRunner
	entered chan struct{}
	release chan struct{}
	done    <-chan struct{}
}

func newTL1357Harness(t *testing.T) *tl1357Harness {
	t.Helper()
	dir := t.TempDir()
	h := &tl1357Harness{
		runner:  &inboxDispatchRunner{inputs: make(chan string, 8)},
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
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

// startBlockingTurn admits one turn whose body parks on h.release — the
// "turn 运行中" backdrop both repros need.
func (h *tl1357Harness) startBlockingTurn(t *testing.T) {
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

func (h *tl1357Harness) releaseTurn() { close(h.release) }

func (h *tl1357Harness) waitTurnDone(t *testing.T) {
	t.Helper()
	select {
	case <-h.done:
	case <-time.After(inboxDispatchTestTimeout):
		failInboxDispatchWait(t, h.c, "turn completion")
	}
}

func (h *tl1357Harness) waitDispatch(t *testing.T, want string) {
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

// itemBodyAvailable reads the durable envelope straight from the store
// (retrievability = the acceptance criterion's "manifest 可检索").
func itemBodyAvailable(t *testing.T, c *Controller, id string) string {
	t.Helper()
	st, err := c.ensureInbox()
	if err != nil {
		t.Fatal(err)
	}
	_, env, err := st.ReadItem(id)
	if err != nil {
		t.Fatalf("durable body must be retrievable: %v", err)
	}
	return env.SubmitText
}

// TestTrySteerPersistsBeforeReceipt is 形态① 的复现钉：turn 运行中受理的
// steer 必须先落 manifest 再返回受理。修前 TrySteer 把文本塞进 agent 内存
// steerQueue（此处 executor 未绑定即直接拒绝），无任何持久行；修后 manifest
// 有行、正文可回读，回合结束后自动重放为下一轮输入。
func TestTrySteerPersistsBeforeReceipt(t *testing.T) {
	h := newTL1357Harness(t)
	h.startBlockingTurn(t)

	const guidance = "tl1357形态①：请在下一步补跑回归"
	if !h.c.TrySteer(guidance) {
		t.Fatal("TrySteer must accept while a turn is running")
	}

	snap := h.c.InboxSnapshot()
	if len(snap.Items) != 1 {
		t.Fatalf("受理即落盘：steer 受理后 manifest 必须已有持久行，got %+v", snap.Items)
	}
	item := snap.Items[0]
	if item.State != sessioninbox.StateQueued && item.State != sessioninbox.StateSteerAccepted {
		t.Fatalf("持久行状态 = %s, want queued 或 steer_accepted", item.State)
	}
	if got := itemBodyAvailable(t, h.c, item.ID); !strings.Contains(got, guidance) {
		t.Fatalf("持久层正文回读 = %q, want it to contain %q", got, guidance)
	}

	// 回合收尾：持久行由收尾 kick 派发为下一轮输入（重放路径不退化）。
	h.releaseTurn()
	h.waitTurnDone(t)
	h.waitDispatch(t, guidance)
	h.waitTurnDone(t)
	if items := h.c.InboxSnapshot().Items; len(items) != 0 {
		t.Fatalf("重放完成后队列应清空，items = %+v", items)
	}
}
