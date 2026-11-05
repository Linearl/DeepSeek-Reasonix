package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

// tl1407 现场（20261105 desktop.log）：ai-novel 会话文件首行自证
// 「[本地折叠] 已省略 107 条早期消息……」——foldProjectionTail 把 index0 的
// system 头一并折掉，落盘出无头转录；之后每个保存周期被 controller 快照守卫
// 拒绝（controller.go「refusing to snapshot … no system message」WARN 风暴），
// 消息只在内存堆积、崩溃即全丢。本件三层防线：
//   ① 源头：折叠永不触碰 system 前缀（本文件）；
//   ② 防护终局：快照守卫检测到无头会话后补默认 system 头续跑（control 侧）；
//   ③ 落盘止损：修复不可用时 raw dump 旁路文件（control 侧）。

// tl1407FoldFixture builds a system-led transcript whose middle history
// massively overshoots the given budget so the fold must act.
func tl1407FoldFixture(sysPrompt string) []provider.Message {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: sysPrompt},
		{Role: provider.RoleUser, Content: strings.Repeat("early history line ", 400)},
		{Role: provider.RoleAssistant, Content: strings.Repeat("old answer line ", 400)},
		{Role: provider.RoleUser, Content: "latest user question"},
	}
	return msgs
}

// TestTL1407FoldKeepsLeadingSystemPrompt pins the injection-point fix: the
// fold's placeholder must land AFTER the leading system message, never on top
// of it. The 20261105 field transcript starts with the fold placeholder as the
// very first line — the system header was folded away with the old history.
func TestTL1407FoldKeepsLeadingSystemPrompt(t *testing.T) {
	const sys = "You are Reasonix, a coding agent."
	msgs := tl1407FoldFixture(sys)

	folded, count := foldProjectionTail(msgs, 500)
	if count <= 0 {
		t.Fatalf("fold must act on an over-budget history (folded=%d)", count)
	}
	if len(folded) == 0 {
		t.Fatal("fold produced an empty transcript")
	}
	if folded[0].Role != provider.RoleSystem || folded[0].Content != sys {
		t.Fatalf("fold dropped/moved the leading system prompt: first message role=%s content=%q", folded[0].Role, folded[0].Content)
	}
	if len(folded) < 2 || folded[1].Role != provider.RoleUser || !strings.Contains(folded[1].Content, "[本地折叠]") {
		head := foldedMessageHead(folded)
		t.Fatalf("placeholder must sit right after the system header: first=%+v role@1=%s", head, foldedRoleAt(folded, 1))
	}
	last := folded[len(folded)-1]
	if last.Content != "latest user question" {
		t.Fatalf("newest message must survive the fold verbatim: got %q", last.Content)
	}
}

// TestTL1407DietSessionMessagesKeepsSystemHeader covers the shared entry: the
// closed-file heal (dietSessionProjection), the open-session heal
// (SelfHealOpenSession) and the overflow diet (runOverflowDiet) all funnel
// through DietSessionMessages — one assertion here pins all three callers.
func TestTL1407DietSessionMessagesKeepsSystemHeader(t *testing.T) {
	const sys = "You are Reasonix, a coding agent."
	msgs := tl1407FoldFixture(sys)
	// Window far below the transcript so the overflow criterion trips
	// (projectionOverflowRatio=0.95) and the diet strips/folds.
	folded, actions, changed := DietSessionMessages(msgs, 300)
	if !changed {
		t.Fatalf("diet must act on an over-window system-led transcript: actions=%+v", actions)
	}
	if len(folded) == 0 || folded[0].Role != provider.RoleSystem || folded[0].Content != sys {
		t.Fatalf("diet dropped the system header: first=%+v", foldedMessageHead(folded))
	}
}

// TestTL1407SelfHealDietPersistsSystemHeader is the disk-level reproduction of
// the field incident: a system-led schema-1 session file goes through the
// closed-file self-heal diet and the RELOADED transcript must still start with
// the identity header — previously the rewritten file began with the fold
// placeholder, which is exactly the on-disk corruption behind the snapshot
// refusal storm.
func TestTL1407SelfHealDietPersistsSystemHeader(t *testing.T) {
	const sys = "You are Reasonix, a coding agent."
	sess := NewSession(sys)
	for i := 0; i < 6; i++ {
		sess.Add(provider.Message{Role: provider.RoleUser, Content: strings.Repeat("history chunk ", 300)})
		sess.Add(provider.Message{Role: provider.RoleAssistant, Content: strings.Repeat("reply chunk ", 300)})
	}
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "latest"})
	path := filepath.Join(t.TempDir(), "tl1407-headless-repro.jsonl")
	if err := sess.Save(path); err != nil {
		t.Fatal(err)
	}

	const window = 400
	before := InspectSessionHealth(path, HealthInspectOptions{WindowTokens: window})
	if before.Healthy {
		t.Fatalf("fixture must be unhealthy (projection overflow): %+v", before.Issues)
	}

	report, err := SelfHealSession(path, SelfHealOptions{Diet: true, WindowTokens: window})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Repaired {
		t.Fatalf("expected repair: %+v", report)
	}
	loaded := mustSnapshot(t, path)
	if len(loaded) == 0 || loaded[0].Role != provider.RoleSystem || loaded[0].Content != sys {
		head := provider.Message{}
		if len(loaded) > 0 {
			head = loaded[0]
		}
		t.Fatalf("healed file lost the system header; first message role=%q content=%q (report=%+v)", head.Role, head.Content, report)
	}
}

func foldedMessageHead(msgs []provider.Message) provider.Message {
	if len(msgs) == 0 {
		return provider.Message{}
	}
	return msgs[0]
}

func foldedRoleAt(msgs []provider.Message, i int) string {
	if i < 0 || i >= len(msgs) {
		return "<none>"
	}
	return string(msgs[i].Role)
}
