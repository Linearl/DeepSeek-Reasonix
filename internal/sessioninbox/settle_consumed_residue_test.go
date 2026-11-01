package sessioninbox

import (
	"path/filepath"
	"testing"
)

// 任务1004（进程内清扫面）：StateSteerConsumed 的队列副本在「已消费 + 已
// settle」时删、「未消费」时保。
//
// 判据分工（两条 settle 路各守各的证据面，互不越界）：
//   - P15 SettleAppliedResidue：Uncertain 残留 × transcript 匹配；
//   - 840/1004 SettlePlannedRestartResidue：Uncertain+两 park 原因（或
//     StateSteerConsumed）× 调用方投递回执 —— 只在计划内恢复点跑；
//   - 本方法 SettleConsumedGuidanceResidue：StateSteerConsumed × transcript
//     匹配 —— 停摆被 Cancel 放弃的同进程恢复（1008 形态）没有 store 重开
//     也没有名册恢复点，只有这条路能把已消费副本清扫下架。
func TestSettleConsumedGuidanceResidueDropsOnlyConsumedMatched(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "s.jsonl"), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 双断言①：已消费 + 已 settle（正文在 transcript 回执集合里）→ 删。
	applied := seedMeta(t, s, "already applied guidance", "k-applied", "desktop", StateSteerConsumed, "")
	// 双断言②：未消费（Queued 真待处理工作）→ 保，即便正文同样出现在
	// transcript 里（用户重复发送同一指令是合法意图）。
	pending := seedMeta(t, s, "repeat me later", "k-pending", "desktop", StateQueued, "")
	// 附加面：已消费但正文不在 transcript（回执缺失）→ 保，宁可留待下次
	// 开店的恢复路，不凭状态单独下架。
	withoutReceipt := seedMeta(t, s, "consumed but never recorded", "k-noreceipt", "desktop", StateSteerConsumed, "")
	// 附加面：Uncertain 停靠行不归本清扫管（840 两条 park 原因有自己的
	// 投递回执判据）。
	parked := seedMeta(t, s, "parked for review", "k-parked", "collab:sc_p", StateUncertain, BlockReasonRestartOrphan)

	appliedText := "already applied guidance"
	pendingText := "repeat me later"
	match := func(meta InboxItemMeta, env PromptEnvelope) bool {
		return env.SubmitText == appliedText || env.SubmitText == pendingText
	}
	n, err := s.SettleConsumedGuidanceResidue(match)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("settled = %d, want 1 (只有「已消费+已 settle」那条)", n)
	}
	states := map[string]InboxState{}
	for _, it := range s.Snapshot().Items {
		states[it.ID] = it.State
	}
	if _, ok := states[applied.ID]; ok {
		t.Fatalf("已消费+已 settle 的副本 %s 必须删", applied.ID)
	}
	for _, keep := range []InboxItemMeta{pending, withoutReceipt, parked} {
		state, ok := states[keep.ID]
		if !ok {
			t.Fatalf("未消费/无回执/停靠行 %s (%s) 不得被清扫", keep.ID, keep.State)
		}
		if state != keep.State {
			t.Fatalf("保留行 %s 状态被改写: %s → %s", keep.ID, keep.State, state)
		}
	}
	// 幂等键随 drop 清理：同键重投得到新行（at-least-once 兜底不失效）。
	again, err := s.Enqueue(EnqueueRequest{
		Envelope:    PromptEnvelope{SubmitText: appliedText},
		Idempotency: "k-applied",
		Source:      "desktop",
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.Idempotent {
		t.Fatal("re-delivery must not dedupe onto the settled row")
	}
}

func TestSettleConsumedGuidanceResidueGuards(t *testing.T) {
	var nilStore *Store
	if n, err := nilStore.SettleConsumedGuidanceResidue(func(InboxItemMeta, PromptEnvelope) bool { return true }); n != 0 || err != ErrClosed {
		t.Fatalf("nil store = (%d, %v), want (0, ErrClosed)", n, err)
	}
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "s.jsonl"), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// nil 回调 = 无证据源 = 不动任何行。
	seedMeta(t, s, "guidance", "k-guard", "desktop", StateSteerConsumed, "")
	if n, err := s.SettleConsumedGuidanceResidue(nil); n != 0 || err != nil {
		t.Fatalf("nil callback = (%d, %v), want (0, nil)", n, err)
	}
	if got := len(s.Snapshot().Items); got != 1 {
		t.Fatalf("items = %d, want 1 (nil callback keeps every row)", got)
	}
}
