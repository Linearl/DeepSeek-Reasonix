package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
)

type exactTurnRunner struct {
	once    sync.Once
	started chan struct{}
}

func (r *exactTurnRunner) Run(ctx context.Context, _ string) error {
	r.once.Do(func() { close(r.started) })
	<-ctx.Done()
	return ctx.Err()
}

func TestTurnRuntimeAPIRoutesStopAnswerAndReplayByExactTurn(t *testing.T) {
	dir := t.TempDir()
	runner := &exactTurnRunner{started: make(chan struct{})}
	sink := &tabEventSink{tabID: "tab", ctx: context.Background()}
	terminal := make(chan event.Event, 1)
	sink.SetBotSink(event.FuncSink(func(e event.Event) {
		if e.Kind == event.TurnDone {
			terminal <- e
		}
	}))
	ctrl := control.New(control.Options{
		Runner: runner, Sink: sink, SessionDir: dir,
		SessionPath: filepath.Join(dir, "session.jsonl"),
	})
	t.Cleanup(ctrl.Close)
	tab := &WorkspaceTab{ID: "tab", Scope: "global", Ready: true, Ctrl: ctrl, sink: sink}
	app := &App{tabs: map[string]*WorkspaceTab{tab.ID: tab}, activeTabID: tab.ID}
	sink.app = app

	start, err := app.StartTurnForTab(tab.ID, "hold this turn", "submission-1")
	if err != nil {
		t.Fatalf("StartTurnForTab: %v", err)
	}
	if !strings.HasPrefix(start.TurnID, "turn_") || start.SubmissionID != "submission-1" {
		t.Fatalf("start receipt = %+v, want stable turn and submission ids", start)
	}
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("turn runner did not start")
	}

	if err := app.AnswerPromptForTab(tab.ID, "turn_stale", "prompt-1", nil); err == nil {
		t.Fatal("stale turn id answered an active turn prompt")
	}
	if _, err := app.EnqueueInboxSteerForTurn(tab.ID, "turn_stale", "late steer", "late steer", ""); err == nil {
		t.Fatal("stale turn id steered the active turn")
	}
	if err := app.AnswerPromptForTab(tab.ID, start.TurnID, "already-answered", nil); err != nil {
		t.Fatalf("same-turn duplicate/unknown answer should be idempotent: %v", err)
	}

	before, err := app.TurnEventsForTab(tab.ID, 0)
	if err != nil {
		t.Fatalf("TurnEventsForTab before cancel: %v", err)
	}
	if len(before.Events) == 0 || before.Events[0].Status != event.TurnQueued {
		t.Fatalf("events before cancel = %+v, want durable queued prefix", before)
	}
	if err := app.InterruptTurnForTab(tab.ID, start.TurnID); err != nil {
		t.Fatalf("InterruptTurnForTab: %v", err)
	}
	select {
	case done := <-terminal:
		if done.Status != event.TurnInterrupted {
			t.Fatalf("terminal status = %q, want interrupted", done.Status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not reach terminal state after exact interrupt")
	}
	after, err := app.TurnEventsForTab(tab.ID, before.Events[len(before.Events)-1].Sequence)
	if err != nil {
		t.Fatalf("TurnEventsForTab after cancel: %v", err)
	}
	if len(after.Events) == 0 || after.Events[len(after.Events)-1].Status != event.TurnInterrupted {
		t.Fatalf("events after cancel = %+v, want non-nil interrupted suffix", after)
	}
	empty, err := app.TurnEventsForTab(tab.ID, after.Events[len(after.Events)-1].Sequence)
	if err != nil {
		t.Fatalf("empty replay: %v", err)
	}
	if empty.Events == nil || len(empty.Events) != 0 {
		t.Fatalf("empty replay events = %#v, want []", empty.Events)
	}
}

// A Stop button rendered for an earlier turn must still stop the turn that is
// running now; only an idle tab is reported back, with a stable code.
func TestInterruptTurnForTabStopsActiveWorkDespiteStaleTurnID(t *testing.T) {
	dir := t.TempDir()
	runner := &exactTurnRunner{started: make(chan struct{})}
	sink := &tabEventSink{tabID: "tab", ctx: context.Background()}
	terminal := make(chan event.Event, 1)
	sink.SetBotSink(event.FuncSink(func(e event.Event) {
		if e.Kind == event.TurnDone {
			terminal <- e
		}
	}))
	ctrl := control.New(control.Options{
		Runner: runner, Sink: sink, SessionDir: dir,
		SessionPath: filepath.Join(dir, "session.jsonl"),
	})
	t.Cleanup(ctrl.Close)
	tab := &WorkspaceTab{ID: "tab", Scope: "global", Ready: true, Ctrl: ctrl, sink: sink}
	app := &App{tabs: map[string]*WorkspaceTab{tab.ID: tab}, activeTabID: tab.ID}
	sink.app = app

	if err := app.InterruptTurnForTab(tab.ID, "turn_none"); !errors.Is(err, errTurnNotRunning) {
		t.Fatalf("idle stop = %v, want %v", err, errTurnNotRunning)
	}
	if err := app.InterruptTurnForTab(tab.ID, "turn_none"); err == nil || err.Error() != "reasonix_error:turn_not_running" {
		t.Fatalf("idle stop wire error = %v, want stable reasonix_error code", err)
	}

	if _, err := app.StartTurnForTab(tab.ID, "hold this turn", "submission-1"); err != nil {
		t.Fatalf("StartTurnForTab: %v", err)
	}
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("turn runner did not start")
	}
	if err := app.InterruptTurnForTab(tab.ID, "turn_stale"); err != nil {
		t.Fatalf("stale-id stop = %v, want the active turn interrupted", err)
	}
	select {
	case done := <-terminal:
		if done.Status != event.TurnInterrupted {
			t.Fatalf("terminal status = %q, want interrupted", done.Status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not reach terminal state after stale-id stop")
	}
}

func TestStartTurnForTabReturnsManagementDispositionWithoutTurnID(t *testing.T) {
	dir := t.TempDir()
	sink := &tabEventSink{tabID: "tab", ctx: context.Background()}
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: filepath.Join(dir, "session.jsonl"), Sink: sink})
	t.Cleanup(ctrl.Close)
	tab := &WorkspaceTab{ID: "tab", Scope: "global", Ready: true, Ctrl: ctrl, sink: sink}
	app := &App{tabs: map[string]*WorkspaceTab{tab.ID: tab}, activeTabID: tab.ID}
	sink.app = app

	start, err := app.StartTurnForTab(tab.ID, "/context", "submission-management")
	if err != nil {
		t.Fatalf("StartTurnForTab management command: %v", err)
	}
	if start.Disposition != control.SubmitManagementHandled || start.TurnID != "" {
		t.Fatalf("management receipt = %+v, want management_handled without turn id", start)
	}
	replay, err := app.TurnEventsForTab(tab.ID, 0)
	if err != nil {
		t.Fatalf("TurnEventsForTab: %v", err)
	}
	if len(replay.Events) != 0 {
		t.Fatalf("management command created durable turn events: %+v", replay.Events)
	}
}

// 任务900（上游 #9806/PR10781 同款 TestStartTurnForTabAnswersWithAdmitting
// ControllerReceipt）：turn id 属于受理本次提交的那个控制器。受理锁释放后
// tab 被换上重建后的控制器（/compact 完成时的 runtime rebuild 即此形状；此
// 处用 TurnStarted 时替换 tab.Ctrl 站位该重建窗口），回执仍须来自受理控制
// 器——向替换者查询会拿到空 turn id（579/580 turn admission 族：消息已受理
// 运行却无 durable turn id 关联）。
func TestStartTurnForTabAnswersWithAdmittingControllerReceipt(t *testing.T) {
	dir := t.TempDir()
	runner := &exactTurnRunner{started: make(chan struct{})}
	sink := &tabEventSink{tabID: "tab", ctx: context.Background()}
	terminal := make(chan event.Event, 1)
	sink.SetBotSink(event.FuncSink(func(e event.Event) {
		if e.Kind == event.TurnDone {
			terminal <- e
		}
	}))
	tab := &WorkspaceTab{ID: "tab", Scope: "global", Ready: true, sink: sink}
	app := &App{tabs: map[string]*WorkspaceTab{tab.ID: tab}, activeTabID: tab.ID}
	sink.app = app

	replacementDir := t.TempDir()
	replacement := control.New(control.Options{SessionDir: replacementDir, SessionPath: filepath.Join(replacementDir, "session.jsonl")})
	t.Cleanup(replacement.Close)
	var swap sync.Once
	ctrl := control.New(control.Options{
		Runner: runner, SessionDir: dir, SessionPath: filepath.Join(dir, "session.jsonl"),
		Sink: event.FuncSink(func(e event.Event) {
			sink.Emit(e)
			if e.Kind == event.TurnStarted {
				swap.Do(func() {
					app.mu.Lock()
					tab.Ctrl = replacement
					app.mu.Unlock()
				})
			}
		}),
	})
	t.Cleanup(ctrl.Close)
	tab.Ctrl = ctrl

	start, err := app.StartTurnForTab(tab.ID, "hold this turn", "submission-swapped")
	if err != nil {
		t.Fatalf("StartTurnForTab: %v", err)
	}
	if want := ctrl.TurnIDForSubmission("submission-swapped"); want == "" || start.TurnID != want {
		t.Fatalf("turn id = %q, want the admitting controller's receipt %q", start.TurnID, want)
	}
	if start.SubmissionID != "submission-swapped" {
		t.Fatalf("submission id = %q, want the submitted id", start.SubmissionID)
	}
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("turn runner did not start")
	}
	ctrl.Cancel()
	select {
	case done := <-terminal:
		if done.Status != event.TurnInterrupted {
			t.Fatalf("terminal status = %q, want interrupted", done.Status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not finish after cancel")
	}
}

func TestStartTurnForTabRejectsManagementDuringActiveTurn(t *testing.T) {
	dir := t.TempDir()
	runner := &exactTurnRunner{started: make(chan struct{})}
	sink := &tabEventSink{tabID: "tab", ctx: context.Background()}
	terminal := make(chan event.Event, 1)
	sink.SetBotSink(event.FuncSink(func(e event.Event) {
		if e.Kind == event.TurnDone {
			terminal <- e
		}
	}))
	ctrl := control.New(control.Options{
		Runner: runner, Sink: sink, SessionDir: dir,
		SessionPath: filepath.Join(dir, "session.jsonl"),
	})
	t.Cleanup(ctrl.Close)
	tab := &WorkspaceTab{ID: "tab", Scope: "global", Ready: true, Ctrl: ctrl, sink: sink}
	app := &App{tabs: map[string]*WorkspaceTab{tab.ID: tab}, activeTabID: tab.ID}
	sink.app = app

	start, err := app.StartTurnForTab(tab.ID, "hold this turn", "submission-active")
	if err != nil {
		t.Fatalf("StartTurnForTab active turn: %v", err)
	}
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("turn runner did not start")
	}
	if _, err := app.StartTurnForTab(tab.ID, "/context", "submission-management"); !errors.Is(err, control.ErrTurnRunning) {
		t.Fatalf("management command during active turn = %v, want ErrTurnRunning", err)
	}
	status := ctrl.RuntimeStatus()
	if !status.Running || status.TurnID != start.TurnID {
		t.Fatalf("active turn changed after rejected management command: %+v", status)
	}
	ctrl.Cancel()
	select {
	case <-terminal:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not finish after cancellation")
	}
}

// tl1304 ②: the stop button's first press must ride the 461-P7 escalation
// machine (CancelStop), not the bare cancel — a wedged turn that ignores the
// graceful L1 then gets the automatic grace→force ladder, and every later
// press (from the same entry or CancelStopForTab) advances the same state.
// The runner ignores BOTH cancellation contexts (the wedged-tool shape), so
// the levels only move when presses move them.
type wedgedTurnRunner struct {
	started chan struct{}
	release chan struct{}
	bodyCtx context.Context
}

func (r *wedgedTurnRunner) Run(ctx context.Context, _ string) error {
	r.bodyCtx = ctx
	r.started <- struct{}{}
	<-r.release
	return nil
}

func TestInterruptTurnForTabDrivesStopEscalationLevels(t *testing.T) {
	dir := t.TempDir()
	runner := &wedgedTurnRunner{started: make(chan struct{}, 1), release: make(chan struct{})}
	sink := &tabEventSink{tabID: "tab", ctx: context.Background()}
	sink.SetBotSink(event.FuncSink(func(event.Event) {}))
	ctrl := control.New(control.Options{
		Runner: runner, Sink: sink, SessionDir: dir,
		SessionPath: filepath.Join(dir, "session.jsonl"),
	})
	t.Cleanup(ctrl.Close)
	t.Cleanup(func() { close(runner.release) })
	tab := &WorkspaceTab{ID: "tab", Scope: "global", Ready: true, Ctrl: ctrl, sink: sink}
	app := &App{tabs: map[string]*WorkspaceTab{tab.ID: tab}, activeTabID: tab.ID}
	sink.app = app

	if _, err := app.StartTurnForTab(tab.ID, "wedged turn", "submission-1"); err != nil {
		t.Fatalf("StartTurnForTab: %v", err)
	}
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("turn runner did not start")
	}

	waitStopLevelAtLeast := func(want int, label string) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if got := ctrl.RuntimeStatus().StopLevel; got >= want {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("%s: stop level = %d, want >= %d", label, ctrl.RuntimeStatus().StopLevel, want)
	}

	// 1st press: L1 graceful cancel + the 30s 保底 armed. The bare cancel this
	// entry used before left StopLevel at None forever.
	if err := app.InterruptTurnForTab(tab.ID, "turn_stale"); err != nil {
		t.Fatalf("first press: %v", err)
	}
	waitStopLevelAtLeast(control.StopLevelNormal, "first press must arm L1 (CancelStop entry)")
	select {
	case <-runner.bodyCtx.Done():
	default:
		t.Fatal("L1 must cancel the turn context (the graceful half)")
	}

	// 2nd press through the same entry: L2 force grace with a countdown.
	if err := app.InterruptTurnForTab(tab.ID, "turn_stale"); err != nil {
		t.Fatalf("second press: %v", err)
	}
	waitStopLevelAtLeast(control.StopLevelGrace, "second press must open the L2 force grace")
	if st := ctrl.RuntimeStatus(); st.StopDeadlineUnix <= time.Now().Unix() {
		t.Fatalf("L2 must carry a future countdown deadline, got %d", st.StopDeadlineUnix)
	}

	// 3rd press through the other surface (CancelStopForTab): same machine → L3.
	app.CancelStopForTab(tab.ID)
	waitStopLevelAtLeast(control.StopLevelForce, "third press must fire L3 force")
	select {
	case <-agent.StopForceDone(runner.bodyCtx):
	default:
		t.Fatal("L3 must fire the executor force signal")
	}
}
