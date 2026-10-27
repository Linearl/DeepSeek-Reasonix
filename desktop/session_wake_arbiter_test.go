package main

// 任务 1206 验收映射（开轮仲裁抽象层，579 族 SSOT）：
// ① 三态判定 canWakeSession：tab（skeleton 语义）/runtime（detached+buildCancel）/
//    lease（StartupErrLeaseHeld）一个函数回答「可开轮?谁负责?排除原因」——矩阵
//    断言（1185 判据原样保留，扩 lease 臂）。
// ② 幂等防双开：claimWake 同会话并发认领恰一胜者；桥+drain 同窗竞态时后到者
//    拿 wake_in_flight 短路，wakeDormantRuntime 按竞态让位（ErrTurnRunning，
//    不计 strike 不写 570）——887 双开形态的收口。
// ③ 记账单点：四认领方统一回写，WakeLedgerFor 单点可查——1007「已读没开轮、
//    原因无处查」病灶的回答面。
// ④ 源码守卫：四触发源各 0 处直接判定 runtime/tab/lease 状态（必须经仲裁层），
//    断言+下限自证（扫描面非空、守卫可被合成反例打红、仲裁层原语在位）。
//
// 既有 579/887/1185/868 链路零回归由既有测试钉住（包级跑）。

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/control"
	"reasonix/internal/sessioncollab"
)

// 验收①：三态判定矩阵。1185 判据（Ctrl!=nil || buildCancel!=nil = 活）原样，
// 扩 lease 臂与责任方分类。
func TestCanWakeSessionTriStateVerdicts(t *testing.T) {
	sp := filepath.Join(t.TempDir(), "sessions", "s1206", "session.jsonl")
	other := filepath.Join(t.TempDir(), "sessions", "other", "session.jsonl")
	ctrl := pathedStub{path: sp}

	cases := []struct {
		name     string
		tabs     map[string]*WorkspaceTab
		detached map[string]*WorkspaceTab
		path     string
		want     wakeVerdict
		why      string
	}{
		{
			name: "no surface at all is stand-up-able",
			path: sp,
			want: wakeVerdict{Owner: wakeOwnerNone},
			why:  "never-opened session: nothing owns it, drain/bridge may stand it up (887)",
		},
		{
			name: "restored skeleton owns nothing",
			tabs: map[string]*WorkspaceTab{"tab-skel": {ID: "tab-skel", SessionPath: sp}},
			path: sp,
			want: wakeVerdict{Owner: wakeOwnerNone, HasSurface: true},
			why:  "1185: a published skeleton is a dormant candidate, not coverage",
		},
		{
			name: "in-flight build owns it (building)",
			tabs: map[string]*WorkspaceTab{"tab-boot": {ID: "tab-boot", SessionPath: sp, buildCancel: func() {}}},
			path: sp,
			want: wakeVerdict{Owner: wakeOwnerBuilding, HasSurface: true, RuntimeLive: true},
			why:  "a booting tab has machinery running — waking again would double-build",
		},
		{
			name: "visible live tab owns it",
			tabs: map[string]*WorkspaceTab{"tab-live": {ID: "tab-live", SessionPath: sp, Ctrl: ctrl}},
			path: sp,
			want: wakeVerdict{Owner: wakeOwnerLiveTab, HasSurface: true, RuntimeLive: true},
			why:  "the user's tab is the responsible party",
		},
		{
			name:     "detached runtime owns it",
			detached: map[string]*WorkspaceTab{"tab-det": {ID: "tab-det", SessionPath: sp, Ctrl: ctrl}},
			path:     sp,
			want:     wakeVerdict{Owner: wakeOwnerDetached, HasSurface: true, RuntimeLive: true},
			why:      "tab closed but work alive: detached is the owner (569/579)",
		},
		{
			name: "removed tab is no surface",
			tabs: map[string]*WorkspaceTab{"tab-gone": {ID: "tab-gone", SessionPath: sp, Ctrl: ctrl, removed: true}},
			path: sp,
			want: wakeVerdict{Owner: wakeOwnerNone},
			why:  "a removed tab owns nothing",
		},
		{
			name: "lease held elsewhere blocks the stand-up",
			tabs: map[string]*WorkspaceTab{"tab-lease": {ID: "tab-lease", SessionPath: sp, StartupErrLeaseHeld: true}},
			path: sp,
			want: wakeVerdict{Owner: wakeOwnerNone, HasSurface: true, LeaseHeldElsewhere: true},
			why:  "the last build failed on a foreign lease — waking now burns strikes against a lock that will not move",
		},
		{
			name: "unrelated session path is not matched",
			tabs: map[string]*WorkspaceTab{"tab-else": {ID: "tab-else", SessionPath: other, Ctrl: pathedStub{path: other}}},
			path: sp,
			want: wakeVerdict{Owner: wakeOwnerNone},
			why:  "matching is by session runtime key, not by existence",
		},
		{
			name: "empty path is unaddressable (fail-closed)",
			path: "",
			want: wakeVerdict{Owner: wakeOwnerNone, Unaddressable: true},
			why:  "no runtime key: nothing may stand it up",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &App{tabs: tc.tabs, detachedSessions: tc.detached}
			got := a.canWakeSession(tc.path)
			if got != tc.want {
				t.Fatalf("canWakeSession = %+v, want %+v (%s)", got, tc.want, tc.why)
			}
			if got.CanStandUp() != (got.BlockReason() == "") {
				t.Fatalf("CanStandUp=%v inconsistent with BlockReason=%q", got.CanStandUp(), got.BlockReason())
			}
			// 1185 种子委托与路径臂同判（delegator 不许漂移）。
			if got2 := (&App{tabs: tc.tabs, detachedSessions: tc.detached}).sessionHasLiveRuntimeForPath(tc.path); got2 != got.RuntimeLive {
				t.Fatalf("sessionHasLiveRuntimeForPath = %v, want %v (arbiter drift)", got2, got.RuntimeLive)
			}
		})
	}
}

// 验收②（幂等）：同会话并发认领恰一胜者；释放后可再认领；不同会话互不干扰。
func TestClaimWakeIdempotentSingleWinner(t *testing.T) {
	sp := filepath.Join(t.TempDir(), "race.session.jsonl")
	a := &App{}

	const n = 24
	var wins int32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, ok := a.claimWake(sp, wakeClaimantDrain)
			if !ok {
				return
			}
			atomic.AddInt32(&wins, 1)
			time.Sleep(2 * time.Millisecond) // hold the window so racers really contend
			release()
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&wins); got != 1 {
		t.Fatalf("concurrent claims: %d winners, want exactly 1 (the 887 double-open gate)", got)
	}

	// 释放后可再认领（下一 pass 的重试面）。
	release, ok := a.claimWake(sp, wakeClaimantBridge)
	if !ok {
		t.Fatal("claim after release must succeed (next-pass retry face)")
	}
	release()
	release() // 幂等 release：重复调用不误删他人（下两行验证认领仍空闲）
	if _, ok := a.claimWake(sp, wakeClaimantPanel); !ok {
		t.Fatal("double release must not leave a ghost claim")
	}
}

// 验收②（887 桥+drain 同窗形态）：drain 认领在飞时，桥的 stand-up 门被
// wake_in_flight 短路；wakeDormantRuntime 按竞态让位（ErrTurnRunning）并记账；
// drain 释放后桥可正常放行——同窗只开一轮。
func TestWakeStandUpGateDrainBridgeRaceYields(t *testing.T) {
	sp := filepath.Join(t.TempDir(), "guardian.session.jsonl")
	a := &App{}

	release, gate := a.wakeStandUpGate(sp, wakeClaimantDrain)
	if !gate.Granted {
		t.Fatalf("drain stand-up must be granted on a clean tri-state, got %+v (reason %s)", gate, gate.Reason)
	}

	// 桥侧同窗：门拒绝 + wakeDormantRuntime 竞态让位（绝不触到 open——若触到，
	// 裸 App 上的 OpenTopicSessionDetached 会失败/panic，测试必红）。
	p := &sessionCollabPump{app: a}
	_, gate2 := a.wakeStandUpGate(sp, wakeClaimantBridge)
	if gate2.Granted || gate2.Reason != wakeStandUpInFlightReason || gate2.Outcome != wakeSkipped {
		t.Fatalf("bridge gate while drain holds the claim = %+v, want denied wake_in_flight/skipped", gate2)
	}
	_, err := p.wakeDormantRuntime(sessioncollab.Identity{ContactID: "c1", SessionPath: sp})
	if !errors.Is(err, control.ErrTurnRunning) {
		t.Fatalf("wakeDormantRuntime under a held claim = %v, want ErrTurnRunning race-yield", err)
	}
	entry, ok := a.WakeLedgerFor(sp)
	if !ok || entry.Outcome != wakeSkipped || entry.Reason != wakeStandUpInFlightReason || entry.Claimant != string(wakeClaimantBridge) {
		t.Fatalf("ledger after race-yield = %+v (found %v), want bridge/skipped/wake_in_flight", entry, ok)
	}

	// drain 释放 → 桥放行（一轮已由 drain 开过后，责任不叠加）。
	release()
	release2, gate3 := a.wakeStandUpGate(sp, wakeClaimantBridge)
	if !gate3.Granted {
		t.Fatalf("bridge gate after release must be granted, got %+v (reason %s)", gate3, gate3.Reason)
	}
	release2()
}

// 验收③（记账单点）：四认领方统一回写；同会话保留最近一行且可查——1007 的
// 「投递已读（queued_followup）却没开轮、原因无处查」由账本单点回答。
func TestWakeLedgerSinglePointAccounting(t *testing.T) {
	sp := filepath.Join(t.TempDir(), "merge-line.session.jsonl")
	a := &App{}

	// ① drain：责任方已在（构建在飞）→ owned 让位，原因可归因。
	a.recordWakeOutcome(wakeLedgerEntry{SessionPath: sp, ContactID: "c", Claimant: string(wakeClaimantDrain), Outcome: wakeOwned, Reason: "owned:building"})
	entry, ok := a.WakeLedgerFor(sp)
	if !ok || entry.Outcome != wakeOwned || entry.Reason != "owned:building" || entry.Claimant != string(wakeClaimantDrain) {
		t.Fatalf("ledger(owned) = %+v (found %v)", entry, ok)
	}

	// ② 桥：同会话后续动作覆盖为最近一行（账本回答「现在」谁负责）。
	a.recordWakeOutcome(wakeLedgerEntry{SessionPath: sp, ContactID: "c", Claimant: string(wakeClaimantBridge), Outcome: wakeOpened})
	entry, ok = a.WakeLedgerFor(sp)
	if !ok || entry.Outcome != wakeOpened || entry.Claimant != string(wakeClaimantBridge) {
		t.Fatalf("ledger(latest wins) = %+v (found %v)", entry, ok)
	}

	// 1007 病灶面：已读没开轮形态下，最近一次开轮决定必有三要素
	// （谁/结果/原因），不再有「绿灯悬空、归因无处」。
	a.recordWakeOutcome(wakeLedgerEntry{SessionPath: sp, ContactID: "c", Claimant: string(wakeClaimantRestore), Outcome: wakeSkipped, Reason: "lease_held_elsewhere", ItemID: "item-7"})
	entry, ok = a.WakeLedgerFor(sp)
	if !ok || entry.Claimant == "" || entry.Outcome == "" || entry.Reason == "" {
		t.Fatalf("1007 face: ledger must answer who/outcome/why, got %+v (found %v)", entry, ok)
	}
	if entry.ItemID != "item-7" {
		t.Fatalf("ledger must keep the queue-head coordinates, got %+v", entry)
	}

	// ③④ 恢复链与面板各自可记账（四个认领方同一条通道）。
	a.recordWakeOutcome(wakeLedgerEntry{SessionPath: sp, Claimant: string(wakeClaimantRestore), Outcome: wakeResumed})
	a.recordWakeOutcome(wakeLedgerEntry{SessionPath: sp, Claimant: string(wakeClaimantPanel), Outcome: wakeResumed})
	if entry, _ = a.WakeLedgerFor(sp); entry.Claimant != string(wakeClaimantPanel) {
		t.Fatalf("panel entry must land in the same single ledger, got %+v", entry)
	}

	// 空路径不可记账也不可查（fail-closed）。
	a.recordWakeOutcome(wakeLedgerEntry{SessionPath: "", Claimant: "x", Outcome: wakeOpened})
	if _, ok := a.WakeLedgerFor(""); ok {
		t.Fatal("empty session path must not be ledger-able")
	}
}

// ---- 源码守卫（验收④）------------------------------------------------------
//
// 四触发源文件里的开轮判定函数体不得再出现直接的 tab/runtime/lease 状态判定
// token——必须经仲裁层入口。下限自证三件套：
//   a) 每个被守卫函数体都真实提取到（空体=扫描面缩坏=红）；
//   b) 合成反例（tab.Ctrl 直判）必须被打红（守卫可证伪）；
//   c) 仲裁层原语全部在位、四文件各自的问询接线计数达标。

// wakeGuardTokens 是「直接判定状态」的字面 token（字段级，大小写敏感）：
// 小写 t.ctrl/target.ctrl 是仲裁层扫描器回填的快照读数，允许；这里禁的是
// 触发源直接摸 WorkspaceTab/App 注册表。
var wakeGuardTokens = []string{
	"tab.Ctrl", "Ctrl == nil", "Ctrl != nil", "buildCancel", "detachedSessions",
	"a.tabs", "p.app.tabs", "tab.Ready", "tab.removed", ".ReadOnly", ".Spectator",
	"StartupErr", "sessionLease", "tabByID(",
}

// wakeGuardedFuncs 是四触发源的开轮判定函数（文件 → 函数名）。
var wakeGuardedFuncs = map[string][]string{
	"session_collab.go":              {"drain"},
	"session_collab_idle_turn.go":    {"sweepIdleInboxTurns", "idleTurnTargetViews"},
	"session_collab_dormant_wake.go": {"collectDormant", "dormantIdleTurnTargets", "dormantTurnView", "wakeDormantRuntime"},
	"restart_inbox_gate.go":          {"autoContinueInboxGateForTab"},
	"inbox_app.go":                   {"SetInboxPaused"},
}

// extractGoFuncBody extracts one named top-level function's body by brace
// matching, so the guard scans the real decision code, not the whole file.
// 支持多行签名（首个 "{" 即函数体起点——Go 参数表内无字面大括号）。
func extractGoFuncBody(src, name string) (string, bool) {
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "func ") || !strings.Contains(line, name+"(") {
			continue
		}
		started := false
		depth := 0
		var b strings.Builder
		for j := i; j < len(lines); j++ {
			l := lines[j]
			if !started {
				if k := strings.Index(l, "{"); k >= 0 {
					started = true
					tail := l[k:]
					depth = strings.Count(tail, "{") - strings.Count(tail, "}")
				}
				continue
			}
			depth += strings.Count(l, "{") - strings.Count(l, "}")
			if depth <= 0 {
				return b.String(), true
			}
			b.WriteString(l)
			b.WriteString("\n")
		}
		return b.String(), started
	}
	return "", false
}

// wakeGuardFindToken 返回 body 中命中的第一个直接判定 token；无则空串。
func wakeGuardFindToken(body string) string {
	for _, tok := range wakeGuardTokens {
		if strings.Contains(body, tok) {
			return tok
		}
	}
	return ""
}

func TestWakeArbiterSourcesAskArbiter(t *testing.T) {
	srcs := map[string]string{}
	for file := range wakeGuardedFuncs {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		srcs[file] = string(data)
	}

	// 下限自证 a)：每个被守卫函数体真实提取且非空；0 处直接判状态。
	bodies := map[string]string{}
	for file, funcs := range wakeGuardedFuncs {
		for _, fn := range funcs {
			body, ok := extractGoFuncBody(srcs[file], fn)
			if !ok || strings.TrimSpace(body) == "" {
				t.Fatalf("guard self-check: cannot extract body of %s in %s — the scanned surface shrank (下限自证破)", fn, file)
			}
			bodies[fn] = body
			if tok := wakeGuardFindToken(body); tok != "" {
				t.Fatalf("%s.%s still judges session state directly (%q found) — it must ask the arbiter (session_wake_arbiter.go, 任务1206)", file, fn, tok)
			}
		}
	}

	// 下限自证 b)：合成反例必须被打红（守卫可证伪，不是恒真断言）。
	if tok := wakeGuardFindToken("if tab.Ctrl == nil { return }"); tok != "tab.Ctrl" {
		t.Fatalf("guard self-check: synthetic direct judgment flagged %q, want tab.Ctrl (证伪能力丢失)", tok)
	}

	// 下限自证 c)：仲裁层原语在位（防「删了仲裁层、触发源直判」倒退）。
	arb, err := os.ReadFile("session_wake_arbiter.go")
	if err != nil {
		t.Fatalf("read arbiter: %v", err)
	}
	ab := string(arb)
	for _, primitive := range []string{
		"func wakeSurfaceLive(",
		"func (a *App) canWakeSession(",
		"func (a *App) sessionHasLiveRuntimeForPath(",
		"func wakeTabRuntimeLive(",
		"func wakeTabLiveCtrl(",
		"func (a *App) dormantWakeCtrlForSessionPath(",
		"func (a *App) claimWake(",
		"func (a *App) wakeStandUpGate(",
		"func (a *App) recordWakeOutcome(",
		"func (a *App) WakeLedgerFor(",
		"func (a *App) sessionCollabLiveTargets(",
		"func (a *App) sessionCollabTargets(",
	} {
		if !strings.Contains(ab, primitive) {
			t.Fatalf("arbiter primitive missing: %s — the SSOT was dismantled (任务1206 回归)", primitive)
		}
	}

	// 四路问询接线计数（每触发源至少一次问仲裁层 + 记账单点）。
	wiring := []struct {
		file string
		fn   string
		need []string
	}{
		{"session_collab.go", "drain", []string{"wakeStandUpGate(", "recordWakeOutcome("}},
		{"session_collab_idle_turn.go", "sweepIdleInboxTurns", []string{"sessionCollabLiveTargets("}},
		{"session_collab_dormant_wake.go", "wakeDormantRuntime", []string{"claimWake(", "recordWakeOutcome(", "dormantWakeCtrlForSessionPath("}},
		{"restart_inbox_gate.go", "autoContinueInboxGateForTab", []string{"wakeTabLiveCtrl(", "recordWakeOutcome("}},
		{"inbox_app.go", "SetInboxPaused", []string{"canWakeSession(", "recordWakeOutcome("}},
	}
	for _, w := range wiring {
		body := bodies[w.fn]
		for _, need := range w.need {
			if !strings.Contains(body, need) {
				t.Fatalf("%s.%s must ask the arbiter via %s (触发源脱钩仲裁层)", w.file, w.fn, need)
			}
		}
	}

	// 扫描器本体必须已搬入仲裁层文件（触发源文件不再定义状态判定）。
	for _, moved := range []string{"func (a *App) sessionCollabLiveTargets(", "func (a *App) sessionCollabTargets("} {
		if strings.Contains(srcs["session_collab.go"], moved) {
			t.Fatalf("%s must live in session_wake_arbiter.go since 1206 (found back in session_collab.go)", moved)
		}
	}

	// 1185 接线守卫（原样保留）：dormant 反查两处仍走活 runtime 判据。
	if n := strings.Count(srcs["session_collab_dormant_wake.go"], "app.sessionHasLiveRuntimeForPath"); n < 2 {
		t.Fatalf("dormant wiring call sites = %d, want >=2 (1185 seam + re-check)", n)
	}
	// 922 收口守卫：桥不再经 tabByID 二次解析（判定归扫描器单点）。
	if strings.Contains(bodies["idleTurnTargetViews"], "tabByID(") {
		t.Fatal("idleTurnTargetViews must not re-resolve ctrl via tabByID — the arbiter scanner owns resolution since 1206")
	}
	// 868 收口守卫：清门不再直读 tab.Ready。
	if strings.Contains(bodies["autoContinueInboxGateForTab"], "tab.Ready") {
		t.Fatal("autoContinueInboxGateForTab must ask wakeTabLiveCtrl instead of reading tab.Ready (1206)")
	}
}

// 验收③行为面（tab 恢复补链）：清门前问仲裁层 per-tab 臂——runtime 未活
// （skeleton/构建在飞）不清门不踢派发（kick 责任在那条时间线的构建完成
// kick）；runtime 活才清+踢，且结果落记账单点。夹具复用 868 状态机测试
// （真 manifest，非手绘快照）。
func TestRestoreChainAsksArbiterForGateClear(t *testing.T) {
	isolateDesktopUserDirs(t)

	t.Run("skeleton tab keeps the gate (arbiter says not live)", func(t *testing.T) {
		session := filepath.Join(t.TempDir(), "skeleton.session.jsonl")
		st := restartGateFixture(t, session, 3)

		app := &App{}
		// 无 Ctrl 的 published skeleton（619② 形态）+ 无人值守姿态：旧形按
		// tab.Ready 拦、新形按仲裁层拦——两种都不得清门。
		tab := &WorkspaceTab{ID: "t-skel", SessionPath: session, autopilot: true}
		app.autoContinueInboxGateForTab(tab)

		after := st.Snapshot()
		if !after.Paused || !after.Recovered {
			t.Fatalf("a skeleton tab must not clear the gate, got %+v", after)
		}
		if _, ok := app.WakeLedgerFor(session); ok {
			t.Fatal("a not-live tab must not write a wake outcome (arbiter refused before any action)")
		}
	})

	t.Run("in-flight build keeps the gate", func(t *testing.T) {
		session := filepath.Join(t.TempDir(), "booting.session.jsonl")
		st := restartGateFixture(t, session, 3)

		app := &App{}
		// 构建在飞（buildCancel != nil，Ctrl 未发布）：1185 反向判据——机制
		// 已运行，清门抢跑只会踢空。
		tab := &WorkspaceTab{ID: "t-boot", SessionPath: session, autopilot: true, buildCancel: func() {}}
		app.autoContinueInboxGateForTab(tab)

		if after := st.Snapshot(); !after.Paused || !after.Recovered {
			t.Fatalf("a booting tab must not clear the gate, got %+v", after)
		}
	})

	t.Run("live runtime clears the gate and books the outcome", func(t *testing.T) {
		session := filepath.Join(t.TempDir(), "live.session.jsonl")
		st := restartGateFixture(t, session, 3)

		ctrl := &gateStoreController{store: st}
		app := &App{}
		tab := &WorkspaceTab{ID: "t-live", SessionPath: session, Ready: true, Ctrl: ctrl, autopilot: true}
		app.autoContinueInboxGateForTab(tab)

		after := st.Snapshot()
		if after.Paused || after.Recovered {
			t.Fatalf("the live runtime must clear the gate, got %+v", after)
		}
		if got := ctrl.kickCount(); got != 1 {
			t.Fatalf("dispatch kick = %d, want 1", got)
		}
		entry, ok := app.WakeLedgerFor(session)
		if !ok || entry.Outcome != wakeResumed || entry.Claimant != string(wakeClaimantRestore) {
			t.Fatalf("ledger after gate clear = %+v (found %v), want restore_chain/resumed", entry, ok)
		}
	})
}
