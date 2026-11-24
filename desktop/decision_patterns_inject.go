package main

import (
	"fmt"
	"log/slog"
	"os"
	"regexp"
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
//
// tl1772 注入内容分层（A 方案）：开关 experimental_decision_patterns_layered
// 开启时注入只带索引——每条 DP 压成一行「编号 场景关键词→决策一句话」
// （≈1.5KB/18 条，省 85%+），块内硬指令要求处理裁决类/用户纠正类消息时先读
// 对应 DP 全文（固化文件路径随 config 注明）；解析不出任何索引行（格式漂移/
// 空条目）时静默回退全文形态，永不注入空块。开关关闭时与原全量注入逐字节
// 一致（回退路径）；B 方案（压缩后增量重注入）明确不做，注入时机维持修订
// 五③。分层开关每次注入现读 config（无 boot 快照依赖），拨动对下一条
// autopilot 消息即生效。

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
	cfg := config.LoadForEdit(config.UserConfigPath())
	if cfg == nil || !cfg.Agent.ExperimentalDecisionPatterns {
		return "", false
	}
	path := strings.TrimSpace(cfg.Agent.DecisionPatternFilePath)
	if path == "" {
		path = config.DefaultDecisionPatternFilePath()
	}
	if path == "" {
		return "", false
	}
	content := readDecisionPatternsContentFrom(path)
	if content == "" {
		return "", false
	}
	// tl1772 分层形态：开关开且文件能解析出至少一条索引行才走索引注入；解析
	// 不出（格式漂移/全部条目缺场景与决策段）静默回退全文形态——永不注入空块。
	if cfg.Agent.ExperimentalDecisionPatternsLayered {
		if indexLines := decisionPatternsIndexLines(content); len(indexLines) > 0 {
			return decisionPatternsLayeredBlockBody(path, indexLines), true
		}
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

// decisionPatternIDRe extracts the DP id from a固化条目标题（如
// "## DP-0001（2026-11-17 固化）" → "DP-0001"）。编号是索引行与全文之间的
// 唯一寻址锚，没有编号的标题无法被按需读取引用，直接跳过。
var decisionPatternIDRe = regexp.MustCompile(`DP-\d+`)

// decisionPatternsIndexLines flattens the solidified file into the tl1772
// index: one line per DP entry, "DP-NNNN 场景关键词→决策一句话". Only the
// task-801 four-part shape (## DP-… heading + "- 场景：" / "- 决策：" bullets)
// yields lines; anything else (header blockquote, the trailing meta comment,
// entries missing both bullets) is skipped. Returns nil when nothing parses —
// the caller then falls back to the full-content injection.
func decisionPatternsIndexLines(content string) []string {
	var lines []string
	id := ""
	scene := ""
	decision := ""
	flush := func() {
		if id != "" && (scene != "" || decision != "") {
			lines = append(lines, fmt.Sprintf("%s %s→%s",
				id,
				decisionPatternsIndexSnippet(scene, decisionPatternsIndexSceneRunes),
				decisionPatternsIndexSnippet(decision, decisionPatternsIndexDecisionRunes)))
		}
		id, scene, decision = "", "", ""
	}
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			flush()
			if m := decisionPatternIDRe.FindString(trimmed); m != "" {
				id = m
			}
			continue
		}
		if id == "" {
			continue
		}
		switch {
		case scene == "" && strings.HasPrefix(trimmed, "- 场景"):
			scene = decisionPatternsBulletText(trimmed, "- 场景")
		case decision == "" && strings.HasPrefix(trimmed, "- 决策"):
			decision = decisionPatternsBulletText(trimmed, "- 决策")
		}
	}
	flush()
	return lines
}

// decisionPatternsIndexSceneRunes / decisionPatternsIndexDecisionRunes cap the
// two index fields so an 18-entry file stays ≈1.5KB (≈90B/line): the index is
// a keyword hint pointing at the full text, not a replacement for it.
const (
	decisionPatternsIndexSceneRunes    = 10
	decisionPatternsIndexDecisionRunes = 16
)

// decisionPatternsBulletText strips the "- 场景" / "- 决策" bullet prefix and
// the following colon (full- or half-width) from a solidified-file line.
func decisionPatternsBulletText(trimmed, prefix string) string {
	rest := strings.TrimPrefix(trimmed, prefix)
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, "：") {
		rest = strings.TrimPrefix(rest, "：")
	} else if strings.HasPrefix(rest, ":") {
		rest = strings.TrimPrefix(rest, ":")
	}
	return strings.TrimSpace(rest)
}

// decisionPatternsIndexSnippet trims and rune-safely caps a field, appending
// an ellipsis when truncated (CJK-safe: []rune slicing, never byte slicing).
func decisionPatternsIndexSnippet(s string, maxRunes int) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes]) + "…"
}

// decisionPatternsLayeredBlockBody frames the index lines as a transient
// prefix with the hard instruction: ruling/user-correction messages must read
// the referenced DP full text from the solidified file before replying.
func decisionPatternsLayeredBlockBody(path string, indexLines []string) string {
	var b strings.Builder
	b.WriteString("<decision-patterns>\n")
	b.WriteString("以下是本工作区沉淀的决策模式索引（每行一条：编号 场景关键词→决策一句话）。硬性要求：处理裁决类或用户纠正类消息时，必须先用文件读取工具读取对应 DP 全文（含完整理由/来源）再回复——全文见 ")
	b.WriteString(path)
	b.WriteString("，按行内 DP 编号定位对应条目。若与本条消息的明确指示冲突，以用户本条消息为准。\n\n")
	for _, line := range indexLines {
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("</decision-patterns>\n\n---\n\n用户消息：\n")
	return b.String()
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
