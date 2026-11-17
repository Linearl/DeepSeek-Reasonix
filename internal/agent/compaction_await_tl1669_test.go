package agent

// tl1669 压缩执行模型改同步 await（用户 20261117 21:33 拍板终稿）。
//
// 本文件钉住四改动点与 US1-4 用户故事：
//
//  1. 执行模型同步化（US1/验收①）：回合的 canonical 写入在压缩在飞时排队，
//     压缩完成后自动继续；压缩期间无并发写入者 ⇒ commit CAS 的
//     「conversation changed」对账结构性不再触发。
//  2. 在途写入排队（US3）：host 引导注入 / steer / 回合消息共用同一条等待
//     语义（tl1648 排队门语义保留，见 compaction_await.go 的对账注释）。
//  3. 失败兜底（US2）：压缩失败不杀当前回合——用现有上下文继续；失败原因
//     落日志（recordCompactionTurnFallback）并在转录可见（第 N 次 + 原因）；
//     连续 3 次失败熔断，本代停自动重试，下次上下文检查恢复。
//  4. 进度可见（US4）：等待期间压缩进度卡照常（tl1657 已交付 tps/fold）。
//
// 与 tl1648 / tl1648x 的对账见 host_guidance_gate_test.go 的同文件注释：
// 1648x 的等待实现已收拢到 compaction_await.go 的单一原语，钉数不减。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// shrinkCompactionAwait shrinks the compaction wait window for the timeout
// paths and restores the production values on cleanup.
func shrinkCompactionAwait(t *testing.T, bound time.Duration) {
	t.Helper()
	origBound, origPoll := compactionAwaitBound, compactionAwaitPoll
	compactionAwaitBound, compactionAwaitPoll = bound, 5*time.Millisecond
	t.Cleanup(func() {
		compactionAwaitBound, compactionAwaitPoll = origBound, origPoll
	})
}

// tl1669GatedProvider answers the summarizer request only when the test
// releases it — the shape of the 18:30/20:34 incident: a minutes-long
// compaction window with the turn still running.
type tl1669GatedProvider struct {
	release chan struct{}
}

func (p *tl1669GatedProvider) Name() string { return "gated" }

func (p *tl1669GatedProvider) ContextBudgetPolicy() provider.ContextBudgetPolicy {
	return provider.ContextBudgetPolicy{WindowMode: provider.ContextWindowIndependent}
}

func (p *tl1669GatedProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 3)
	go func() {
		select {
		case <-p.release:
			ch <- provider.Chunk{Type: provider.ChunkText, Text: "digest of the folded prefix"}
			ch <- provider.Chunk{Type: provider.ChunkDone}
			close(ch)
		case <-ctx.Done():
			close(ch)
		}
	}()
	return ch, nil
}

// waitForMaintenanceInFlight blocks until a real maintenance transaction is in
// flight (the 任务920 maintaining counter the whole await mechanism keys on).
func waitForMaintenanceInFlight(t *testing.T, a *Agent) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if a.maintaining.Load() > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the compaction never entered its in-flight window")
}

// TestTl1669TurnWriteWaitsForCompaction is the US1/US3 pin: a canonical write
// that arrives while a compaction pass is in flight parks instead of landing,
// so the pass's commit CAS (transcriptVersion + len + covered prefix) stays
// frozen; the same write lands after the pass settles and the turn continues
// by itself.
func TestTl1669TurnWriteWaitsForCompaction(t *testing.T) {
	a := newSteerActiveAgent(t)
	a.maintaining.Add(1)
	ver0, len0 := transcriptVersionLen(t, a)

	landed := make(chan struct{})
	go func() {
		defer close(landed)
		a.appendTurnMessage(context.Background(), provider.Message{Role: provider.RoleAssistant, Content: "turn round"})
	}()
	select {
	case <-landed:
		t.Fatal("a turn write landed while the compaction was in flight — the pass's CAS would break")
	case <-time.After(300 * time.Millisecond):
		// Still queued: the transcript is untouched while the pass runs.
	}
	if ver, length := transcriptVersionLen(t, a); ver != ver0 || length != len0 {
		t.Fatalf("the queued turn write mutated the transcript: version %d→%d len %d→%d", ver0, ver, len0, length)
	}

	a.maintaining.Add(-1)
	select {
	case <-landed:
	case <-time.After(5 * time.Second):
		t.Fatal("the queued turn write never landed after the compaction settled")
	}
	msgs, ver := a.sess.conversation.snapshotMessagesVersion()
	if ver == ver0 || len(msgs) != len0+1 {
		t.Fatalf("the turn write did not land exactly once: version %d→%d len %d→%d", ver0, ver, len0, len(msgs))
	}
	if msgs[len(msgs)-1].Content != "turn round" {
		t.Fatalf("the landed write lost its content: %+v", msgs[len(msgs)-1])
	}
}

// TestTl1669IdleWriteIsImmediate pins the zero-cost face: with no compaction
// in flight the funnel is a pass-through (one atomic load) and the write
// lands synchronously — ordinary sampling rounds pay nothing.
func TestTl1669IdleWriteIsImmediate(t *testing.T) {
	a := newSteerActiveAgent(t)
	if a.maintaining.Load() != 0 {
		t.Fatalf("test setup leaked an in-flight count: %d", a.maintaining.Load())
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.appendTurnMessage(context.Background(), provider.Message{Role: provider.RoleAssistant, Content: "immediate"})
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the idle write did not land immediately — the idle face must stay zero-cost")
	}
	msgs, _ := a.sess.conversation.snapshotMessagesVersion()
	if len(msgs) != 1 || msgs[0].Content != "immediate" {
		t.Fatalf("the idle write landed wrong: %+v", msgs)
	}
}

// TestTl1669CompactionCommitsWhileTurnWrites is the acceptance-① pin: the
// 18:30/20:34 incident shape reproduced end to end. A real compaction pass
// opens its window (the summarizer request hangs), the turn writes and a
// resume-shaped injection arrive during that window, and the pass still
// COMMITS — no "conversation changed while compression", no burned pass.
//
// The contrast is the mechanism: a bypassing write moves the transcript
// version, which is exactly what commitSummaryProjection compares.
func TestTl1669CompactionCommitsWhileTurnWrites(t *testing.T) {
	shrinkCompactionAwait(t, 30*time.Second)
	prov := &tl1669GatedProvider{release: make(chan struct{})}
	sess := foldableSessionOverForce(6)
	a := agentOverForce(t, prov, sess)

	prepared := make(chan error, 1)
	go func() {
		prepared <- prepareContext(context.Background(), a, CompactionTriggerPressure)
	}()
	waitForMaintenanceInFlight(t, a)

	// The pass's own CAS comparands, frozen the way commitSummaryProjection
	// snapshots them.
	ver0, len0 := transcriptVersionLen(t, a)

	// The turn's own round write + the resume-shaped injection both arrive
	// inside the compaction window and both queue.
	writesDone := make(chan struct{})
	go func() {
		defer close(writesDone)
		a.appendTurnMessage(context.Background(), provider.Message{Role: provider.RoleAssistant, Content: "the round landed"})
		a.addHostGuidance(HostGeneratedUserMessage("The app updated and restarted itself while this session was working."))
	}()
	time.Sleep(200 * time.Millisecond)
	if ver, length := transcriptVersionLen(t, a); ver != ver0 || length != len0 {
		t.Fatalf("a queued write broke the CAS freeze: version %d→%d len %d→%d", ver0, ver, len0, length)
	}

	// The summarizer answers: the pass commits.
	close(prov.release)
	select {
	case err := <-prepared:
		if err != nil {
			t.Fatalf("the compaction pass failed instead of committing: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the compaction pass never resolved")
	}
	if a.currentProjectionVersion() == 0 {
		t.Fatal("the pass installed no projection — it was burned by a concurrent write")
	}
	if r := a.sess.compactionState.LastReceipt; r == nil || r.Status != "applied" {
		t.Fatalf("receipt = %+v, want an applied fold", r)
	}

	// The queued writes land after the pass settles (US1: 自动继续).
	select {
	case <-writesDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the queued writes never resumed after the pass settled")
	}
	msgs, _ := a.sess.conversation.snapshotMessagesVersion()
	if len(msgs) < len0+2 {
		t.Fatalf("the queued writes did not both land: len %d→%d", len0, len(msgs))
	}
}

// TestTl1669CompactionFailureKeepsTheRoundAlive is the US2 pin: a failed
// summarizer must NOT end the round — the round goes out on the context the
// session already has (竞品语义: dsh「warn 后 continuing the turn」/ zcode
//「失败不应让 turn 失败」).
func TestTl1669CompactionFailureKeepsTheRoundAlive(t *testing.T) {
	sess := foldableSessionOverForce(6)
	a := agentOverForce(t, &fakeProvider{streamErr: errors.New("provider down")}, sess)

	frozen, err := a.prepareSamplingRequest(context.Background())
	if err != nil {
		t.Fatalf("a failed compaction killed the round: %v", err)
	}
	if len(frozen.req.Messages) == 0 {
		t.Fatal("the round carried no context at all")
	}
}

// TestTl1669FallbackRebuildsTheRoundOnTheCurrentContext pins the fallback leg
// itself: when the compaction round failed, the request is rebuilt on the
// CURRENT visible context WITHOUT running a second compaction pass (no retry
// storm, no second summary spend).
func TestTl1669FallbackRebuildsTheRoundOnTheCurrentContext(t *testing.T) {
	sess := foldableSessionOverForce(3)
	a := agentOverForce(t, &fakeProvider{reply: "digest"}, sess)

	frozen, ok := a.compactionFallbackRequest(context.Background(), errors.New("summarizer request failed"))
	if !ok {
		t.Fatal("the fallback leg failed to rebuild the round on the current context")
	}
	if len(frozen.req.Messages) == 0 {
		t.Fatal("the fallback carried no context")
	}
	if a.currentProjectionVersion() != 0 {
		t.Fatal("the fallback installed a projection — it must not retry the compaction")
	}
}

// TestTl1669FailureReasonVisibleInTranscript is the US2 visibility pin: the
// failed card carries the attempt counter and the reason, so the transcript
// answers 「第 N 次 + 原因」 without reading the log.
func TestTl1669FailureReasonVisibleInTranscript(t *testing.T) {
	a := newSteerActiveAgent(t)
	var done *event.Compaction
	sink := event.FuncSink(func(e event.Event) {
		if e.Kind == event.CompactionDone && e.Compaction.Outcome == "failed" {
			c := e.Compaction
			done = &c
		}
	})
	a.SetSink(sink)

	a.emitCompactionFailed(CompactionTriggerPressure, errCompressStaleContext)
	if done == nil {
		t.Fatal("a failed pass must emit a CompactionDone event")
	}
	if done.Attempt != 1 {
		t.Fatalf("the first failure must carry attempt=1, got %d", done.Attempt)
	}
	if done.Reason == "" {
		t.Fatal("the failed card carries no reason")
	}
	if !strings.Contains(done.Reason, "context changed during compaction") {
		t.Fatalf("the failed card reason = %q, want the stale-context classification", done.Reason)
	}
}

// TestTl1669BreakerOpensAfterThreeFailures is the US2 circuit-breaker pin:
// three consecutive failed passes park the AUTOMATIC compaction path for this
// generation, and the user gets a visible notice. A fourth failure does not
// re-notify (the breaker is already open).
func TestTl1669BreakerOpensAfterThreeFailures(t *testing.T) {
	a := newSteerActiveAgent(t)
	notices := 0
	sink := event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice && e.Code == event.NoticeCodeCompactionBreaker {
			notices++
		}
	})
	a.SetSink(sink)

	for range compactionFailBreakerLimit - 1 {
		a.emitCompactionFailed(CompactionTriggerPressure, errCompressStaleContext)
	}
	if a.sess.compaction.breakerOpen.Load() {
		t.Fatal("the breaker opened before the limit")
	}
	a.emitCompactionFailed(CompactionTriggerPressure, errCompressStaleContext)
	if !a.sess.compaction.breakerOpen.Load() {
		t.Fatalf("the breaker did not open after %d consecutive failures", compactionFailBreakerLimit)
	}
	if notices != 1 {
		t.Fatalf("breaker notices = %d, want exactly one", notices)
	}
	// The automatic path is parked; the manual path always runs.
	if !a.compactionBreakerBlocks("hash", CompactionTriggerPressure) {
		t.Fatal("the automatic path is not parked by the breaker")
	}
	if a.compactionBreakerBlocks("hash", CompactionTriggerManual) {
		t.Fatal("the manual path must not be parked by the breaker")
	}
}

// TestTl1669BreakerRearmsOnTheNextContextCheck is the US2 re-arm pin: the
// next context check with NEW input re-arms the automatic path (「本代内不再
// 自动重试改为下次 context check 再试」).
func TestTl1669BreakerRearmsOnTheNextContextCheck(t *testing.T) {
	a := newSteerActiveAgent(t)
	a.sess.compaction.breakerOpen.Store(true)
	// The receipt of the failed view: its blocked hash is what the next
	// context check compares against.
	a.sess.compactionMu.Lock()
	a.sess.compactionState.LastReceipt = &ContextMaintenanceReceipt{
		Status: "failed", Action: "summary", Trigger: CompactionTriggerPressure,
		BlockedInputHash: "failed-hash", InputHash: "failed-hash",
	}
	a.sess.compactionMu.Unlock()

	if !a.compactionBreakerBlocks("failed-hash", CompactionTriggerPressure) {
		t.Fatal("the same view must stay parked by the breaker")
	}
	if a.compactionBreakerBlocks("brand-new-hash", CompactionTriggerPressure) {
		t.Fatal("a NEW view must re-arm the automatic path at the next context check")
	}
	if a.sess.compaction.breakerOpen.Load() {
		t.Fatal("the breaker stayed open after the new context check re-armed it")
	}
}

// TestTl1669NoBareCanonicalWriteRemains is the source-level assertion (the
// tl1648x write-site pin, extended to the whole canonical write surface):
// every canonical append must go through the compaction-await funnel. A bare
// conversation.Add / Session().Add reintroduces the burn-the-pass bypass.
func TestTl1669NoBareCanonicalWriteRemains(t *testing.T) {
	for _, dir := range []string{".", filepath.Join("..", "control")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatalf("read %s: %v", filepath.Join(dir, name), err)
			}
			text := string(data)
			// The funnel itself is the single allowed writer.
			if name == "compaction_await.go" {
				continue
			}
			for _, banned := range []string{"conversation.Add(", "conversation.AddBatch(", "Session().Add("} {
				if strings.Contains(text, banned) {
					t.Errorf("%s writes the canonical transcript via %q — route it through the tl1669 compaction-await funnel", name, banned)
				}
			}
		}
	}
}
