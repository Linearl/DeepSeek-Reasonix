package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/memory"
)

// 任务 801 Phase 3（注入链）验收测试：
//   - 非 autopilot 会话零注入（代码路径级：decisionPatternsInjectBlock 首个
//     分支短路，pending 置位也拦——双保险）；
//   - autopilot 下注入恰好一次（pending 原子消费，第二条消息不再注入）；
//   - 文件不存在/为空静默跳过（零降级噪音），有内容出块且带原文；
//   - 压缩成功完成（Summary 非空）重新置位实现重注入；aborted pass（空
//     Summary）与非 autopilot 不置位；
//   - 开关关闭态（isolateDesktopUserDirs 默认配置）提交路径零行为且不阻断。

func TestDecisionPatternsInjectRequiresAutopilotAndPending(t *testing.T) {
	app := NewApp()
	tab := &WorkspaceTab{ID: "t1", Scope: "global"}

	// 非 autopilot：即便 pending 被意外置位也不注入（同源判定的双保险分支）。
	tab.decisionPatternsPending.Store(true)
	if block, ok := app.decisionPatternsInjectBlock(tab); ok || block != "" {
		t.Fatalf("non-autopilot tab must never inject, got ok=%v block=%q", ok, block)
	}
	// nil tab 安全。
	if _, ok := app.decisionPatternsInjectBlock(nil); ok {
		t.Fatal("nil tab must not inject")
	}

	// autopilot + pending：允许进入读文件步骤。用户目录被隔离且开关默认关，
	// 读到的内容为空 → 不注入，但 pending 已被消费。
	tab.autopilot = true
	if block, ok := app.decisionPatternsInjectBlock(tab); ok || block != "" {
		t.Fatalf("switch off must not inject, got ok=%v block=%q", ok, block)
	}
	if tab.decisionPatternsPending.Load() {
		t.Fatal("pending must be consumed even when the file read yields nothing (no retry churn)")
	}
	// pending 已清：第二条消息不再进入读步骤。
	if _, ok := app.decisionPatternsInjectBlock(tab); ok {
		t.Fatal("second submission must not inject without re-arm")
	}
}

func TestDecisionPatternsFileStatesSilent(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.md")
	// 不存在：静默空（零降级噪音）。
	if got := readDecisionPatternsContentFrom(missing); got != "" {
		t.Fatalf("missing file must resolve empty, got %q", got)
	}
	empty := filepath.Join(dir, "empty.md")
	if err := os.WriteFile(empty, []byte("   \n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readDecisionPatternsContentFrom(empty); got != "" {
		t.Fatalf("empty file must resolve empty, got %q", got)
	}
	filled := filepath.Join(dir, "filled.md")
	content := "## DP-0001（2026-10-12 固化）\n- 场景：x\n- 决策：y\n- 理由：z\n- 来源：w\n"
	if err := os.WriteFile(filled, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readDecisionPatternsContentFrom(filled); got != strings.TrimSpace(content) {
		t.Fatalf("filled file must come back trimmed, got %q", got)
	}
}

func TestDecisionPatternsBlockBodyFramesContent(t *testing.T) {
	body := decisionPatternsBlockBody("## DP-0001\n- 场景：s")
	for _, want := range []string{"<decision-patterns>", "## DP-0001\n- 场景：s", "用户消息："} {
		if !strings.Contains(body, want) {
			t.Fatalf("block body must contain %q, got:\n%s", want, body)
		}
	}
	if !strings.HasPrefix(body, "<decision-patterns>") || !strings.HasSuffix(body, "用户消息：\n") {
		t.Fatalf("block must frame the file content before the user message, got:\n%s", body)
	}
}

func TestDecisionPatternsRearmOnCompactionDone(t *testing.T) {
	app := NewApp()
	tab := &WorkspaceTab{ID: "t2", Scope: "global", autopilot: true}
	app.mu.Lock()
	app.tabs = map[string]*WorkspaceTab{"t2": tab}
	app.mu.Unlock()

	success := event.Event{Kind: event.CompactionDone, Compaction: event.Compaction{Trigger: "auto", Summary: "briefing"}}
	app.rearmDecisionPatternsForTab("t2", success)
	if !tab.decisionPatternsPending.Load() {
		t.Fatal("successful compaction must re-arm injection for an autopilot tab")
	}

	// aborted pass（空 Summary）：不重注入。
	tab.decisionPatternsPending.Store(false)
	aborted := event.Event{Kind: event.CompactionDone, Compaction: event.Compaction{Trigger: "auto"}}
	app.rearmDecisionPatternsForTab("t2", aborted)
	if tab.decisionPatternsPending.Load() {
		t.Fatal("aborted compaction must not re-arm (old context still intact)")
	}

	// 非 autopilot：不置位。
	tab.autopilot = false
	app.rearmDecisionPatternsForTab("t2", success)
	if tab.decisionPatternsPending.Load() {
		t.Fatal("non-autopilot tab must not re-arm")
	}
	tab.autopilot = true

	// 非 CompactionDone 事件：不置位。
	tab.decisionPatternsPending.Store(false)
	app.rearmDecisionPatternsForTab("t2", event.Event{Kind: event.TurnDone})
	if tab.decisionPatternsPending.Load() {
		t.Fatal("non-compaction events must not re-arm")
	}
	// 未知 tab：安全。
	app.rearmDecisionPatternsForTab("ghost", success)
}

// TestSubmitToTabSwitchOffInjectsNothing pins the off-state zero-behaviour on
// the real submission path (禁区：关闭态与现状一致；注入失败不阻断).
func TestSubmitToTabSwitchOffInjectsNothing(t *testing.T) {
	isolateDesktopUserDirs(t) // 实验开关默认关：整链短路
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "dp-inject-off.jsonl")
	mem := memory.Load(memory.Options{CWD: t.TempDir()})
	sess := agent.NewSession("sys")
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	runner := &appendingDesktopRunner{session: sess, started: make(chan string, 1)}
	ctrl := control.New(control.Options{
		Runner: runner, Executor: exec, Sink: event.Discard, SessionDir: dir,
		SessionPath: path, Label: "test", Memory: mem,
	})
	defer ctrl.Close()

	app := NewApp()
	app.setTestCtrl(ctrl, "deepseek/test")
	app.mu.RLock()
	tab := app.tabs["test"]
	app.mu.RUnlock()
	tab.autopilot = true
	tab.decisionPatternsPending.Store(true) // 即便意外置位，开关关也必须零注入

	const prompt = "继续推进任务"
	app.SubmitToTab("test", prompt)
	composed := <-runner.started
	waitNotRunning(t, ctrl)
	if strings.Contains(composed, "decision-patterns") {
		t.Fatalf("switch-off must inject nothing, got %q", composed)
	}
	if !strings.Contains(composed, prompt) {
		t.Fatalf("user input must reach the model untouched, got %q", composed)
	}
	if tab.decisionPatternsPending.Load() {
		t.Fatal("pending consumed by the submission even in the off state (no retry churn)")
	}
}

// TestSubmitToTabAutopilotInjectsDecisionPatternsOnce pins the happy path on
// the real submission chain: switch on + file content + autopilot arm → the
// next message carries the block exactly once, the user's words stay in the
// display record, and the following message is clean (修订五③注入时机).
func TestSubmitToTabAutopilotInjectsDecisionPatternsOnce(t *testing.T) {
	home := isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dpFile := filepath.Join(home, "dp.md")
	const patterns = "## DP-0001（2026-10-12 固化）\n- 场景：上游新增机制\n- 决策：不自动跟随\n- 理由：判据是我方需求\n- 来源：会话 x\n"
	if err := os.WriteFile(dpFile, []byte(patterns), 0o644); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	if err := app.SetExperimentalDecisionPatterns(true); err != nil {
		t.Fatal(err)
	}
	if err := app.SetDecisionPatternFilePath(dpFile); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(config.SessionDir(), "dp-inject-on.jsonl")
	mem := memory.Load(memory.Options{CWD: t.TempDir()})
	sess := agent.NewSession("sys")
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	runner := &appendingDesktopRunner{session: sess, started: make(chan string, 2)}
	ctrl := control.New(control.Options{
		Runner: runner, Executor: exec, Sink: event.Discard, SessionDir: dir,
		SessionPath: path, Label: "test", Memory: mem,
	})
	defer ctrl.Close()

	app.setTestCtrl(ctrl, "deepseek/test")
	app.mu.RLock()
	tab := app.tabs["test"]
	app.mu.RUnlock()
	tab.autopilot = true
	tab.decisionPatternsPending.Store(true)

	app.SubmitToTab("test", "继续推进")
	composed := <-runner.started
	waitNotRunning(t, ctrl)
	if !strings.Contains(composed, "<decision-patterns>") || !strings.Contains(composed, "DP-0001") {
		t.Fatalf("armed autopilot submission must carry the decision-patterns block, got %q", composed)
	}
	if !strings.Contains(composed, "用户消息：\n继续推进") {
		t.Fatalf("user words must follow the block, got %q", composed)
	}
	if tab.decisionPatternsPending.Load() {
		t.Fatal("pending must be consumed by the first injection")
	}

	// 第二条消息：未重置位 → 零注入（幂等窗口外不再携带）。
	app.SubmitToTab("test", "第二条")
	composed2 := <-runner.started
	waitNotRunning(t, ctrl)
	if strings.Contains(composed2, "<decision-patterns>") {
		t.Fatalf("un-armed submission must not carry the block, got %q", composed2)
	}
	if !strings.Contains(composed2, "第二条") {
		t.Fatalf("second user input must reach the model untouched, got %q", composed2)
	}
}

// writeDecisionPatternsFixture writes a task-801 four-part fixture with two
// entries plus the file header/blockquote/meta-comment noise, mirroring the
// real global-workspace/docs/decision-patterns.md shape.
func writeDecisionPatternsFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "decision-patterns.md")
	const content = `# 决策模式（decision patterns）

> 本文件由任务 801 固化链维护，autopilot 会话启动时自动注入。

## DP-0001（2026-11-17 固化）
- 场景：回合开头出现宿主注入的恢复/中断提示（如 The app updated and restarted itself）
- 决策：当作环境状态说明处理，从中断点续做；不重述计划、不等待输入
- 理由：这类文本是宿主对已完成动作的通报，不是新指令
- 来源：decision-the-app-updated-and-a74554

## DP-0002（2026-11-18 固化）
- 场景：用户说调查/查一下/看看某现象
- 决策：交付物是评估报告，报告完即停；不顺手改代码，除非用户显式说修
- 理由：用户原话：如果要修，我会显式指出的
- 来源：用户沟通习惯-说调查就是纯调查
<!-- decision-patterns-meta {"lastSolidifiedAt":"2026-11-21T15:40:53Z","solidifiedCount":2} -->
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestDecisionPatternsIndexLinesParsing pins the tl1772 flattening: one line
// per DP entry, DP id extracted from the heading, scene→decision bullets
// picked, rune-safe truncation, and header/meta noise skipped.
func TestDecisionPatternsIndexLinesParsing(t *testing.T) {
	raw, err := os.ReadFile(writeDecisionPatternsFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	lines := decisionPatternsIndexLines(strings.TrimSpace(string(raw)))
	if len(lines) != 2 {
		t.Fatalf("fixture must flatten to exactly 2 index lines, got %d: %q", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], "DP-0001 ") || !strings.Contains(lines[0], "→") {
		t.Fatalf("index line must carry the DP id and the scene→decision arrow, got %q", lines[0])
	}
	// 决策段按 16 rune 截断（"当作环境状态说明处理，从中断点续" + "…"）——
	// 索引行只保留关键词，全文按需读。
	if !strings.Contains(lines[0], "DP-0001") || !strings.Contains(lines[0], "当作环境状态说明处理，从中断点续…") {
		t.Fatalf("index line must keep the truncated decision gist, got %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "DP-0002 ") || !strings.Contains(lines[1], "评估报告") {
		t.Fatalf("second entry must flatten in order, got %q", lines[1])
	}
	for _, line := range lines {
		if strings.Contains(line, "理由：") || strings.Contains(line, "来源：") || strings.Contains(line, "## ") {
			t.Fatalf("index line must not leak full-text sections, got %q", line)
		}
	}
	// 截断为 rune 安全且带省略号：场景行按 10 rune 截断（"这个场景远超过十个字"+…）。
	if got := decisionPatternsIndexSnippet("这个场景远超过十个字符会被截断处理", 10); got != "这个场景远超过十个字…" {
		t.Fatalf("snippet truncation must cap at the rune budget with an ellipsis, got %q", got)
	}
	if got := decisionPatternsIndexSnippet("短", 10); got != "短" {
		t.Fatalf("short snippet must pass through untouched, got %q", got)
	}

	// 无 DP 编号的格式漂移：解析不出任何索引行 → 空切片（调用方回退全文）。
	if got := decisionPatternsIndexLines("# 没有固化条目\n只有正文"); len(got) != 0 {
		t.Fatalf("non-DP content must flatten to nothing, got %q", got)
	}
	empty := filepath.Join(t.TempDir(), "empty-shape.md")
	if err := os.WriteFile(empty, []byte("## DP-0001\n没有场景与决策段的条目\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(empty); err != nil {
		t.Fatal(err)
	} else if got := decisionPatternsIndexLines(strings.TrimSpace(string(raw))); len(got) != 0 {
		t.Fatalf("entries without scene/decision bullets must be skipped, got %q", got)
	}
}

// TestDecisionPatternsLayeredBlockBodyFramesIndex pins the layered block:
// hard instruction + resolved file path + index lines, same transient framing
// (prefix/suffix) as the legacy full-content block.
func TestDecisionPatternsLayeredBlockBodyFramesIndex(t *testing.T) {
	body := decisionPatternsLayeredBlockBody("/dp/decision-patterns.md", []string{"DP-0001 场景关键词→决策一句话"})
	if !strings.HasPrefix(body, "<decision-patterns>\n") || !strings.HasSuffix(body, "</decision-patterns>\n\n---\n\n用户消息：\n") {
		t.Fatalf("layered block must keep the transient framing, got:\n%s", body)
	}
	for _, want := range []string{
		"处理裁决类或用户纠正类消息时，必须先用文件读取工具读取对应 DP 全文",
		"/dp/decision-patterns.md",
		"按行内 DP 编号定位",
		"以用户本条消息为准",
		"DP-0001 场景关键词→决策一句话",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("layered block must contain %q, got:\n%s", want, body)
		}
	}
}

// armDecisionPatternsForTest isolates user dirs, points the solidified file at
// the fixture and arms the injection for one fresh autopilot tab.
func armDecisionPatternsForTest(t *testing.T, path string, layered bool) (*App, *WorkspaceTab) {
	t.Helper()
	isolateDesktopUserDirs(t)
	app := NewApp()
	if err := app.SetExperimentalDecisionPatterns(true); err != nil {
		t.Fatal(err)
	}
	if err := app.SetExperimentalDecisionPatternsLayered(layered); err != nil {
		t.Fatal(err)
	}
	if err := app.SetDecisionPatternFilePath(path); err != nil {
		t.Fatal(err)
	}
	tab := &WorkspaceTab{ID: "t-l1772", Scope: "global", autopilot: true}
	tab.decisionPatternsPending.Store(true)
	return app, tab
}

// TestDecisionPatternsLayeredSwitchOffByteIdentical pins the rollback path:
// layered off resolves to EXACTLY the legacy full-content block (byte-for-byte
// with the pre-tl1772 shape), not merely "contains the same text".
func TestDecisionPatternsLayeredSwitchOffByteIdentical(t *testing.T) {
	raw, err := os.ReadFile(writeDecisionPatternsFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	app, tab := armDecisionPatternsForTest(t, writeDecisionPatternsFixture(t), false)
	block, ok := app.decisionPatternsInjectBlock(tab)
	if !ok {
		t.Fatal("armed autopilot tab must inject with the switch on")
	}
	if want := decisionPatternsBlockBody(strings.TrimSpace(string(raw))); block != want {
		t.Fatalf("layered-off block must be byte-identical to the legacy full-content block\ngot:  %q\nwant: %q", block, want)
	}
	if tab.decisionPatternsPending.Load() {
		t.Fatal("pending must be consumed by the injection")
	}
}

// TestDecisionPatternsLayeredSwitchOnInjectsIndex pins the layered form end to
// end on decisionPatternsInjectBlock: index lines + hard instruction ride the
// block, full-text-only sections (理由/来源 bullets) do not.
func TestDecisionPatternsLayeredSwitchOnInjectsIndex(t *testing.T) {
	app, tab := armDecisionPatternsForTest(t, writeDecisionPatternsFixture(t), true)
	block, ok := app.decisionPatternsInjectBlock(tab)
	if !ok {
		t.Fatal("armed autopilot tab must inject with the layered switch on")
	}
	for _, want := range []string{"DP-0001 ", "DP-0002 ", "→", "必须先用文件读取工具读取对应 DP 全文", "decision-patterns.md"} {
		if !strings.Contains(block, want) {
			t.Fatalf("layered block must carry %q, got:\n%s", want, block)
		}
	}
	if strings.Contains(block, "理由：") || strings.Contains(block, "来源：decision-the-app-updated") {
		t.Fatalf("layered block must not carry the full 理由/来源 text, got:\n%s", block)
	}
	if len(block) >= 4096 {
		t.Fatalf("layered block must stay lean (≈2KB budget), got %d bytes", len(block))
	}

	// 格式漂移回退：解析不出索引行 → 静默回退全文形态（永不注入空块）。
	fallbackPath := filepath.Join(t.TempDir(), "drifted.md")
	const drifted = "# 决策模式\n\n正文说明，没有固化条目。\n"
	if err := os.WriteFile(fallbackPath, []byte(drifted), 0o644); err != nil {
		t.Fatal(err)
	}
	app2, tab2 := armDecisionPatternsForTest(t, fallbackPath, true)
	block2, ok2 := app2.decisionPatternsInjectBlock(tab2)
	if !ok2 {
		t.Fatal("drifted file must still inject (full-content fallback)")
	}
	if want := decisionPatternsBlockBody(strings.TrimSpace(drifted)); block2 != want {
		t.Fatalf("unparseable file must fall back to the legacy block byte-for-byte\ngot:  %q\nwant: %q", block2, want)
	}
}
