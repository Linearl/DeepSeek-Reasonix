package agent

// tl1485c 回归钉：偏旧（behind）活动戳的 repair 自愈。tl1485 把「UpdatedAt
// 冻结在转录最后消息之下」的写入侧修在了提交漏斗（persistSessionListing
// Projection 的 raise-only），tl1485b 给存量自愈补了「偏新（ahead）」触发判据
// ——但偏旧形态没有任何触发判据：counts 全 fresh 的冻结行是 valid，repair
// 队列只取 unknown；repair 的 index 分支还把这种行 AlreadyCurrent 短路。装机
// 之后用户没有再次保存过的会话就永远显「1 个月」（2026-11-08 二轮打回现场）。
// 修复=双向探针 SessionListingActivityMismatch（一次尾部读同判两向）接入
// recordFromOrder 与 repairSessionListingFromIndex；settle 的 raise-only 分支
// （tl1485 已有）负责把偏旧戳抬回消息级。
//
// 四枚钉：
//  1. counts 全 fresh + UpdatedAt 落后消息级 ≥slack → repair 必须走 replay
//     把戳抬到消息级（修前红：AlreadyCurrent 短路，戳原样保留）；
//  2. 落后在 slack 内（合法 meta-only 写延迟）→ 不动戳（防误伤，双向恒绿）；
//  3. 偏新形态（tl1485b 行为）仍被 repair 降回消息级（回归保护）；
//  4. 探针单元钉：behind/ahead/合法lag/无时间戳 四象限各归其位。
//
// 修前红口径：本文件引用 tl1485c 新符号 SessionListingActivityMismatch——
// 在基线（c1da49189）上放本文件跑 = 编译错 undefined，即修前红。

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// seedFreshProjectionTL1485c 用一次真实 save 建立「计数/ledger/投影/活动戳全
// 对齐到消息级最新」的权威基线（与 tl1485b 钉的手法同源：病灶形态就是计数
// 全对、只有活动戳错位）。
func seedFreshProjectionTL1485c(t *testing.T, path string, msgAt time.Time) {
	t.Helper()
	s := NewSession("")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "question", CreatedAt: msgAt.UnixMilli()})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "answer", CreatedAt: msgAt.Add(time.Minute).UnixMilli()})
	if err := s.Save(path); err != nil {
		t.Fatalf("seed save: %v", err)
	}
}

// freezeStampTL1485c 模拟存量冻结：meta-only 写入只改 UpdatedAt，投影字段
// 原样保留（tl1485 前的死戳形态——collab/detached 渠道的消息进来时 UpdatedAt
// 不动）。
func freezeStampTL1485c(t *testing.T, path string, stamp time.Time) {
	t.Helper()
	if err := UpdateBranchMeta(path, false, func(meta *BranchMeta) error {
		meta.UpdatedAt = stamp
		return nil
	}); err != nil {
		t.Fatalf("freeze stamp: %v", err)
	}
}

// TestListingRepairHealsBehindStampTL1485c 是二轮打回的事故钉：counts 全
// fresh、UpdatedAt 冻结在消息级之下 30 天的会话，repair 必须把戳抬到转录
// 自己的最后消息时间——不允许 AlreadyCurrent 短路把冻结值永远留在左栏。
func TestListingRepairHealsBehindStampTL1485c(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "behind.jsonl")
	msgAt := time.Now().Add(-48 * time.Hour)
	seedFreshProjectionTL1485c(t, path, msgAt)
	frozen := msgAt.Add(-30 * 24 * time.Hour)
	freezeStampTL1485c(t, path, frozen)

	result, err := RepairSessionListingProjection(context.Background(), path)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if result.Status != SessionListingRepairApplied {
		t.Fatalf("repair status = %v, want applied — a behind-stamp row must reach the replay branch, got %v", result.Status, result)
	}
	meta, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("load meta: ok=%v err=%v", ok, err)
	}
	want := msgAt.Add(time.Minute)
	if got := meta.UpdatedAt.UnixMilli(); got < want.Add(-2*time.Hour).UnixMilli() || got > want.Add(2*time.Hour).UnixMilli() {
		t.Fatalf("UpdatedAt = %v, want the transcript's last-message time (~%v); the frozen value %v survived the repair", meta.UpdatedAt, want, frozen)
	}
}

// TestListingRepairLeavesLegitimateLagUntouchedTL1485c 防误伤钉：戳落后消息
// 5 分钟是合法的 meta-only 写延迟（in-flight turn 标记、collab 回执），必须
// 保持 AlreadyCurrent、一字不动。
func TestListingRepairLeavesLegitimateLagUntouchedTL1485c(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lag.jsonl")
	msgAt := time.Now().Add(-48 * time.Hour)
	seedFreshProjectionTL1485c(t, path, msgAt)
	lagged := msgAt.Add(time.Minute).Add(-5 * time.Minute)
	freezeStampTL1485c(t, path, lagged)

	result, err := RepairSessionListingProjection(context.Background(), path)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if result.Status != SessionListingRepairAlreadyCurrent {
		t.Fatalf("repair status = %v, want already_current for a within-slack lag", result.Status)
	}
	meta, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("load meta: ok=%v err=%v", ok, err)
	}
	if got := meta.UpdatedAt.UnixMilli(); got < lagged.Add(-time.Minute).UnixMilli() || got > lagged.Add(time.Minute).UnixMilli() {
		t.Fatalf("UpdatedAt = %v moved off the legitimate lag value %v", meta.UpdatedAt, lagged)
	}
}

// TestListingRepairStillHealsAheadStampTL1485c 回归保护钉：tl1485b 的偏新
// 自愈（死 meta-only 泵抬高的戳降回消息级）在双向探针下行为不变。
func TestListingRepairStillHealsAheadStampTL1485c(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ahead.jsonl")
	msgAt := time.Now().Add(-48 * time.Hour)
	seedFreshProjectionTL1485c(t, path, msgAt)
	freezeStampTL1485c(t, path, time.Now()) // 泵抬高 48h ≥ slack

	if _, err := RepairSessionListingProjection(context.Background(), path); err != nil {
		t.Fatalf("repair: %v", err)
	}
	meta, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("load meta: ok=%v err=%v", ok, err)
	}
	want := msgAt.Add(time.Minute)
	if got := meta.UpdatedAt.UnixMilli(); got < want.Add(-2*time.Hour).UnixMilli() || got > want.Add(2*time.Hour).UnixMilli() {
		t.Fatalf("UpdatedAt = %v, want the inflated stamp dropped to the last-message time ~%v", meta.UpdatedAt, want)
	}
}

// writeStampProbeTranscriptTL1485c 写一个只有 createdAt 字段有效的最小转录，
// 供探针单元钉使用（probeTailCreatedAt 只解析 createdAt）。
func writeStampProbeTranscriptTL1485c(t *testing.T, path string, createdAtMilli int64) {
	t.Helper()
	line := `{"role":"user","content":"probe","createdAt":` + strconv.FormatInt(createdAtMilli, 10) + "}\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatalf("write probe transcript: %v", err)
	}
}

// TestSessionListingActivityMismatchDirectionsTL1485c 探针单元钉：一次尾部读
// 必须同时给出两个方向的判定，四象限各归其位。
func TestSessionListingActivityMismatchDirectionsTL1485c(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	// behind：转录最新消息比戳新 2h ≥ slack（冻结戳形态）。
	behindPath := filepath.Join(dir, "behind.jsonl")
	writeStampProbeTranscriptTL1485c(t, behindPath, now.UnixMilli())
	behind, ahead := SessionListingActivityMismatch(behindPath, now.Add(-2*time.Hour))
	if !behind || ahead {
		t.Fatalf("behind form: behind=%v ahead=%v, want behind only", behind, ahead)
	}

	// ahead：戳比转录最新消息新 2h ≥ slack（tl1485b 死泵形态）。
	aheadPath := filepath.Join(dir, "ahead.jsonl")
	writeStampProbeTranscriptTL1485c(t, aheadPath, now.Add(-2*time.Hour).UnixMilli())
	behind, ahead = SessionListingActivityMismatch(aheadPath, now)
	if behind || !ahead {
		t.Fatalf("ahead form: behind=%v ahead=%v, want ahead only", behind, ahead)
	}

	// 合法 lag：戳落后 5 分钟 < slack → 双 false。
	lagPath := filepath.Join(dir, "lag.jsonl")
	writeStampProbeTranscriptTL1485c(t, lagPath, now.UnixMilli())
	behind, ahead = SessionListingActivityMismatch(lagPath, now.Add(-5*time.Minute))
	if behind || ahead {
		t.Fatalf("lag form: behind=%v ahead=%v, want both false", behind, ahead)
	}

	// 无证据：转录没有消息级时间戳 → 双 false（缺证据不乱判）。
	legacyPath := filepath.Join(dir, "legacy.jsonl")
	if err := os.WriteFile(legacyPath, []byte("{\"role\":\"user\",\"content\":\"legacy\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	behind, ahead = SessionListingActivityMismatch(legacyPath, now.Add(-30*24*time.Hour))
	if behind || ahead {
		t.Fatalf("legacy form: behind=%v ahead=%v, want both false on missing evidence", behind, ahead)
	}

	// 零值戳 → 双 false（无基准可比）。
	behind, ahead = SessionListingActivityMismatch(behindPath, time.Time{})
	if behind || ahead {
		t.Fatalf("zero stamp: behind=%v ahead=%v, want both false", behind, ahead)
	}
}
