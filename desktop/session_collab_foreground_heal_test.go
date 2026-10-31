package main

// tl1304 ④ 最小扩面验收：796/1288 的 heal seam 扩到非 collab 前台 tab。
// 此前的边界（sweepIdleInboxTurns 只flatten collab live targets + dormant，
// 且 sweepTargets 的边界 2 在 heal 判定之前 continue）意味着普通 tab 的同形
// wedge 无人值守时零自愈、active 前台 tab 连 collab target 也不做 heal。
// 前台 pass 遍历 tab 注册表（不分 collab/非 collab、不分 active/非 active），
// 以相同阈值（idleTurnFinishingStuckTimeout / idleTurnRunningStuckTimeout）
// 递同一对 heal seam；全部护体条件留在 heal 方法内部（controller 侧
// finishing_heal_test / running_stall_heal_test 已钉：fresh ledger 拒绝、
// parked 拒绝、后台作业拒绝、健康窗口拒绝），本文件钉 pass 侧的
// 路由 / 阈值 / collab 门独立性 / 健康长轮不误杀。

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/control"
	"reasonix/internal/event"
)

// foregroundHealFake overrides exactly the surface the foreground pass
// touches; everything else stays on the embedded (nil) interface and must
// never be reached.
type foregroundHealFake struct {
	control.SessionAPI

	status       control.RuntimeStatus
	healResult   bool
	finishingGot atomic.Int32
	runningGot   atomic.Int32
	finishingArg time.Duration
	runningArg   time.Duration
}

func (f *foregroundHealFake) RuntimeStatus() control.RuntimeStatus { return f.status }

func (f *foregroundHealFake) HealStuckFinishing(maxAge time.Duration) (bool, time.Duration) {
	f.finishingGot.Add(1)
	f.finishingArg = maxAge
	return f.healResult, idleTurnFinishingStuckTimeout
}

func (f *foregroundHealFake) HealStalledRunning(maxQuiet time.Duration) (bool, time.Duration) {
	f.runningGot.Add(1)
	f.runningArg = maxQuiet
	return false, 0
}

func foregroundHealApp(t *testing.T, ctrls map[string]control.SessionAPI) (*App, *sessionCollabPump) {
	t.Helper()
	// config.Load 隔离：REASONIX_HOME 指向临时目录，并落一份显式关掉 collab
	// 实验的 config.toml——不能只给空文件（Default() 把 experimental_session_collab
	// 打底为 true，config.go 稳定档默认开），也不给回退真实用户配置的机会。
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.toml"),
		[]byte("[agent]\nexperimental_session_collab = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tabs := make(map[string]*WorkspaceTab, len(ctrls))
	active := ""
	for id, ctrl := range ctrls {
		tabs[id] = &WorkspaceTab{ID: id, Scope: "global", Ready: true, Ctrl: ctrl}
		if active == "" {
			active = id
		}
	}
	app := &App{tabs: tabs, activeTabID: active}
	return app, newSessionCollabPump(app)
}

// ① 路由+阈值：非 collab tab 的 finishing-only 楔死形（窗口超过 90s 阈值）
// 必须被递到 HealStuckFinishing，且阈值就是 collab sweep 用的同一个常量。
func TestForegroundHealPassOffersFinishingSeamOnPlainTab(t *testing.T) {
	fake := &foregroundHealFake{status: control.RuntimeStatus{
		Finishing:          true,
		FinishingSinceUnix: time.Now().Add(-idleTurnFinishingStuckTimeout - time.Minute).Unix(),
	}}
	_, p := foregroundHealApp(t, map[string]control.SessionAPI{"plain_tab": fake})

	p.sweepForegroundTurnHeals(time.Now())
	if got := fake.finishingGot.Load(); got != 1 {
		t.Fatalf("finishing heal offered %d times, want 1", got)
	}
	if fake.finishingArg != idleTurnFinishingStuckTimeout {
		t.Fatalf("finishing heal threshold = %v, want the collab-sweep constant %v", fake.finishingArg, idleTurnFinishingStuckTimeout)
	}
	if got := fake.runningGot.Load(); got != 0 {
		t.Fatalf("running seam must not be offered for a finishing-shape tab, got %d", got)
	}
}

// ② pass 侧阈值护体：finishing 窗口年轻（< 90s）不递 heal——健康收尾窗
// 永不被触碰；controller 侧再验一次（finishing_heal_test）。
func TestForegroundHealPassSkipsYoungFinishingWindow(t *testing.T) {
	fake := &foregroundHealFake{status: control.RuntimeStatus{
		Finishing:          true,
		FinishingSinceUnix: time.Now().Add(-time.Second).Unix(),
	}}
	_, p := foregroundHealApp(t, map[string]control.SessionAPI{"young_tab": fake})

	p.sweepForegroundTurnHeals(time.Now())
	if got := fake.finishingGot.Load(); got != 0 {
		t.Fatalf("a young finishing window must not be offered the heal, got %d calls", got)
	}
}

// ③ 路由+阈值：running 楔死形（无 finishing 窗口）递 HealStalledRunning，
// 阈值 = tl1288 的静默阈（10 分钟）；假体拒绝即返回，无副作用。
func TestForegroundHealPassOffersRunningSeamOnPlainTab(t *testing.T) {
	fake := &foregroundHealFake{status: control.RuntimeStatus{Running: true}}
	_, p := foregroundHealApp(t, map[string]control.SessionAPI{"busy_tab": fake})

	p.sweepForegroundTurnHeals(time.Now())
	if got := fake.runningGot.Load(); got != 1 {
		t.Fatalf("running heal offered %d times, want 1", got)
	}
	if fake.runningArg != idleTurnRunningStuckTimeout {
		t.Fatalf("running heal threshold = %v, want the tl1288 quiet constant %v", fake.runningArg, idleTurnRunningStuckTimeout)
	}
	if got := fake.finishingGot.Load(); got != 0 {
		t.Fatalf("finishing seam must not be offered for a running-shape tab, got %d", got)
	}
}

// ④ collab 门独立性：collab 实验开关关（隔离配置下默认关）时，pumpTick
// 仍必须跑前台 heal pass——普通 tab 的自愈不依赖 collab 是否启用。
func TestPumpTickRunsForegroundHealWithoutCollabEnabled(t *testing.T) {
	fake := &foregroundHealFake{status: control.RuntimeStatus{
		Finishing:          true,
		FinishingSinceUnix: time.Now().Add(-idleTurnFinishingStuckTimeout - time.Minute).Unix(),
	}, healResult: true}
	_, p := foregroundHealApp(t, map[string]control.SessionAPI{"gate_tab": fake})
	if sessionCollabEnabled() {
		t.Fatal("fixture: isolated config must default the collab experiment off")
	}

	p.pumpTick(time.Now())
	if got := fake.finishingGot.Load(); got != 1 {
		t.Fatalf("pumpTick with collab off must still run the foreground heal pass, got %d calls", got)
	}
}

// ⑤ 健康长轮不误杀（全栈回归）：真实 controller + 真实 running 轮（ledger
// 活性是新鲜的），前台 pass 照常递 seam，但 controller 侧护体必须拒绝——
// 轮体原样活着、无 force 信号、投影仍 busy。controller 侧同形拒绝已在
// running_stall_heal_test 钉过，这里钉 pass 接进真实链路后同样安全。
func TestForegroundHealPassSparesHealthyRunningTurn(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	runner := runnerFunc(func(ctx context.Context, input string) error {
		started <- struct{}{}
		<-release
		return nil
	})
	ctrl := control.New(control.Options{
		Runner:      runner,
		Sink:        event.Discard,
		SessionDir:  t.TempDir(),
		SessionPath: filepath.Join(t.TempDir(), "session.jsonl"),
	})
	t.Cleanup(ctrl.Close)
	t.Cleanup(func() { close(release) })
	app, p := foregroundHealApp(t, map[string]control.SessionAPI{"healthy_tab": ctrl})

	if _, err := app.StartTurnForTab("healthy_tab", "a genuinely slow but alive turn", "submission-1"); err != nil {
		t.Fatalf("StartTurnForTab: %v", err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("turn runner did not start")
	}
	if !ctrl.RuntimeStatus().Running {
		t.Fatal("fixture: the healthy turn must report running")
	}

	p.pumpTick(time.Now())

	if st := ctrl.RuntimeStatus(); !st.Running {
		t.Fatalf("a healthy long turn must survive the foreground heal pass, got %+v", st)
	}
	select {
	case <-release:
		t.Fatal("the healthy turn body was released — the heal abandoned a live turn")
	default:
	}
}

// runnerFunc adapts a func to the runner interface control.New expects.
type runnerFunc func(ctx context.Context, input string) error

func (f runnerFunc) Run(ctx context.Context, input string) error { return f(ctx, input) }
