package control

import (
	"strings"
	"testing"

	"reasonix/internal/sessioninbox"
)

// Task 1106: 合并消息重复渲染。载体重合并——一次合并后的 carrier 因提交失败
// （busy / runtime 未发布 / 崩溃恢复）带着合并体留在队列，下次 dispatch 再与
// 新条目合并时，旧合并体被整体当作一个成员塞进新合并体：标题 [合并消息 ×N]
// 与同一 inbox 条目的块头在消息体中出现两次。修复 = 合并前展平嵌套合并体，
// 每条原始消息恰好一个段。

func TestMergeEnvelopeFlattensNestedMergedMember(t *testing.T) {
	// 模拟真实嵌套：成员一是一个旧合并体（12ba92b9 + id-b），成员二是新条目。
	nested := mergeInboxEnvelope(
		[]sessioninbox.InboxItemMeta{{ID: "12ba92b9", Source: "collab:sc_a"}, {ID: "id-b"}},
		[]sessioninbox.PromptEnvelope{{SubmitText: "原文A"}, {SubmitText: "原文B"}},
		CollabInboxMergeAll,
	)
	merged := mergeInboxEnvelope(
		[]sessioninbox.InboxItemMeta{{ID: "carrier", Source: "collab:sc_a"}, {ID: "id-c"}},
		[]sessioninbox.PromptEnvelope{nested, {SubmitText: "原文C"}},
		CollabInboxMergeAll,
	)
	body := merged.SubmitText
	// 标题只出现一次，且计数 = 展平后的叶子段数（3 条原始消息）
	if got, want := strings.Count(body, "[合并消息 ×"), 1; got != want {
		t.Fatalf("title line appears %d times, want %d:\n%s", got, want, body)
	}
	if !strings.Contains(body, "[合并消息 ×3]") {
		t.Fatalf("title must count flattened leaf segments (3):\n%s", body)
	}
	// 每个原始条目的块头恰好一次
	for _, id := range []string{"12ba92b9", "id-b", "id-c"} {
		if got, want := strings.Count(body, "── 合并自 inbox 条目 "+id), 1; got != want {
			t.Fatalf("segment header for %s appears %d times, want %d:\n%s", id, got, want, body)
		}
	}
	// 原文一个不丢
	for _, want := range []string{"原文A", "原文B", "原文C"} {
		if !strings.Contains(body, want) {
			t.Fatalf("flattened body lost %q:\n%s", want, body)
		}
	}
	// 载体外壳（其 header 不对应任何原始条目）不得残留为段头
	if strings.Contains(body, "── 合并自 inbox 条目 carrier") {
		t.Fatalf("carrier shell header must not survive flattening:\n%s", body)
	}
	// mergedFrom 与段头一致（task 543 settle 面依赖叶子 id）
	if got, want := merged.Extra["mergedFrom"], "12ba92b9,id-b,id-c"; got != want {
		t.Fatalf("mergedFrom = %q, want %q", got, want)
	}
}

func TestMergeEnvelopeFlattensDoublyNestedMergedMember(t *testing.T) {
	first := mergeInboxEnvelope(
		[]sessioninbox.InboxItemMeta{{ID: "id-a"}, {ID: "id-b"}},
		[]sessioninbox.PromptEnvelope{{SubmitText: "A"}, {SubmitText: "B"}},
		CollabInboxMergeAll,
	)
	second := mergeInboxEnvelope(
		[]sessioninbox.InboxItemMeta{{ID: "carrier1"}, {ID: "id-c"}},
		[]sessioninbox.PromptEnvelope{first, {SubmitText: "C"}},
		CollabInboxMergeAll,
	)
	merged := mergeInboxEnvelope(
		[]sessioninbox.InboxItemMeta{{ID: "carrier2"}, {ID: "id-d"}},
		[]sessioninbox.PromptEnvelope{second, {SubmitText: "D"}},
		CollabInboxMergeAll,
	)
	body := merged.SubmitText
	if !strings.Contains(body, "[合并消息 ×4]") {
		t.Fatalf("two nesting layers must flatten to 4 leaf segments:\n%s", body)
	}
	if got, want := strings.Count(body, "── 合并自 inbox 条目 "), 4; got != want {
		t.Fatalf("segment header count = %d, want %d:\n%s", got, want, body)
	}
	for _, want := range []string{"A", "B", "C", "D"} {
		if !strings.Contains(body, want) {
			t.Fatalf("flattened body lost %q:\n%s", want, body)
		}
	}
}

func TestMergeEnvelopeDedupesRepeatedLeafID(t *testing.T) {
	// 同一条目 id 只保留首次出现的段——防御性去重，重复合并不再放大。
	nested := mergeInboxEnvelope(
		[]sessioninbox.InboxItemMeta{{ID: "dup-id"}},
		[]sessioninbox.PromptEnvelope{{SubmitText: "dup body"}},
		CollabInboxMergeAll,
	)
	merged := mergeInboxEnvelope(
		[]sessioninbox.InboxItemMeta{{ID: "dup-id"}, {ID: "id-x"}},
		[]sessioninbox.PromptEnvelope{nested, {SubmitText: "X"}},
		CollabInboxMergeAll,
	)
	body := merged.SubmitText
	if got, want := strings.Count(body, "── 合并自 inbox 条目 dup-id"), 1; got != want {
		t.Fatalf("dup-id header appears %d times, want %d:\n%s", got, want, body)
	}
	if !strings.Contains(body, "[合并消息 ×2]") {
		t.Fatalf("dedupe must keep 2 segments:\n%s", body)
	}
}

func TestMergeEnvelopePlainMembersUnchanged(t *testing.T) {
	// 普通成员零回归：段数、顺序、mergedFrom 与既有行为一致。
	metas := []sessioninbox.InboxItemMeta{{ID: "id-a", Source: "desktop"}, {ID: "id-b"}}
	merged := mergeInboxEnvelope(
		metas,
		[]sessioninbox.PromptEnvelope{{SubmitText: "first"}, {SubmitText: "second"}},
		CollabInboxMergeAll,
	)
	body := merged.SubmitText
	if !strings.Contains(body, "[合并消息 ×2]") {
		t.Fatalf("plain merge must count 2:\n%s", body)
	}
	if got, want := strings.Count(body, "── 合并自 inbox 条目 "), 2; got != want {
		t.Fatalf("segment header count = %d, want %d:\n%s", got, want, body)
	}
	if strings.Index(body, "first") > strings.Index(body, "second") {
		t.Fatalf("dispatch order must be preserved:\n%s", body)
	}
	if got, want := merged.Extra["mergedFrom"], "id-a,id-b"; got != want {
		t.Fatalf("mergedFrom = %q, want %q", got, want)
	}
}

// 端到端：carrier 带合并体留队后再次入组，maybeMergeInboxDispatchGroup 产出
// 单层合并体——标题与每个块头各一次。
func TestInboxMergeRetiredCarrierReenterDoesNotDuplicate(t *testing.T) {
	armInboxMerge(t, CollabInboxMergeAll)
	c, _, _ := newInboxDispatchController(t)
	carrier := enqueueMergeItem(t, c, "collab:sc_a", "原文A")
	member := enqueueMergeItem(t, c, "collab:sc_b", "原文B")

	// 第一次合并：carrier 承载 2 条（模拟后提交失败留队）
	if got := c.maybeMergeInboxDispatchGroup(carrier); got.ID != carrier.ID {
		t.Fatalf("carrier changed: %s -> %s", carrier.ID, got.ID)
	}
	_, env, err := c.ReadInboxItem(carrier.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(env.SubmitText, "[合并消息 ×2]") {
		t.Fatalf("first merge must count 2:\n%s", env.SubmitText)
	}

	// 新条目入队，carrier（已持合并体）再次成为组首
	fresh := enqueueMergeItem(t, c, "collab:sc_c", "原文C")
	if got := c.maybeMergeInboxDispatchGroup(carrier); got.ID != carrier.ID {
		t.Fatalf("carrier changed: %s -> %s", carrier.ID, got.ID)
	}
	_, env, err = c.ReadInboxItem(carrier.ID)
	if err != nil {
		t.Fatal(err)
	}
	body := env.SubmitText
	if got, want := strings.Count(body, "[合并消息 ×"), 1; got != want {
		t.Fatalf("title line appears %d times, want %d:\n%s", got, want, body)
	}
	if !strings.Contains(body, "[合并消息 ×3]") {
		t.Fatalf("remerge must flatten to 3 leaf segments:\n%s", body)
	}
	for _, id := range []string{carrier.ID, member.ID, fresh.ID} {
		if got, want := strings.Count(body, "── 合并自 inbox 条目 "+id), 1; got != want {
			t.Fatalf("segment header for %s appears %d times, want %d:\n%s", id, got, want, body)
		}
	}
	for _, want := range []string{"原文A", "原文B", "原文C"} {
		if !strings.Contains(body, want) {
			t.Fatalf("remerged body lost %q:\n%s", want, body)
		}
	}
}
