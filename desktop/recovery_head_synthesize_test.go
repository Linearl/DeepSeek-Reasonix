package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// 任务 1065：「选 HEAD → 反向合成最长链」的后端面。
//
// 验收（tl 1065 正文）：
//  1. 面板内选 HEAD + 勾选分支集合 → 一键反向合成最长链；
//  2. 合成后目标链独有计数涵盖各参与分支的独有事件（总条数≈并集），留操
//     作记录+进度展示；
//  3. 重复事件零双份；合成后其余分支可经 1064 删除；旧面板全部规则（tail
//     预览/轮次优先推荐/预览降级）在新面板等价可用；
//  4. Go 包级测试+前端 harness 绿，无预存红。
//
// 本测试钉住数据层：合成一次后主线持有并集（重复事件零双份），落败分支的
// 独有前缀被 graft 回主线（Prefixed>0），其余分支进回收站，合成结果可读。

func TestSynthesizeSessionHeadsFoldsUniqueEventsOntoChosenHead(t *testing.T) {
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(dir, "heads-synth-1.jsonl")
	copyPath := filepath.Join(dir, "heads-synth-1-recovery-0123456789abcdef.jsonl")
	t.Cleanup(func() {
		_ = os.Remove(mainPath)
		_ = os.Remove(copyPath)
	})

	// Main: sys + q1 + a1 (a "canonical" prefix the copy will fork from).
	s := agent.NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "q1"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "a1"})
	if err := s.Save(mainPath); err != nil {
		t.Fatal(err)
	}

	// Copy: same prefix + one extra user turn (the "unique" work the user is
	// deciding against by keeping the main's chain as HEAD).
	b, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	copyBody := string(b) +
		`{"role":"user","content":"q2-unique-to-copy"}` + "\n" +
		`{"role":"assistant","content":"a2-unique-to-copy"}` + "\n"
	if err := os.WriteFile(copyPath, []byte(copyBody), 0o600); err != nil {
		t.Fatal(err)
	}
	// The consolidate engine requires the copy's branch meta sidecar to be
	// present (it loads the winner's meta before renaming it onto the main
	// identity); EnsureBranchMeta is the same production path a copy gets when
	// it is first written by a real session.
	if _, err := agent.EnsureBranchMeta(copyPath); err != nil {
		t.Fatalf("ensure copy meta: %v", err)
	}

	app := newBranchDeleteApp(t)
	report, err := app.SynthesizeSessionHeads(mainPath, "")
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if len(report.Items) == 0 {
		t.Fatal("the pre-run reading must list the participating branch")
	}
	if report.Items[0].Unique == 0 {
		t.Fatalf("the copy's unique events must be non-zero before synthesis: %+v", report.Items[0])
	}

	// The copy is folded into the recoverable trash, not hard-deleted.
	if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
		t.Fatalf("the losing branch must be archived away from the live directory, stat err=%v", err)
	}

	// The main transcript survives (it was the chosen HEAD) and the report
	// carries a post-run reading.
	if _, err := os.Stat(mainPath); err != nil {
		t.Fatalf("the chosen HEAD's transcript must survive synthesis: %v", err)
	}
	if report.AfterMessages == 0 {
		t.Fatalf("the report must carry a post-run reading of the new main: %+v", report)
	}
}

func TestSynthesizeSessionHeadsRefusesSingleChain(t *testing.T) {
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(dir, "heads-synth-2.jsonl")
	t.Cleanup(func() { _ = os.Remove(mainPath) })

	s := agent.NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "q1"})
	if err := s.Save(mainPath); err != nil {
		t.Fatal(err)
	}

	app := newBranchDeleteApp(t)
	if _, err := app.SynthesizeSessionHeads(mainPath, ""); err == nil {
		t.Fatal("a conversation with one chain has nothing to synthesize; the call must be refused")
	} else if !strings.Contains(err.Error(), "only one chain") {
		t.Fatalf("refusal must say what is missing, got %v", err)
	}
}

// 任务 1117 的原修复（「未选 HEAD」对纯 v3 头会话不再误报 only one chain，
// TestSynthesizeSessionHeadsUnselectedHeadSeesV3Heads）已随 schema-2 头重放
// 退役整体失效：tl1227 读侧单路径化后 ListSessionHeads 是恒空垫片（签名保留
// 供 desktop 版本/lineage 面调用，恒返回空 = 「无版本对象」的被动收敛，
// tl1173 §4.2 同款），tl1280 又删掉了产生多 v3 heads 的 DAG 写路径。原 fixture
// 手写的双 live head 事件序列不再被重放，多链前提不可达——原测试自 tl1227 起
// 稳定红，本测试改钉退役后的收敛契约：任何形态的会话对合成而言都是单链，
// 「this conversation has only one chain」是唯一且正确的拒绝结果（显式注释
// 非静默 SKIP，tl1280③ 同款纪律）。
func TestSynthesizeSessionHeadsV3HeadsRetiredRefusesAsSingleChain(t *testing.T) {
	path, _, _ := writeV3OnlyTwoHeadFixture(t)

	if heads, err := agent.ListSessionHeads(path); err != nil || len(heads) != 0 {
		t.Fatalf("ListSessionHeads must stay the retired empty shim (tl1227), got %v err=%v", heads, err)
	}
	app := newBranchDeleteApp(t)
	if _, err := app.SynthesizeSessionHeads(path, ""); err == nil {
		t.Fatal("with v3 head replay retired there is no second chain to synthesize; the call must be refused")
	} else if !strings.Contains(err.Error(), "only one chain") {
		t.Fatalf("refusal must carry the single-chain verdict, got %v", err)
	}
}

// 任务 1146 返修2（用户 20261025 裁决）原钉三条路径：① 显式选定落败链 →
// 先切换后清理；② 未选 HEAD → 只清理其余活链；③ 未知 headID → 明确报错。
// ①② 已随 schema-2 头重放退役（tl1227/tl1280）整体不可达——v3-only 会话
// 不再存在第二条链，切换/清理无从谈起，对应两测试
// （TestSynthesizeSessionHeadsV3OnlySwitchesToChosenHeadAndRetiresLosers /
// TestSynthesizeSessionHeadsV3OnlyUnselectedKeepsMainAndRetiresOthers）删除，
// 收敛后契约由 TestSynthesizeSessionHeadsV3HeadsRetiredRefusesAsSingleChain
// 钉住。③（RefusesUnknownHead）保留：未知 headID 必须报错、不静默的约束
// 在退役后依然成立。fixture 保留给 ③ 与收敛钉复用。
func writeV3OnlyTwoHeadFixture(t *testing.T) (path, mainHead, forkHead string) {
	t.Helper()
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(dir, "heads-synth-1146r2-v3only.jsonl")
	eventsPath := store.SessionEventLog(path)
	t.Cleanup(func() {
		_ = os.Remove(path)
		_ = os.Remove(eventsPath)
	})
	mainHead = agent.SessionMainHead
	forkHead = agent.NewHeadID()
	q1, a1, q2, a2, q3, a3 := agent.NewMessageID(), agent.NewMessageID(), agent.NewMessageID(), agent.NewMessageID(), agent.NewMessageID(), agent.NewMessageID()
	base := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

	msgEntry := func(head, parent, turn, id string, role provider.Role, content string, at time.Time) any {
		return map[string]any{
			"schema_version": 2,
			"type":           "message",
			"id":             id,
			"head":           head,
			"turn":           turn,
			"at":             at.UTC().Format(time.RFC3339Nano),
			"parent":         parent,
			"digest":         "",
			"msgs":           []any{map[string]any{"role": string(role), "content": content, "id": id}},
		}
	}
	forkEntry := func(head, newHead, from string, at time.Time) any {
		return map[string]any{
			"schema_version": 2,
			"type":           "fork",
			"head":           head,
			"new_head":       newHead,
			"from":           from,
			"kind":           agent.HeadKindFork,
			"at":             at.UTC().Format(time.RFC3339Nano),
		}
	}
	// main：q1/a1 + 分叉点后的 q3/a3（最后活动更新 ⇒ 重放默认选中 main）；
	// fork：q1/a1 之后接独立的 q2/a2。两链互不覆盖。
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, e := range []any{
		msgEntry(mainHead, "", "", q1, provider.RoleUser, "q1", base.Add(-4*time.Second)),
		msgEntry(mainHead, q1, "", a1, provider.RoleAssistant, "a1", base.Add(-3*time.Second)),
		forkEntry(mainHead, forkHead, a1, base.Add(-2*time.Second)),
		msgEntry(forkHead, a1, "", q2, provider.RoleUser, "q2", base.Add(-1*time.Second)),
		msgEntry(forkHead, q2, "", a2, provider.RoleAssistant, "a2", base),
		msgEntry(mainHead, a1, "", q3, provider.RoleUser, "q3", base.Add(2*time.Second)),
		msgEntry(mainHead, q3, "", a3, provider.RoleAssistant, "a3", base.Add(3*time.Second)),
	} {
		if err := enc.Encode(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(eventsPath, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, mainHead, forkHead
}

// TestSynthesizeSessionHeadsV3OnlySwitchesToChosenHeadAndRetiresLosers 与
// TestSynthesizeSessionHeadsV3OnlyUnselectedKeepsMainAndRetiresOthers 已删——
// 依赖 v3 头重放产生第二链条（tl1227/tl1280 退役），收敛契约见
// TestSynthesizeSessionHeadsV3HeadsRetiredRefusesAsSingleChain。
func TestSynthesizeSessionHeadsV3OnlyRefusesUnknownHead(t *testing.T) {
	path, _, _ := writeV3OnlyTwoHeadFixture(t)

	app := newBranchDeleteApp(t)
	if _, err := app.SynthesizeSessionHeads(path, "no-such-head"); err == nil {
		t.Fatal("an unknown head id must be refused, not silently ignored")
	}
}
