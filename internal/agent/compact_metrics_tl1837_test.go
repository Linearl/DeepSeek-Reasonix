package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/store"
)

// tl1837：压缩/折叠关键读数持久化验收。
//
// 契约：每次压缩事件（fold 计划 / 执行完成 / ranges 增量 / 拒绝 / 失败）
// 同步追加一行 JSON 到会话兄弟侧车 <stem>.compactmetrics.jsonl——desktop.log
// 轮转/进程重启后历史读数仍可读（案B：fold 计划 1384 实际 329、672.8K
// 分项现场不再因日志轮转失去数据源）。上界：512KiB 字节界 + 尾部 256 行
// 滚动覆盖。三链登记：store.SessionSidecarFiles（删除/换位/迁移单一来源）
// + 备份白名单 + 差集门禁自动覆盖。

// TestCompactMetricsSidecarRecordsPlanAndDone 验收①（红面先行）：触发一次
// 压力压缩 → 读侧车 → 断言 fold_plan 与 fold_done 关键字段（计划/执行差距
// 的两半边 + buckets 分项可复原）。
func TestCompactMetricsSidecarRecordsPlanAndDone(t *testing.T) {
	const window = 40_000
	prov := &fullSendStubProvider{window: window}
	a := buildPreFoldAgent(t, prov, 48, window)
	sessionPath := filepath.Join(t.TempDir(), "session.jsonl")
	a.sess.path = sessionPath

	if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
		t.Fatalf("pressure maintenance: %v", err)
	}
	recs, err := ListSessionCompactMetrics(sessionPath)
	if err != nil {
		t.Fatalf("read compactmetrics sidecar: %v", err)
	}
	var plan, done *CompactMetricsRecord
	for i := range recs {
		switch recs[i].Kind {
		case compactMetricsKindFoldPlan:
			if plan == nil {
				plan = &recs[i]
			}
		case compactMetricsKindFoldDone:
			done = &recs[i] // keep the last install
		}
	}
	if plan == nil {
		t.Fatalf("no fold_plan record after a pressure fold; kinds seen: %v", compactMetricsKindsOf(recs))
	}
	if done == nil {
		t.Fatalf("no fold_done record after an installed fold; kinds seen: %v", compactMetricsKindsOf(recs))
	}
	// 计划半边：触发源、视图规模、折叠区边界、计划折量全部在场且成谱。
	if plan.Trigger != CompactionTriggerPressure {
		t.Fatalf("plan trigger = %q, want %q", plan.Trigger, CompactionTriggerPressure)
	}
	if plan.ViewMessages <= 0 || plan.Start <= plan.Head {
		t.Fatalf("plan geometry broken: head=%d start=%d view=%d", plan.Head, plan.Start, plan.ViewMessages)
	}
	if plan.FoldMessages <= 0 || plan.KeptMessages < 0 {
		t.Fatalf("plan fold size broken: fold=%d kept=%d", plan.FoldMessages, plan.KeptMessages)
	}
	if !plan.SplitActive {
		t.Fatal("pressure plan must carry split_active=true (tl1713fix full active-turn split)")
	}
	if plan.Session == "" || plan.At == "" {
		t.Fatalf("plan record missing common fields: at=%q session=%q", plan.At, plan.Session)
	}
	// 执行半边：实际折量、token 读数、压缩比、两份 buckets 分项。
	if done.FoldedMessages <= 0 {
		t.Fatalf("done folded_messages = %d, want >0 (案B「实际」半边)", done.FoldedMessages)
	}
	if done.SourceTokens <= 0 || done.ProjectionTokens <= 0 {
		t.Fatalf("done tokens broken: source=%d projection=%d", done.SourceTokens, done.ProjectionTokens)
	}
	if done.ProjectionSourceRatio <= 0 || done.ProjectionSourceRatio > 1 {
		t.Fatalf("done projection_source_ratio = %v, want (0,1]", done.ProjectionSourceRatio)
	}
	if done.BucketsPre == nil || done.BucketsPost == nil {
		t.Fatalf("done record missing buckets: pre=%v post=%v", done.BucketsPre, done.BucketsPost)
	}
	// total_tokens 锚 done 行同源读数（desktop.log buckets_pre/post 行的
	// total_tokens 字段同口径；桶和与它的差值是校准尺口径差，live 日志同
	// 样存在——分项字段本身才是案B要的构成复原面）。
	if done.BucketsPre.TotalTokens != done.SourceTokens || done.BucketsPost.TotalTokens != done.ProjectionTokens {
		t.Fatalf("bucket totals drift from the done line: pre %d/%d post %d/%d",
			done.BucketsPre.TotalTokens, done.SourceTokens, done.BucketsPost.TotalTokens, done.ProjectionTokens)
	}
	if done.BucketsPre.Messages <= 0 || done.BucketsPost.Messages <= 0 {
		t.Fatalf("bucket message counts broken: pre=%d post=%d", done.BucketsPre.Messages, done.BucketsPost.Messages)
	}
}

// TestCompactMetricsSidecarSurvivesRestart 验收②：侧车是普通文件——进程
// 重启（新读会话）后历史行仍可读。模拟：写入后用 ListSessionCompactMetrics
// 重新读（无任何进程内状态）。
func TestCompactMetricsSidecarSurvivesRestart(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "session.jsonl")
	rec := foldDoneMetricsRecord(CompactionTriggerManual, CompactionTelemetry{SourceTokens: 100, FoldTokens: 90, ProjectionTokens: 40}, 100, 3, 40, -1, compactionViewBuckets{messages: 5}, compactionViewBuckets{messages: 2})
	if err := appendCompactMetricsRecord(store.SessionCompactMetricsLog(sessionPath), rec); err != nil {
		t.Fatalf("append: %v", err)
	}
	rec2 := foldDeclinedMetricsRecord(CompactionTriggerModelRanges, "ranges_model_window")
	if err := appendCompactMetricsRecord(store.SessionCompactMetricsLog(sessionPath), rec2); err != nil {
		t.Fatalf("append: %v", err)
	}
	recs, err := ListSessionCompactMetrics(sessionPath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("records = %d, want 2", len(recs))
	}
	if recs[0].Kind != compactMetricsKindFoldDone || recs[0].ProjectionTokens != 40 || recs[0].CompactionIntervalMin != -1 {
		t.Fatalf("first record drifted: %+v", recs[0])
	}
	if recs[1].Kind != compactMetricsKindDeclined || recs[1].Reason != "ranges_model_window" {
		t.Fatalf("declined record drifted: %+v", recs[1])
	}
}

// TestCompactMetricsSidecarTailCap 验收③：侧车大小有上界。契约：追加后超
// 512KiB 即截尾，截尾保留「字节界 ∩ 行数界」内的最新行；字节界恒成立
// （含肥记录形态），行数界在截尾时刻生效。
func TestCompactMetricsSidecarTailCap(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "session.jsonl")
	logPath := store.SessionCompactMetricsLog(sessionPath)
	// 追加循环：越过字节上限必然触发截尾——最终大小必须回到界内，且最新
	// 记录幸存（截尾只丢最旧）。
	bulk := strings.Repeat("x", 1200)
	for i := range compactMetricsTailLines + 200 {
		rec := foldFailedMetricsRecord(CompactionTriggerPressure, bulk)
		rec.Session = "cap-fixture"
		if err := appendCompactMetricsRecord(logPath, rec); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	st, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if st.Size() > compactMetricsMaxFileBytes+4096 {
		t.Fatalf("sidecar size %d exceeds the cap %d (+one line slack)", st.Size(), compactMetricsMaxFileBytes)
	}
	recs, err := ListSessionCompactMetrics(sessionPath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(recs) == 0 {
		t.Fatal("tail truncation wiped every record — the newest lines must survive")
	}
	if recs[len(recs)-1].Kind != compactMetricsKindFailed {
		t.Fatalf("newest record kind = %q, want the last appended failure", recs[len(recs)-1].Kind)
	}
	// 截尾函数直调（确定性钉双界）：300 条 3KB 胖行（~900KB > 上限）→ 截尾
	// 后同时满足 ≤256 行与 ≤字节上限。
	fatPath := store.SessionCompactMetricsLog(filepath.Join(t.TempDir(), "fat.jsonl"))
	fat := `{"at":"t","kind":"fold_failed","trigger":"pressure","reason":"` + strings.Repeat("y", 2900) + `"}`
	var sb strings.Builder
	for i := 0; i < 300; i++ {
		sb.WriteString(fat)
		sb.WriteString("\n")
	}
	if err := os.WriteFile(fatPath, []byte(sb.String()), 0o600); err != nil {
		t.Fatalf("seed fat file: %v", err)
	}
	if err := truncateCompactMetricsTail(fatPath); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	stFat, err := os.Stat(fatPath)
	if err != nil {
		t.Fatalf("stat fat: %v", err)
	}
	if stFat.Size() > compactMetricsMaxFileBytes {
		t.Fatalf("fat-record sidecar size %d exceeds the cap %d", stFat.Size(), compactMetricsMaxFileBytes)
	}
	fatLines := strings.Count(mustReadFile(t, fatPath), "\n")
	if fatLines > compactMetricsTailLines {
		t.Fatalf("fat truncation kept %d lines, want ≤ %d", fatLines, compactMetricsTailLines)
	}
	if fatLines == 0 {
		t.Fatal("fat truncation wiped every line")
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// TestCompactMetricsNoBaselineDropsRecords 无持久化基线（从未保存的会话）
// 落读数即丢——没有盘上会话形态就没有事后分析对象；至少不能崩、不能凭空
// 建文件。
func TestCompactMetricsNoBaselineDropsRecords(t *testing.T) {
	dir := t.TempDir()
	a := agentOverForce(t, &fullSendStubProvider{}, foldableSessionOverForce(2))
	a.sess.path = "" // never saved
	a.recordCompactMetrics(foldDeclinedMetricsRecord(CompactionTriggerPressure, "test"))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("no-baseline record must not create files; created: %v", names)
	}
}

// TestCompactMetricsSidecarThreeChainRegistered 验收②（登记面）：三链同
// commit——store 清单含侧车（删除/换位/迁移单一来源）、备份白名单可归档、
// 转录分类不再把它当主转录。差集门禁
// TestSessionBackupAllowlistGateCoversStaticInventory 由清单×白名单联动
// 自动钉住本侧车。
func TestCompactMetricsSidecarThreeChainRegistered(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "session.jsonl")
	want := store.SessionCompactMetricsLog(sessionPath)
	if want == "" || filepath.Base(want) != "session.compactmetrics.jsonl" {
		t.Fatalf("store constructor spelling broken: %q", want)
	}
	var inSidecars bool
	for _, artifact := range store.SessionSidecarFiles(sessionPath) {
		if artifact == want {
			inSidecars = true
			break
		}
	}
	if !inSidecars {
		t.Fatal("store.SessionSidecarFiles must carry the compact metrics log (清单单一来源纪律)")
	}
	if _, ok := sessionBackupAllowlist["compactmetrics.jsonl"]; !ok {
		t.Fatal("sessionBackupAllowlist lost the compactmetrics key — promotion-swap backups would skip it")
	}
	if store.IsSessionTranscriptName(filepath.Base(want)) {
		t.Fatal("compactmetrics sidecar must not classify as a primary transcript")
	}
	// 差集门禁本体（tl1841 判例）：静态清单里每一件都必须在白名单或豁免里
	// ——新侧车两处同 commit 补登后本调用必须零缺口。
	if gaps := tl1841AllowlistGaps(tl1841StaticInventoryRels(t)); len(gaps) > 0 {
		t.Fatalf("inventory×allowlist gate red (三链未同步): %v", gaps)
	}
}

func compactMetricsKindsOf(recs []CompactMetricsRecord) []string {
	kinds := make([]string, 0, len(recs))
	for _, r := range recs {
		kinds = append(kinds, r.Kind)
	}
	return kinds
}
