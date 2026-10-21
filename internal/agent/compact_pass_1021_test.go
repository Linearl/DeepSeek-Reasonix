package agent

// 任务 1021 验收（后端半）：多轮压缩的会话轮次计数——CompactionStarted/Done
// 携带 1-based Pass，成功的 pass 关闭会话（下一轮从 1 重开），失败/中止的
// pass 保持会话开放（retry 继续同一会话）。
//
// 前端 reducer 依赖这个计数判定「会话是否仍在跑」：一个 pass 的 ok Done 只在
// 它是会话最后一轮时才落「上下文已压缩」终态。

import (
	"context"
	"errors"
	"testing"

	"reasonix/internal/event"
)

// TestCompactionPassCountsAcrossSessions pins the pass lifecycle:
//   - each Started emits pass = session round (1, 2, ...);
//   - a FAILED pass keeps the session open — the retry's Started continues
//     counting (pass 2) on the same card;
//   - a successful pass CLOSES the session — the next episode's first Started
//     restarts at pass 1, so a terminal card's round count never absorbs a
//     later, unrelated round.
func TestCompactionPassCountsAcrossSessions(t *testing.T) {
	sess := foldableSessionOverForce(6)
	// "boom" is not transient-retryable: the first pass FAILS visibly and the
	// blocked view backs off the same-view retry, so the second pass runs on a
	// CHANGED view (appended messages) — the follow-up round the multi-round
	// screenshot shows.
	a := agentOverForce(t, &fakeProvider{streamErrs: []error{errors.New("boom")}, reply: "- kept: alpha constraint"}, sess)

	type passEvent struct {
		kind    string // "started" | "done"
		pass    int
		outcome string
	}
	var events []passEvent
	a.svc.sink = event.FuncSink(func(e event.Event) {
		switch e.Kind {
		case event.CompactionStarted:
			events = append(events, passEvent{kind: "started", pass: e.Compaction.Pass})
		case event.CompactionDone:
			events = append(events, passEvent{kind: "done", pass: e.Compaction.Pass, outcome: e.Compaction.Outcome})
		}
	})

	// Pass 1: fails.
	_ = prepareContext(context.Background(), a, CompactionTriggerPressure)
	// New view (the changed input lifts the same-view backoff) — pass 2 runs
	// and succeeds, closing the session.
	sess.Messages = append(sess.Messages, foldableSessionOverForce(1).Messages...)
	_ = prepareContext(context.Background(), a, CompactionTriggerPressure)

	if len(events) < 4 {
		t.Fatalf("events = %+v, want started/done pairs for both passes", events)
	}
	if events[0].kind != "started" || events[0].pass != 1 {
		t.Fatalf("first event = %+v, want started pass 1", events[0])
	}
	if events[1].kind != "done" || events[1].outcome != "failed" || events[1].pass != 1 {
		t.Fatalf("second event = %+v, want failed done pass 1 (session stays open)", events[1])
	}
	if events[2].kind != "started" || events[2].pass != 2 {
		t.Fatalf("third event = %+v, want started pass 2 (same session continues)", events[2])
	}
	if events[3].kind != "done" || events[3].outcome != "ok" || events[3].pass != 2 {
		t.Fatalf("fourth event = %+v, want ok done pass 2 (session closes)", events[3])
	}
	if got := a.sess.compactionSessionPass.Load(); got != 0 {
		t.Fatalf("session pass after the successful close = %d, want 0 (next episode restarts at 1)", got)
	}

	// A new episode's first Started restarts the count at pass 1: a fresh
	// agent over the same over-threshold transcript is exactly the reopen
	// shape (new lineage → reset → first compaction of the new session).
	sess2 := foldableSessionOverForce(6)
	b := agentOverForce(t, &fakeProvider{reply: "- kept: alpha constraint"}, sess2)
	var first event.Compaction
	b.svc.sink = event.FuncSink(func(e event.Event) {
		if e.Kind == event.CompactionStarted && first.Pass == 0 {
			first = e.Compaction
		}
	})
	_ = prepareContext(context.Background(), b, CompactionTriggerPressure)
	if first.Pass != 1 {
		t.Fatalf("first Started of a new session = pass %d, want 1", first.Pass)
	}
}

// TestCompactionPassZeroForLegacyWiring pins the wire-compat floor: an event
// emitted through the legacy path (no session bookkeeping consulted) must not
// fabricate a pass number — 0 keeps pre-1021 consumers byte-compatible.
func TestCompactionPassZeroForLegacyWiring(t *testing.T) {
	a := &Agent{}
	var done event.Compaction
	a.svc.sink = event.FuncSink(func(e event.Event) {
		if e.Kind == event.CompactionDone {
			done = e.Compaction
		}
	})
	a.emitCompactionAborted("manual")
	if done.Pass != 0 {
		t.Fatalf("aborted done pass = %d, want 0 (fresh agent, no session)", done.Pass)
	}
}
