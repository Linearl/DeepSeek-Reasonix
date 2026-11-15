package main

// tl1607 收尾窗口注入矩阵（desktop 分支层）。
//
// 窗口定义（internal/agent Steer 注释原文）：turn 的 exit flush 与 controller
// 观测 running=false 之间，无围栏 steer 会坐进队列「不进模型、不进历史」
// ——580 家族「消息没进来」机理温床。修法（用户拍板）：desktop 无 turnID
// 的引导一律走 followup 语义（inbox_app.go turnID=="" 分支只调
// TryEnqueueFollowup），从入口上消除「收进将死之轮」的路径。
//
// 矩阵逐格：
//
//	A  idle    × 无围栏 steer 入口   → queued_followup 回执 + 立即派发（下一轮=新 turn）
//	B  running × 无围栏 steer 入口   → queued_followup 回执；当轮请求不含该消息、
//	                                    不新增请求（不注入当轮）；当轮结束后作为
//	                                    下一轮请求到达（不丢）
//	C  running × 精确 turnID 围栏（活跃 turn）→ steer_accepted（精确路径不回退）
//	D  stale turnID × 精确围栏      → 拒绝且零入队零派发
//	E  idle    × followup 基线入口   → 照常派发（改前既有行为不变）
//
// 窗口态（running=false + finishing=true）的 followup 语义由 internal/control
// inbox_finishing_gate_test.go 轨迹①以 wedge 确定性钉死（窗口期入队留队、
// 关窗 kick 消费）。desktop 三个无 turnID 入口（composer EnqueueInboxSteer /
// collab pump deliverOne / servepool gate）全部汇入同一条
// enqueueInboxWithControllerSource 分支：格 B 证明该分支在「provably 当轮
// 运行中」都不产生当轮注入，窗口态同分支更无注入路径。
//
// 闸门模型：provider 记录每个请求的 user 文本后阻塞，直到测试开闸；开闸后
// 所有请求（含 steer 注入引发的后续轮次）直通完成。回合数不受测试控制，用
// 闸门而非逐请求 token，断言不受轮数影响且无死锁。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/sessioninbox"
	"reasonix/internal/tool"
)

// windowMatrixProvider records the user-role text of every provider request
// and blocks every request until the test opens the gate; after that all
// requests (including post-steer follow-up rounds) complete immediately.
type windowMatrixProvider struct {
	mu       sync.Mutex
	bodies   []string
	gate     chan struct{}
	gateOnce sync.Once
}

func (p *windowMatrixProvider) Name() string { return "window-matrix" }

func (p *windowMatrixProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	var sb strings.Builder
	for _, m := range req.Messages {
		if m.Role == provider.RoleUser {
			sb.WriteString(m.Content)
			sb.WriteString("\n")
		}
	}
	p.mu.Lock()
	p.bodies = append(p.bodies, sb.String())
	p.mu.Unlock()
	ch := make(chan provider.Chunk, 2)
	go func() {
		defer close(ch)
		select {
		case <-p.gate:
			ch <- provider.Chunk{Type: provider.ChunkText, Text: "ok"}
			ch <- provider.Chunk{Type: provider.ChunkDone}
		case <-ctx.Done():
		}
	}()
	return ch, nil
}

func (p *windowMatrixProvider) openGate() { p.gateOnce.Do(func() { close(p.gate) }) }

func (p *windowMatrixProvider) snapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.bodies...)
}

// windowMatrixTestApp builds an isolated App + tab with a real Controller
// whose runner blocks on a gate the test opens explicitly. TurnDone events
// are recorded at the Options.Sink layer (wrapping the tab sink) so
// multi-turn flows are all counted — the tab sink's bot forwarder detaches
// after the first TurnDone and cannot observe turn 2.
func windowMatrixTestApp(t *testing.T) (*App, *WorkspaceTab, *windowMatrixProvider, <-chan event.Event) {
	t.Helper()
	isolateDesktopUserDirs(t)
	// Windows: a running turn goroutine holds file handles on the session dir;
	// use a manual temp dir removed after Close (same rationale as the steer
	// notice harness).
	dir, err := os.MkdirTemp("", "window-matrix-test")
	if err != nil {
		t.Fatal(err)
	}
	prov := &windowMatrixProvider{gate: make(chan struct{})}
	sess := agent.NewSession("sys")
	exec := agent.New(prov, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	inner := &tabEventSink{tabID: "tab-window-matrix", ctx: context.Background()}
	doneCh := make(chan event.Event, 16)
	sink := event.FuncSink(func(e event.Event) {
		if e.Kind == event.TurnDone {
			doneCh <- e
		}
		inner.Emit(e)
	})
	ctrl := control.New(control.Options{
		Runner: exec, Executor: exec, Sink: sink,
		SessionDir:  dir,
		SessionPath: filepath.Join(dir, "s.jsonl"),
	})
	// Cleanup order matters (t.Cleanup is LIFO): the gate open registered
	// below runs BEFORE ctrl.Close, so blocked provider goroutines are freed
	// before Close waits on the autosave waitgroup.
	t.Cleanup(func() {
		ctrl.Close()
		time.Sleep(200 * time.Millisecond)
		_ = os.RemoveAll(dir)
	})
	tab := &WorkspaceTab{ID: inner.tabID, Scope: "global", Ready: true, Ctrl: ctrl, sink: inner}
	app := &App{tabs: map[string]*WorkspaceTab{tab.ID: tab}, activeTabID: tab.ID}
	inner.app = app
	inner.setContext(context.Background())
	inner.runtimeEvents.emit = func(_ context.Context, _ string, _ ...any) {}
	t.Cleanup(func() { prov.openGate() })
	return app, tab, prov, doneCh
}

func waitForWindowMatrixBodies(t *testing.T, p *windowMatrixProvider, n int) []string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b := p.snapshot(); len(b) >= n {
			return b
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("provider requests: got %d, want >= %d (bodies=%v)", len(p.snapshot()), n, p.snapshot())
	return nil
}

func waitForWindowMatrixTurnDone(t *testing.T, doneCh <-chan event.Event, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-doneCh:
		case <-time.After(15 * time.Second):
			t.Fatalf("turn done count: got %d of %d", i, n)
		}
	}
}

// requireNoWindowMatrixRequest asserts no new provider request appears for a
// settle window — the "not injected into the current turn" face.
func requireNoWindowMatrixRequest(t *testing.T, p *windowMatrixProvider, want int) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := len(p.snapshot()); got != want {
			t.Fatalf("unexpected extra provider request(s): got %d, want %d (bodies=%v)", got, want, p.snapshot())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := len(p.snapshot()); got != want {
		t.Fatalf("unexpected extra provider request(s): got %d, want %d", got, want)
	}
}

// 格 A：idle × 无围栏 steer 入口 → followup 回执 + 立即派发。
func TestWindowInjectionMatrixAIdleUnfencedSteerDispatchesNow(t *testing.T) {
	app, tab, prov, doneCh := windowMatrixTestApp(t)

	rec, err := app.EnqueueInboxSteer(tab.ID, "tl1607-A-marker", "tl1607-A-marker", "tl1607-A")
	if err != nil {
		t.Fatalf("EnqueueInboxSteer: %v", err)
	}
	if rec.Disposition != string(sessioninbox.DispositionQueuedFollowup) {
		t.Fatalf("unfenced steer must land as queued_followup (never mid-turn admission), got %q", rec.Disposition)
	}
	bodies := waitForWindowMatrixBodies(t, prov, 1)
	if !strings.Contains(bodies[0], "tl1607-A-marker") {
		t.Fatalf("idle unfenced steer must dispatch immediately as the next turn, got %q", bodies[0])
	}
	prov.openGate()
	waitForWindowMatrixTurnDone(t, doneCh, 1)
}

// 格 B：running × 无围栏 steer 入口 → 不注入当轮、不丢、下一轮注入。
func TestWindowInjectionMatrixBRunningUnfencedSteerIsNextTurnNoLoss(t *testing.T) {
	app, tab, prov, doneCh := windowMatrixTestApp(t)

	if _, err := app.StartTurnForTab(tab.ID, "tl1607-B-turn-one", "tl1607-B-sub1"); err != nil {
		t.Fatalf("StartTurnForTab: %v", err)
	}
	bodies := waitForWindowMatrixBodies(t, prov, 1)
	if !strings.Contains(bodies[0], "tl1607-B-turn-one") {
		t.Fatalf("turn one must be in flight, got %q", bodies[0])
	}

	rec, err := app.EnqueueInboxSteer(tab.ID, "tl1607-B-marker", "tl1607-B-marker", "tl1607-B")
	if err != nil {
		t.Fatalf("EnqueueInboxSteer: %v", err)
	}
	if rec.Disposition != string(sessioninbox.DispositionQueuedFollowup) {
		t.Fatalf("unfenced steer while running must stay queued_followup (no mid-turn admission), got %q", rec.Disposition)
	}
	// 不注入当轮：不新增请求，已有请求不含该消息。
	requireNoWindowMatrixRequest(t, prov, 1)
	if got := prov.snapshot(); strings.Contains(got[0], "tl1607-B-marker") {
		t.Fatalf("marker must not enter the current turn's request, got %q", got[0])
	}

	// 开闸：当轮结束（TurnDone#1）→ 关窗 kick 把排队消息派发为下一轮
	// turn（TurnDone#2）→ 不丢。
	prov.openGate()
	waitForWindowMatrixTurnDone(t, doneCh, 2)
	bodies = waitForWindowMatrixBodies(t, prov, 2)
	if strings.Contains(bodies[0], "tl1607-B-marker") {
		t.Fatalf("marker must not enter the current turn's request, got %q", bodies[0])
	}
	if !strings.Contains(bodies[1], "tl1607-B-marker") {
		t.Fatalf("queued steer must arrive as the next turn after the current one ends, got %q", bodies[1])
	}
	// 消费干净：不留 uncertain/paused 残留（不挂）。
	if items := tab.Ctrl.InboxSnapshot().Items; len(items) != 0 {
		t.Fatalf("delivered follow-up must leave the inbox, items = %+v", items)
	}
}

// 格 C：running × 精确 turnID 围栏（活跃 turn）→ steer_accepted，精确路径
// 不回退。围栏回执是本格断言；关闸前直接收尾（cleanup 开闸放行后续轮次），
// 与既有 steer notice 钉的收尾方式一致。
func TestWindowInjectionMatrixCRunningFencedSteerAccepted(t *testing.T) {
	app, tab, prov, _ := windowMatrixTestApp(t)

	start, err := app.StartTurnForTab(tab.ID, "tl1607-C-turn", "tl1607-C-sub1")
	if err != nil {
		t.Fatalf("StartTurnForTab: %v", err)
	}
	waitForWindowMatrixBodies(t, prov, 1)

	rec, err := app.EnqueueInboxSteerForTurn(tab.ID, start.TurnID, "tl1607-C-marker", "tl1607-C-marker", "tl1607-C")
	if err != nil {
		t.Fatalf("EnqueueInboxSteerForTurn: %v", err)
	}
	if rec.Disposition != string(sessioninbox.DispositionSteerAccepted) {
		t.Fatalf("fenced steer against the active turn must stay steer_accepted (no regression), got %q", rec.Disposition)
	}
}

// 格 D：stale turnID × 精确围栏 → 拒绝且零入队零派发。
func TestWindowInjectionMatrixDStaleTurnIDRejected(t *testing.T) {
	app, tab, prov, _ := windowMatrixTestApp(t)

	if _, err := app.EnqueueInboxSteerForTurn(tab.ID, "tl1607-stale-turn", "tl1607-D-marker", "tl1607-D-marker", "tl1607-D"); err == nil {
		t.Fatal("stale turnId must be rejected, not silently steered elsewhere")
	}
	requireNoWindowMatrixRequest(t, prov, 0)
	if items := tab.Ctrl.InboxSnapshot().Items; len(items) != 0 {
		t.Fatalf("rejected fenced steer must not enqueue anything, items = %+v", items)
	}
}

// 格 E：idle × followup 基线入口 → 照常派发（既有行为不变）。
func TestWindowInjectionMatrixEIdleFollowupBaselineDispatch(t *testing.T) {
	app, tab, prov, doneCh := windowMatrixTestApp(t)

	if _, err := app.EnqueueInboxFollowup(tab.ID, "tl1607-E-marker", "tl1607-E-marker", "tl1607-E"); err != nil {
		t.Fatalf("EnqueueInboxFollowup: %v", err)
	}
	bodies := waitForWindowMatrixBodies(t, prov, 1)
	if !strings.Contains(bodies[0], "tl1607-E-marker") {
		t.Fatalf("idle followup must dispatch as usual, got %q", bodies[0])
	}
	prov.openGate()
	waitForWindowMatrixTurnDone(t, doneCh, 1)
}
