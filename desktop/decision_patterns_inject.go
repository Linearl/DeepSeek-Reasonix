package main

import (
	"log/slog"
	"os"
	"strings"

	"reasonix/internal/config"
	"reasonix/internal/event"
)

// 任务 801 Phase 3（注入链）：把固化决策模式文件注入 autopilot 会话。
//
// 同源判定（设计修订四）：注入挂在「进入 autopilot」的同一信号 tab.autopilot
// 上——切档分支置位 pending，desktop 提交汇聚点 submitToTabResult 消费；非
// autopilot 会话在代码路径级零注入（decisionPatternsInjectBlock 首个分支）。
//
// 注入时机（设计修订五③）：切到 autopilot 后的下一条消息（下一轮 turn 组装）
// 注入一次；压缩成功完成后重新置位，下一条消息复用同一条提交路径重注入。
// 注入幂等——重复注入无害，极端竞态下多/少一次可接受。
//
// 注入方式沿用 memory compose 的既有模式：controller 组装面拿到的 input 前
// 置 transient 内容，display 保持用户原文（StripComposePrefixes 家族先例）。
// 不碰系统 prompt 组装面——boot golden 捕获面零触碰。
//
// 禁区条款：注入失败只 warning 不阻断提交；文件不存在/为空静默跳过（零降级
// 噪音）；experimental_decision_patterns 关闭时整链短路。

// decisionPatternsInjectBlock returns the prefix to prepend to the model input
// for this submission, or ok=false when nothing should be injected. The pending
// flag is consumed atomically so exactly one submission per arm carries the
// block; a failed read still consumes the arm (no retry churn, no spam).
func (a *App) decisionPatternsInjectBlock(tab *WorkspaceTab) (string, bool) {
	if tab == nil || !tab.autopilot {
		// 非 autopilot 零注入：切档分支之外该标记永不置位，此处双保险短路。
		return "", false
	}
	if !tab.decisionPatternsPending.CompareAndSwap(true, false) {
		return "", false
	}
	content := readDecisionPatternsContent()
	if content == "" {
		return "", false
	}
	return decisionPatternsBlockBody(content), true
}

// rearmDecisionPatternsForTab re-arms the injection after a successful
// compaction pass (修订五③：压缩后重注入，复用切换注入的同一条提交路径).
// Aborted passes (empty Summary) leave the flag alone — the old context was
// never folded, so the previously injected block is still in history.
func (a *App) rearmDecisionPatternsForTab(tabID string, e event.Event) {
	if a == nil || e.Kind != event.CompactionDone || strings.TrimSpace(e.Compaction.Summary) == "" {
		return
	}
	tab, _ := a.tabAndCtrlByID(tabID)
	if tab == nil || !tab.autopilot {
		return
	}
	tab.decisionPatternsPending.Store(true)
}

// readDecisionPatternsContent loads the solidified decision-patterns file per
// the lab config (修订七：文件位置可指定，空=默认 global-workspace/docs 路径).
// Switch off resolves to "" silently.
func readDecisionPatternsContent() string {
	cfg := config.LoadForEdit(config.UserConfigPath())
	if cfg == nil || !cfg.Agent.ExperimentalDecisionPatterns {
		return ""
	}
	path := strings.TrimSpace(cfg.Agent.DecisionPatternFilePath)
	if path == "" {
		path = config.DefaultDecisionPatternFilePath()
	}
	if path == "" {
		return ""
	}
	return readDecisionPatternsContentFrom(path)
}

// readDecisionPatternsContentFrom reads and trims the file: a missing or empty
// file resolves to "" silently; an unreadable file warns (once per arm — the
// pending flag was already consumed) and degrades to no injection — never
// blocks the submission.
func readDecisionPatternsContentFrom(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("desktop: decision-patterns file unreadable; skipping injection (task 801)", "path", path, "err", err)
		}
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// decisionPatternsBlockBody frames the file content as a transient prefix so
// the model can tell the reference block apart from the user's own words.
func decisionPatternsBlockBody(content string) string {
	var b strings.Builder
	b.WriteString("<decision-patterns>\n")
	b.WriteString("以下是本工作区沉淀的决策模式（用户确认过的决策边界），处理本条消息时参考遵守；若与本条消息的明确指示冲突，以用户本条消息为准。\n\n")
	b.WriteString(content)
	b.WriteString("\n</decision-patterns>\n\n---\n\n用户消息：\n")
	return b.String()
}
