package main

// 任务 414 阶段 1：发消息→首响（任务 370 口径）的会话侧读数。
//
// 数据源是 sessions/*.turns.jsonl（turnevent ledger，schema v1/v2）——与
// 任务 370「会话卡死判定」、doctor responsiveness 用的是同一份持久记录：
// turn_started 与首个模型输出（reasoning/text/tool_dispatch/tool_started，
// 与 telemetry sink 的 cli_first_response_latency 同口径）各带 createdAt
// 时间戳，按 turn 配对即首响。不依赖 events.jsonl 的 turn_begin 标记：实测
// 该标记骑在下一次 save 上（文件顺序与时间戳错位）、且仅 schema-2 会话写入，
// 窗口内取不到样本（2026-10-09 实测 16 个事件文件 0 样本）。
//
// 只读：不修复、不重写 ledger（与 doctor 同一约定）。ledger 会随 checkpoint
// 截断旧事件——历史最坏样本曾因此对周读数不可见（tl1684 归因：公布 Max
// 160s，真实 Max ≥180s）。tl1684 R7 起写侧在 checkpoint 截断时把首响样本
// 留存进 checkpoint 行（internal/turnevent firstResponseStat），本扫描把
// 留存样本与 live 事件配对样本跨 checkpoint 聚合（按 TurnID 去重），P95/Max
// 反映真实长尾；留存自写侧落地起生效，此前已被截断删除的样本不可恢复。

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reasonix/internal/store"
)

// firstResponseKinds 是「首个模型输出」的 ledger kind 集合，与
// doctor/responsiveness.go 的 responsivenessResponseKinds 逐字一致
// （turn_phase/usage 等只算活动，不算对用户的首响）。
var firstResponseKinds = map[string]bool{
	"reasoning":     true,
	"text":          true,
	"tool_dispatch": true,
	"tool_started":  true,
}

// turnSample 是一次 turn 的首响读数。TurnID 用于 checkpoint 留存样本与
// live 事件配对样本的去重键（同一 turn 两处可见时只计一次）。
type turnSample struct {
	Session string
	TurnID  string
	At      time.Time
	MS      int64
	// Provider 是该 turn 所属会话的 provider 分层键（tl1685 ①）：会话
	// meta 侧车（<session>.jsonl.meta）model 字段（canonical "provider/
	// model"）的 provider 段；meta 缺失/无 model/损坏一律归 "unknown"。
	Provider string
}

// providerUnknown 是 provider 分层的兜底桶：ledger 本身不带 model 信息
// （eventwire.Event 无 modelRef），provider 只能从会话 meta 侧车取，
// 侧车缺失的老会话归此桶，不阻断首响扫描。
const providerUnknown = "unknown"

// sessionScan 是一轮 ledger 扫描的产物。
type sessionScan struct {
	FilesScanned   int
	Turns          []turnSample
	FromCheckpoint int // 经 checkpoint 留存池聚合进来的样本数（tl1684 R7）
	NoResponse     int // turn_done 时仍无首响（取消/失败）——窗口内计数
	InFlight       int // 扫描结束仍开着且无首响的 turn（进行中，不计入失败）
	Torn           int // 尾部截断即停止的 ledger 文件数（运行时下次打开修复）
}

// ledgerRec 是两种 schema 共有的信封头部；v1 行没有 recordType，同样命中。
type ledgerRec struct {
	SchemaVersion int    `json:"schemaVersion"`
	RecordType    string `json:"recordType"`
	TurnID        string `json:"turnId"`
	Kind          string `json:"kind"`
	CreatedAt     int64  `json:"createdAt"`
}

// sessionRoots 返回扫描根：默认 <home>/sessions 与 <home>/projects/*/sessions
// （项目级会话是日常主力，漏掉会把样本量砍掉大半）；显式指定则只扫该目录。
func sessionRoots(home, override string) []string {
	if strings.TrimSpace(override) != "" {
		return []string{override}
	}
	roots := []string{filepath.Join(home, "sessions")}
	if matches, err := filepath.Glob(filepath.Join(home, "projects", "*", "sessions")); err == nil {
		roots = append(roots, matches...)
	}
	return roots
}

// scanLedgers 扫描各根目录下窗口内修改过的 *.turns.jsonl。全部根目录不可读
// 才返回错误；个别目录缺失（如无项目目录）忽略。
func scanLedgers(roots []string, window time.Duration, now time.Time) (*sessionScan, error) {
	out := &sessionScan{}
	cutoff := now.Add(-window)
	skewEnd := now.Add(time.Minute) // 允许轻微时钟超前
	readable := 0
	var firstErr error
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		readable++
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			n := e.Name()
			// .trash 等子目录不在这一层；guardian 看门狗的 turn 不是
			// 「用户发消息」，排除以免污染首响分布。
			if !strings.HasSuffix(n, store.SessionTurnEventLogSuffix) || strings.Contains(n, ".guardian.") {
				continue
			}
			info, err := e.Info()
			if err != nil || info.ModTime().Before(cutoff) {
				continue
			}
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			scanLedgerFile(filepath.Join(root, n), n, cutoff, skewEnd, out)
		}
	}
	if readable == 0 && firstErr != nil {
		return nil, firstErr
	}
	sort.Slice(out.Turns, func(i, j int) bool { return out.Turns[i].At.Before(out.Turns[j].At) })
	return out, nil
}

// ledgerOpen 是扫描过程中每个未闭合 turn 的状态。
type ledgerOpen struct {
	start     time.Time
	responded bool
}

// sessionProviderOf 读会话 meta 侧车（<sessionPath>.jsonl.meta，与
// internal/agent.BranchMeta 同一份文件）的 model 字段，返回 provider 分层键
// （canonical "provider/model" 的 provider 段）。只读；meta 缺失、无 model、
// 损坏一律归 "unknown"（providerUnknown），绝不阻断首响扫描。
// 粒度是会话级：model 是会话当前值，窗口内会话换过模型的历史 turn 不重分层
// （跨期换模型的影响由分层行的 Note 明示）。
func sessionProviderOf(ledgerPath string) string {
	sessionPath := strings.TrimSuffix(ledgerPath, store.SessionTurnEventLogSuffix) + ".jsonl"
	b, err := os.ReadFile(sessionPath + ".meta")
	if err != nil {
		return providerUnknown
	}
	var meta struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(b, &meta); err != nil {
		return providerUnknown
	}
	model := strings.TrimSpace(meta.Model)
	if model == "" {
		return providerUnknown
	}
	if i := strings.Index(model, "/"); i > 0 {
		return model[:i]
	}
	return model
}

// scanLedgerFile 单文件状态机。行级规则与 doctor.readTurnLedger 对齐：
// checkpoint/projection_ack 是保留簿记（跳过），无法解析/未知 recordType/
// 缺 turnId 即视为尾部截断并停止（运行时下次打开修复，这里不修补）。
// turn_done 之后的记录（恢复重开的罕见形态）忽略——重开会产生新 turn_started。
// tl1684 R7：checkpoint 行携带的首响留存样本聚合进样本集——被截断的 turn 的
// 事件行已物理删除，留存池是它们唯一的读数来源；与 live 配对按 TurnID 去重。
// tl1685 ①：本文件产出的全部样本（live 配对 + checkpoint 留存池）统一挂
// 该会话 meta 的 provider 分层键。
func scanLedgerFile(path, name string, cutoff, skewEnd time.Time, out *sessionScan) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	out.FilesScanned++
	provider := sessionProviderOf(path)
	first := len(out.Turns)

	inWindow := func(s *ledgerOpen) bool {
		return !s.start.Before(cutoff) && !s.start.After(skewEnd)
	}
	at := func(ms int64) (time.Time, bool) {
		if ms <= 0 {
			return time.Time{}, false
		}
		t := time.UnixMilli(ms)
		return t, !t.Before(cutoff) && !t.After(skewEnd)
	}
	seen := map[string]bool{} // 已计入样本的 turnId：留存池与 live 配对去重
	open := map[string]*ledgerOpen{}
	done := map[string]bool{}
	torn := false
	r := bufio.NewReader(f)
	for !torn {
		line, readErr := r.ReadString('\n')
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			torn = !foldLedgerLine(trimmed, name, cutoff, skewEnd, inWindow, at, seen, open, done, out)
		}
		if readErr != nil {
			break // EOF 或读错误：正常收尾（截断只由 fold 判定）
		}
	}
	for i := first; i < len(out.Turns); i++ {
		out.Turns[i].Provider = provider
	}
	if torn {
		out.Torn++
	}
	// 悬着的 turn：进行中，不冒充失败。
	for _, s := range open {
		if !s.responded && inWindow(s) {
			out.InFlight++
		}
	}
}

// foldLedgerLine 折叠一行；返回 false 表示尾部截断、停止解析该文件。
func foldLedgerLine(line, name string, cutoff, skewEnd time.Time,
	inWindow func(*ledgerOpen) bool,
	at func(int64) (time.Time, bool),
	seen map[string]bool,
	open map[string]*ledgerOpen, done map[string]bool, out *sessionScan) bool {

	var rec ledgerRec
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		return false // 半截行（崩溃/截断）
	}
	switch {
	case rec.RecordType == "checkpoint" || rec.RecordType == "projection_ack":
		if rec.RecordType == "checkpoint" {
			foldCheckpointFirstResponses(line, name, at, seen, out)
		}
		return true // 保留簿记，无逐事件时间线
	case rec.RecordType != "" && rec.RecordType != "event":
		return false // 未知 recordType：更新版本的行，这里读不懂就停
	case rec.TurnID == "" || rec.CreatedAt <= 0:
		return false
	}
	if done[rec.TurnID] {
		return true // turn_done 之后的迟到记录，忽略
	}
	at2 := time.UnixMilli(rec.CreatedAt)
	s := open[rec.TurnID]
	if s == nil {
		s = &ledgerOpen{start: at2}
		open[rec.TurnID] = s
	}
	switch {
	case firstResponseKinds[rec.Kind] && !s.responded:
		s.responded = true
		ms := at2.Sub(s.start).Milliseconds()
		if ms >= 0 && inWindow(s) && !seen[rec.TurnID] {
			seen[rec.TurnID] = true
			out.Turns = append(out.Turns, turnSample{Session: name, TurnID: rec.TurnID, At: s.start, MS: ms})
		}
	case rec.Kind == "turn_done":
		if !s.responded && inWindow(s) {
			out.NoResponse++
		}
		done[rec.TurnID] = true
		delete(open, rec.TurnID)
	}
	return true
}

// checkpointFirstResponses 是 checkpoint 行首响留存段的形状（tl1684 R7）。
// 与 internal/turnevent 的 firstResponseStat 对齐；空池缺字段，解析为 nil。
type checkpointFirstResponses struct {
	FirstResponses []struct {
		TurnID    string `json:"turnId"`
		StartedAt int64  `json:"startedAt"`
		MS        int64  `json:"ms"`
	} `json:"firstResponses"`
}

// foldCheckpointFirstResponses 把 checkpoint 行留存的首响样本并入样本集。
// 窗口过滤由 at 回调承担（与 live 配对同口径：turn 起点落在窗口内）；与
// live 配对样本按 TurnID 去重——留存池与事件行可能同时覆盖同一 turn
// （截断边界之外的 turn 两处都有），只计一次。
func foldCheckpointFirstResponses(line, name string,
	at func(int64) (time.Time, bool),
	seen map[string]bool, out *sessionScan) {
	var ck checkpointFirstResponses
	if err := json.Unmarshal([]byte(line), &ck); err != nil {
		return // 留存段损坏不判定截断：正文事件行仍可继续折叠
	}
	for _, fr := range ck.FirstResponses {
		if fr.TurnID == "" || fr.StartedAt <= 0 || fr.MS < 0 || seen[fr.TurnID] {
			continue
		}
		start, ok := at(fr.StartedAt)
		if !ok {
			continue
		}
		seen[fr.TurnID] = true
		out.FromCheckpoint++
		out.Turns = append(out.Turns, turnSample{Session: name, TurnID: fr.TurnID, At: start, MS: fr.MS})
	}
}
