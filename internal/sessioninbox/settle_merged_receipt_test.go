package sessioninbox

import (
	"path/filepath"
	"strings"
	"testing"
)

// 任务543：合并回执（task 221 的「[合并消息 ×N]」全文）结算的三条命中路径与
// 保留路径，直接钉在 Store 层。P15 的单条精确等式对合并消费的成员行永不成立
// ——合并全文 ≠ 成员自己的正文——于是合并消费过的条目重启后以 uncertain 重入。
//
// sessioninbox 不能 import control（control 反向依赖本包），所以回执文本按
// task 221 合并格式手写：这份字面量同时从消费侧钉住格式合同；writer 与
// parser 的往返在 control 侧（TestMergedEnvelopeRoundTripsThroughSegmentParser）。
// 匹配器同样手写三条命中路径，重点钉 SettleAppliedResidue 的合同：matcher 收
// 到行 meta（id 命中依赖它）、只动 uncertain、幂等键随 drop 清理。
func TestSettleAppliedResidueUnderstandsMergedReceipts(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.jsonl"), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	envelope := func(text, idem string) EnqueueRequest {
		return EnqueueRequest{Envelope: PromptEnvelope{SubmitText: text}, Idempotency: idem}
	}
	// 成员行先入队：合并全文要携带成员 A 的真实条目 id。
	memberIDHit, err := s.Enqueue(envelope("member text A", "k-id-hit"))
	if err != nil {
		t.Fatal(err)
	}
	memberTextHit, err := s.Enqueue(envelope("member text B", "k-text-hit"))
	if err != nil {
		t.Fatal(err)
	}
	const foreignID = "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	mergedFullText := "[合并消息 ×2]\n\n" +
		"── 合并自 inbox 条目 " + memberIDHit.ItemID + "（来源 desktop） ──\nmember text A\n\n" +
		"── 合并自 inbox 条目 " + foreignID + "（来源 desktop） ──\nmember text B"
	// 载体行：正文就是合并全文（P15 精确等式路径，重启后同为 uncertain）。
	carrier, err := s.Enqueue(envelope(mergedFullText, "k-carrier"))
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := s.Enqueue(envelope("genuinely unresolved work", "k-unrelated"))
	if err != nil {
		t.Fatal(err)
	}
	live, err := s.Enqueue(envelope("member text A", "k-live"))
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := s.Enqueue(envelope("member text B", "k-blocked"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{carrier.ItemID, memberIDHit.ItemID, memberTextHit.ItemID, unrelated.ItemID} {
		if err := s.SetState(id, StateUncertain, "in-flight owner is no longer active"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetState(blocked.ItemID, StateBlocked, "reference unavailable"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPaused(true); err != nil {
		t.Fatal(err)
	}

	matcher := func(meta InboxItemMeta, env PromptEnvelope) bool {
		if meta.ID == memberIDHit.ItemID { // 段头 id 命中（权威键，来自回执段头）
			return true
		}
		text := strings.TrimSpace(env.SubmitText)
		return text == mergedFullText || text == "member text B" // 全文精确 + 段文本兜底
	}
	dropped, err := s.SettleAppliedResidue(matcher)
	if err != nil {
		t.Fatal(err)
	}
	if dropped != 3 {
		t.Fatalf("dropped = %d, want 3 (carrier + id-hit member + text-hit member)", dropped)
	}
	states := make(map[string]InboxState)
	for _, item := range s.Snapshot().Items {
		states[item.ID] = item.State
	}
	for _, id := range []string{carrier.ItemID, memberIDHit.ItemID, memberTextHit.ItemID} {
		if _, ok := states[id]; ok {
			t.Fatalf("applied merged residue %s must be settled, not replayed", id)
		}
	}
	if states[unrelated.ItemID] != StateUncertain {
		t.Fatalf("unrelated uncertain row must stay for review, got %+v", states)
	}
	if states[live.ItemID] != StateQueued {
		t.Fatalf("queued live row must stay queued, got %+v", states)
	}
	if states[blocked.ItemID] != StateBlocked {
		t.Fatalf("blocked row stays user-decided even when its body matches, got %+v", states)
	}
	// 幂等键随 drop 清理：同文同键重新入队得到新行。
	again, err := s.Enqueue(envelope("member text A", "k-id-hit"))
	if err != nil {
		t.Fatal(err)
	}
	if again.Idempotent || again.ItemID == memberIDHit.ItemID {
		t.Fatalf("re-enqueue deduplicated onto the settled row: %+v", again)
	}
}
