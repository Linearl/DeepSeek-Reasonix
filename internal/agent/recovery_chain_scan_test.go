package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// tl1355：DAG 索引删除后链信息内容扫描合成的验收测试。
//
// 验收（任务书 1355）：① 面板候选链显示必须修复=真实条数/轮次/预览；
// ② 无 DAG 也要能合成最长链——链关系改为会话文件内容扫描合成（digest/
// 前缀覆盖判定），不因删 DAG 丢能力。本文件钉 agent 层四件事：
//  a) 快照读数方法（PreviewTurns/LastActivityTime）与 DAG 时代 head 记录
//     同口径（tail-first 预览、用户轮计数、最后活动时间戳）；
//  b) ScanSessionChainViews 的真实读数与覆盖标注（前缀覆盖 → CoveredBy）；
//  c) 等长互覆盖只标后者（先者胜，主转录保住容器身份）；
//  d) 预算降级（超 ChainScanMaxBytes → 仅身份行）与残缺日志 tolerant
//     兜底行（计数真实、不参与覆盖判定）。

func TestSessionContentSnapshotChainReadings(t *testing.T) {
	base := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "第一问", CreatedAt: base.UnixMilli()},
		{Role: provider.RoleAssistant, Content: "第一答", CreatedAt: base.Add(time.Second).UnixMilli()},
		{Role: provider.RoleUser, Content: "第二问", CreatedAt: base.Add(2 * time.Second).UnixMilli()},
		{Role: provider.RoleAssistant, Content: "第二答", CreatedAt: base.Add(3 * time.Second).UnixMilli()},
	}
	snap := SessionContentSnapshot{messages: msgs, digest: "deadbeef"}
	if snap.Len() != 4 {
		t.Fatalf("Len = %d, want 4", snap.Len())
	}
	if snap.Digest() != "deadbeef" {
		t.Fatalf("Digest = %q, want deadbeef", snap.Digest())
	}
	preview, turns := snap.PreviewTurns()
	if turns != 2 {
		t.Fatalf("turns = %d, want 2 (两个用户轮)", turns)
	}
	// tl 1177 tail-first：预览取最新的可读用户轮。
	if !strings.Contains(preview, "第二问") {
		t.Fatalf("preview = %q, want tail-first (第二问)", preview)
	}
	if la := snap.LastActivityTime(); !la.Equal(base.Add(3 * time.Second)) {
		t.Fatalf("LastActivityTime = %v, want %v（最后一条带时间戳的消息）", la, base.Add(3*time.Second))
	}
	// 全零 CreatedAt → 零时间：调用方落到文件 mtime，不编造。
	zeroSnap := SessionContentSnapshot{messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}}}
	if !zeroSnap.LastActivityTime().IsZero() {
		t.Fatalf("messages without timestamps must report the zero time, got %v", zeroSnap.LastActivityTime())
	}
}

// saveScanFixture 用真实 Save 写一份 schema-1 会话（与面板生产数据同形），
// 返回 (path, 用户轮数)。
func saveScanFixture(t *testing.T, dir, name string, pairs int) string {
	t.Helper()
	sess := NewSession("sys")
	base := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < pairs; i++ {
		sess.Add(provider.Message{
			Role:      provider.RoleUser,
			Content:   "排查问题 " + string(rune('A'+i)),
			CreatedAt: base.Add(time.Duration(i) * time.Minute).UnixMilli(),
		})
		sess.Add(provider.Message{
			Role:      provider.RoleAssistant,
			Content:   "处理完成 " + string(rune('A'+i)),
			CreatedAt: base.Add(time.Duration(i)*time.Minute + 30*time.Second).UnixMilli(),
		})
	}
	path := filepath.Join(dir, name)
	if err := sess.Save(path); err != nil {
		t.Fatalf("save fixture %s: %v", name, err)
	}
	return path
}

func TestScanSessionChainViewsCoverage(t *testing.T) {
	dir := t.TempDir()
	mainPath := saveScanFixture(t, dir, "scan-main.jsonl", 3)
	copyPath := saveScanFixture(t, dir, "scan-copy.jsonl", 1)

	views := ScanSessionChainViews([]string{mainPath, copyPath})
	if len(views) != 2 {
		t.Fatalf("views = %d, want 2", len(views))
	}
	main, copy := views[0], views[1]
	if main.Path != mainPath || copy.Path != copyPath {
		t.Fatalf("views order broken: %+v", views)
	}
	if !main.Loadable || !main.Readable {
		t.Fatalf("main must scan strict-loadable: %+v", main)
	}
	// 快照 Len 含 NewSession 的引导 system 消息（3 轮问答 + 1 system = 7）；
	// 轮次只数用户轮（system/assistant 不计）。
	if main.MessageCount != 7 {
		t.Fatalf("main messageCount = %d, want 7（3 轮问答 + 引导 system）", main.MessageCount)
	}
	if main.Turns != 3 {
		t.Fatalf("main turns = %d, want 3", main.Turns)
	}
	if !strings.Contains(main.Preview, "排查问题 C") {
		t.Fatalf("main preview = %q, want tail-first（最后一轮用户文本）", main.Preview)
	}
	if main.Digest == "" {
		t.Fatal("main digest must be set for loadable views")
	}
	// 最后活动 = 最后一条消息的 CreatedAt（9:02:30），不是编造的零值。
	wantActivity := time.Date(2026, 11, 1, 9, 2, 30, 0, time.UTC)
	if !main.LastActivity.Equal(wantActivity) {
		t.Fatalf("main lastActivity = %v, want %v", main.LastActivity, wantActivity)
	}
	// 副本（主转录的严格前缀）被主转录覆盖；主转录不被任何候选覆盖。
	if !copy.Loadable {
		t.Fatalf("copy must scan strict-loadable: %+v", copy)
	}
	if copy.CoveredBy != mainPath {
		t.Fatalf("copy CoveredBy = %q, want %q（前缀覆盖判定）", copy.CoveredBy, mainPath)
	}
	if main.CoveredBy != "" {
		t.Fatalf("main must not be covered, got %q", main.CoveredBy)
	}
}

func TestScanSessionChainViewsEqualContentTieBreak(t *testing.T) {
	dir := t.TempDir()
	first := saveScanFixture(t, dir, "tie-first.jsonl", 2)
	second := saveScanFixture(t, dir, "tie-second.jsonl", 2)
	views := ScanSessionChainViews([]string{first, second})
	if len(views) != 2 {
		t.Fatalf("views = %d, want 2", len(views))
	}
	if views[1].CoveredBy != first {
		t.Fatalf("second CoveredBy = %q, want %q（等长互覆盖只标后者）", views[1].CoveredBy, first)
	}
	if views[0].CoveredBy != "" {
		t.Fatalf("first must keep the container role, got %q", views[0].CoveredBy)
	}
}

func TestScanSessionChainViewsBudgetDegrades(t *testing.T) {
	dir := t.TempDir()
	path := saveScanFixture(t, dir, "budget.jsonl", 1)
	old := ChainScanMaxBytes
	ChainScanMaxBytes = 1
	defer func() { ChainScanMaxBytes = old }()

	views := ScanSessionChainViews([]string{path})
	if len(views) != 1 {
		t.Fatalf("views = %d, want 1", len(views))
	}
	v := views[0]
	// 协调线输入①：降级行的条数由纯行计数填真实值（不解析 JSON），不再是
	// 无读数身份行。checkpoint 3 行 = 引导 system + 1 轮问答（2 条消息）。
	if v.Readable || v.Loadable {
		t.Fatalf("over-budget file must skip the full parse: %+v", v)
	}
	if v.MessageCount != 3 {
		t.Fatalf("degraded row must carry the real line count, got %d want 3", v.MessageCount)
	}
	if v.Turns != 0 || v.Preview != "" {
		t.Fatalf("degraded row has no full-content readings (turns/preview): %+v", v)
	}
	if v.CoveredBy != "" {
		t.Fatalf("identity rows must not carry coverage claims: %+v", v)
	}
	if v.LastActivity.IsZero() {
		t.Fatalf("degraded row must carry the mtime as last-activity: %+v", v)
	}
}

// 协调线输入②：10 秒时间预算——预算耗尽后余行整文件解析即停，纯计数降级，
// 面板展开多副本会话的等待有上界。
func TestScanSessionChainViewsTimeBudgetQuickCounts(t *testing.T) {
	dir := t.TempDir()
	first := saveScanFixture(t, dir, "tb-first.jsonl", 2)
	second := saveScanFixture(t, dir, "tb-second.jsonl", 1)
	old := ChainScanTimeBudget
	ChainScanTimeBudget = time.Nanosecond
	defer func() { ChainScanTimeBudget = old }()

	views := ScanSessionChainViews([]string{first, second})
	if len(views) != 2 {
		t.Fatalf("views = %d, want 2", len(views))
	}
	for i, v := range views {
		if v.Readable || v.Loadable {
			t.Fatalf("view %d must skip the full parse under an exhausted budget: %+v", i, v)
		}
		if v.MessageCount == 0 {
			t.Fatalf("view %d must carry the real quick line count: %+v", i, v)
		}
		if v.Turns != 0 || v.Preview != "" || v.CoveredBy != "" {
			t.Fatalf("quick-count rows have no full-content readings or coverage: %+v", v)
		}
	}
}

func TestScanSessionChainViewsTolerantFallback(t *testing.T) {
	dir := t.TempDir()
	path := saveScanFixture(t, dir, "damaged.jsonl", 2)
	// 损坏事件日志（追加一条残缺记录）：重放提前停止并带 damaged 标记——
	// 严格快照必须拒绝（eventLogDamaged fail-closed），tolerant 兜底仍读出
	// 停止点前的可读前缀（replay 返回 damaged=true + 部分消息 + nil error）。
	f, err := os.OpenFile(store.SessionEventLog(path), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{\"broken\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	views := ScanSessionChainViews([]string{path})
	if len(views) != 1 {
		t.Fatalf("views = %d, want 1", len(views))
	}
	v := views[0]
	if v.Loadable {
		t.Fatalf("damaged log must refuse the strict snapshot: %+v", v)
	}
	if !v.Readable {
		t.Fatalf("tolerant loader must still count the readable prefix: %+v", v)
	}
	if v.MessageCount == 0 || v.Turns == 0 {
		t.Fatalf("tolerant row must carry real readings: %+v", v)
	}
	// fail-closed：残缺日志不产生覆盖声明，也不产生内容指纹。
	if v.CoveredBy != "" || v.Digest != "" {
		t.Fatalf("damaged logs must not claim coverage or digest: %+v", v)
	}
	if v.LastActivity.IsZero() {
		t.Fatalf("tolerant row must carry a real last-activity reading: %+v", v)
	}
}
