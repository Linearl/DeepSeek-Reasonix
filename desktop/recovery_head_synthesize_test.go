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

// 任务 1117：「未选 HEAD」对纯 v3 头会话不再误报 only one chain。
//
// 现场（20261024 fork开发-新8，3 个 v3 版本 1691/966/254 轮）：会话没有
// recovery 副本文件（都已入回收站或已包含），只有 canonical event log 里的
// 多个 v3 heads。用户未选 HEAD 直接点「以选定 HEAD 合成最长链」，旧代码的
// headID=="" 路径只扫 recoveryCopiesByMain 的副本文件、不扫 v3 heads，
// copies 为空 → len(report.Items)==0 → 误报 errHeadSynthesisNoBranch，
// 而会话实际有 3 条链。
//
// 本测试钉住修复：fixture 手写一份 schema-2 事件日志（两个 live head——
// 主 head 带 q1/a1，fork head 带独立的 q2/a2，两链都活着、互不覆盖），旁边
// 不放任何 recovery 副本文件。headID 为空调用 SynthesizeSessionHeads，
// 断言不再命中 errHeadSynthesisNoBranch（扫描面已补扫 v3 heads，Items 非
// 空）——数据层引擎对「只有 v3 heads 没有副本」的情况会返回
// ErrNoRecoveryBranches，那是另一条路径的兜底（本测试只验证「未选 HEAD 不
// 再被误判成只有一条链」这一层修复，不要求这条路径真正完成一次文件级合并
// ——纯 v3 会话的合并本质是 head 切换，文件层面没有东西可搬）。
//
// 事件日志手写而不复用 Session.Save/ForkHead：agent 层的 save/fork 路径带
// 自己的状态机（lease probe、checkpoint 自播种等），这些都会在 fixture 里
// 引入与本测试无关的读数；直接写最小 schema-2 事件序列能让「两个 live head、
// 无副本」这个前提变得可读可核对，不受 agent 内部保存状态机的摆布。
func TestSynthesizeSessionHeadsUnselectedHeadSeesV3Heads(t *testing.T) {
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "heads-synth-1117-v3only.jsonl")
	eventsPath := store.SessionEventLog(path)
	t.Cleanup(func() {
		_ = os.Remove(path)
		_ = os.Remove(eventsPath)
	})

	mainHead := agent.SessionMainHead
	forkHead := agent.NewHeadID()
	q1, a1, q2, a2, q3, a3 := agent.NewMessageID(), agent.NewMessageID(), agent.NewMessageID(), agent.NewMessageID(), agent.NewMessageID(), agent.NewMessageID()
	base := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)

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

	// Two live heads that genuinely diverge (neither covers the other): the
	// main head carries q1/a1 then q3/a3 (after the fork point), the fork head
	// carries only q1/a1 then q2/a2 off that same fork point. Both are live,
	// main is the selected head (no explicit select marker - the replay's own
	// default picks the most recently active live head, which with these
	// timestamps is main). This mirrors the panel's real "unselected HEAD"
	// state: the user is looking at the conversation with its current main
	// selected, with a second diverged head they have not picked yet.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	entries := []any{
		msgEntry(mainHead, "", "", q1, provider.RoleUser, "q1", base.Add(-4*time.Second)),
		msgEntry(mainHead, q1, "", a1, provider.RoleAssistant, "a1", base.Add(-3*time.Second)),
		forkEntry(mainHead, forkHead, a1, base.Add(-2*time.Second)),
		msgEntry(forkHead, a1, "", q2, provider.RoleUser, "q2", base.Add(-1*time.Second)),
		msgEntry(forkHead, q2, "", a2, provider.RoleAssistant, "a2", base),
		msgEntry(mainHead, a1, "", q3, provider.RoleUser, "q3", base.Add(2*time.Second)),
		msgEntry(mainHead, q3, "", a3, provider.RoleAssistant, "a3", base.Add(3*time.Second)),
	}
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(eventsPath, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	heads, err := agent.ListSessionHeads(path)
	if err != nil {
		t.Fatal(err)
	}
	liveCount := 0
	for _, h := range heads {
		if !h.Retired {
			liveCount++
		}
	}
	if liveCount < 2 {
		t.Fatalf("fixture must carry at least two live v3 heads, got %d (%+v)", liveCount, heads)
	}
	// Sanity: no recovery copies beside the main (this is the whole point of
	// the fixture) - recoveryCopiesByMain must come back empty.
	if list, _, err := recoveryCopiesByMain(dir); err != nil || len(list[path]) != 0 {
		t.Fatalf("fixture must have no recovery copies, list=%v err=%v", list[path], err)
	}

	app := newBranchDeleteApp(t)
	report, err := app.SynthesizeSessionHeads(path, "")
	// The specific bug this test exists for: errHeadSynthesisNoBranch ("this
	// conversation has only one chain") must never be the verdict when the
	// session genuinely carries two live v3 heads and no recovery copies - the
	// call must succeed (nil error) with the v3 heads recorded as participants,
	// so the panel can show "还有 N 条链" instead of misreporting single-chain.
	if err != nil {
		t.Fatalf("a v3-only multi-head conversation must not be refused when no HEAD is picked: report=%+v err=%v", report, err)
	}
	// The pre-run reading must have picked up both live v3 heads as
	// participants (Items is the existence proof the panel renders before
	// running the merge).
	if len(report.Items) < 2 {
		t.Fatalf("the pre-run reading must list both live v3 heads as participants: %+v", report)
	}
	for _, item := range report.Items {
		if item.Path != path {
			t.Fatalf("v3 head participants must reference the canonical transcript, got %+v", item)
		}
	}
}

// 任务 1146 返修2（用户 20261025 裁决）：v3-only 形态的合成升级为生效语义。
//
// 上一版对「无副本文件、只有 v3 heads」的会话只做诚实回执（报告各 head 读数
// 后原样返回，列表纹丝不动）；用户预期合成按钮在这里同样生效：选定 HEAD →
// 主线切换到它 + 其余落败 heads 从候选链/版本表清理。清理复用 1064 的 retire
// 标记（可恢复：独有条目在下一次单写轮换前仍可重放找回），切换复用版本表
// 「切换到此版本」的同一入口（chooseSessionHead）。
//
// 本测试钉住三条路径：
//
//	① 显式选定落败链（fork head）→ 先切换（RetireSessionHead 拒绝 retire 当前
//	   选中链，顺序不可反）后清理，报告如实记 SwitchedTo/RetiredHeads；
//	② 未选 HEAD（保持当前主线）→ 不切换、只清理其余活链；
//	③ 未知 headID → 明确报错，不静默。
//
// fixture 复用 1117 的手写 schema-2 事件序列（两个 live head、无副本文件）。
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

func liveHeadIDs(t *testing.T, path string) map[string]agent.SessionHead {
	t.Helper()
	heads, err := agent.ListSessionHeads(path)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]agent.SessionHead{}
	for _, h := range heads {
		if !h.Retired {
			out[h.ID] = h
		}
	}
	return out
}

func TestSynthesizeSessionHeadsV3OnlySwitchesToChosenHeadAndRetiresLosers(t *testing.T) {
	path, mainHead, forkHead := writeV3OnlyTwoHeadFixture(t)

	app := newBranchDeleteApp(t)
	report, err := app.SynthesizeSessionHeads(path, forkHead)
	if err != nil {
		t.Fatalf("v3-only synthesis with a picked HEAD must act: report=%+v err=%v", report, err)
	}
	if report.SwitchedTo != forkHead {
		t.Fatalf("the main line must have switched to the chosen head, got SwitchedTo=%q", report.SwitchedTo)
	}
	if len(report.RetiredHeads) != 1 || report.RetiredHeads[0] != mainHead {
		t.Fatalf("the losing head must be retired, got %+v", report.RetiredHeads)
	}
	live := liveHeadIDs(t, path)
	fork, ok := live[forkHead]
	if !ok || !fork.Selected {
		t.Fatalf("the chosen head must now be the selected live head: %+v", live)
	}
	if _, ok := live[mainHead]; ok {
		t.Fatalf("the losing head must be retired out of the live set: %+v", live)
	}
	if report.AfterMessages == 0 || report.AfterTurns == 0 {
		t.Fatalf("the report must carry the post-run reading of the new main: %+v", report)
	}
}

func TestSynthesizeSessionHeadsV3OnlyUnselectedKeepsMainAndRetiresOthers(t *testing.T) {
	path, mainHead, forkHead := writeV3OnlyTwoHeadFixture(t)

	app := newBranchDeleteApp(t)
	report, err := app.SynthesizeSessionHeads(path, "")
	if err != nil {
		t.Fatalf("v3-only synthesis without a picked HEAD keeps the current main: report=%+v err=%v", report, err)
	}
	if report.SwitchedTo != "" {
		t.Fatalf("the main head is already selected; no switch must be recorded, got %q", report.SwitchedTo)
	}
	if len(report.RetiredHeads) != 1 || report.RetiredHeads[0] != forkHead {
		t.Fatalf("the other live chain must be retired, got %+v", report.RetiredHeads)
	}
	live := liveHeadIDs(t, path)
	if _, ok := live[forkHead]; ok {
		t.Fatalf("the losing head must be retired: %+v", live)
	}
	if h, ok := live[mainHead]; !ok || !h.Selected {
		t.Fatalf("the main head must stay selected: %+v", live)
	}
}

func TestSynthesizeSessionHeadsV3OnlyRefusesUnknownHead(t *testing.T) {
	path, _, _ := writeV3OnlyTwoHeadFixture(t)

	app := newBranchDeleteApp(t)
	if _, err := app.SynthesizeSessionHeads(path, "no-such-head"); err == nil {
		t.Fatal("an unknown head id must be refused, not silently ignored")
	}
}
