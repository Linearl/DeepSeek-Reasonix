package sessioncatalog

// tl1485c 回归钉：偏旧（behind）活动戳的 catalog 触发判据与全链自愈。
// tl1485b 只补了「偏新（ahead）」的触发判据（recordFromOrder 的 tail 探针），
// 偏旧形态——UpdatedAt 冻结在转录最后消息之下（tl1485 修复前 collab/detached
// 渠道的死戳存量）——从未进过 repair 队列：counts 全 fresh 的冻结行停留
// valid，队列只取 unknown；装机后用户没再保存过的会话左栏永远显「1 个月」
// （2026-11-08 二轮打回现场）。修复=recordFromOrder 换双向探针
// agent.SessionListingActivityMismatch（behind 也打 unknown+ActivityStale），
// repair 链 settle 的 raise-only 分支把戳抬回消息级。
//
// 统一先跑一次 agent repair 建立权威投影再冻结 UpdatedAt——病灶形态就是
// 「计数全对、只有活动戳错位」。四钉：
//  1. 扫描侧探针把偏旧行打成 unknown + ActivityStale，repair 波次把边车
//     UpdatedAt 抬到消息级最新，再扫描把 last_activity_at 落正确值（全链，
//     修前红）；
//  2. UpdatedAt≈消息级最新的正常行不进队（探针为假，防误伤）；
//  3. 已 valid 的行再次被检出偏旧时，exact 路径不得用旧 valid 行回证
//     （ActivityStale carve-out 对 behind 同样生效），并按「valid→unknown
//     转换」重排队列（修前红）；
//  4. 目录重扫路径同理：preserveKnownSourceStates 不得把探针判定洗回 valid
//     （修前红——这是 tl1485b 只给 exact 路径加 carve-out 留下的洞）。
//
// 修前红口径：钉 1/3/4 在基线上编译可过但行为红（behind 不打标/被洗回）；
// 钉 2 修前修后均绿（防误伤）。

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
)

// TestRecordFromOrderFlagsBehindStampTL1485c 单元钉：偏旧行被打成
// unknown + ActivityStale；正常行（戳在合法 lag 内）停留 valid。
func TestRecordFromOrderFlagsBehindStampTL1485c(t *testing.T) {
	dir := t.TempDir()
	msgAt := time.Now().Add(-48 * time.Hour)
	behind := filepath.Join(dir, "behind.jsonl")
	saveStampedSessionTL1485b(t, behind, msgAt)
	// 冻结形态：戳被盖回 30 天前（消息级最新=msgAt+1min，落后 ≥slack）。
	pumpStampTL1485b(t, behind, msgAt.Add(-30*24*time.Hour))

	target := DirectoryTarget{Path: dir, Scope: "global"}
	record := recordFromOrder(target, orderInfoTL1485b(t, behind))
	if record.TurnsState != TurnsUnknown || !record.ActivityStale {
		t.Fatalf("behind row not flagged: state=%v activityStale=%v", record.TurnsState, record.ActivityStale)
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

// TestBehindStampRowRepairWaveHealsStampTL1485c 全链钉：偏旧行进队 → repair
// 波次把边车抬到消息级最新 → 行回 valid → 再扫描把 last_activity_at 落正确值。
func TestBehindStampRowRepairWaveHealsStampTL1485c(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	msgAt := time.Now().Add(-48 * time.Hour)
	path := filepath.Join(dir, "behind.jsonl")
	saveStampedSessionTL1485b(t, path, msgAt)
	frozen := msgAt.Add(-30 * 24 * time.Hour)
	pumpStampTL1485b(t, path, frozen)

	catalog, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "catalog.sqlite"), DisableRepair: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })
	target := DirectoryTarget{Path: dir, Scope: "global"}
	if err := catalog.IndexSessionPath(ctx, target, path); err != nil {
		t.Fatal(err)
	}
	assertRowTL1485c := func(wantTurns TurnsState, wantRepair string) {
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
	assertRowTL1485c(TurnsUnknown, "pending")

	catalog.runRepairWave(ctx)
	assertRowTL1485c(TurnsValid, "complete")

	meta, ok, err := agent.LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("load meta ok=%v err=%v", ok, err)
	}
	if diff := meta.UpdatedAt.Sub(msgAt.Add(time.Minute)); diff < -time.Minute || diff > time.Minute {
		t.Fatalf("after wave UpdatedAt = %v, want the transcript's last-message time (~%v); frozen %v survived",
			meta.UpdatedAt, msgAt.Add(time.Minute), frozen)
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

// TestReconcileDirectoryKeepsActivityStaleUnknownTL1485c 目录扫描防洗标钉：
// 目录重扫时 preserveKnownSourceStates 不得用库中 valid 行把探针打出的
// unknown（behind 形态）洗回 valid——洗掉就进不了 repair 队列（修前红）。
func TestReconcileDirectoryKeepsActivityStaleUnknownTL1485c(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	msgAt := time.Now().Add(-48 * time.Hour)
	path := filepath.Join(dir, "rescan.jsonl")
	saveStampedSessionTL1485b(t, path, msgAt)
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

	// 冻结复发后走目录扫描（不是 exact 路径）：库中 valid 行就在
	// preserveKnownSourceStates 手边，探针判定必须活下来。
	pumpStampTL1485b(t, path, msgAt.Add(-30*24*time.Hour))
	if err := catalog.ReconcileDirectory(ctx, target); err != nil {
		t.Fatal(err)
	}
	var repair string
	if err := catalog.db.QueryRowContext(ctx, `SELECT turns_state,repair_state FROM catalog_sessions
		WHERE path_key=?`, catalog.pathKey(path)).Scan(&turns, &repair); err != nil {
		t.Fatal(err)
	}
	if turns != TurnsUnknown || repair != "pending" {
		t.Fatalf("rescanned row = turns:%v repair:%s, want unknown/pending — the directory scan washed the probe's finding", turns, repair)
	}
}

// TestBehindStampExactPathDoesNotRecertifyTL1485c 防回证钉：已 valid 的行被
// 再次检出偏旧时，exact 路径不得恢复 valid，并按转换重排队列（pending+0）。
func TestBehindStampExactPathDoesNotRecertifyTL1485c(t *testing.T) {
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

	// 冻结复发：meta-only 再把 UpdatedAt 盖回 30 天前。
	pumpStampTL1485b(t, path, msgAt.Add(-30*24*time.Hour))
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
