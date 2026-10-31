package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/store"
)

// tl 1172 验收落测（sweep 接线侧；agent 侧原语在
// internal/agent/session_events_zst_test.go / session_events_trim_test.go）。
// 全部用注入动作驱动 pass，不碰真实会话目录。

// slimKnobs 造一个开启降级的 pass 配置；compress/trim 由各测试注入。
func slimKnobs() eventsIdleSweepConfig {
	return eventsIdleSweepConfig{enabled: true, factor: 4.0, capMB: 50}
}

func slimWiring(compress func(path string) (int64, int64, error), trim func(path string) (int64, int64, int, error)) eventsIdleSweepSlim {
	return eventsIdleSweepSlim{
		Enabled:  true,
		CapBytes: 50 << 20,
		Compress: compress,
		Trim:     trim,
	}
}

var (
	errSlimFake = errors.New("slim fake failure")
	slimMB      = int64(1) << 20
)

func noCompact(path string) (int64, int64, error) { return 0, 0, nil }

// 验收 1：foldProfitable=false && overLimit 的空闲会话必须走 L2——压缩被调
// 用、结果不再是「left in place」，体积下降如实入账。
func TestSweepSlimTakesOverFoldNoShrinkIdle(t *testing.T) {
	calls := 0
	compress := func(path string) (int64, int64, error) {
		calls++
		return 107 * slimMB, 12 * slimMB, nil // 107MB → 12MB，≥50% 下降
	}
	park := map[string]eventsIdleSweepPark{}
	cands := []eventsIdleSweepCandidate{
		{Path: "big.jsonl", EventsBytes: 107 * slimMB, LiveBytes: 97 * slimMB, LastActivityAt: 1},
	}
	results := runEventsIdleSweepPass(cands, slimKnobs(), park, nil, noCompact, slimWiring(compress, nil), nil)
	if calls != 1 {
		t.Fatalf("idle fold-no-shrink session must slim exactly once, calls=%d", calls)
	}
	if len(results) != 1 || results[0].Skipped {
		t.Fatalf("slim takeover must produce an actioned result, got %+v", results)
	}
	if got := results[0].Freed; got != (107-12)*slimMB {
		t.Fatalf("slim freed = %d, want %d", got, (107-12)*slimMB)
	}
	if len(park) != 1 {
		t.Fatalf("slim success must park the session, park=%v", park)
	}
}

// 验收 2（频繁折叠回归）：压缩落地后，同尺寸同活动戳的后续 tick 不再重压
// （park 闸）——物化回明文的会话不会每 30 分钟被重写一遍。
func TestSweepSlimParkBlocksSameSizeRecompress(t *testing.T) {
	calls := 0
	compress := func(path string) (int64, int64, error) {
		calls++
		return 107 * slimMB, 12 * slimMB, nil
	}
	park := map[string]eventsIdleSweepPark{}
	cands := []eventsIdleSweepCandidate{
		{Path: "big.jsonl", EventsBytes: 107 * slimMB, LiveBytes: 97 * slimMB, LastActivityAt: 1},
	}
	for tick := 0; tick < 3; tick++ {
		results := runEventsIdleSweepPass(cands, slimKnobs(), park, nil, noCompact, slimWiring(compress, nil), nil)
		if tick == 0 {
			if len(results) != 1 || results[0].Skipped {
				t.Fatalf("tick 0 must slim, got %+v", results)
			}
			continue
		}
		if len(results) != 1 || !results[0].Skipped || results[0].Error != "parked: awaiting regrowth or new activity" {
			t.Fatalf("tick %d must park, got %+v", tick, results)
		}
	}
	if calls != 1 {
		t.Fatalf("3 ticks must compress exactly once, got %d", calls)
	}
	// 新活动戳解除 park：允许再压。
	cands[0].LastActivityAt = 2
	runEventsIdleSweepPass(cands, slimKnobs(), park, nil, noCompact, slimWiring(compress, nil), nil)
	if calls != 2 {
		t.Fatalf("new activity must re-arm slim, calls=%d", calls)
	}
}

// tl1285（用户 20261031 01:02 口径）：busy 判据改为「真在工作」，tab 打开
// 不再整轮否决，busy 维护窗口（busyStreak/4-tick）退役。本进程 tab 是否
// busy 由 tick 侧 eventsIdleSweepBusyFor 判定（另测）；pass 层面的契约只有
// 一条：busy 候选一律让路，永不被 slim/L1 碰。
func TestSweepSlimBusyNeverTouched(t *testing.T) {
	calls := 0
	compress := func(path string) (int64, int64, error) {
		calls++
		return 107 * slimMB, 12 * slimMB, nil
	}
	cands := []eventsIdleSweepCandidate{
		{Path: "busy.jsonl", EventsBytes: 107 * slimMB, LiveBytes: 97 * slimMB, LastActivityAt: 1, Busy: true},
	}
	for tick := 0; tick < 4; tick++ {
		runEventsIdleSweepPass(cands, slimKnobs(), map[string]eventsIdleSweepPark{}, nil, noCompact, slimWiring(compress, nil), nil)
	}
	if calls != 0 {
		t.Fatalf("busy sessions must never slim, calls=%d", calls)
	}
}

// tl1285 busy 判据纯函数（用户 20261031 01:02 口径）：tab 打开 ≠ busy；
// 真在工作（ActiveWork）或刚活跃（<10 分钟窗口）才让路；空闲超窗纳入
// sweep；运行态不可读时保守让路；无 tab 的候选交给外部 lease 检查。
func TestEventsIdleSweepBusyForTabCriterion(t *testing.T) {
	now := time.UnixMilli(1790000000000)
	idle := now.Add(-15 * time.Minute)   // 空闲超窗
	recent := now.Add(-3 * time.Minute)  // 窗口内刚活跃
	if !eventsIdleSweepBusyFor(true, true, idle, now, true) {
		t.Fatal("an actively working tab session must stay busy")
	}
	if !eventsIdleSweepBusyFor(true, false, recent, now, true) {
		t.Fatal("a tab session active within the window must stay busy")
	}
	if eventsIdleSweepBusyFor(true, false, idle, now, true) {
		t.Fatal("a tab session idle past the window must enter the sweep (tab open is not busy)")
	}
	if !eventsIdleSweepBusyFor(true, false, idle, now, false) {
		t.Fatal("unreadable runtime state must conservatively stay busy")
	}
	if eventsIdleSweepBusyFor(false, false, recent, now, true) {
		t.Fatal("no-tab candidates are judged by the caller's external lease check, not here")
	}
}

// 验收 5（回归面）：小会话（未超限）与 fold 有收益的会话不进降级——开关开
// 也不改变既有两类的行为；开关关时 fold-no-shrink 维持原样跳过。
func TestSweepSlimBoundaries(t *testing.T) {
	t.Run("under-limit sessions untouched", func(t *testing.T) {
		calls := 0
		compress := func(path string) (int64, int64, error) {
			calls++
			return 1, 1, nil
		}
		cands := []eventsIdleSweepCandidate{
			{Path: "small.jsonl", EventsBytes: 5 * slimMB, LiveBytes: 4 * slimMB, LastActivityAt: 1},
		}
		results := runEventsIdleSweepPass(cands, slimKnobs(), map[string]eventsIdleSweepPark{}, nil, noCompact, slimWiring(compress, nil), nil)
		if calls != 0 || len(results) != 0 {
			t.Fatalf("under-limit must not slim, calls=%d results=%v", calls, results)
		}
	})
	t.Run("fold-profitable still folds (L1 unchanged)", func(t *testing.T) {
		compacted := 0
		compact := func(path string) (int64, int64, error) {
			compacted++
			return 107 * slimMB, 50 * slimMB, nil
		}
		calls := 0
		compress := func(path string) (int64, int64, error) {
			calls++
			return 107 * slimMB, 12 * slimMB, nil
		}
		cands := []eventsIdleSweepCandidate{
			// events 107MB vs live 10.7MB：sweep 口径折叠有收益（live ≤ 90%）。
			{Path: "fold.jsonl", EventsBytes: 107 * slimMB, LiveBytes: 107 * slimMB / 10, LastActivityAt: 1},
		}
		results := runEventsIdleSweepPass(cands, slimKnobs(), map[string]eventsIdleSweepPark{}, nil, compact, slimWiring(compress, nil), nil)
		if compacted != 1 {
			t.Fatalf("L1 fold must run as today, compacted=%d", compacted)
		}
		if calls != 0 {
			t.Fatalf("profitable fold must not degrade to slim, calls=%d", calls)
		}
		if len(results) != 1 || results[0].Skipped || results[0].Freed <= 0 {
			t.Fatalf("L1 result must be a real shrink, got %+v", results)
		}
	})
	t.Run("switch off keeps fold-no-shrink as a plain skip", func(t *testing.T) {
		calls := 0
		compress := func(path string) (int64, int64, error) {
			calls++
			return 1, 1, nil
		}
		off := slimWiring(compress, nil)
		off.Enabled = false
		cands := []eventsIdleSweepCandidate{
			{Path: "big.jsonl", EventsBytes: 107 * slimMB, LiveBytes: 97 * slimMB, LastActivityAt: 1},
		}
		results := runEventsIdleSweepPass(cands, slimKnobs(), map[string]eventsIdleSweepPark{}, nil, noCompact, off, nil)
		if calls != 0 {
			t.Fatalf("switch off must never compress, calls=%d", calls)
		}
		if len(results) != 1 || !results[0].Skipped || results[0].Error != "fold would not shrink" {
			t.Fatalf("switch off must keep the legacy skip, got %+v", results)
		}
	})
}

// L1 跑了但没省到字节（sweep 的 live 代理与真实 rotated content 判断分歧的
// 形态）→ 降级 L2，「任一级成功即停」。
func TestSweepSlimFollowupAfterNoGainFold(t *testing.T) {
	compacted := 0
	compact := func(path string) (int64, int64, error) {
		compacted++
		return 107 * slimMB, 107 * slimMB, nil // 无收益重写
	}
	calls := 0
	compress := func(path string) (int64, int64, error) {
		calls++
		return 107 * slimMB, 12 * slimMB, nil
	}
	cands := []eventsIdleSweepCandidate{
		{Path: "split.jsonl", EventsBytes: 107 * slimMB, LiveBytes: 107 * slimMB / 10, LastActivityAt: 1},
	}
	results := runEventsIdleSweepPass(cands, slimKnobs(), map[string]eventsIdleSweepPark{}, nil, compact, slimWiring(compress, nil), nil)
	if compacted != 1 || calls != 1 {
		t.Fatalf("ladder must run L1 then degrade to L2, compacted=%d calls=%d", compacted, calls)
	}
	if len(results) != 1 || results[0].Skipped || results[0].Freed != (107-12)*slimMB {
		t.Fatalf("final result must be the L2 landing, got %+v", results)
	}
}

// L3：L2 压缩后仍超 cap ⇒ 裁剪 + 重归档。
func TestSweepSlimTrimFollowsStillOverCap(t *testing.T) {
	compressCalls, trimCalls := 0, 0
	compress := func(path string) (int64, int64, error) {
		compressCalls++
		if compressCalls == 1 {
			return 107 * slimMB, 60 * slimMB, nil // 第一压：仍超 50MB cap
		}
		return 60 * slimMB, 20 * slimMB, nil // 裁剪后重归档
	}
	trim := func(path string) (int64, int64, int, error) {
		trimCalls++
		return 60 * slimMB, 55 * slimMB, 3, nil
	}
	park := map[string]eventsIdleSweepPark{}
	cands := []eventsIdleSweepCandidate{
		{Path: "stillbig.jsonl", EventsBytes: 107 * slimMB, LiveBytes: 97 * slimMB, LastActivityAt: 1},
	}
	results := runEventsIdleSweepPass(cands, slimKnobs(), park, nil, noCompact, slimWiring(compress, trim), nil)
	if compressCalls != 2 || trimCalls != 1 {
		t.Fatalf("ladder must compress, trim, re-archive: compress=%d trim=%d", compressCalls, trimCalls)
	}
	if len(results) != 1 || results[0].Skipped || results[0].After != 20*slimMB {
		t.Fatalf("final landing must be the re-archive, got %+v", results)
	}
}

// L2 压缩失败：如实记 skip、不 park、下一 tick 重试（与 L1 失败同一契约）。
func TestSweepSlimCompressFailureRetries(t *testing.T) {
	calls := 0
	compress := func(path string) (int64, int64, error) {
		calls++
		return 0, 0, errSlimFake
	}
	park := map[string]eventsIdleSweepPark{}
	cands := []eventsIdleSweepCandidate{
		{Path: "fail.jsonl", EventsBytes: 107 * slimMB, LiveBytes: 97 * slimMB, LastActivityAt: 1},
	}
	for tick := 0; tick < 2; tick++ {
		results := runEventsIdleSweepPass(cands, slimKnobs(), park, nil, noCompact, slimWiring(compress, nil), nil)
		if len(results) != 1 || !results[0].Skipped {
			t.Fatalf("tick %d must record the failure as a skip, got %+v", tick, results)
		}
		if len(park) != 0 {
			t.Fatalf("failed slim must not park, park=%v", park)
		}
	}
	if calls != 2 {
		t.Fatalf("failed slim must retry next tick, calls=%d", calls)
	}
}

// ---- tl 1172 剩余部分：turn ledger checkpoint 截断的 sweep 接线 ----

// ledgerSeed 造一个带真实 turns.jsonl 侧文件的候选：第二遍循环用
// statFileSize 读 ledger 尺寸，size=0 的候选直接跳过，所以接线测试必须落盘。
func ledgerSeed(t *testing.T) eventsIdleSweepCandidate {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger-case.jsonl")
	if err := os.WriteFile(store.SessionTurnEventLog(path), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("seed turns.jsonl: %v", err)
	}
	return eventsIdleSweepCandidate{Path: path, EventsBytes: 107 * slimMB, LiveBytes: 97 * slimMB, LastActivityAt: 1}
}

// 正交断言（fold-vs-截断）：超限+fold 不划算+空闲的会话，既有 L2 压缩与
// ledger checkpoint 截断各自恰好执行一次，互不替代、互不挤占。
func TestSweepLedgerCompactRunsAlongsideSlimOnOverLimitIdle(t *testing.T) {
	compressCalls, ledgerCalls := 0, 0
	compress := func(path string) (int64, int64, error) {
		compressCalls++
		return 107 * slimMB, 12 * slimMB, nil
	}
	compactLedger := func(path string) (int64, int64, error) {
		ledgerCalls++
		return 9 << 20, 1 << 20, nil
	}
	slim := slimWiring(compress, nil)
	slim.CompactLedger = compactLedger
	ledgerPark := map[string]int64{}
	results := runEventsIdleSweepPass([]eventsIdleSweepCandidate{ledgerSeed(t)}, slimKnobs(),
		map[string]eventsIdleSweepPark{}, nil, noCompact, slim, ledgerPark)
	if compressCalls != 1 || ledgerCalls != 1 {
		t.Fatalf("slim=%d ledger=%d, want 1/1 (orthogonal actions on the same over-limit session)", compressCalls, ledgerCalls)
	}
	if len(results) != 1 || results[0].Skipped {
		t.Fatalf("slim result must stay actioned, got %+v", results)
	}
	if len(ledgerPark) != 1 {
		t.Fatalf("ledger success must park, ledgerPark=%v", ledgerPark)
	}
}

func TestSweepLedgerCompactSkipsBusyAndParkedSessions(t *testing.T) {
	ledgerCalls := 0
	compactLedger := func(path string) (int64, int64, error) {
		ledgerCalls++
		return 9 << 20, 1 << 20, nil
	}
	slim := eventsIdleSweepSlim{Enabled: true, CapBytes: 50 << 20, Compress: noCompact, CompactLedger: compactLedger}
	busy := ledgerSeed(t)
	busy.Busy = true
	runEventsIdleSweepPass([]eventsIdleSweepCandidate{busy}, slimKnobs(),
		map[string]eventsIdleSweepPark{}, nil, noCompact, slim, map[string]int64{})
	if ledgerCalls != 0 {
		t.Fatalf("busy session ledger must never be touched, calls=%d", ledgerCalls)
	}

	// park 命中：落地尺寸 +10% 内不重截。键=候选完整路径（与 pass 记账一致）。
	parked := ledgerSeed(t)
	// 当前 ledger 尺寸是字面 "{}\n"（3 字节），远小于 park 值的 1.1 倍 ⇒ parked。
	ledgerPark := map[string]int64{parked.Path: 1 << 20}
	calls := ledgerCalls
	runEventsIdleSweepPass([]eventsIdleSweepCandidate{parked}, slimKnobs(),
		map[string]eventsIdleSweepPark{}, nil, noCompact, slim, ledgerPark)
	if ledgerCalls != calls {
		t.Fatalf("parked ledger must not be re-compacted, calls=%d", ledgerCalls)
	}
}

func TestSweepLedgerCompactFailureRetriesWithoutParking(t *testing.T) {
	ledgerCalls := 0
	compactLedger := func(path string) (int64, int64, error) {
		ledgerCalls++
		return 0, 0, errSlimFake
	}
	slim := eventsIdleSweepSlim{Enabled: true, CapBytes: 50 << 20, Compress: noCompact, CompactLedger: compactLedger}
	ledgerPark := map[string]int64{}
	for tick := 0; tick < 2; tick++ {
		runEventsIdleSweepPass([]eventsIdleSweepCandidate{ledgerSeed(t)}, slimKnobs(),
			map[string]eventsIdleSweepPark{}, nil, noCompact, slim, ledgerPark)
		if len(ledgerPark) != 0 {
			t.Fatalf("failed ledger compact must not park, ledgerPark=%v", ledgerPark)
		}
	}
	if ledgerCalls != 2 {
		t.Fatalf("failed ledger compact must retry next tick, calls=%d", ledgerCalls)
	}
}

// 开关关态（CompactLedger 不注入）：第二遍循环整体跳过，零行为。
func TestSweepLedgerCompactOffByDefault(t *testing.T) {
	ledgerCalls := 0
	_ = ledgerCalls
	results := runEventsIdleSweepPass([]eventsIdleSweepCandidate{ledgerSeed(t)}, slimKnobs(),
		map[string]eventsIdleSweepPark{}, nil, noCompact, eventsIdleSweepSlim{}, map[string]int64{})
	if ledgerCalls != 0 {
		t.Fatalf("off switch must never compact the ledger, calls=%d", ledgerCalls)
	}
	if len(results) != 1 || !results[0].Skipped || results[0].Error != "fold would not shrink" {
		t.Fatalf("off switch keeps the pre-existing L1-refusal shape, got %+v", results)
	}
}
