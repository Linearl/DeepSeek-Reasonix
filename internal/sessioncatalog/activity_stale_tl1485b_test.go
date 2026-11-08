package sessioncatalog

// tl1485b 回归钉：偏新 stamp 的 catalog 触发判据。存量病灶在投影完全 fresh
// 的行上（2026-11-08 实盘 35/35 全 fresh），修复队列按 turns_state='unknown'
// 取件——不补判据就永远不碰它们。三钉：
//  1. 扫描侧探针把偏新行打成 unknown + ActivityStale，repair 波次把边车
//     UpdatedAt 降到消息级最新，再扫描把 last_activity_at 落正确值（全链）；
//  2. UpdatedAt≈消息级最新的正常行不进队（探针为假，钉① catalog 级）；
//  3. 已 valid 的行再次被检出偏新时，exact 路径不得用旧 valid 行回证
//     （ActivityStale carve-out），并按「valid→unknown 转换」重排队列。
//
// 统一先跑一次 agent repair 建立权威投影再泵抬 UpdatedAt——病灶形态就是
// 「计数全对、只有活动戳偏新」。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
)

func saveStampedSessionTL1485b(t *testing.T, path string, msgAt time.Time) {
	t.Helper()
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "question", CreatedAt: msgAt.UnixMilli()},
		{Role: provider.RoleAssistant, Content: "answer", CreatedAt: msgAt.Add(time.Minute).UnixMilli()},
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	enc := json.NewEncoder(f)
	for _, m := range msgs {
		if err := enc.Encode(m); err != nil {
			_ = f.Close()
			t.Fatalf("encode: %v", err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// 建立权威投影（ledger/projection/活动戳全部对齐到消息级最新）。
	if result, err := agent.RepairSessionListingProjection(context.Background(), path); err != nil || result.Status != agent.SessionListingRepairApplied {
		t.Fatalf("seed repair = %+v err=%v", result, err)
	}
}

func pumpStampTL1485b(t *testing.T, path string, stamp time.Time) {
	t.Helper()
	// 泵形态：meta-only 写入只抬 UpdatedAt，投影字段原样保留。
	if err := agent.UpdateBranchMeta(path, false, func(meta *agent.BranchMeta) error {
		meta.UpdatedAt = stamp
		return nil
	}); err != nil {
		t.Fatalf("pump stamp: %v", err)
	}
}

func orderInfoTL1485b(t *testing.T, path string) agent.SessionOrderInfo {
	t.Helper()
	order, err := agent.ListSessionOrder(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, info := range order {
		if info.Path == path {
			return info
		}
	}
	t.Fatalf("session %s not discovered", path)
	return agent.SessionOrderInfo{}
}

// TestRecordFromOrderFlagsInflatedStampTL1485b 单元钉：偏新行被打成
// unknown + ActivityStale；正常行停留 valid。
func TestRecordFromOrderFlagsInflatedStampTL1485b(t *testing.T) {
	dir := t.TempDir()
	msgAt := time.Now().Add(-48 * time.Hour)
	inflated := filepath.Join(dir, "inflated.jsonl")
	saveStampedSessionTL1485b(t, inflated, msgAt)
	pumpStampTL1485b(t, inflated, time.Now())

	target := DirectoryTarget{Path: dir, Scope: "global"}
	record := recordFromOrder(target, orderInfoTL1485b(t, inflated))
	if record.TurnsState != TurnsUnknown || !record.ActivityStale {
		t.Fatalf("inflated row not flagged: state=%v activityStale=%v", record.TurnsState, record.ActivityStale)
	}

	healthy := filepath.Join(dir, "healthy.jsonl")
	saveStampedSessionTL1485b(t, healthy, msgAt)
	// 正常形态：消息落盘后 meta 盖一次同源时间（分钟级落盘延迟）。
	pumpStampTL1485b(t, healthy, msgAt.Add(2*time.Minute))
	record = recordFromOrder(target, orderInfoTL1485b(t, healthy))
	if record.TurnsState != TurnsValid || record.ActivityStale {
		t.Fatalf("healthy row flagged: state=%v activityStale=%v", record.TurnsState, record.ActivityStale)
	}
}

// TestActivityStaleRowRepairWaveHealsStampTL1485b 全链钉：偏新行进队 → repair
// 波次把边车降到消息级最新 → 行回 valid → 再扫描把 last_activity_at 落正确值。
func TestActivityStaleRowRepairWaveHealsStampTL1485b(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	msgAt := time.Now().Add(-48 * time.Hour)
	path := filepath.Join(dir, "inflated.jsonl")
	saveStampedSessionTL1485b(t, path, msgAt)
	pumpStampTL1485b(t, path, time.Now())

	catalog, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "catalog.sqlite"), DisableRepair: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })
	target := DirectoryTarget{Path: dir, Scope: "global"}
	if err := catalog.IndexSessionPath(ctx, target, path); err != nil {
		t.Fatal(err)
	}
	assertRow := func(wantTurns TurnsState, wantRepair string) {
		t.Helper()
		var turns TurnsState
		var repair string
		if err := catalog.db.QueryRowContext(ctx, `SELECT turns_state,repair_state FROM catalog_sessions
			WHERE path_key=?`, catalog.pathKey(path)).Scan(&turns, &repair); err != nil {
			t.Fatal(err)
		}
		if turns != wantTurns || repair != wantRepair {
			t.Fatalf("row = turns:%v repair:%s, want %v/%s", turns, repair, wantTurns, wantRepair)
		}
	}
	assertRow(TurnsUnknown, "pending")

	catalog.runRepairWave(ctx)
	assertRow(TurnsValid, "complete")

	meta, ok, err := agent.LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("load meta ok=%v err=%v", ok, err)
	}
	if diff := meta.UpdatedAt.Sub(msgAt.Add(time.Minute)); diff < -time.Minute || diff > time.Minute {
		t.Fatalf("after wave UpdatedAt = %v, want the transcript's last-message time (~%v)",
			meta.UpdatedAt, msgAt.Add(time.Minute))
	}

	// repair 后目录再扫描：边车已是正确值，投影把正确值落到 last_activity_at。
	if err := catalog.ReconcileDirectory(ctx, target); err != nil {
		t.Fatal(err)
	}
	var lastActivity int64
	if err := catalog.db.QueryRowContext(ctx, `SELECT last_activity_at FROM catalog_sessions
		WHERE path_key=?`, catalog.pathKey(path)).Scan(&lastActivity); err != nil {
		t.Fatal(err)
	}
	if want := msgAt.Add(time.Minute).UnixMilli(); lastActivity < want-time.Minute.Milliseconds() || lastActivity > want+time.Minute.Milliseconds() {
		t.Fatalf("last_activity_at = %v, want ~%v", time.UnixMilli(lastActivity), time.UnixMilli(want))
	}
}

// TestActivityStaleExactPathDoesNotRecertifyTL1485b 防回证钉：已 valid 的行被
// 再次检出偏新时，exact 路径不得恢复 valid，并按转换重排队列（pending+0）。
func TestActivityStaleExactPathDoesNotRecertifyTL1485b(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	msgAt := time.Now().Add(-48 * time.Hour)
	path := filepath.Join(dir, "relapsed.jsonl")
	saveStampedSessionTL1485b(t, path, msgAt)
	// 先落一个正常时间让首扫成为 valid 行。
	pumpStampTL1485b(t, path, msgAt.Add(2*time.Minute))
	catalog, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "catalog.sqlite"), DisableRepair: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })
	target := DirectoryTarget{Path: dir, Scope: "global"}
	if err := catalog.IndexSessionPath(ctx, target, path); err != nil {
		t.Fatal(err)
	}
	var turns TurnsState
	if err := catalog.db.QueryRowContext(ctx, `SELECT turns_state FROM catalog_sessions
		WHERE path_key=?`, catalog.pathKey(path)).Scan(&turns); err != nil {
		t.Fatal(err)
	}
	if turns != TurnsValid {
		t.Fatalf("seed row = %v, want valid", turns)
	}

	// 泵复发：meta-only 再把 UpdatedAt 抬到 50h 后。
	pumpStampTL1485b(t, path, msgAt.Add(50*time.Hour))
	if err := catalog.IndexSessionPath(ctx, target, path); err != nil {
		t.Fatal(err)
	}
	var repair string
	if err := catalog.db.QueryRowContext(ctx, `SELECT turns_state,repair_state FROM catalog_sessions
		WHERE path_key=?`, catalog.pathKey(path)).Scan(&turns, &repair); err != nil {
		t.Fatal(err)
	}
	if turns != TurnsUnknown || repair != "pending" {
		t.Fatalf("relapsed row = turns:%v repair:%s, want unknown/pending — the exact path re-certified a measured defect", turns, repair)
	}
}
