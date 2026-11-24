package agent

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/sessioncontext"
)

// tl1770 日志增强（用户令 20261123 15:19，与 cache 读数同批）验收。
//
// 契约：done 时两条构成日志——fold 前 source 分桶 + 压后 projection 分桶，
// 桶=真用户输入/session-context 载体/assistant/tool/图片/摘要（+系统提示，
// Q1 构成表口径），同一把校准尺（messageTokens），桶和=视图总量；
// transient_blocks（<context-state> 等骑在 user 轮头部的宿主瞬态块）与
// images 为子读数（含在宿主桶内，不参与求和）；『其他』桶≈0，非零即点名
// 未分类角色。

func TestCompactionViewBucketsClassifyAndSum(t *testing.T) {
	snap := sessioncontext.Build(sessioncontext.Sections{
		Environment: "env facts",
		Workspace:   "workspace facts",
	})
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "system prompt"},
		{Role: provider.RoleUser, Content: "<context-state>window occupancy 10k/20k (50%)</context-state>\nreal user ask about the budget"},
		{Role: provider.RoleUser, Origin: provider.MessageOriginHost, Content: snap.Content},
		{Role: provider.RoleAssistant, Content: "assistant reply body"},
		{Role: provider.RoleUser, Content: summaryTagOpen + "\nthe digest of an earlier fold\n" + summaryTagClose},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "c1", Name: "read_file", Arguments: `{"path":"x"}`}}},
		{Role: provider.RoleTool, Name: "read_file", ToolCallID: "c1", Content: "tool result body"},
		{Role: "banana", Content: "unclassified role body"},
		{Role: provider.RoleUser, Content: "local-only user turn", LocalOnly: true},
	}
	a := agentOverForce(t, &fullSendStubProvider{}, foldableSessionOverForce(2))
	b := a.compactionViewBuckets(msgs)

	if b.messages != 8 {
		t.Fatalf("messages = %d, want 8 (LocalOnly skipped)", b.messages)
	}
	if b.system == 0 {
		t.Fatal("system bucket empty")
	}
	if b.realUser == 0 {
		t.Fatal("real_user bucket empty")
	}
	if b.transient == 0 || b.transient >= b.realUser {
		t.Fatalf("transient_blocks = %d, want a nonempty subset of real_user %d", b.transient, b.realUser)
	}
	if b.carrier == 0 {
		t.Fatal("session_context carrier bucket empty (sessioncontext.Build content not classified)")
	}
	if b.assistant == 0 || b.tool == 0 || b.summary == 0 {
		t.Fatalf("assistant/tool/summary buckets = %d/%d/%d, want all nonempty", b.assistant, b.tool, b.summary)
	}
	if b.other == 0 {
		t.Fatal("other bucket empty — the unclassified-role fixture must land there")
	}
	// 逐桶和 = 视图总量（同尺对账；此视图无图片，image_tokens=0）。
	expected := 0
	for _, m := range msgs {
		if m.LocalOnly || IsPinnedContextRevision(m) {
			continue
		}
		expected += a.messageTokens(m)
	}
	if got := b.total(); got != expected {
		t.Fatalf("bucket sum = %d, want the view's own ruler total %d", got, expected)
	}
	if b.images != 0 || b.imageCount != 0 {
		t.Fatalf("images = %d/%d, want zero for a text-only fixture", b.images, b.imageCount)
	}
}

// TestCompactionViewBucketsImageTokensPricesIntoOwnerBucket pins the Q1 口径:
// an image prices INSIDE its owning message's bucket (messageTokens includes
// the resolution caliber) and shows up once more as the cross-cut readout.
func TestCompactionViewBucketsImageTokensPricesIntoOwnerBucket(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "screenshot turn", Images: []string{"https://example.invalid/shot.png"}},
	}
	a := agentOverForce(t, &fullSendStubProvider{}, foldableSessionOverForce(2))
	noImg := a.compactionViewBuckets(msgs[:1])
	with := a.compactionViewBuckets(msgs)
	if with.realUser <= noImg.realUser {
		t.Fatalf("image priced at %d tokens on top of the %d-token text owner, want the owner bucket to grow", with.realUser, noImg.realUser)
	}
	if with.images <= 0 || with.imageCount != 1 {
		t.Fatalf("image cross-cut readout = %d/%d, want the resolution price and count 1", with.images, with.imageCount)
	}
}

// TestCompactionDoneEmitsBucketLogs pins the done-time pair: one source
// (buckets_pre) line pricing the pre-fold view and one projection
// (buckets_post) line pricing the installed view, same ruler as
// source_tokens / projection_tokens.
func TestCompactionDoneEmitsBucketLogs(t *testing.T) {
	prov := &fullSendStubProvider{}
	a := agentOverForceWindow(t, prov, foldableSessionOverForce(48), 40_000)
	buf := captureSlog(t)
	if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
		t.Fatalf("pressure maintenance: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "compaction source buckets") || !strings.Contains(out, "stage=buckets_pre") {
		t.Fatalf("source buckets line missing:\n%s", tailForLog(out))
	}
	if !strings.Contains(out, "compaction projection buckets") || !strings.Contains(out, "stage=buckets_post") {
		t.Fatalf("projection buckets line missing:\n%s", tailForLog(out))
	}
	for _, field := range []string{
		"bucket_system_tokens=", "bucket_real_user_tokens=", "bucket_session_context_tokens=",
		"bucket_assistant_tokens=", "bucket_tool_tokens=", "bucket_summary_tokens=",
		"bucket_other_tokens=", "transient_blocks_tokens=", "image_tokens=", "image_count=",
	} {
		if strings.Count(out, field) < 2 {
			t.Fatalf("bucket field %s missing from one of the two lines:\n%s", field, tailForLog(out))
		}
	}
	if receipt := a.sess.compactionState.LastReceipt; receipt == nil || receipt.Status != "applied" {
		t.Fatalf("receipt = %+v, want applied", receipt)
	}
	_ = event.Discard
}

func tailForLog(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > 6 {
		lines = lines[len(lines)-6:]
	}
	return strings.Join(lines, "\n")
}
