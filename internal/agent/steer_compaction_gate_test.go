package agent

// tl1648 压缩中在途输入处理：压缩 pass 在飞（任务920 的 maintaining 计数）
// 时 steer 注入必须排队至压缩结束——否则注入让压缩 commit 的 CAS 失配，
// 整条 pass 被丢（errCompressStaleContext，20261116 14:50 现场
// user_dropped=6 的实证形态）。本文件钉：
//  1. gate 本体（压缩在飞 → consumeSteer 不弹队不注入）；
//  2. settle 后恢复注入（排队语义闭环）；
//  3. 无压缩时零影响（既有即时注入行为不变）；
//  4. 队列非空撑住 turn 收尾（注入机会保留，条目绝不静默丢失）；
//  5. 失败卡新字段（FoldInstalled/LastSuccessAt）与失败原因文案零虚假承诺。

import (
	"errors"
	"strings"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/tool"
)

func newSteerActiveAgent(t *testing.T) *Agent {
	t.Helper()
	a := New(testutil.NewMock("m", testutil.Turn{Text: "done"}), tool.NewRegistry(), NewSession(""), Options{}, event.Discard)
	a.steerMu.Lock()
	a.steerRunActive = true
	a.steerMu.Unlock()
	return a
}

// TestTl1648SteerWaitsForCompaction is the end-to-end pin for the acceptance
// criterion "压缩中注入→压缩完成→注入进上下文": a steer arriving while a real
// maintenance transaction is in flight stays queued (the transcript is not
// touched, so the compaction commit's CAS cannot be invalidated by it), and
// the same entry is handed out on the first consume after the pass settles.
func TestTl1648SteerWaitsForCompaction(t *testing.T) {
	a := newSteerActiveAgent(t)
	if !a.Steer("queued during compaction") {
		t.Fatal("steer during an active turn should be accepted")
	}

	// Compaction in flight (the same counter 任务920 sets past the fast path).
	a.maintaining.Add(1)
	payload, itemID, ok := a.consumeSteer()
	if ok || payload.Text != "" || itemID != "" {
		t.Fatalf("consumeSteer during compaction must hold the entry, got (%q, %q, %v)", payload.Text, itemID, ok)
	}
	if n := a.steerQueueLen(); n != 1 {
		t.Fatalf("gated steer must stay queued, len=%d", n)
	}
	for _, m := range a.Session().Messages {
		if _, isSteer := SteerText(m.Content); isSteer {
			t.Fatal("gated steer leaked into the transcript while compaction was in flight")
		}
	}

	// Pass settles (endMaintenance's last release).
	a.maintaining.Add(-1)
	payload, itemID, ok = a.consumeSteer()
	if !ok || payload.Text != "queued during compaction" {
		t.Fatalf("consumeSteer after settle must hand out the queued entry, got (%q, %q, %v)", payload.Text, itemID, ok)
	}
	if n := a.steerQueueLen(); n != 0 {
		t.Fatalf("queue should drain after settle, len=%d", n)
	}
}

// TestTl1648SteerGateIdleWithoutCompaction pins the zero-cost face: with no
// maintenance in flight the gate never fires and the pre-tl1648 consume
// behavior (immediate injection) is byte-identical.
func TestTl1648SteerGateIdleWithoutCompaction(t *testing.T) {
	a := newSteerActiveAgent(t)
	if !a.Steer("immediate") {
		t.Fatal("active steer should be accepted")
	}
	if a.maintaining.Load() != 0 {
		t.Fatalf("test setup leaked an in-flight count: %d", a.maintaining.Load())
	}
	payload, _, ok := a.consumeSteer()
	if !ok || payload.Text != "immediate" {
		t.Fatalf("consumeSteer without compaction must inject immediately, got (%q, %v)", payload.Text, ok)
	}
}

// TestTl1648QueuedSteerKeepsTurnAlive pins the survival face: the turn-exit
// check sees the gated entry and keeps the loop alive, so the entry always
// gets an injection point after the pass settles instead of being flushed as
// unapplied.
func TestTl1648QueuedSteerKeepsTurnAlive(t *testing.T) {
	a := newSteerActiveAgent(t)
	a.maintaining.Add(1)
	if !a.Steer("held") {
		t.Fatal("steer should be accepted while compaction is in flight")
	}
	if a.closeSteerIntakeIfIdle() {
		t.Fatal("a gated steer must keep the turn alive until it is injected")
	}
	a.maintaining.Add(-1)
	if _, _, ok := a.consumeSteer(); !ok {
		t.Fatal("queued steer should be consumable after settle")
	}
	if !a.closeSteerIntakeIfIdle() {
		t.Fatal("drained queue should let the turn close")
	}
}

// TestTl1648FailedCardCarriesFoldAndSuccessFacts pins the 缺口d wire facts: a
// failed pass reports whether a valid projection is already serving (the
// "reading will not drop" explanation) and when compaction last succeeded.
func TestTl1648FailedCardCarriesFoldAndSuccessFacts(t *testing.T) {
	a := New(testutil.NewMock("m", testutil.Turn{Text: "done"}), tool.NewRegistry(), NewSession(""), Options{}, event.Discard)

	var done *event.Compaction
	sink := event.FuncSink(func(e event.Event) {
		if e.Kind == event.CompactionDone && e.Compaction.Outcome == "failed" {
			c := e.Compaction
			done = &c
		}
	})
	a.SetSink(sink)

	// Before any success: no stamp, nothing installed → both facts false/zero.
	a.emitCompactionFailed(CompactionTriggerPressure, errCompressStaleContext)
	if done == nil {
		t.Fatal("failed pass must emit a CompactionDone event")
	}
	if done.FoldInstalled {
		t.Fatal("fresh session without a projection must report foldInstalled=false")
	}
	if done.LastSuccessAt != 0 {
		t.Fatalf("no success recorded yet, lastSuccessAt=%d", done.LastSuccessAt)
	}
	if done.Attempt != 1 {
		t.Fatalf("first failure must carry attempt=1, got %d", done.Attempt)
	}

	// A success stamps the clock; the next failure carries it.
	a.emitCompactionSucceeded(CompactionTriggerPressure, 3, "summary", "", CompactionTelemetry{})
	if a.sess.lastCompactionSuccessAt.Load() == 0 {
		t.Fatal("successful install must stamp lastCompactionSuccessAt")
	}
	done = nil
	a.emitCompactionFailed(CompactionTriggerPressure, errCompressStaleContext)
	if done == nil {
		t.Fatal("second failed pass must emit a Done event")
	}
	if done.LastSuccessAt == 0 {
		t.Fatal("failure after a success must carry the lastSuccessAt stamp")
	}
	if done.Attempt != 1 {
		t.Fatalf("success must reset the failure streak, got attempt=%d", done.Attempt)
	}
}

// TestTl1648StaleReasonNamesTheRealRetry pins 缺口b on the Go side: the stale
// failure reason must stop promising "will retry" (the pass is blocked for
// this generation) and name the next step that actually exists — the next
// context check. The frontend localizes by exact match, so the string must
// stay in the closed set documented in desktop TranscriptCards.
func TestTl1648StaleReasonNamesTheRealRetry(t *testing.T) {
	reason := compactionFailureReason(errCompressStaleContext)
	if strings.Contains(reason, "will retry") {
		t.Fatalf("stale reason still promises a retry the generation blocks: %q", reason)
	}
	if !strings.Contains(reason, "next context check") {
		t.Fatalf("stale reason must name the real next step, got %q", reason)
	}
	if got := compactionFailureReason(nil); got != "" {
		t.Fatalf("nil error must map to empty reason, got %q", got)
	}
	// Other classifications unchanged.
	if got := compactionFailureReason(errors.New("boom")); got != "summarizer request failed" {
		t.Fatalf("unclassified error reason changed: %q", got)
	}
}
