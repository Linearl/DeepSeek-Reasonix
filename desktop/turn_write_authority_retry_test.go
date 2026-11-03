package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/tool"
)

// tl1342 验收：UI 发送遇 session write authority stale 报错不再静默丢——
// desktop 提交面在闸门拒绝后走 1311/1201 修复 seam 自愈，冷却后重新
// beginTabTurn 恰好自动重试一次；heal 被拒保持单次丢弃（横幅兜底），健康
// 路径零变化（不重试、不双发）。本文件用真 Controller（带 executor session，
// 否则 authority bind 是静默 no-op）走完整 submitToTabResult / 提交包装链。

type collectedWireEvent struct {
	kind         string
	text         string
	submissionID string
	turnID       string
}

type retryEventRecorder struct {
	mu     sync.Mutex
	events []collectedWireEvent
}

func (r *retryEventRecorder) add(payload any) {
	ev := collectedWireEvent{}
	switch p := payload.(type) {
	case correlatedWireEventTab:
		ev.kind, ev.text, ev.turnID = p.Kind, p.Text, p.TurnID
		ev.submissionID = p.SubmissionID
	case wireEventTab:
		ev.kind, ev.text, ev.turnID = p.Kind, p.Text, p.TurnID
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *retryEventRecorder) snapshot() []collectedWireEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]collectedWireEvent(nil), r.events...)
}

func (r *retryEventRecorder) count(match func(collectedWireEvent) bool) int {
	n := 0
	for _, ev := range r.snapshot() {
		if match(ev) {
			n++
		}
	}
	return n
}

// countEventually 等到至少 want 条匹配事件从 webview emit 队列排空（事件走
// asyncRuntimeEmitter 异步入队，同步 snapshot 会漏排程中的），再留一个静默
// 期让潜在重试风暴现身，返回静默后的计数。
func (r *retryEventRecorder) countEventually(t *testing.T, want int, match func(collectedWireEvent) bool) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for r.count(match) < want {
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	return r.count(match)
}

// waitFor 轮询等待满足条件的事件（turn 事件流既有同步段也有 turn goroutine
// 段，轮询比一次性 snapshot 更稳）。
func (r *retryEventRecorder) waitFor(t *testing.T, timeout time.Duration, match func(collectedWireEvent) bool) collectedWireEvent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		for _, ev := range r.snapshot() {
			if match(ev) {
				return ev
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("event not delivered within %v", timeout)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func noticeMatching(fragment string) func(collectedWireEvent) bool {
	return func(ev collectedWireEvent) bool {
		return ev.kind == "notice" && strings.Contains(ev.text, fragment)
	}
}

// newStaleRetryFixture 建一个带 SessionPath 的真控制器 tab（submission 全链
// 可走完整 admission），并装好事件记录 sink。
func newStaleRetryFixture(t *testing.T, name string) (*App, *WorkspaceTab, *control.Controller, *retryEventRecorder, string) {
	t.Helper()
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, name+".jsonl")
	rec := &retryEventRecorder{}
	sink := &tabEventSink{tabID: "stale_" + name, ctx: context.Background()}
	sink.runtimeEvents.emit = func(_ context.Context, _ string, payload ...any) {
		rec.add(payload[0])
	}
	executor := agent.New(nil, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	ctrl := control.New(control.Options{
		Runner:      executor,
		Executor:    executor,
		Sink:        sink,
		SessionDir:  dir,
		SessionPath: path,
		Label:       name,
	})
	t.Cleanup(ctrl.Close)
	tab := &WorkspaceTab{ID: "stale_" + name, Scope: "global", Ready: true, Ctrl: ctrl, sink: sink}
	app := &App{
		tabs:        map[string]*WorkspaceTab{tab.ID: tab},
		activeTabID: tab.ID,
		tabOrder:    []string{tab.ID},
	}
	return app, tab, ctrl, rec, path
}

// staleSideController 用同一 lease 给旁路 controller 发新一代 authority，
// 把主 controller 的绑定顶成 stale（= 17:01 风暴的进程内形态）。
func staleSideController(t *testing.T, dir, path string, lease *agent.SessionLease, label string) {
	t.Helper()
	executor := agent.New(nil, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	side := control.New(control.Options{
		Runner: executor, Executor: executor, Sink: event.Discard,
		SessionDir: dir, SessionPath: path, Label: label,
	})
	defer side.Close()
	if err := control.IssueAndBindWriteAuthority(side, lease); err != nil {
		t.Fatalf("rotate generation via side controller: %v", err)
	}
}

// 验收①：stale 场景 UI 发送自动成功，无需手动重发——submitToTabResult 检出
// 闸门拒绝 → heal（活 lease 重发代）→ 冷却后重新 beginTabTurn → 恰好一次
// 重试，轮真实开出来（TurnID 落账 + turn_started 事件），且向流上补一条
// Info notice 纠正首弃的 warn 文案。
func TestSubmitToTabResultAutoHealsStaleWriteAuthorityAndRetries(t *testing.T) {
	origCooldown := staleWriteAuthoritySubmitRetryCooldown
	staleWriteAuthoritySubmitRetryCooldown = 0
	t.Cleanup(func() { staleWriteAuthoritySubmitRetryCooldown = origCooldown })

	app, tab, ctrl, rec, path := newStaleRetryFixture(t, "retry_main")
	lease, err := agent.TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	t.Cleanup(lease.Release)
	tab.adoptSessionLease(lease)
	if err := control.IssueAndBindWriteAuthority(ctrl, lease); err != nil {
		t.Fatalf("bind first generation: %v", err)
	}
	staleSideController(t, config.SessionDir(), path, lease, "retry_main_side")
	staleGen := ctrl.WriteAuthorityGeneration()
	if !submissionRefusedByStaleWriteAuthority(ctrl) {
		t.Fatal("precondition failed: controller not detected as stale before submit")
	}

	result, err := app.submitToTabResult(tab.ID, "hello", false, true, "u-stale")
	if err != nil {
		t.Fatalf("submit with stale authority must auto-heal, got error: %v", err)
	}
	if result.Disposition != control.SubmitTurnStarted {
		t.Fatalf("disposition after auto-retry = %q, want turn_started", result.Disposition)
	}
	if result.TurnID == "" {
		t.Fatal("no turn id after auto-retry — the retried submission never admitted a turn")
	}
	if got := ctrl.WriteAuthorityGeneration(); got <= staleGen {
		t.Fatalf("authority generation after heal = %d, want fresh value above %d", got, staleGen)
	}
	tab.sessionLeaseMu.Lock()
	sameLease := tab.sessionLease == lease
	tab.sessionLeaseMu.Unlock()
	if !sameLease {
		t.Fatal("heal replaced the lease object; want an in-place rebind on the live lease")
	}
	if n := rec.countEventually(t, 1, noticeMatching("no longer writable")); n != 1 {
		t.Fatalf("gate refusal notices = %d, want exactly 1", n)
	}
	if n := rec.countEventually(t, 1, noticeMatching("restored automatically")); n != 1 {
		t.Fatalf("auto-restored notices = %d, want exactly 1 (no retry storm)", n)
	}
	ev := rec.waitFor(t, 2*time.Second, func(e collectedWireEvent) bool {
		return e.kind == "turn_started" && e.submissionID == "u-stale"
	})
	if ev.turnID != result.TurnID {
		t.Fatalf("turn_started turn id = %q, want the retried receipt %q", ev.turnID, result.TurnID)
	}
}

// 验收①（横幅重发面）：SubmitDeliveryRecoveryToTab 的兜底重发同样撞闸门拒绝
// 时走同一 heal+单次重试——兜底路径自己不能死在它要恢复的门槛上。
func TestSubmitDeliveryRecoveryToTabAutoHealsStaleWriteAuthority(t *testing.T) {
	origCooldown := staleWriteAuthoritySubmitRetryCooldown
	staleWriteAuthoritySubmitRetryCooldown = 0
	t.Cleanup(func() { staleWriteAuthoritySubmitRetryCooldown = origCooldown })

	app, tab, ctrl, rec, path := newStaleRetryFixture(t, "retry_recovery")
	lease, err := agent.TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	t.Cleanup(lease.Release)
	tab.adoptSessionLease(lease)
	if err := control.IssueAndBindWriteAuthority(ctrl, lease); err != nil {
		t.Fatalf("bind first generation: %v", err)
	}
	staleSideController(t, config.SessionDir(), path, lease, "retry_recovery_side")
	staleGen := ctrl.WriteAuthorityGeneration()

	if err := app.submitDeliveryRecoveryToTab(tab.ID, "hello", "hello", "u-recovery"); err != nil {
		t.Fatalf("delivery recovery with stale authority must auto-heal, got error: %v", err)
	}
	if got := ctrl.WriteAuthorityGeneration(); got <= staleGen {
		t.Fatalf("authority generation after heal = %d, want fresh value above %d", got, staleGen)
	}
	if n := rec.countEventually(t, 1, noticeMatching("no longer writable")); n != 1 {
		t.Fatalf("gate refusal notices = %d, want exactly 1", n)
	}
	rec.waitFor(t, 2*time.Second, noticeMatching("restored automatically"))
	rec.waitFor(t, 2*time.Second, func(e collectedWireEvent) bool {
		return e.kind == "turn_started" && e.submissionID == "u-recovery"
	})
}

// heal 被拒（活锁被本进程另一 runtime 持有）：不重试、不偷锁——保持原行为
// （单次丢弃 + 拒绝 notice，交给横幅兜底），authority 世代原样。
func TestSubmitToTabResultDropsOnceWhenHealDeclined(t *testing.T) {
	origCooldown := staleWriteAuthoritySubmitRetryCooldown
	staleWriteAuthoritySubmitRetryCooldown = 0
	t.Cleanup(func() { staleWriteAuthoritySubmitRetryCooldown = origCooldown })

	app, tab, ctrl, rec, path := newStaleRetryFixture(t, "retry_declined")
	// 持有者 lease 保持存活但永不装到 tab 上：fast path 无 held lease 可发代，
	// takeover 撞活锁被拒。
	holder, err := agent.TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("acquire holder lease: %v", err)
	}
	t.Cleanup(holder.Release)
	if err := control.IssueAndBindWriteAuthority(ctrl, holder); err != nil {
		t.Fatalf("bind first generation: %v", err)
	}
	staleSideController(t, config.SessionDir(), path, holder, "retry_declined_side")
	staleGen := ctrl.WriteAuthorityGeneration()

	result, err := app.submitToTabResult(tab.ID, "hello", false, true, "u-declined")
	if err != nil {
		t.Fatalf("declined heal keeps the notice-only drop contract, got error: %v", err)
	}
	if result.TurnID != "" {
		t.Fatalf("turn id = %q, want empty (submission stayed dropped)", result.TurnID)
	}
	if got := ctrl.WriteAuthorityGeneration(); got != staleGen {
		t.Fatalf("authority generation = %d, want unchanged %d (no heal, no retry)", got, staleGen)
	}
	if n := rec.countEventually(t, 1, noticeMatching("no longer writable")); n != 1 {
		t.Fatalf("gate refusal notices = %d, want exactly 1 (no retry storm)", n)
	}
	if n := rec.countEventually(t, 0, noticeMatching("restored automatically")); n != 0 {
		t.Fatalf("auto-restored notices = %d, want 0 on a declined heal", n)
	}
}

// 健康路径零变化：authority 正常时提交照常开轮，不触发 heal/重试/双发；
// 管理命令（started=false 但 authority 正常）同样不得重试。
func TestSubmitToTabResultHealthyPathsNeverRetry(t *testing.T) {
	origCooldown := staleWriteAuthoritySubmitRetryCooldown
	staleWriteAuthoritySubmitRetryCooldown = 0
	t.Cleanup(func() { staleWriteAuthoritySubmitRetryCooldown = origCooldown })

	app, tab, ctrl, rec, path := newStaleRetryFixture(t, "retry_healthy")
	lease, err := agent.TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	t.Cleanup(lease.Release)
	tab.adoptSessionLease(lease)
	if err := control.IssueAndBindWriteAuthority(ctrl, lease); err != nil {
		t.Fatalf("bind authority: %v", err)
	}
	freshGen := ctrl.WriteAuthorityGeneration()

	result, err := app.submitToTabResult(tab.ID, "hello", false, true, "u-healthy")
	if err != nil {
		t.Fatalf("healthy submit: %v", err)
	}
	if result.TurnID == "" {
		t.Fatal("healthy submit produced no turn id")
	}
	if got := ctrl.WriteAuthorityGeneration(); got != freshGen {
		t.Fatalf("authority generation = %d, want unchanged %d (no heal on the healthy path)", got, freshGen)
	}
	if n := rec.countEventually(t, 0, noticeMatching("no longer writable")); n != 0 {
		t.Fatalf("refusal notices on healthy path = %d, want 0", n)
	}
	if n := rec.countEventually(t, 0, noticeMatching("restored automatically")); n != 0 {
		t.Fatalf("auto-restored notices on healthy path = %d, want 0", n)
	}
	rec.waitFor(t, 2*time.Second, func(e collectedWireEvent) bool {
		return e.kind == "turn_started" && e.submissionID == "u-healthy"
	})
	// 等 nil-provider 轮收尾（turn_done）再提交管理命令——占坑未释放时
	// beginTabTurn 本就该拒绝（ErrTurnRunning），那不是本件要测的形态。
	rec.waitFor(t, 5*time.Second, func(e collectedWireEvent) bool {
		return e.kind == "turn_done"
	})

	management, err := app.submitToTabResult(tab.ID, "/tree", false, true, "u-mgmt")
	if err != nil {
		t.Fatalf("management submit: %v", err)
	}
	if management.Disposition != control.SubmitManagementHandled {
		t.Fatalf("management disposition = %q, want management_handled", management.Disposition)
	}
	if got := ctrl.WriteAuthorityGeneration(); got != freshGen {
		t.Fatalf("authority generation after management submit = %d, want unchanged %d", got, freshGen)
	}
	if n := rec.countEventually(t, 0, noticeMatching("restored automatically")); n != 0 {
		t.Fatalf("management submit must never retry, got %d auto-restored notices", n)
	}
}

// 分类族钉死（对齐 1311 分类测试）：包装 stale/missing 识别为闸门拒绝，
// 其他错误与无探针的控制器不识别。
func TestSubmissionRefusedByStaleWriteAuthorityClassification(t *testing.T) {
	base := control.New(control.Options{Sink: event.Discard})
	t.Cleanup(base.Close)
	wrapped := struct {
		control.SessionAPI
		err error
	}{SessionAPI: base}
	fakeWithoutProbe := struct{ control.SessionAPI }{base}

	for _, tc := range []struct {
		name string
		ctrl control.SessionAPI
		want bool
	}{{name: "wrapped stale", ctrl: refusedProbe{err: agent.ErrSessionWriteAuthorityStale}, want: true},
		{name: "wrapped missing", ctrl: refusedProbe{err: agent.ErrSessionWriteAuthorityMissing}, want: true},
		{name: "wrapped provider failure", ctrl: refusedProbe{err: errors.New("provider boom")}, want: false},
		{name: "wrapped other authority error", ctrl: refusedProbe{err: errors.New(" unrelated")}, want: false},
		{name: "controller without stale authority", ctrl: base, want: false},
		{name: "fake without probe", ctrl: fakeWithoutProbe, want: false},
		{name: "wrapped session api without probe", ctrl: wrapped, want: false},
		{name: "nil controller", ctrl: nil, want: false}} {
		t.Run(tc.name, func(t *testing.T) {
			if got := submissionRefusedByStaleWriteAuthority(tc.ctrl); got != tc.want {
				t.Fatalf("submissionRefusedByStaleWriteAuthority(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// refusedProbe 只加 WriteAuthorityReady 探针（wrapper 形态回归：控制层包装
// 错误时 errors.Is 判别必须穿透）。
type refusedProbe struct {
	control.SessionAPI
	err error
}

func (p refusedProbe) WriteAuthorityReady() error {
	return p.err
}
