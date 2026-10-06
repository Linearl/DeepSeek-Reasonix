package control

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/sessioninbox"
)

// ── 任务543：合并回执解析（mergeSegmentHeader / mergeInboxEnvelope 的逆操作）──
// P15 的结算匹配是「一条 inbox 条目 = 一条 steer」的单条精确等式，与合并消费
// （N 条 → 1 条合并全文）不匹配 ⇒ 合并消费过的条目若为 uncertain，重启后的
// 结算永远匹配不上 ⇒ 重入。这里把 writer 的段格式解析回成员 id 与段文本。

func TestParseMergedSteerSegments(t *testing.T) {
	t.Run("two segments with sources", func(t *testing.T) {
		text := "[合并消息 ×2]\n\n" +
			"── 合并自 inbox 条目 id-a（来源 desktop） ──\nfirst body\nsecond line\n\n" +
			"── 合并自 inbox 条目 id-b（来源 collab:sc_x） ──\nbody b"
		ids, bodies, ok := parseMergedSteerSegments(text)
		if !ok {
			t.Fatal("merged body must parse")
		}
		if want := []string{"id-a", "id-b"}; !reflect.DeepEqual(ids, want) {
			t.Fatalf("ids = %v, want %v", ids, want)
		}
		if want := []string{"first body\nsecond line", "body b"}; !reflect.DeepEqual(bodies, want) {
			t.Fatalf("bodies = %q, want %q", bodies, want)
		}
	})
	t.Run("header without source part", func(t *testing.T) {
		ids, bodies, ok := parseMergedSteerSegments("── 合并自 inbox 条目 id-c ──\nbody c")
		if !ok {
			t.Fatal("source-less segment header must parse")
		}
		if want := []string{"id-c"}; !reflect.DeepEqual(ids, want) {
			t.Fatalf("ids = %v, want %v", ids, want)
		}
		if want := []string{"body c"}; !reflect.DeepEqual(bodies, want) {
			t.Fatalf("bodies = %q, want %q", bodies, want)
		}
	})
	t.Run("plain steer body is not merged", func(t *testing.T) {
		if ids, bodies, ok := parseMergedSteerSegments("just a normal steer"); ok || ids != nil || bodies != nil {
			t.Fatalf("plain body = (%v, %v, %v), want no merge", ids, bodies, ok)
		}
	})
	t.Run("title only without segment headers", func(t *testing.T) {
		if _, _, ok := parseMergedSteerSegments("[合并消息 ×2]\n\nno headers after all"); ok {
			t.Fatal("title without segment headers must not count as merged")
		}
	})
	t.Run("header with empty id is ignored", func(t *testing.T) {
		if _, _, ok := parseMergedSteerSegments("── 合并自 inbox 条目  ──\nx"); ok {
			t.Fatal("empty-id header must not count as merged")
		}
	})
	t.Run("unterminated header line is not merged", func(t *testing.T) {
		// 段头缺右括线（如传输截断）时整段不算合并态：不给任何匹配键，
		// 宁可留给用户裁决也不误删。
		if ids, bodies, ok := parseMergedSteerSegments("── 合并自 inbox 条目 id-no-suffix"); ok || ids != nil || bodies != nil {
			t.Fatalf("unterminated header = (%v, %v, %v), want no merge", ids, bodies, ok)
		}
	})
}

// writer → parser 必须无损往返：真实 mergeInboxEnvelope 产出的合并全文，解析
// 回的成员 id 与段文本和输入一一对应。这是结算匹配所依赖的格式合同。
func TestMergedEnvelopeRoundTripsThroughSegmentParser(t *testing.T) {
	metas := []sessioninbox.InboxItemMeta{
		{ID: "id-a", Source: "desktop"},
		{ID: "id-b"}, // 无来源成员——段头不带（来源 …）部分
		{ID: "id-c", Source: "collab:sc_x"},
	}
	texts := []string{"first\nmulti line", "second", "third body"}
	envelopes := make([]sessioninbox.PromptEnvelope, len(texts))
	for i, txt := range texts {
		envelopes[i] = sessioninbox.PromptEnvelope{SubmitText: txt}
	}
	merged := mergeInboxEnvelope(metas, envelopes, CollabInboxMergeAll)
	ids, bodies, ok := parseMergedSteerSegments(merged.SubmitText)
	if !ok {
		t.Fatalf("writer output must parse back: %q", merged.SubmitText)
	}
	if want := []string{"id-a", "id-b", "id-c"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	if want := texts; !reflect.DeepEqual(bodies, want) {
		t.Fatalf("bodies = %q, want %q", bodies, want)
	}
}

// seedInboxItemMeta is seedInboxItem with the planted row returned so tests
// can build merged receipts carrying the real member ids.
func seedInboxItemMeta(t *testing.T, session, text, idem string, state sessioninbox.InboxState) sessioninbox.InboxItemMeta {
	t.Helper()
	st, err := sessioninbox.Open(session, sessioninbox.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rec, err := st.Enqueue(sessioninbox.EnqueueRequest{
		Envelope:    sessioninbox.PromptEnvelope{SubmitText: text},
		Idempotency: idem,
	})
	if err != nil {
		t.Fatal(err)
	}
	if state != sessioninbox.StateQueued {
		if err := st.SetState(rec.ItemID, state, "in-flight owner is no longer active"); err != nil {
			t.Fatal(err)
		}
	}
	for _, it := range st.Snapshot().Items {
		if it.ID == rec.ItemID {
			return it
		}
	}
	t.Fatalf("seeded item %s missing from snapshot", rec.ItemID)
	return sessioninbox.InboxItemMeta{}
}

// mergedSteerReceipt builds the transcript receipt via the real writer.
func mergedSteerReceipt(t *testing.T, metas []sessioninbox.InboxItemMeta, texts []string) string {
	t.Helper()
	envelopes := make([]sessioninbox.PromptEnvelope, len(texts))
	for i, txt := range texts {
		envelopes[i] = sessioninbox.PromptEnvelope{SubmitText: txt}
	}
	return mergeInboxEnvelope(metas, envelopes, CollabInboxMergeAll).SubmitText
}

// 任务543 端到端：合并消费 → 重启 → uncertain 存量 → settle 应清理。
// 现场复现：两条引导被合并为一条注入（transcript 回执 = 合并全文），重启后成
// 员行以 uncertain 复现——单条精确等式永不命中。升级后三条路径各有断言：
// memberA 走段头 id 命中，memberB 的 id 不出现在任何段头、走段文本兜底，
// 无关 uncertain 行与 queued/blocked 行一律保留。
func TestReopenSettlesMergedConsumedResidue(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(session, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	memberA := seedInboxItemMeta(t, session, "merged member guidance A", "k-a", sessioninbox.StateUncertain)
	memberB := seedInboxItemMeta(t, session, "merged member guidance B", "k-b", sessioninbox.StateUncertain)
	unrelated := seedInboxItemMeta(t, session, "genuinely unresolved work", "k-unrelated", sessioninbox.StateUncertain)
	// 重复已完成指令是用户的活意图：同文 queued 行永不自动结算。
	live := seedInboxItemMeta(t, session, "merged member guidance A", "k-live", sessioninbox.StateQueued)
	// P15 的有意设计：blocked 留用户裁决，正文匹配也不动。
	blocked := seedInboxItemMeta(t, session, "merged member guidance B", "k-blocked", sessioninbox.StateBlocked)

	// 第二段头故意携带一个无关 id：memberB 只能靠段文本命中。
	receipt := mergedSteerReceipt(t,
		[]sessioninbox.InboxItemMeta{
			{ID: memberA.ID, Source: "desktop"},
			{ID: "retired-member-not-in-manifest", Source: "desktop"},
		},
		[]string{"merged member guidance A", "merged member guidance B"})

	c := New(Options{
		SessionPath: session,
		SessionDir:  dir,
		Sink:        event.Discard,
		InboxAppliedReceipts: func(string) map[string]struct{} {
			return map[string]struct{}{receipt: {}}
		},
	})
	snap := c.InboxSnapshot()
	states := make(map[string]sessioninbox.InboxState)
	for _, it := range snap.Items {
		states[it.ID] = it.State
	}
	if _, ok := states[memberA.ID]; ok {
		t.Fatal("member A (segment-header id hit) must settle, not replay")
	}
	if _, ok := states[memberB.ID]; ok {
		t.Fatal("member B (segment body text hit) must settle, not replay")
	}
	if states[unrelated.ID] != sessioninbox.StateUncertain {
		t.Fatalf("unrelated uncertain row must stay for review, got %+v", states)
	}
	if states[live.ID] != sessioninbox.StateQueued {
		t.Fatalf("queued live row must stay queued, got %+v", states)
	}
	if states[blocked.ID] != sessioninbox.StateBlocked {
		t.Fatalf("blocked row must stay user-decided, got %+v", states)
	}

	// 验收③：幂等键随 drop 清理——同文同键重新入队得到新行，不去重到已删行。
	st, err := sessioninbox.Open(session, sessioninbox.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	again, err := st.Enqueue(sessioninbox.EnqueueRequest{
		Envelope:    sessioninbox.PromptEnvelope{SubmitText: "merged member guidance A"},
		Idempotency: "k-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.Idempotent || again.ItemID == memberA.ID {
		t.Fatalf("re-enqueue deduplicated onto the settled row: %+v", again)
	}
}
