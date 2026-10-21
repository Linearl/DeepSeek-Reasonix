package control

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

// stallRecoveryRunner is a Runner that never returns — the "停摆期" (stalled
// runtime) half of the 1008 incident. Its only job is to keep a turn open so
// the test can enqueue steers against a busy controller, then be abandoned the
// way a real stalled host is abandoned: by giving up on the turn without ever
// letting the runner return normally.
type stallRecoveryRunner struct {
	started chan struct{}
	once    sync.Once
}

func (r *stallRecoveryRunner) Run(ctx context.Context, _ string) error {
	r.once.Do(func() { close(r.started) })
	<-ctx.Done()
	return ctx.Err()
}

// 任务1004 复现：1008 实测——右下「待处理引导」面板堆出断联期（10:43-11:04
// 新7 停摆）用户插入的 steer，恢复后实际已消费但队列副本未结算 ⇒ 重入显示。
//
// 家族：840（跨更新切换引导重入 done）、543（引导消息重入根因模式：结算匹配
// 粒度 vs 合并构造粒度不匹配）。本次新形态=停摆期积压+恢复未结算。
//
// 场景逐步还原（与 1008 现场一致，方向性复现）：
//  1. 用户消息开一轮 turn，模型卡在工具调用里不再返回（runner 挂住）——
//     停摆期，running=true。
//  2. 停摆期间用户通过 composer 依次插入 3 条 steer。第一/第二条经
//     TryEnqueueAndSteer 落 Queued 后被 dispatch 消费（消费即 StateQueued→
//     StateSteerConsumed 的 durable 边界），第三条因停摆仍在队列中。
//  3. 恢复：宿主放弃停摆的 turn（Cancel），然后对已消费的引导做结算清扫
//     （SettlePlannedRestartGuidance 的同一合同）。若清扫正确，快照里只剩
//     未消费的那条——已消费的副本不得重入显示。
func TestStalledSteerBacklogSettlesAfterRecovery(t *testing.T) {
	runner := &stallRecoveryRunner{started: make(chan struct{})}
	dir := t.TempDir()
	c := New(Options{
		Runner: runner,
		Sink:   event.Discard,
		// 停摆期 steer 的 settled 探针（570 投递回执）：三条均已在停摆前
		// 完成投递——这是"恢复后已消费"判定的唯一证据来源。
		InboxSettledProbe: func(sessioninbox.InboxItemMeta) bool { return true },
		SessionDir:        dir,
		SessionPath:       filepath.Join(dir, "session.jsonl"),
	})
	t.Cleanup(func() {
		c.Close()
		c.autosaveWG.Wait()
	})

	// 1. 停摆期：一次永不返回的 turn。
	c.Submit("analyze this hang")
	select {
	case <-runner.started:
	case <-time.After(inboxDispatchTestTimeout):
		t.Fatal("the stalled turn never started")
	}

	// 2. 停摆期间插入 3 条 steer：均经 TryEnqueueAndSteer 落为
	//    queued_followup（停摆期无法 mid-turn 注入，走队列回退语义）。
	for i, body := range []string{"check the log rotation", "also verify the memory swap", "and rerun the failing test"} {
		rec, err := c.TryEnqueueAndSteer(InboxRequest{Intent: sessioninbox.IntentSteer, Submit: body})
		if err != nil {
			t.Fatalf("enqueue steer %d: %v", i, err)
		}
		if rec.Disposition != sessioninbox.DispositionQueuedFollowup {
			t.Fatalf("steer %d disposition = %q, want queued_followup (停摆期无法 mid-turn 注入)", i, rec.Disposition)
		}
	}

	// 3. 消费前两条：走 durable 交付边界（inboxSteerLoader 内部调用的同一
	//    Store 方法，需要先经过 SteerAccepted 的合法前驱态）。第三条保持
	//    Queued——真实未消费工作。
	st, err := c.ensureInbox()
	if err != nil {
		t.Fatal(err)
	}
	before := c.InboxSnapshot()
	if len(before.Items) != 3 {
		t.Fatalf("stalled snapshot = %d items, want 3", len(before.Items))
	}
	for i := 0; i < 2; i++ {
		if err := st.SetState(before.Items[i].ID, sessioninbox.StateSteerAccepted, ""); err != nil {
			t.Fatalf("steer %d to accepted: %v", i, err)
		}
		if err := st.MarkSteerConsumed(before.Items[i].ID); err != nil {
			t.Fatalf("mark steer %d consumed: %v", i, err)
		}
	}

	// 4. 恢复：放弃停摆的 turn（Cancel 不结算 inbox——正是 1008 现场
	//    "恢复后已消费但队列副本未结算"的形态），再对已消费引导做结算清扫。
	c.Cancel()
	dropped := c.SettlePlannedRestartGuidance()
	if dropped != 2 {
		t.Fatalf("settled = %d, want 2 (前两条已消费引导的队列副本)", dropped)
	}

	// 恢复后快照：只剩未消费的第 3 条——已消费的副本不得重入显示。
	after := c.InboxSnapshot()
	if len(after.Items) != 1 {
		t.Fatalf("recovered snapshot = %d items, want 1 (未消费的第 3 条): %+v", len(after.Items), after.Items)
	}
	if !strings.Contains(after.Items[0].Preview, "rerun the failing test") {
		t.Fatalf("surviving item = %+v, want the unconsumed third steer", after.Items[0])
	}

	// 结算清扫不得把"有真实未消费工作"的队列误暂停（1008 现场的面板恢复
	// 语义：清扫后队列应可正常派发下一条）。
	if after.Paused {
		t.Fatalf("queue paused after settling consumed residue while real work remains: %+v", after)
	}

	// 恢复后真正跑一轮：未消费的第 3 条应被正常派发（不重入、不丢）。
	// 先释放 c 的会话文件锁，避免与 c2 的 turn ledger 写入冲突。
	c.Close()
	c.autosaveWG.Wait()
	done := make(chan struct{}, 1)
	c2 := New(Options{
		Runner: &inboxDispatchRunner{inputs: make(chan string, 1)},
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.TurnDone {
				done <- struct{}{}
			}
		}),
		SessionDir:  dir,
		SessionPath: filepath.Join(dir, "session.jsonl"),
	})
	t.Cleanup(func() {
		c2.Close()
		c2.autosaveWG.Wait()
	})
	c2.maybeDispatchInbox()
	select {
	case <-done:
	case <-time.After(inboxDispatchTestTimeout):
		t.Fatal("the surviving steer was not dispatched after recovery")
	}
}
