package main

// tl1674 阶段二：pre-repair 分叉**手动拼接入口**（预览+登记）的桌面侧单测。
//
// 验收钉五组：
//  ① 链预览分段结构：分叉长链形态给出 前缀/丢失段/拼接点 + 两侧总条数
//     （面板确认弹窗的对屏读数）；covered 形态 splicable=false + 人话原因；
//  ② 目标闸三态：孤儿备份 / 宿主已归档 / 已知会话目录之外——与合并桥同一
//     纪律，逐态拒绝；
//  ③ **手动登记纯元信息（字节级钉）**：登记前后主线转录与事件日志逐字节
//     不变；档案袋登记带 manual 来源；重复登记拒绝（幂等）；
//  ④ covered 的手动入口=回执非错误（registered=false + 原因），零档案袋写；
//  ⑤ 健康闸提示替代硬拦：宿主不健康时预览带 codes 披露，拼接照常可用
//     （纯元信息操作不受健康态影响；删除闸的既有保护零回退）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
)

// providerUserMessage 是桌面测试的消息构造缝（pre_repair_merge_test.go 同一
// 形态）：单条用户消息的轮次。
func providerUserMessage(content string) provider.Message {
	return provider.Message{Role: provider.RoleUser, Content: content}
}

func spliceTranscriptPath(sessions, stem string) string {
	return filepath.Join(sessions, stem+".pre-repair-20261110T010000", stem+".jsonl")
}

func spliceEventsPath(ownerPath string) string {
	return ownerPath[:len(ownerPath)-len(".jsonl")] + ".events.jsonl"
}

func spliceLedgerPath(ownerPath string) string {
	return ownerPath[:len(ownerPath)-len(".jsonl")] + ".splices.json"
}

// ① 链预览分段结构：分叉长链（宿主=[C,C2,F,G]，备份=[C,C2,D,E]）给全量
// 分段读数——前缀 2 / 丢失段 [2,4) / 拼接点 2 / 两侧各 4 条（trim 口径）。
func TestPreRepairSplicePreviewSegmentsDivergedFork(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	ownerPath, backupDir := seedAutosplicePair(t, sessions, "topic")

	app := &App{}
	preview, err := app.PreviewPreRepairSplice(backupDir)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !preview.Splicable || preview.Kind != string(agent.PreRepairSpliceDiverged) {
		t.Fatalf("preview = %+v, want splicable diverged-long-chain", preview)
	}
	if preview.SharedPrefix != 2 || preview.SegmentStart != 2 || preview.SegmentEnd != 4 ||
		preview.SplicePoint != 2 || preview.SegmentMessages != 2 {
		t.Fatalf("segments = %+v, want prefix 2 / segment [2,4) / splice point 2 / 2 messages", preview)
	}
	if preview.OwnerMessages != 4 || preview.BackupMessages != 4 {
		t.Fatalf("side counts = %d/%d, want 4/4 (trim 口径)", preview.OwnerMessages, preview.BackupMessages)
	}
	if preview.OwnerPath != ownerPath {
		t.Fatalf("owner = %q, want %q", preview.OwnerPath, ownerPath)
	}
	// 预览是纯读数：档案袋不得被预览写出。
	if _, statErr := os.Stat(spliceLedgerPath(ownerPath)); !os.IsNotExist(statErr) {
		t.Fatal("a preview must not write the splice ledger")
	}
}

// ① covered 形态：预览如实给 splicable=false + 人话原因（无事可登记）。
func TestPreRepairSplicePreviewCoveredShowsReason(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	ownerPath := filepath.Join(sessions, "long.jsonl")
	owner := agent.NewSession("sys")
	owner.Add(providerUserMessage("C shared"))
	owner.Add(providerUserMessage("D folded away"))
	owner.Add(providerUserMessage("E folded away"))
	owner.Add(providerUserMessage("F new"))
	if err := owner.Save(ownerPath); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(sessions, "long.pre-repair-20261110T010000")
	if err := os.MkdirAll(backup, 0o755); err != nil {
		t.Fatal(err)
	}
	backupSession := agent.NewSession("sys")
	backupSession.Add(providerUserMessage("C shared"))
	backupSession.Add(providerUserMessage("D folded away"))
	backupSession.Add(providerUserMessage("E folded away"))
	if err := backupSession.Save(filepath.Join(backup, "long.jsonl")); err != nil {
		t.Fatal(err)
	}

	app := &App{}
	preview, err := app.PreviewPreRepairSplice(backup)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Splicable {
		t.Fatalf("preview = %+v, want non-splicable (covered)", preview)
	}
	if !strings.Contains(preview.Reason, "covered") {
		t.Fatalf("reason = %q, want the covered explanation", preview.Reason)
	}
}

// ② 目标闸三态（孤儿/归档/目录外）与合并桥同一纪律。
func TestPreRepairSpliceTargetGates(t *testing.T) {
	sessions := stagePreRepairFixture(t)

	// 孤儿备份：宿主不在，无可回主线。
	_, orphanDir := seedAutosplicePair(t, sessions, "orphan")
	if err := os.Remove(filepath.Join(sessions, "orphan.jsonl")); err != nil {
		t.Fatal(err)
	}
	app := &App{}
	if _, err := app.PreviewPreRepairSplice(orphanDir); err == nil {
		t.Fatal("an orphan backup has no main line to splice into — must refuse")
	}

	// 宿主已归档：先还原对话再拼。
	_, archDir := seedAutosplicePair(t, sessions, "arch14")
	bucket := filepath.Join(sessions, ".trash", "arch14.jsonl-deleted-1")
	if err := os.MkdirAll(bucket, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(sessions, "arch14.jsonl"), filepath.Join(bucket, "arch14.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(archDir, filepath.Join(bucket, filepath.Base(archDir))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bucket, sessionTrashMetaFile), []byte(`{"key":"arch14.jsonl","deletedAt":1729000000000}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.MergePreRepairSplice(filepath.Join(bucket, filepath.Base(archDir))); err == nil {
		t.Fatal("an archived owner must refuse the splice until the conversation is restored")
	}

	// 已知会话目录之外的路径。
	_, rogueDir := seedAutosplicePair(t, t.TempDir(), "rogue")
	if _, err := app.PreviewPreRepairSplice(rogueDir); err == nil {
		t.Fatal("splice must refuse paths outside known session dirs")
	}
}

// ③ 手动登记（字节级钉）：主线转录与事件日志逐字节不变；档案袋登记带
// manual 来源；回执读数对屏；重复登记拒绝。
func TestPreRepairManualSpliceRegistersManualSourceAndNeverTouchesProjection(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	ownerPath, backupDir := seedAutosplicePair(t, sessions, "topic")
	transcriptBefore, err := os.ReadFile(ownerPath)
	if err != nil {
		t.Fatal(err)
	}
	eventsBefore, err := os.ReadFile(spliceEventsPath(ownerPath))
	if err != nil {
		t.Fatal(err)
	}

	app := &App{}
	receipt, err := app.MergePreRepairSplice(backupDir)
	if err != nil {
		t.Fatalf("manual splice: %v", err)
	}
	if !receipt.Registered || receipt.Kind != string(agent.PreRepairSpliceDiverged) {
		t.Fatalf("receipt = %+v, want a registered diverged splice", receipt)
	}
	if receipt.Source != agent.PreRepairSpliceSourceManual {
		t.Fatalf("source = %q, want %q (手动来源标记)", receipt.Source, agent.PreRepairSpliceSourceManual)
	}
	if receipt.SplicePoint != 2 || receipt.SegmentMessages != 2 || receipt.OwnerMessages != 4 {
		t.Fatalf("receipt readouts = %+v, want splicePoint 2 / 2 segment messages / 4 owner messages", receipt)
	}
	// 投影零改动（字节级）：转录与事件日志与登记前逐字节一致。
	transcriptAfter, err := os.ReadFile(ownerPath)
	if err != nil {
		t.Fatal(err)
	}
	eventsAfter, err := os.ReadFile(spliceEventsPath(ownerPath))
	if err != nil {
		t.Fatal(err)
	}
	if string(transcriptBefore) != string(transcriptAfter) {
		t.Fatal("the transcript changed during a metadata-only manual splice (投影内容一律不变 violated)")
	}
	if string(eventsBefore) != string(eventsAfter) {
		t.Fatal("the event log changed during a metadata-only manual splice (投影内容一律不变 violated)")
	}
	// 档案袋核对：manual 来源 + 校验值三条齐。
	entries, err := agent.ReadPreRepairSpliceLedger(ownerPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("ledger has %d entries, want 1", len(entries))
	}
	if entries[0].Source != agent.PreRepairSpliceSourceManual || entries[0].SegmentHash == "" ||
		entries[0].BoundaryPrevHash == "" || entries[0].BoundaryNextHash == "" {
		t.Fatalf("entry = %+v, want manual source and the three recomputed checksums", entries[0])
	}
	// 幂等：同一备份重复登记拒绝。
	if _, err := app.MergePreRepairSplice(backupDir); err == nil {
		t.Fatal("a duplicate manual registration must be refused (idempotent)")
	}
}

// ④ covered 的手动入口=回执非错误：registered=false + 人话原因，零档案袋写。
func TestPreRepairManualSpliceCoveredIsReceiptNotError(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	ownerPath := filepath.Join(sessions, "long.jsonl")
	owner := agent.NewSession("sys")
	owner.Add(providerUserMessage("C shared"))
	owner.Add(providerUserMessage("D folded away"))
	owner.Add(providerUserMessage("E folded away"))
	owner.Add(providerUserMessage("F new"))
	if err := owner.Save(ownerPath); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(sessions, "long.pre-repair-20261110T010000")
	if err := os.MkdirAll(backup, 0o755); err != nil {
		t.Fatal(err)
	}
	backupSession := agent.NewSession("sys")
	backupSession.Add(providerUserMessage("C shared"))
	backupSession.Add(providerUserMessage("D folded away"))
	backupSession.Add(providerUserMessage("E folded away"))
	if err := backupSession.Save(filepath.Join(backup, "long.jsonl")); err != nil {
		t.Fatal(err)
	}

	app := &App{}
	receipt, err := app.MergePreRepairSplice(backup)
	if err != nil {
		t.Fatalf("covered splice must be a receipt, not an error: %v", err)
	}
	if receipt.Registered {
		t.Fatalf("receipt = %+v, want registered=false (covered)", receipt)
	}
	if !strings.Contains(receipt.Reason, "covered") {
		t.Fatalf("reason = %q, want the covered explanation", receipt.Reason)
	}
	if _, statErr := os.Stat(spliceLedgerPath(ownerPath)); !os.IsNotExist(statErr) {
		t.Fatal("a covered splice must not write the ledger")
	}
}

// ⑤ 健康闸提示替代硬拦：宿主不健康时预览带 codes 披露（splicable 不受影
// 响），手动登记照常成功——删除闸的既有保护零回退（不在本面削它）。
func TestPreRepairSpliceHealthNoticeNotHardBlock(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	ownerPath, backupDir := seedAutosplicePair(t, sessions, "sick")
	previous := preRepairHealthInspect
	preRepairHealthInspect = func(path string, _ agent.HealthInspectOptions) agent.SessionHealthReport {
		return agent.SessionHealthReport{Path: path, Healthy: false, Issues: []agent.SessionHealthIssue{{Code: "projection_overflow"}}}
	}
	t.Cleanup(func() { preRepairHealthInspect = previous })

	app := &App{}
	preview, err := app.PreviewPreRepairSplice(backupDir)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.OwnerHealthy || len(preview.HealthCodes) != 1 || preview.HealthCodes[0] != "projection_overflow" {
		t.Fatalf("health readout = %+v, want unhealthy with the projection_overflow code", preview)
	}
	if !preview.Splicable {
		t.Fatalf("preview = %+v, want splicable (健康提示不得硬拦纯元信息拼接)", preview)
	}
	receipt, err := app.MergePreRepairSplice(backupDir)
	if err != nil {
		t.Fatalf("manual splice under an unhealthy owner must proceed (metadata-only): %v", err)
	}
	if !receipt.Registered {
		t.Fatalf("receipt = %+v, want registered", receipt)
	}
	entries, err := agent.ReadPreRepairSpliceLedger(ownerPath)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ledger = %+v (err %v), want exactly one entry", entries, err)
	}
}
