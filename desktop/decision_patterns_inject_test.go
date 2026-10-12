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
