package agent

// tl1485b 回归钉：偏新 stamp 存量自愈。tl1485 修了写入侧（raise-only）后，
// 存量里被历史 meta-only 泵（touch=true 盖 now）抬高的边车 UpdatedAt 因
// 「只升不降」永不自愈——2026-11-08 实盘扫描 464 行中顶层 35 行偏新 >1h
//（最大 1351h），全部投影 fresh，现有 repair 队列永远不碰它们。本组钉覆盖
// 四个形态与整条触发链（基准一律消息级 CreatedAt，禁用文件 mtime——
// 10-29 维护波已证 mtime 基准 95/261 误抬）：
//  1. 钉① 正常形态：UpdatedAt≈消息级最新，repair 不得动、不得踢进全量重放；
//  2. 钉② 略新形态：UpdatedAt 比消息级最新新几分钟（合法 activity 写入落盘
//     延迟），settle 不动；
//  3. 钉③ 空壳形态：无转录消息/全零时间戳，缺证据不落笔；
//  4. 偏新形态：UpdatedAt 比消息级最新新 ≥1h，repair 链降到消息级最新
//     （fresh 投影也要经探针落进 replay 精确校正）。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// TestSettleKeepsCurrentStampTL1485b 钉①：UpdatedAt 与消息级最新一致（正常
// 活跃会话），settle 原样保留。
func TestSettleKeepsCurrentStampTL1485b(t *testing.T) {
	msgAt := time.Now().Add(-30 * time.Minute)
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "hello", CreatedAt: msgAt.Add(-time.Minute).UnixMilli()},
		{Role: provider.RoleAssistant, Content: "hi", CreatedAt: msgAt.UnixMilli()},
	}
	stamp := msgAt
	meta := BranchMeta{UpdatedAt: stamp}
	settleSessionListingActivity(&meta, msgs)
	if !meta.UpdatedAt.Equal(stamp) {
		t.Fatalf("settle moved a current stamp: got %v, want %v", meta.UpdatedAt, stamp)
	}
}

// TestSettleKeepsLegitimateActivityLagTL1485b 钉②：UpdatedAt 比消息级最新新
// 几分钟（合法 activity 写入的落盘延迟形态），settle 不动。
func TestSettleKeepsLegitimateActivityLagTL1485b(t *testing.T) {
	msgAt := time.Now().Add(-2 * time.Hour)
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "hello", CreatedAt: msgAt.UnixMilli()},
	}
	stamp := msgAt.Add(5 * time.Minute)
	meta := BranchMeta{UpdatedAt: stamp}
	settleSessionListingActivity(&meta, msgs)
	if !meta.UpdatedAt.Equal(stamp) {
		t.Fatalf("settle lowered a legitimate activity lag: got %v, want %v", meta.UpdatedAt, stamp)
	}
}

// TestSettleKeepsTimestamplessShellTL1485b 钉③：空会话与全零时间戳 legacy
// 转录都没有证据，settle 不落笔。
func TestSettleKeepsTimestamplessShellTL1485b(t *testing.T) {
	stamp := time.Now().Add(-3 * time.Hour)
	for name, msgs := range map[string][]provider.Message{
		"empty":  nil,
		"no-ts":  {{Role: provider.RoleUser, Content: "legacy message without timestamps"}},
		"no-ts2": {{Role: provider.RoleUser, Content: "a"}, {Role: provider.RoleAssistant, Content: "b"}},
	} {
		meta := BranchMeta{UpdatedAt: stamp}
		settleSessionListingActivity(&meta, msgs)
		if !meta.UpdatedAt.Equal(stamp) {
			t.Fatalf("%s: settle stamped without evidence: got %v, want %v", name, meta.UpdatedAt, stamp)
		}
	}
}

// TestSettleLowersClearlyInflatedStampTL1485b 偏新形态单元钉：比消息级最新
// 新 ≥1h 的存量误抬被降到消息级最新。
func TestSettleLowersClearlyInflatedStampTL1485b(t *testing.T) {
	msgAt := time.Now().Add(-48 * time.Hour)
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "hello", CreatedAt: msgAt.UnixMilli()},
	}
	stamp := msgAt.Add(50 * time.Hour)
	meta := BranchMeta{UpdatedAt: stamp}
	settleSessionListingActivity(&meta, msgs)
	// 消息级时间以 unix 毫秒承载，比较同精度。
	if meta.UpdatedAt.UnixMilli() != msgAt.UnixMilli() {
		t.Fatalf("settle kept an inflated stamp: got %v, want %v", meta.UpdatedAt, msgAt)
	}
}

// TestSessionListingActivityAheadOfTranscriptTruthTableTL1485b 探针真值表：
// 偏新为真；正常/略新/空转录/无时间戳一律为假（宁可漏报不造假证据）。
func TestSessionListingActivityAheadOfTranscriptTruthTableTL1485b(t *testing.T) {
	dir := t.TempDir()
	msgAt := time.Now().Add(-48 * time.Hour)
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "question", CreatedAt: msgAt.UnixMilli()},
		{Role: provider.RoleAssistant, Content: "answer", CreatedAt: msgAt.Add(time.Minute).UnixMilli()},
	}

	path := filepath.Join(dir, "session.jsonl")
	writeSessionFile(t, path, msgs)
	if !SessionListingActivityAheadOfTranscript(path, msgAt.Add(2*time.Hour)) {
		t.Fatalf("inflated stamp not detected in %s", path)
	}
	if SessionListingActivityAheadOfTranscript(path, msgAt.Add(time.Minute)) {
		t.Fatalf("normal stamp flagged in %s", path)
	}
	if SessionListingActivityAheadOfTranscript(path, msgAt.Add(30*time.Minute)) {
		t.Fatalf("legitimate activity lag flagged in %s", path)
	}
	if SessionListingActivityAheadOfTranscript(path, time.Time{}) {
		t.Fatalf("zero stamp flagged in %s", path)
	}

	shell := filepath.Join(dir, "shell.jsonl")
	writeSessionFile(t, shell, []provider.Message{{Role: provider.RoleUser, Content: "no timestamps"}})
	if SessionListingActivityAheadOfTranscript(shell, time.Now().Add(24*time.Hour)) {
		t.Fatalf("timestamp-less transcript flagged in %s", shell)
	}

	legacy := filepath.Join(dir, "legacy.jsonl")
	writeSessionFile(t, legacy, nil)
	if SessionListingActivityAheadOfTranscript(legacy, time.Now().Add(24*time.Hour)) {
		t.Fatalf("empty transcript flagged in %s", legacy)
	}
}

// TestSessionListingActivityProbeGrowsPastTaillessTailTL1485b 探针尾窗增长钉
// （2026-11-08 实盘 Gap B）：大转录最后 64KB 是一条无时间戳的巨行（工具结果
// 常态），时间戳只在更早的行里——探针必须逐级放大窗口找到证据，而不是漏报。
func TestSessionListingActivityProbeGrowsPastTaillessTailTL1485b(t *testing.T) {
	dir := t.TempDir()
	msgAt := time.Now().Add(-48 * time.Hour)
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "question", CreatedAt: msgAt.UnixMilli()},
		// 巨行工具结果：300KB，无 CreatedAt——首窗 64KB、次窗 256KB 都落在它里面。
		{Role: provider.RoleAssistant, Content: "tool result wall", ToolCalls: []provider.ToolCall{{
			ID: "call-1", Name: "read_file",
			Arguments: `{"path":"` + strings.Repeat("x", 300<<10) + `"}`,
		}}},
	}
	path := filepath.Join(dir, "big-tail.jsonl")
	writeSessionFile(t, path, msgs)
	if info, err := os.Stat(path); err != nil || info.Size() < 256<<10 {
		t.Fatalf("fixture too small: %v err=%v", info, err)
	}
	if !SessionListingActivityAheadOfTranscript(path, msgAt.Add(2*time.Hour)) {
		t.Fatalf("inflated stamp missed past the tailless tail")
	}
	if SessionListingActivityAheadOfTranscript(path, msgAt.Add(30*time.Minute)) {
		t.Fatalf("healthy stamp flagged after window growth")
	}
}

// TestRepairEntrySettlesStampWhileFixingCountsTL1485b 计数修正出口钉（实盘
// Gap A）：投影计数陈旧 + 活动戳偏新时，index 分支的计数修正路径不得以
// Applied 收场而保留偏新戳——探针必须把它交给 replay 一次修齐。
func TestRepairEntrySettlesStampWhileFixingCountsTL1485b(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stale-counts.jsonl")
	msgAt := time.Now().Add(-48 * time.Hour)
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "question", CreatedAt: msgAt.UnixMilli()},
		{Role: provider.RoleAssistant, Content: "answer", CreatedAt: msgAt.Add(time.Minute).UnixMilli()},
	}
	writeSessionFile(t, path, msgs)
	if result, err := RepairSessionListingProjection(context.Background(), path); err != nil || result.Status != SessionListingRepairApplied {
		t.Fatalf("seed repair = %+v err=%v", result, err)
	}
	// 计数写歪 + 泵抬活动戳：制造「计数修正出口 + 偏新戳」并存形态。
	if err := SaveBranchMetaPreserveUpdated(path, BranchMeta{
		ID: BranchID(path), Turns: 99, UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("corrupt counts: %v", err)
	}
	result, err := RepairSessionListingProjection(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status == SessionListingRepairAlreadyCurrent {
		t.Fatalf("stale-count row short-circuited as already_current: %+v", result)
	}
	meta, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("load meta ok=%v err=%v", ok, err)
	}
	if meta.Turns == 99 {
		t.Fatalf("counts not fixed: %+v", meta)
	}
	if diff := meta.UpdatedAt.Sub(msgAt.Add(time.Minute)); diff < -time.Minute || diff > time.Minute {
		t.Fatalf("counts fixed but the inflated stamp survived: UpdatedAt = %v, want ~%v",
			meta.UpdatedAt, msgAt.Add(time.Minute))
	}
}

// TestRepairEntryLowersInflatedStampOnFreshProjectionTL1485b 触发链端到端钉
// （基线红）：投影完全 fresh、计数全对的会话只带一个偏新 UpdatedAt，既有
// AlreadyCurrent 短路会让它永不自愈。修复后：index 分支探针发现偏新 → 落进
// replay 全量解码 → settle 降到消息级最新。
func TestRepairEntryLowersInflatedStampOnFreshProjectionTL1485b(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inflated.jsonl")
	msgAt := time.Now().Add(-48 * time.Hour)
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "question", CreatedAt: msgAt.UnixMilli()},
		{Role: provider.RoleAssistant, Content: "answer", CreatedAt: msgAt.Add(time.Minute).UnixMilli()},
	}
	writeSessionFile(t, path, msgs)

	// 第一次 repair 建立权威投影（ledger/projection 全部对齐）。
	if result, err := RepairSessionListingProjection(context.Background(), path); err != nil || result.Status != SessionListingRepairApplied {
		t.Fatalf("seed repair = %+v err=%v", result, err)
	}
	// 模拟泵误抬：meta-only 把 UpdatedAt 盖到「现在」，投影字段原样保留。
	pumped := time.Now()
	if err := SaveBranchMetaPreserveUpdated(path, BranchMeta{ID: BranchID(path), UpdatedAt: pumped}); err != nil {
		t.Fatalf("pump stamp: %v", err)
	}
	result, err := RepairSessionListingProjection(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status == SessionListingRepairAlreadyCurrent {
		t.Fatalf("inflated stamp short-circuited as already_current: %+v", result)
	}
	meta, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("load meta ok=%v err=%v", ok, err)
	}
	if diff := meta.UpdatedAt.Sub(msgAt.Add(time.Minute)); diff < -time.Minute || diff > time.Minute {
		t.Fatalf("after repair UpdatedAt = %v, want the transcript's last-message time (~%v); the pump stamp %v survived",
			meta.UpdatedAt, msgAt.Add(time.Minute), pumped)
	}
}

// TestRepairEntryKeepsCurrentStampOnFreshProjectionTL1485b 钉①端到端镜像：
// UpdatedAt≈消息级最新的正常会话必须停留在 AlreadyCurrent，不得被探针踢进
// 全量重放。
func TestRepairEntryKeepsCurrentStampOnFreshProjectionTL1485b(t *testing.T) {
	path := filepath.Join(t.TempDir(), "current.jsonl")
	msgAt := time.Now().Add(-2 * time.Hour)
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "question", CreatedAt: msgAt.UnixMilli()},
		{Role: provider.RoleAssistant, Content: "answer", CreatedAt: msgAt.Add(time.Minute).UnixMilli()},
	}
	writeSessionFile(t, path, msgs)
	if result, err := RepairSessionListingProjection(context.Background(), path); err != nil || result.Status != SessionListingRepairApplied {
		t.Fatalf("seed repair = %+v err=%v", result, err)
	}
	// 正常形态：消息落盘后 meta 跟着盖一次同源时间。
	if err := SaveBranchMetaPreserveUpdated(path, BranchMeta{ID: BranchID(path), UpdatedAt: msgAt.Add(2 * time.Minute)}); err != nil {
		t.Fatalf("stamp meta: %v", err)
	}
	result, err := RepairSessionListingProjection(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != SessionListingRepairAlreadyCurrent {
		t.Fatalf("healthy session left the fast path: %+v", result)
	}
	meta, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("load meta ok=%v err=%v", ok, err)
	}
	if want := msgAt.Add(2 * time.Minute); !meta.UpdatedAt.Equal(want) {
		t.Fatalf("healthy session stamp moved: got %v, want %v", meta.UpdatedAt, want)
	}
}
