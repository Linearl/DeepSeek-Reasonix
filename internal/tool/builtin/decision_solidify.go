package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"reasonix/internal/fileutil"
	"reasonix/internal/tool"
)

// 决策模式固化工具（任务 801 Phase 2）：心跳整合链的执行面。心跳复用对话里的
// 模型经本工具完成「增量检查 → 归纳汇报 → 批准后固化写入」：
//   - action=list：读「上次固化时刻之后」的决策记忆条目，报告数量与阈值判定。
//     未达标时心跳对话直接结束（轻量零成本）；达标才归纳。
//   - action=write：把人工批准（或 auto_approve 显式开启）的模式条目按修订五的
//     四段式结构追加进固化文件；写入前把当前版本推入 .bak-1~.bak-5 备份轮转，
//     并更新文件头部的固化游标注释（机器增量定位的唯一事实源）。
//
// 文件位置可指定（修订七）：boot 注入 filePath（默认
// <reasonix>/global-workspace/docs/decision-patterns.md），备份与游标随文件同目录。
// 工具不 import memory（会与 tool 成环）：数据源经 DecisionSolidifySource 窄接口
// 由 boot 注入，boot 用 memory.Store 适配。

// DecisionSolidifyItem 是固化工具看到的单条决策记忆（与 memory.Memory 解耦的最小面）。
type DecisionSolidifyItem struct {
	Name      string    `json:"name"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DecisionSolidifySource 是「增量决策记忆」读取接口。实现必须只读：固化链
// 从不删除或改写源记忆（禁区条款）。
type DecisionSolidifySource interface {
	// DecisionMemoriesSince 返回更新时刻晚于 since 的决策记忆条目（按更新时间升序）。
	DecisionMemoriesSince(since time.Time) []DecisionSolidifyItem
}

// decisionSolidifyMetaMark 是固化文件头部的机器游标注释行：HTML 注释对人不可见，
// 固化链可解析。唯一事实源，杜绝文件头与旁挂 json 双源漂移。
const decisionSolidifyMetaMark = "<!-- decision-patterns-meta "

// decisionSolidifyBackups 是备份轮转链（修订六：保留最近 5 个版本）。
var decisionSolidifyBackups = []string{"", ".bak-1", ".bak-2", ".bak-3", ".bak-4", ".bak-5"}

// NewDecisionSolidifyTool 构造固化工具。filePath 为空、source 为 nil 时工具
// 报「不可用」而不是静默丢弃（remember 工具同款约定）。autoApprove 来自
// decision_pattern_auto_approve 配置：true 时 list 报告允许免批准直写
// （修订六：默认关，关闭时心跳必须等用户批准）。
func NewDecisionSolidifyTool(source DecisionSolidifySource, filePath string, threshold int, autoApprove bool) tool.Tool {
	return decisionSolidifyTool{source: source, filePath: filePath, threshold: threshold, autoApprove: autoApprove}
}

type decisionSolidifyTool struct {
	source      DecisionSolidifySource
	filePath    string
	threshold   int
	autoApprove bool
}

func (decisionSolidifyTool) Name() string { return "decision_pattern_solidify" }

func (decisionSolidifyTool) Description() string {
	return "Decision-pattern solidification for the heartbeat curation chain (task 801). " +
		"action=\"list\" reports decision memories saved since the last solidification, with the configured threshold and whether it is met — run this first each heartbeat; when the threshold is not met, summarize in one line and end the run. " +
		"action=\"write\" appends human-approved pattern entries to the decision-patterns file as structured four-field entries (场景→决策→理由→来源), rotating .bak-1..bak-5 backups first. " +
		"Default posture: report the patterns in the conversation and wait for explicit user approval before write; write without approval only when the decision_pattern_auto_approve lab option is on. " +
		"The tool never deletes or rewrites source memories."
}

func (decisionSolidifyTool) Schema() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"action": {"type": "string", "enum": ["list", "write"], "description": "list = incremental check (run first, cheap, ends the run below threshold); write = solidify approved patterns."},
		"patterns": {"type": "array", "description": "write only: the approved patterns in conversation order.", "items": {
			"type": "object",
			"properties": {
				"scene": {"type": "string", "description": "场景：什么时候适用（一句话）。"},
				"decision": {"type": "string", "description": "决策：该怎么做（一句话）。"},
				"reason": {"type": "string", "description": "理由：为什么（可引用用户原话归纳）。"},
				"source": {"type": "string", "description": "来源：哪次会话/哪些记忆条目，保证可追溯。"}
			},
			"required": ["scene", "decision", "reason", "source"]
		}}
	},
	"required": ["action"]
}`)
}

func (decisionSolidifyTool) ReadOnly() bool { return false }

func (t decisionSolidifyTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if t.source == nil || strings.TrimSpace(t.filePath) == "" {
		return "", fmt.Errorf("decision-pattern solidification is unavailable: boot did not wire a memory source or file path (lab switch experimental_decision_patterns is likely off)")
	}
	var in struct {
		Action   string `json:"action"`
		Patterns []struct {
			Scene    string `json:"scene"`
			Decision string `json:"decision"`
			Reason   string `json:"reason"`
			Source   string `json:"source"`
		} `json:"patterns"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	switch strings.TrimSpace(in.Action) {
	case "list":
		return t.list()
	case "write":
		return t.write(in.Patterns)
	default:
		return "", fmt.Errorf("action must be \"list\" or \"write\"")
	}
}

// decisionPatternMeta 是文件头游标注释的载荷。
type decisionPatternMeta struct {
	LastSolidifiedAt string `json:"lastSolidifiedAt"`
	SolidifiedCount  int    `json:"solidifiedCount"`
}

func (t decisionSolidifyTool) list() (string, error) {
	meta, err := t.readMeta()
	if err != nil {
		return "", err
	}
	since := time.Time{}
	if meta.LastSolidifiedAt != "" {
		if parsed, perr := time.Parse(time.RFC3339, meta.LastSolidifiedAt); perr == nil {
			since = parsed
		}
	}
	items := t.source.DecisionMemoriesSince(since)
	threshold := t.threshold
	if threshold <= 0 {
		threshold = 5
	}
	out, err := json.MarshalIndent(map[string]any{
		"filePath":         t.filePath,
		"lastSolidifiedAt": meta.LastSolidifiedAt,
		"solidifiedCount":  meta.SolidifiedCount,
		"pendingCount":     len(items),
		"threshold":        threshold,
		"thresholdMet":     len(items) >= threshold,
		"autoApprove":      t.autoApprove,
		"items":            items,
	}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (t decisionSolidifyTool) write(patterns []struct {
	Scene    string `json:"scene"`
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
	Source   string `json:"source"`
}) (string, error) {
	if len(patterns) == 0 {
		return "", fmt.Errorf("patterns is required for action=write; nothing to solidify")
	}
	for i, p := range patterns {
		if strings.TrimSpace(p.Scene) == "" || strings.TrimSpace(p.Decision) == "" {
			return "", fmt.Errorf("patterns[%d]: scene and decision are required", i)
		}
	}
	// 目录可能尚不存在（默认路径的 docs/ 首次写入）；先建目录再轮转。
	if err := decisionPatternDir(t.filePath); err != nil {
		return "", fmt.Errorf("prepare decision patterns dir: %w", err)
	}
	// 关键顺序：先把当前文件的全部信息读出来（游标、编号、正文），再轮转备份
	// （轮转会把当前文件改名成 .bak-1，之后的读取都会落空）。
	body, err := os.ReadFile(t.filePath)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read decision patterns: %w", err)
	}
	meta := parseDecisionMetaLine(string(body))
	next := maxDecisionPatternNumberIn(string(body)) + 1
	if err := rotateDecisionBackups(t.filePath); err != nil {
		return "", fmt.Errorf("backup rotation: %w", err)
	}
	var b strings.Builder
	if len(strings.TrimSpace(string(body))) == 0 {
		b.WriteString("# 决策模式（decision patterns）\n\n")
		b.WriteString("> 本文件由任务 801 固化链维护，autopilot 会话启动时自动注入。条目按「场景→决策→理由→来源」四段式组织；每次写入前自动轮转 .bak-1~.bak-5 备份，可直接人工编辑，下次注入生效。\n\n")
	}
	now := time.Now().UTC()
	for _, p := range patterns {
		fmt.Fprintf(&b, "\n## DP-%04d（%s 固化）\n", next, now.Format("2006-01-02"))
		fmt.Fprintf(&b, "- 场景：%s\n", strings.TrimSpace(p.Scene))
		fmt.Fprintf(&b, "- 决策：%s\n", strings.TrimSpace(p.Decision))
		fmt.Fprintf(&b, "- 理由：%s\n", strings.TrimSpace(p.Reason))
		fmt.Fprintf(&b, "- 来源：%s\n", strings.TrimSpace(p.Source))
		next++
	}
	updated := decisionPatternMeta{
		LastSolidifiedAt: now.Format(time.RFC3339),
		SolidifiedCount:  meta.SolidifiedCount + len(patterns),
	}
	metaLine := decisionSolidifyMetaMark + string(marshalMetaJSON(updated)) + " -->"
	content := stripDecisionMetaLine(string(body)) + b.String() + "\n" + metaLine + "\n"
	if err := fileutil.AtomicWriteFile(t.filePath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write decision patterns: %w", err)
	}
	return fmt.Sprintf("Solidified %d pattern(s) as DP-%04d..DP-%04d into %s (previous version rotated to .bak-1; lastSolidifiedAt=%s). Source memories were left untouched.",
		len(patterns), next-len(patterns), next-1, t.filePath, updated.LastSolidifiedAt), nil
}

// readMeta 解析固化文件头游标；文件不存在/无注释行 = 零值游标（首次固化全量
// 扫描）。
func (t decisionSolidifyTool) readMeta() (decisionPatternMeta, error) {
	raw, err := os.ReadFile(t.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return decisionPatternMeta{}, nil
		}
		return decisionPatternMeta{}, err
	}
	return parseDecisionMetaLine(string(raw)), nil
}

// parseDecisionMetaLine 从文件内容解析游标注释；损坏的游标按零值处理（多扫
// 一轮，不失败）。
func parseDecisionMetaLine(content string) decisionPatternMeta {
	meta := decisionPatternMeta{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, decisionSolidifyMetaMark) {
			continue
		}
		payload := strings.TrimSuffix(strings.TrimPrefix(line, decisionSolidifyMetaMark), "-->")
		if json.Unmarshal([]byte(strings.TrimSpace(payload)), &meta) != nil {
			return decisionPatternMeta{}
		}
		break
	}
	return meta
}

// rotateDecisionBackups 把当前文件推入 .bak-1~.bak-5 轮转链：bak-4→bak-5、…、
// 当前→bak-1。链上无文件或当前文件不存在都是正常态（首次写入无备份可推）。
func rotateDecisionBackups(path string) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for i := len(decisionSolidifyBackups) - 1; i > 0; i-- {
		dst := path + decisionSolidifyBackups[i]
		if i == 1 {
			continue // bak-1 由当前文件填充，最后处理
		}
		src := path + decisionSolidifyBackups[i-1]
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := os.Rename(src, dst); err != nil {
			return err
		}
	}
	return os.Rename(path, path+".bak-1")
}

// maxDecisionPatternNumberIn 扫描内容中现有 DP- 标题取最大编号，保证追加不重号。
func maxDecisionPatternNumberIn(content string) int {
	re := regexp.MustCompile(`(?m)^## DP-(\d{4,})`)
	max := 0
	for _, m := range re.FindAllStringSubmatch(content, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n > max {
			max = n
		}
	}
	return max
}

// stripDecisionMetaLine 移除旧游标注释行（含其换行），写入前由新行替代。
func stripDecisionMetaLine(content string) string {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), decisionSolidifyMetaMark) {
			continue
		}
		out = append(out, line)
	}
	// 去掉条目区与游标行之间残留的空行尾巴，保持文件整洁。
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

func marshalMetaJSON(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return raw
}

// decisionPatternDir 确保固化文件目录存在（boot 默认路径可能尚无 docs 目录）。
func decisionPatternDir(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o755)
}
