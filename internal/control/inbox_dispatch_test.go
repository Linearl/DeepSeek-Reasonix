package control

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/sessioninbox"
)

const inboxDispatchTestTimeout = 15 * time.Second

func TestClosedControllerCannotOpenInboxFromLateDispatch(t *testing.T) {
	dir := t.TempDir()
	c := New(Options{})
	// Model the dispatcher having a persisted path but no opened sidecar yet.
	c.mu.Lock()
	c.sessionPath = filepath.Join(dir, "session.jsonl")
	c.mu.Unlock()
	c.SetBeforeInboxDispatch(func(*Controller) (func(), error) { t.Error("closed controller entered admission"); return nil, nil })
	c.Close()
	c.NotifyInboxRuntimeReady()
	if _, err := c.ensureInbox(); err == nil {
		t.Fatal("closed controller opened an inbox")
	}
	c.rebindInbox()
	c.autosaveWG.Wait()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("late dispatch created sidecars: %v %v", entries, err)
	}
}

type inboxDispatchRunner struct {
	inputs chan string
}

func (r *inboxDispatchRunner) Run(_ context.Context, input string) error {
	r.inputs <- input
	return nil
}

func newInboxDispatchController(t *testing.T) (*Controller, *inboxDispatchRunner, <-chan struct{}) {
	t.Helper()
	dir := t.TempDir()
	runner := &inboxDispatchRunner{inputs: make(chan string, 8)}
	done := make(chan struct{}, 8)
	c := New(Options{
		Runner: runner,
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.TurnDone {
				done <- struct{}{}
			}
		}),
		SessionDir:  dir,
		SessionPath: filepath.Join(dir, "session.jsonl"),
	})
	t.Cleanup(func() {
		c.Close()
		c.autosaveWG.Wait()
	})
	return c, runner, done
}

func failInboxDispatchWait(t *testing.T, c *Controller, waitingFor string) {
	t.Helper()
	c.inbox.mu.Lock()
	active := c.inbox.activeIDs()
	dispatching := c.inbox.dispatching
	dispatchPending := c.inbox.dispatchPending
	c.inbox.mu.Unlock()
	sort.Strings(active)
	t.Fatalf(
		"timed out after %s waiting for %s: runtime=%+v inbox=%+v active_items=%v dispatching=%t dispatch_pending=%t",
		inboxDispatchTestTimeout,
		waitingFor,
		c.RuntimeStatus(),
		c.InboxSnapshot(),
		active,
		dispatching,
		dispatchPending,
	)
}

func waitForInboxDispatch(t *testing.T, c *Controller, runner *inboxDispatchRunner) string {
	t.Helper()
	select {
	case input := <-runner.inputs:
		return input
	case <-time.After(inboxDispatchTestTimeout):
		failInboxDispatchWait(t, c, "inbox dispatch")
		return ""
	}
}

func waitForInboxTurnDone(t *testing.T, c *Controller, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(inboxDispatchTestTimeout):
		failInboxDispatchWait(t, c, "inbox turn completion")
	}
}

func TestEndRotationDispatchesQueuedInboxItem(t *testing.T) {
	c, runner, done := newInboxDispatchController(t)
	if err := c.beginRotation(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.TryEnqueueFollowup(InboxRequest{
		Intent: sessioninbox.IntentFollowup,
		Submit: "queued during rotation",
	}); err != nil {
		t.Fatal(err)
	}
	c.endRotation()

	if got := waitForInboxDispatch(t, c, runner); got != "queued during rotation" {
		t.Fatalf("dispatched input = %q", got)
	}
	waitForInboxTurnDone(t, c, done)
}

func TestRejectedIdleSteerDispatchesAsFollowup(t *testing.T) {
	c, runner, done := newInboxDispatchController(t)
	rec, err := c.EnqueueInbox(InboxRequest{
		Intent: sessioninbox.IntentSteer,
		Submit: "late steer becomes follow-up",
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := c.TrySteerInboxItem(rec.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Disposition != sessioninbox.DispositionQueuedFollowup {
		t.Fatalf("disposition = %q", receipt.Disposition)
	}

	if got := waitForInboxDispatch(t, c, runner); got != "late steer becomes follow-up" {
		t.Fatalf("dispatched input = %q", got)
	}
	waitForInboxTurnDone(t, c, done)
}

func TestInboxDispatchKickDuringEmptyScanIsNotLost(t *testing.T) {
	c, runner, done := newInboxDispatchController(t)
	scanReached := make(chan struct{})
	releaseScan := make(chan struct{})
	var once sync.Once
	c.inbox.mu.Lock()
	c.inbox.afterDispatchScan = func(found bool) {
		if found {
			return
		}
		once.Do(func() {
			close(scanReached)
			<-releaseScan
		})
	}
	c.inbox.mu.Unlock()

	dispatchReturned := make(chan struct{})
	go func() {
		c.maybeDispatchInbox()
		close(dispatchReturned)
	}()
	select {
	case <-scanReached:
	case <-time.After(inboxDispatchTestTimeout):
		failInboxDispatchWait(t, c, "dispatcher empty scan")
	}
	if _, err := c.EnqueueInbox(InboxRequest{Submit: "arrived during empty scan"}); err != nil {
		t.Fatal(err)
	}
	// This kick lands while the first dispatcher still owns the handoff. The
	// pending level must make that dispatcher scan again before it exits.
	c.maybeDispatchInbox()
	close(releaseScan)

	select {
	case <-dispatchReturned:
	case <-time.After(inboxDispatchTestTimeout):
		failInboxDispatchWait(t, c, "dispatcher return")
	}
	if got := waitForInboxDispatch(t, c, runner); got != "arrived during empty scan" {
		t.Fatalf("dispatched input = %q", got)
	}
	waitForInboxTurnDone(t, c, done)
}

func TestInboxDispatchRetriesTransientOwnerFailure(t *testing.T) {
	c, runner, done := newInboxDispatchController(t)
	retryReady := make(chan func(), 1)
	failedOnce := false
	c.inbox.mu.Lock()
	c.inbox.beforeDispatchSubmit = func(string) error {
		if failedOnce {
			return nil
		}
		failedOnce = true
		return errors.New("temporary dispatch failure")
	}
	c.inbox.scheduleDispatchRetry = func(_ time.Duration, retry func()) {
		retryReady <- retry
	}
	c.inbox.mu.Unlock()
	if _, err := c.EnqueueInbox(InboxRequest{Submit: "retry me"}); err != nil {
		t.Fatal(err)
	}
	c.maybeDispatchInbox()

	var retry func()
	select {
	case retry = <-retryReady:
	case <-time.After(inboxDispatchTestTimeout):
		failInboxDispatchWait(t, c, "transient failure retry")
	}
	select {
	case got := <-runner.inputs:
		t.Fatalf("item dispatched before scheduled retry: %q", got)
	default:
	}
	retry()
	if got := waitForInboxDispatch(t, c, runner); got != "retry me" {
		t.Fatalf("retried input = %q", got)
	}
	waitForInboxTurnDone(t, c, done)
}

type gatedInboxDispatchRunner struct {
	inputs       chan string
	firstStarted chan struct{}
	releaseFirst chan struct{}
	once         sync.Once
}

func (r *gatedInboxDispatchRunner) Run(ctx context.Context, input string) error {
	r.inputs <- input
	blocked := false
	r.once.Do(func() {
		blocked = true
		close(r.firstStarted)
	})
	if !blocked {
		return nil
	}
	select {
	case <-r.releaseFirst:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestNaturalCompletionAutoDispatchesDurableFIFO(t *testing.T) {
	dir := t.TempDir()
	runner := &gatedInboxDispatchRunner{
		inputs:       make(chan string, 8),
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}
	done := make(chan struct{}, 8)
	c := New(Options{
		Runner: runner,
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.TurnDone {
				done <- struct{}{}
			}
		}),
		SessionDir:  dir,
		SessionPath: filepath.Join(dir, "session.jsonl"),
	})
	t.Cleanup(func() {
		c.Close()
		c.autosaveWG.Wait()
	})

	c.Submit("active turn")
	select {
	case <-runner.firstStarted:
	case <-time.After(inboxDispatchTestTimeout):
		failInboxDispatchWait(t, c, "active turn start")
	}
	if got := <-runner.inputs; got != "active turn" {
		t.Fatalf("initial input = %q", got)
	}
	for _, input := range []string{"queued one", "queued two"} {
		if _, err := c.EnqueueInbox(InboxRequest{Intent: sessioninbox.IntentFollowup, Submit: input}); err != nil {
			t.Fatal(err)
		}
	}
	close(runner.releaseFirst)
	waitForInboxTurnDone(t, c, done)
	for _, want := range []string{"queued one", "queued two"} {
		if got := waitForInboxDispatch(t, c, &inboxDispatchRunner{inputs: runner.inputs}); got != want {
			t.Fatalf("FIFO input = %q, want %q", got, want)
		}
		waitForInboxTurnDone(t, c, done)
	}
	if snap := c.InboxSnapshot(); len(snap.Items) != 0 || snap.Paused {
		t.Fatalf("completed FIFO left inbox state: %+v", snap)
	}
}

// ---- 任务579: runtime-unpublished 死路径的有界重试（5/15/45s，上限 3 次）----

// runtimeUnpublishedAdmission is a host admission that always answers the
// 任务579 dead-path error; publish flips it to success for recovery tests.
type runtimeUnpublishedAdmission struct{ published atomic.Bool }

func (a *runtimeUnpublishedAdmission) admit(*Controller) (func(), error) {
	if a.published.Load() {
		return func() {}, nil
	}
	return nil, ErrInboxRuntimeUnpublished
}

// captureRuntimeRetries installs the deterministic timer seam and returns a
// channel delivering every armed retry (delay, fire).
func captureRuntimeRetries(c *Controller) <-chan struct {
	delay time.Duration
	fire  func()
} {
	ch := make(chan struct {
		delay time.Duration
		fire  func()
	}, 8)
	c.inbox.mu.Lock()
	c.inbox.scheduleRuntimeRetry = func(delay time.Duration, retry func()) {
		ch <- struct {
			delay time.Duration
			fire  func()
		}{delay, retry}
	}
	c.inbox.mu.Unlock()
	return ch
}

// 验收③+④（控制层）：runtime 未发布时开轮失败 ⇒ 恰好 3 次有界重试
// （5s/15s/45s），第 3 次仍失败 ⇒ 用尽报告（钩子收到 item 与协作回执坐标），
// 期间不产生任何 turn（幂等：无重复轮）。
func TestInboxRuntimeUnpublishedRetriesBoundedThenReports(t *testing.T) {
	c, runner, _ := newInboxDispatchController(t)
	admission := &runtimeUnpublishedAdmission{}
	c.SetBeforeInboxDispatch(admission.admit)
	retries := captureRuntimeRetries(c)

	exhausted := make(chan InboxDispatchExhausted, 1)
	c.SetOnInboxDispatchExhausted(func(info InboxDispatchExhausted) { exhausted <- info })

	rec, err := c.TryEnqueueFollowup(InboxRequest{
		Intent:       sessioninbox.IntentFollowup,
		Submit:       "stuck on a detached runtime",
		Source:       "collab:sender-1",
		CollabMsgID:  "msg_1",
		CollabMailTo: "target-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	wantDelays := []time.Duration{5 * time.Second, 15 * time.Second, 45 * time.Second}
	for i, wantDelay := range wantDelays {
		var armed struct {
			delay time.Duration
			fire  func()
		}
		select {
		case armed = <-retries:
		case <-time.After(inboxDispatchTestTimeout):
			failInboxDispatchWait(t, c, fmt.Sprintf("runtime retry %d", i+1))
		}
		if armed.delay != wantDelay {
			t.Fatalf("retry %d delay = %s, want %s", i+1, armed.delay, wantDelay)
		}
		armed.fire()
	}

	select {
	case info := <-exhausted:
		if info.ItemID != rec.ItemID || info.Attempts != len(inboxRuntimeRetryBackoff) {
			t.Fatalf("exhaustion report = %+v, want item %s with %d attempts", info, rec.ItemID, len(inboxRuntimeRetryBackoff))
		}
		if info.CollabMsgID != "msg_1" || info.CollabMailTo != "target-1" || info.Source != "collab:sender-1" {
			t.Fatalf("exhaustion report lost collab receipt coordinates: %+v", info)
		}
		if !errors.Is(info.Err, ErrInboxRuntimeUnpublished) {
			t.Fatalf("exhaustion report err = %v", info.Err)
		}
	case <-time.After(inboxDispatchTestTimeout):
		failInboxDispatchWait(t, c, "exhaustion report")
	}
	// 用尽 ≠ 丢弃：item 仍在队列里，且全程没有产生任何 turn（幂等）。
	snap := c.InboxSnapshot()
	if len(snap.Items) != 1 || snap.Items[0].ID != rec.ItemID {
		t.Fatalf("exhausted item must stay queued, snapshot = %+v", snap)
	}
	c.autosaveWG.Wait()
	select {
	case got := <-runner.inputs:
		t.Fatalf("exhausted budget must not have opened a turn, got %q", got)
	default:
	}
}

// 验收⑤（幂等）：3 次重试期间 runtime 发布 ⇒ 恰好开一轮、恰好消费一次，
// 不因重试叠出重复 turn。
func TestInboxRuntimeRecoveryOpensExactlyOneTurn(t *testing.T) {
	c, runner, done := newInboxDispatchController(t)
	admission := &runtimeUnpublishedAdmission{}
	c.SetBeforeInboxDispatch(admission.admit)
	retries := captureRuntimeRetries(c)

	if _, err := c.TryEnqueueFollowup(InboxRequest{Intent: sessioninbox.IntentFollowup, Submit: "wake me once"}); err != nil {
		t.Fatal(err)
	}

	// 前两次重试仍失败。收到第 3 次装填（证明第 2 次排水的「未发布」结论已
	// 落定——排水在 autosaveWG 上异步跑，先发布再等会在竞态下跳过第 3 次）。
	var third func()
	for i := 0; i < 2; i++ {
		select {
		case a := <-retries:
			a.fire()
		case <-time.After(inboxDispatchTestTimeout):
			failInboxDispatchWait(t, c, fmt.Sprintf("runtime retry %d", i+1))
		}
	}
	select {
	case a := <-retries:
		third = a.fire
	case <-time.After(inboxDispatchTestTimeout):
		failInboxDispatchWait(t, c, "runtime retry 3 arming")
	}
	admission.published.Store(true)
	third()

	if got := waitForInboxDispatch(t, c, runner); got != "wake me once" {
		t.Fatalf("recovered input = %q", got)
	}
	waitForInboxTurnDone(t, c, done)
	c.autosaveWG.Wait()
	// 不许有第二轮。
	select {
	case got := <-runner.inputs:
		t.Fatalf("duplicate turn after recovery: %q", got)
	default:
	}
	if snap := c.InboxSnapshot(); len(snap.Items) != 0 {
		t.Fatalf("recovered item must be consumed, snapshot = %+v", snap)
	}
}

// 验收③（预算复位）：用尽后队列清空 ⇒ 下一条消息拿到全新的 3 次预算，
// 而不是继承已烧尽的预算立即用尽。
func TestInboxRuntimeRetryBudgetRearmsAfterEpisode(t *testing.T) {
	c, _, _ := newInboxDispatchController(t)
	admission := &runtimeUnpublishedAdmission{}
	c.SetBeforeInboxDispatch(admission.admit)
	retries := captureRuntimeRetries(c)
	c.SetOnInboxDispatchExhausted(func(InboxDispatchExhausted) {})

	if _, err := c.TryEnqueueFollowup(InboxRequest{Intent: sessioninbox.IntentFollowup, Submit: "episode one"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(inboxRuntimeRetryBackoff); i++ {
		select {
		case a := <-retries:
			a.fire()
		case <-time.After(inboxDispatchTestTimeout):
			failInboxDispatchWait(t, c, "episode one retry")
		}
	}
	// 用尽后删除该 item；下一次空扫描把预算复位。
	if err := c.DeleteInboxItem(c.InboxSnapshot().Items[0].ID); err != nil {
		t.Fatal(err)
	}
	c.maybeDispatchInbox()
	c.autosaveWG.Wait()

	// 新消息必须重新拿到 3 次预算（第一枪就是 5s，而不是立即用尽）。
	if _, err := c.TryEnqueueFollowup(InboxRequest{Intent: sessioninbox.IntentFollowup, Submit: "episode two"}); err != nil {
		t.Fatal(err)
	}
	select {
	case a := <-retries:
		if a.delay != inboxRuntimeRetryBackoff[0] {
			t.Fatalf("rearmed budget first delay = %s, want %s", a.delay, inboxRuntimeRetryBackoff[0])
		}
	case <-time.After(inboxDispatchTestTimeout):
		failInboxDispatchWait(t, c, "episode two first retry")
	}
}
