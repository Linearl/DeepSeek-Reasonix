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
// 截断旧事件，所以样本只覆盖窗口内仍保留记录的 turn——这正是窗口的语义。

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

// turnSample 是一次 turn 的首响读数。
type turnSample struct {
	Session string
	At      time.Time
	MS      int64
}

// sessionScan 是一轮 ledger 扫描的产物。
type sessionScan struct {
	FilesScanned int
	Turns        []turnSample
	NoResponse   int // turn_done 时仍无首响（取消/失败）——窗口内计数
	InFlight     int // 扫描结束仍开着且无首响的 turn（进行中，不计入失败）
	Torn         int // 尾部截断即停止的 ledger 文件数（运行时下次打开修复）
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

// scanLedgerFile 单文件状态机。行级规则与 doctor.readTurnLedger 对齐：
// checkpoint/projection_ack 是保留簿记（跳过），无法解析/未知 recordType/
// 缺 turnId 即视为尾部截断并停止（运行时下次打开修复，这里不修补）。
// turn_done 之后的记录（恢复重开的罕见形态）忽略——重开会产生新 turn_started。
func scanLedgerFile(path, name string, cutoff, skewEnd time.Time, out *sessionScan) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	out.FilesScanned++

	inWindow := func(s *ledgerOpen) bool {
		return !s.start.Before(cutoff) && !s.start.After(skewEnd)
	}
	open := map[string]*ledgerOpen{}
	done := map[string]bool{}
	torn := false
	r := bufio.NewReader(f)
	for !torn {
		line, readErr := r.ReadString('\n')
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			torn = !foldLedgerLine(trimmed, name, cutoff, skewEnd, inWindow, open, done, out)
		}
		if readErr != nil {
			break // EOF 或读错误：正常收尾（截断只由 fold 判定）
		}
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
	open map[string]*ledgerOpen, done map[string]bool, out *sessionScan) bool {

	var rec ledgerRec
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		return false // 半截行（崩溃/截断）
	}
	switch {
	case rec.RecordType == "checkpoint" || rec.RecordType == "projection_ack":
		return true // 保留簿记，无逐事件时间线
	case rec.RecordType != "" && rec.RecordType != "event":
		return false // 未知 recordType：更新版本的行，这里读不懂就停
	case rec.TurnID == "" || rec.CreatedAt <= 0:
		return false
	}
	if done[rec.TurnID] {
		return true // turn_done 之后的迟到记录，忽略
	}
	at := time.UnixMilli(rec.CreatedAt)
	s := open[rec.TurnID]
	if s == nil {
		s = &ledgerOpen{start: at}
		open[rec.TurnID] = s
	}
	switch {
	case firstResponseKinds[rec.Kind] && !s.responded:
		s.responded = true
		ms := at.Sub(s.start).Milliseconds()
		if ms >= 0 && inWindow(s) {
			out.Turns = append(out.Turns, turnSample{Session: name, At: s.start, MS: ms})
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
