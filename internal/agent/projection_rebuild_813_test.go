package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// 任务813 回归钉：投影失效（降级 episode）后的自愈链。
//
// 病灶（调研-2 形态）：719 的有界尾部视图把 ContextUsedTokens 压到
// 0.60x 窗口后，kickProjectionRebuild 的 `ContextUsedTokens() < fold`
// 门槛对「失效 + 超窗」会话永远不满足——后台重建被静默跳过，面板钉死在
// 「投影失效 · 全量回退中」+ canonical 超窗红标，空闲会话永不自愈。
//
// 三重钉：①门槛不再被降级视图压制（gate）；②重建全程有界——每个摘要
// 请求 ≤ 安全上限、视图最终回到 fold 之下（segmented + bounded）；
// ③无法推进时早停，不空烧轮数（parked stop）。

// oversizedTranscript813 builds a canonical transcript whose bulk is
// assistant text (prune cannot reclaim it for free), sized several windows
// past the hard ceiling. All bulk messages share one backing string.
func oversizedTranscript813(turns int) *Session {
	big := strings.Repeat("word ", 2400) // ~12000 bytes ≈ 3000 est tokens
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "standing constraint: never change the public API"},
	}
	for range turns {
		msgs = append(msgs,
			provider.Message{Role: provider.RoleAssistant, Content: big},
			provider.Message{Role: provider.RoleUser, Content: "continue"},
		)
	}
	return &Session{Messages: msgs}
}

// sharedWindowRecorder813 records every request for boundedness assertions
// and answers summaries instantly. Shared window mode engages the #9572 safe
// summary prefix cap that production gateways run under.
type sharedWindowRecorder813 struct {
	mu       sync.Mutex
	requests []provider.Request
	replies  int
	fail     bool // when true, every Stream errors (non-transient)
}

func (p *sharedWindowRecorder813) Name() string { return "recorder-813" }

func (p *sharedWindowRecorder813) ContextBudgetPolicy() provider.ContextBudgetPolicy {
	return provider.ContextBudgetPolicy{
		WindowMode: provider.ContextWindowShared, AutoOutputTokens: 8192,
		MaxOutputTokens: 8192, LimitMode: provider.OutputLimitOmitWhenSafe,
	}
}

func (p *sharedWindowRecorder813) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.replies++
	fail := p.fail
	p.mu.Unlock()
	ch := make(chan provider.Chunk, 2)
	if fail {
		ch <- provider.Chunk{Type: provider.ChunkError, Err: errSummaryRejected813}
	} else {
		ch <- provider.Chunk{Type: provider.ChunkText, Text: "digest of the folded prefix"}
	}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

// errSummaryRejected813 is a deterministic non-transient summary failure.
var errSummaryRejected813 = &provider.APIError{Provider: "recorder-813", Status: 400, Body: "rejected"}

func agentOverWindow813(t *testing.T, prov provider.Provider, sess *Session) *Agent {
	t.Helper()
	return New(prov, tool.NewRegistry(), sess, Options{
		ContextWindow: 100_000,
		CompactRatio:  0.80,
		ArchiveDir:    t.TempDir(),
	}, event.Discard)
}

// waitForRebuildSettled polls until the invalid-projection episode is over:
// a usable projection is back and the visible view sits under the fold
// trigger — the 任务813 acceptance for an already-over-limit old session.
func waitForRebuildSettled(t *testing.T, a *Agent) bool {
	t.Helper()
	fold := a.compactTrigger()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		msgs, _ := a.sess.conversation.snapshotMessagesVersion()
		a.sess.compactionMu.Lock()
		st := a.sess.compactionState
		pending := a.sess.rebuildPending.Load()
		a.sess.compactionMu.Unlock()
		if projectionValid(st, msgs) && !a.sess.tailView.active() && !pending {
			if visible := modelVisibleFromProjection(st.Projection, msgs); len(visible) > 0 &&
				a.estimatedVisibleRequestTokens(visible) < fold {
				return true
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// ① 门槛钉：超窗 + 投影失效时，降级视图把 ContextUsedTokens 压到 fold 之下，
// 但踢除必须照常触发（719 引入的压制形态，813 修复）。在 813 之前的代码上
// 本测试失败：kick 直接 return，投影永不恢复。
func TestKickProjectionRebuildNotSuppressedByDegradedTailView(t *testing.T) {
	a := agentOverWindow813(t, &sharedWindowRecorder813{}, oversizedTranscript813(140))
	fold := a.compactTrigger()
	if fold <= 0 {
		t.Fatalf("compactTrigger = %d, fixture needs a window", fold)
	}
	// The degraded view hides the pressure: the visible estimate sits below
	// the fold trigger even though the canonical transcript dwarfs the window.
	if used := a.ContextUsedTokens(); used >= fold {
		t.Fatalf("fixture setup: ContextUsedTokens = %d should sit below fold %d on the degraded view", used, fold)
	}
	if !a.sess.tailView.active() {
		t.Fatal("fixture setup: the over-ceiling invalid-projection session must be on the degraded tail view")
	}
	canonical, _ := a.sess.conversation.snapshotMessagesVersion()
	if est := a.estimatedVisibleRequestTokens(canonical); est < a.hardInputCeiling() {
		t.Fatalf("fixture setup: canonical %d must exceed the hard ceiling %d", est, a.hardInputCeiling())
	}
	a.kickProjectionRebuild("test_813_gate")
	if !waitForRebuildSettled(t, a) {
		msgs, _ := a.sess.conversation.snapshotMessagesVersion()
		a.sess.compactionMu.Lock()
		defer a.sess.compactionMu.Unlock()
		t.Fatalf("projection not rebuilt while the degraded view hid the pressure: valid=%v pending=%v msgs=%d",
			projectionValid(a.sess.compactionState, msgs), a.sess.rebuildPending.Load(), len(msgs))
	}
}

// ② 有界+自愈钉：失效 episode 的后台重建分段推进——每个摘要请求都不得超过
// 硬输入上限（永远不做全量重投影），最终投影恢复、视图回到 fold 之下，
// 面板读数（ContextMaintenanceSnapshot）回到有效形态。
func TestDegradedEpisodeRebuildStaysBoundedAndSelfHeals(t *testing.T) {
	prov := &sharedWindowRecorder813{}
	a := agentOverWindow813(t, prov, oversizedTranscript813(140))
	hard := a.hardInputCeiling()
	a.kickProjectionRebuild("test_813_bounded")
	if !waitForRebuildSettled(t, a) {
		t.Fatal("over-limit invalid-projection session did not self-heal to a settled view")
	}
	prov.mu.Lock()
	requests := append([]provider.Request(nil), prov.requests...)
	prov.mu.Unlock()
	if len(requests) == 0 {
		t.Fatal("rebuild ran no summary requests")
	}
	if len(requests) > projectionRebuildMaxRounds*2+1 {
		t.Fatalf("rebuild spent %d summary requests, above the %d-round bounded budget",
			len(requests), projectionRebuildMaxRounds)
	}
	for i, req := range requests {
		if used := a.estimatedRequestTokens(req); used > hard {
			t.Fatalf("summary request %d carried %d tokens past the hard ceiling %d — full reprojection leaked",
				i, used, hard)
		}
	}
	snap := a.ContextMaintenanceSnapshot()
	if !snap.ProjectionValid {
		t.Fatal("settled session must report ProjectionValid")
	}
	if snap.TriggerTokens > 0 && snap.ProjectedTokens >= snap.TriggerTokens {
		t.Fatalf("settled view %d still sits at or above the fold trigger %d", snap.ProjectedTokens, snap.TriggerTokens)
	}
	if snap.CanonicalTokens <= snap.ProjectedTokens {
		t.Fatalf("projected readout %d must sit below canonical %d after the rebuild", snap.ProjectedTokens, snap.CanonicalTokens)
	}
}

// ③ 早停钉：摘要通道持续失败时，episode 在不再推进的那一轮停下——不空烧
// 轮数、不 panic，rebuildPending 复位，会话停在已提交的部分进度上。
func TestDegradedEpisodeRebuildStopsWhenParked(t *testing.T) {
	prov := &sharedWindowRecorder813{fail: true}
	a := agentOverWindow813(t, prov, oversizedTranscript813(140))
	a.kickProjectionRebuild("test_813_parked")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !a.sess.rebuildPending.Load() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if a.sess.rebuildPending.Load() {
		t.Fatal("rebuild episode never ended")
	}
	prov.mu.Lock()
	spent := len(prov.requests)
	prov.mu.Unlock()
	if spent == 0 {
		t.Fatal("expected the episode to attempt at least one summary")
	}
	if spent > projectionRebuildMaxRounds*2+1 {
		t.Fatalf("parked episode spent %d summary requests, above the bounded budget", spent)
	}
	a.sess.compactionMu.Lock()
	installed := len(a.sess.compactionState.Projection.Messages)
	a.sess.compactionMu.Unlock()
	if installed != 0 {
		t.Fatalf("failed summaries must not install a projection body, got %d messages", installed)
	}
}
