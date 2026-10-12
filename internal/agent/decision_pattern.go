package agent

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/memory"
)

// 任务 801 决策模式自动提取（MVP，Phase 1）：压缩事件触发 + 最低 turn 间隔兜底，
// 对当前会话 transcript 做裁决轮次识别（E1 启发式），增量收集（E3，上次提取时刻
// 之后的用户轮次），把命中的裁决轮次写成**正常记忆条目**（type=feedback，
// activation=relevant，scope=project）。不删除、不改写任何既有记忆；提取全程
// 不向调用方返回错误——失败只记日志，绝不阻断压缩或 turn 主流程（禁区条款）。
//
// 开关默认关：ExperimentalDecisionPatterns=false 时 maybeExtractDecisionPatterns
// 在第一个分支返回，不读盘不写盘，行为与引入前逐字节一致。

const (
	// decisionExtractStateFile 是「上次提取位置」的落点（E3 增量游标），
	// 放在 project memory 目录下。记忆索引/加载路径只认 *.md，这个 .json
	// 状态文件不会被当作记忆条目拾取。
	decisionExtractStateFile = "decision-extract-state.json"
	// decisionExtractMinTurnsDefault 是最低 turn 间隔兜底（C 混合触发的兜底腿）：
	// 两次提取之间至少间隔这么多条用户消息（含未触发压缩的长会话）。
	decisionExtractMinTurnsDefault = 50
	// decisionExtractTextMinLen/MaxLen 过滤明显不是「用户裁决」的输入：
	// 过短（一个词）与超长（粘贴的代码块/日志）都不构成决策信号。dream 同款口径。
	decisionExtractTextMinLen = 8
	decisionExtractTextMaxLen = 400
)

// decisionRulingMarkers 是 E1 裁决轮次词表：用户否定/修正 AI 行为的措辞。
// 匹配规则 = 小写化后 contains。信噪比靠两条硬过滤兜底：只收命中词表的轮次
// （非裁决轮次零产出），只收长度合规的纯文本输入。
var decisionRulingMarkers = []string{
	// 中文（用户以中文为主）
	"不对", "错了", "搞错了", "理解错了", "方向错了", "别这样", "不要这样",
	"不是这样", "不是的", "我说的是", "我说过", "重做", "重来", "重新做",
	"撤销", "回滚", "回退", "纠正一下", "更正一下",
	// 英文（跨语言会话兜底）
	"no,", "wrong", "don't", "do not ", "revert", "undo ", "i said", "i meant",
	"stop doing", "that's not", "that is not",
}

// DecisionExtractOptions 是任务 801 提取链的 agent 侧配置。boot 装配；零值 =
// 关闭（Enabled=false），此时 agent 行为与引入前完全一致。
type DecisionExtractOptions struct {
	// Enabled 来自实验室开关 experimental_decision_patterns（默认关）。
	Enabled bool
	// Memory 是写入目标的记忆仓库（与 remember/dream 同一 Store）。零 Store
	// 视为不可用，提取静默跳过。
	Memory memory.Store
	// MinTurns 是最低 turn 间隔兜底阈值；<=0 用 decisionExtractMinTurnsDefault。
	MinTurns int
}

// decisionExtractState 是增量游标的落盘格式：每个会话文件记录上次提取运行时刻。
// 游标语义 = 「该时刻之后写入的记录都是新的」——提取在压缩完成后运行，此刻
// 之后产生的记录时间戳必然晚于上次运行时刻，边界安全。
type decisionExtractState struct {
	Version int               `json:"version"`
	Files   map[string]string `json:"files"` // transcript 文件名 -> RFC3339
}

// decisionExtractTurns 记录自上次提取以来的用户消息条数（turn 兜底腿的计数）。
// decisionExtractRunning 用 TryLock 语义防并发重复提取（压缩触发与兜底触发
// 可能几乎同时到达）。

// maybeExtractDecisionPatterns 是提取触发统一入口。compaction=true 表示由压缩
// 成功落地触发（C 混合的主腿）；false 表示由 turn 计数兜底触发。任何路径下
// 都不返回错误、不 panic 外泄。
func (a *Agent) maybeExtractDecisionPatterns(compaction bool) {
	cfg := a.decisionExtract
	if a == nil || !cfg.Enabled || cfg.Memory.Dir == "" && cfg.Memory.GlobalDir == "" {
		return
	}
	// turn 兜底腿：未达最低间隔且非压缩事件 → 只计数不提取。
	a.decisionMu.Lock()
	a.decisionTurns++
	minTurns := cfg.MinTurns
	if minTurns <= 0 {
		minTurns = decisionExtractMinTurnsDefault
	}
	due := compaction || a.decisionTurns >= minTurns
	if !due {
		a.decisionMu.Unlock()
		return
	}
	if a.decisionRunning {
		a.decisionMu.Unlock()
		return
	}
	a.decisionRunning = true
	a.decisionTurns = 0
	a.decisionMu.Unlock()

	// 异步执行：压缩关键路径上只花一次锁竞争的成本，扫描/写盘全部让出。
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Warn("decision extract panicked (suppressed)", "panic", r)
			}
			a.decisionMu.Lock()
			a.decisionRunning = false
			a.decisionMu.Unlock()
		}()
		if err := a.extractDecisionPatterns(); err != nil {
			slog.Warn("decision extract skipped", "err", err)
		}
	}()
}

// extractDecisionPatterns 执行一次增量提取。返回 error 仅用于日志（调用方
// 已保证不阻断主流程）。
func (a *Agent) extractDecisionPatterns() error {
	transcript := strings.TrimSpace(a.SessionPath())
	if transcript == "" {
		return fmt.Errorf("no transcript path bound")
	}
	if _, err := os.Stat(transcript); err != nil {
		return fmt.Errorf("transcript unreadable: %w", err)
	}
	cfg := a.decisionExtract
	stateDir := cfg.Memory.Dir
	if stateDir == "" {
		stateDir = cfg.Memory.GlobalDir
	}
	statePath := filepath.Join(stateDir, decisionExtractStateFile)
	state := readDecisionExtractState(statePath)

	base := filepath.Base(transcript)
	cutoff := time.Time{}
	if prev, ok := state.Files[base]; ok {
		if t, perr := time.Parse(time.RFC3339, prev); perr == nil {
			cutoff = t
		}
	}
	// 本轮游标先于扫描确定：扫描期间新写入的记录留给下一轮。
	now := time.Now().UTC()

	users, _ := scanSessionJSONL(transcript, cutoff)
	saved := 0
	for _, u := range users {
		text := strings.TrimSpace(u.Text)
		if !decisionRulingCandidate(text) {
			continue
		}
		name := decisionFactName(text)
		if _, err := cfg.Memory.Save(memory.Memory{
			Name:        name,
			Title:       truncateTitle(text, 48),
			Description: truncateTitle(text, 80),
			Type:        memory.TypeFeedback,
			Scope:       memory.FactScopeProject,
			Activation:  memory.ActivationRelevant,
			Keywords:    "决策模式 decision pattern autopilot",
			Body: fmt.Sprintf("%s\n\n**Why:** 决策模式提取（任务 801）于 %s 从会话 %s 的裁决轮次识别：该轮次命中否定/修正启发式，代表一条决策边界。\n\n**How to apply:** 后续遇到同类场景时优先遵循该裁决；条目仅追加，不删除源记忆，固化由心跳整合链（Phase 2）负责。",
				text, now.Format("2006-01-02 15:04"), base),
		}); err != nil {
			return fmt.Errorf("decision extract save: %w", err)
		}
		saved++
	}

	if state.Files == nil {
		state.Files = map[string]string{}
	}
	state.Files[base] = now.Format(time.RFC3339)
	state.Version = 1
	if err := writeDecisionExtractState(statePath, state); err != nil {
		return fmt.Errorf("decision extract state: %w", err)
	}
	if saved > 0 {
		slog.Info("decision extract saved ruling memories", "count", saved, "session", base)
	}
	return nil
}

// decisionRulingCandidate 是 E1 裁决轮次判定：长度合规 + 命中词表。
func decisionRulingCandidate(text string) bool {
	text = strings.TrimSpace(text)
	if len(text) < decisionExtractTextMinLen || len(text) > decisionExtractTextMaxLen {
		return false
	}
	lower := strings.ToLower(text)
	for _, marker := range decisionRulingMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// decisionFactName 与 dreamFactName 同款：可读 slug + 6 位内容 hash，同一句话
// 重跑得到同名（天然去重），不同内容得到不同名（不吞真不同条目）。
func decisionFactName(text string) string {
	words := strings.Fields(strings.ToLower(text))
	clean := make([]string, 0, 4)
	for _, w := range words {
		w = strings.Trim(w, "，。！？：；、\"'`()[]{}<>《》—…·!?.,:;\"'()[]{}<>-_/\\|~`@#$%^&*=+")
		if w == "" {
			continue
		}
		clean = append(clean, w)
		if len(clean) >= 4 {
			break
		}
	}
	sum := fnv.New32a()
	_, _ = sum.Write([]byte(strings.Join(strings.Fields(text), " ")))
	hash := fmt.Sprintf("%06x", sum.Sum32()&0xffffff)
	slug := "ruling"
	if len(clean) > 0 {
		slug = strings.Join(clean, "-")
	}
	return "decision-" + slug + "-" + hash
}

// readDecisionExtractState 读游标文件；损坏/缺失一律返回空 state（提取从零
// 重新开始，最坏情况是多扫一轮，绝不因状态文件问题失败）。
func readDecisionExtractState(path string) decisionExtractState {
	state := decisionExtractState{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return state
	}
	if json.Unmarshal(raw, &state) != nil || state.Files == nil {
		return decisionExtractState{}
	}
	return state
}

// writeDecisionExtractState 原子落盘游标（tmp+rename），并发写以最后落盘为准。
func writeDecisionExtractState(path string, state decisionExtractState) error {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
