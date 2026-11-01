package control

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/sessioninbox"
)

// 任务1004（进程内清扫触发点钉）：停摆被 Cancel 放弃的同进程恢复里，
// close() 必须把「已消费 + 已 settle」的队列副本清扫下架。
//
// 为什么触发点是 close()：1008 现场是用户 Stop 掉卡死的 turn 后继续用——
// 没有进程重启（P15 的 drop 只在 store open/rebind 跑）、没有计划内恢复名
// 册（840/1004 的 SettlePlannedRestartGuidance 只挂在 roster 三处消费点）。
// 两条既有路都够不到，副本就留在 manifest 里重入显示。close 是本进程
// 「这段会话用完了」的天然清扫点：清扫失败也只丢日志，下一次开店的恢复路
// 仍是兜底。

// consumedResidueTurnRunner 是一个永不返回的 runner：turn 保持打开，测试
// 随后直接操作 store 制造停摆积压形态，再用 Close 收尾。
type consumedResidueTurnRunner struct {
	started chan struct{}
	once    sync.Once
}

func (r *consumedResidueTurnRunner) Run(ctx context.Context, _ string) error {
	r.once.Do(func() { close(r.started) })
	<-ctx.Done()
	return ctx.Err()
}

func TestCloseSettlesConsumedResidueOnly(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	// InboxAppliedReceipts = 「transcript 已注入回执」的正文集合（生产接线
	// boot.go → agent.AppliedSteerReceiptTexts）；测试注入同形桩。
	var appliedMu sync.Mutex
	applied := map[string]struct{}{}
	c := New(Options{
		Runner: &consumedResidueTurnRunner{started: make(chan struct{})},
		Sink:   event.Discard,
		InboxAppliedReceipts: func(string) map[string]struct{} {
			appliedMu.Lock()
			defer appliedMu.Unlock()
			out := map[string]struct{}{}
			for k := range applied {
				out[k] = struct{}{}
			}
			return out
		},
		InboxAppliedBodyReceipts: func(string) map[string]struct{} { return nil },
		SessionDir:               dir,
		SessionPath:              sessionPath,
	})
	t.Cleanup(func() {
		c.Close()
		c.autosaveWG.Wait()
	})

	st, err := c.ensureInbox()
	if err != nil {
		t.Fatal(err)
	}

	// 造三条 durable 行：前两条已越过 MarkSteerConsumed 边界，第三条未消费。
	seed := func(body string, consumed bool) sessioninbox.InboxItemMeta {
		t.Helper()
		rec, err := st.Enqueue(sessioninbox.EnqueueRequest{
			Envelope:    sessioninbox.PromptEnvelope{SubmitText: body},
			Idempotency: "k-" + body[:6],
			Source:      "desktop",
		})
		if err != nil {
			t.Fatal(err)
		}
		if consumed {
			if err := st.SetState(rec.ItemID, sessioninbox.StateSteerAccepted, ""); err != nil {
				t.Fatal(err)
			}
			if err := st.MarkSteerConsumed(rec.ItemID); err != nil {
				t.Fatal(err)
			}
		}
		for _, it := range st.Snapshot().Items {
			if it.ID == rec.ItemID {
				return it
			}
		}
		t.Fatalf("seeded item %s missing", rec.ItemID)
		return sessioninbox.InboxItemMeta{}
	}
	appliedRow := seed("applied guidance body", true)
	unappliedRow := seed("consumed but never recorded", true)
	pendingRow := seed("genuinely pending work", false)

	// 只有第一条的正文出现在 transcript 回执集合里（= 已 settle）。
	appliedMu.Lock()
	applied["applied guidance body"] = struct{}{}
	appliedMu.Unlock()

	states := func() map[string]sessioninbox.InboxState {
		out := map[string]sessioninbox.InboxState{}
		for _, it := range st.Snapshot().Items {
			out[it.ID] = it.State
		}
		return out
	}

	// 判据双断言：已消费+已 settle 删；未消费/无回执的已消费保留。
	if dropped := c.SettleConsumedGuidanceResidue("test"); dropped != 1 {
		t.Fatalf("swept = %d, want 1 (只有「已消费+已 settle」)", dropped)
	}
	if s, ok := states()[appliedRow.ID]; ok {
		t.Fatalf("已消费+已 settle 的副本 %s 必须删，仍在 %s", appliedRow.ID, s)
	}
	for _, keep := range []sessioninbox.InboxItemMeta{unappliedRow, pendingRow} {
		state, ok := states()[keep.ID]
		if !ok {
			t.Fatalf("未消费/无回执行 %s 不得被清扫", keep.ID)
		}
		if state != keep.State {
			t.Fatalf("保留行 %s 状态被改写: %s → %s", keep.ID, keep.State, state)
		}
	}

	// Close 接线：close() 内部跑同一清扫（幂等——上一步已删的不再命中），
	// 且把行真正落盘删除，下一次开店的快照里残留行不再重入显示。
	c.Close()
	c.autosaveWG.Wait()
	reopened, err := sessioninbox.Open(sessionPath, sessioninbox.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	after := map[string]sessioninbox.InboxState{}
	for _, it := range reopened.Snapshot().Items {
		after[it.ID] = it.State
	}
	if len(after) != 2 {
		t.Fatalf("close 清扫后残留 = %d, want 2 (未消费 + 无回执的已消费): %+v", len(after), after)
	}
	if _, ok := after[appliedRow.ID]; ok {
		t.Fatalf("close 后重开快照仍带已消费+已 settle 的副本 %s（重入显示）", appliedRow.ID)
	}
	if _, ok := after[unappliedRow.ID]; !ok {
		t.Fatalf("无回执的已消费行 %s 不得被 close 清扫", unappliedRow.ID)
	}
	if _, ok := after[pendingRow.ID]; !ok {
		t.Fatalf("未消费行 %s 不得被 close 清扫", pendingRow.ID)
	}
}
