package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/extension"
	"reasonix/internal/extension/dispatch"
	"reasonix/internal/extension/protocol"
	"reasonix/internal/provider"
)

// tl1770 拍板②③（用户 20261123 15:05）验收。
//
// 拍板②：常规压缩（主动+被动）的摘要请求改全量送（含 D 尾部）——请求=
// 会话请求自身形态（system+全视图+追加指令），provider 前缀缓存全程覆盖
// （竞品实证 codex=history+追加指令 / opencode=conversation+prior-summary）。
// fold 区域规划不变（FoldTokens 仍报折叠区），focus 选段道/分块道/slim 阶
// 不动。
// 拍板③：超窗从头删（抄 codex compact.rs L311 "Trim from the beginning to
// preserve cache (prefix-based) and keep recent messages intact"）——整
// replay unit 从最旧端逐个删直到入预算；保护 system、compaction summary
// （合并源）与最新 unit；删到地板仍超预算照发最大删减形态，provider 溢出
// 道接管。

// fullSendStubProvider answers every request with a digest and records them.
type fullSendStubProvider struct {
	window int
	reqs   []provider.Request
}

func (p *fullSendStubProvider) Name() string { return "full-send-tl1770" }

func (p *fullSendStubProvider) ContextBudgetPolicy() provider.ContextBudgetPolicy {
	return provider.ContextBudgetPolicy{
		WindowMode: provider.ContextWindowShared, AutoOutputTokens: 8192, MaxOutputTokens: 8192,
		LimitMode: provider.OutputLimitOmitWhenSafe,
	}
}

func (p *fullSendStubProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.reqs = append(p.reqs, req)
	ch := make(chan provider.Chunk, 3)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: "- goal: x"}
	ch <- provider.Chunk{Type: provider.ChunkUsage, Usage: &provider.Usage{PromptTokens: requestTokensOf(req), CompletionTokens: 8, TotalTokens: requestTokensOf(req) + 8, RequestCount: 1}}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

// TestFullSendPressureCarriesWholeViewAndInstruction pins 拍板② end to end:
// the pressure lane's summary request carries the WHOLE view (system + every
// message incl. the verbatim D tail) plus exactly one appended instruction
// message — the conversation request's own shape, so the provider's prefix
// cache covers it fully.
func TestFullSendPressureCarriesWholeViewAndInstruction(t *testing.T) {
	// Fixture sizing: the view must sit ABOVE the pressure trigger (0.5×40K =
	// 20K) yet BELOW the safe prompt budget (~29.8K), so the round fires and
	// the full-send input goes out untrimmed — shape assertions read the whole
	// view + appended instruction directly.
	const window = 40_000
	prov := &fullSendStubProvider{window: window}
	a := buildPreFoldAgent(t, prov, 48, window)
	view := a.sess.conversation.Snapshot()
	if est := a.estimatedVisibleRequestTokens(view); est <= a.compactTrigger() {
		t.Fatalf("fixture view %d sits below the trigger %d — pressure would noop", est, a.compactTrigger())
	} else if est >= budgetOf(t, a) {
		t.Fatalf("fixture view %d sits above the safe budget %d — full-send would trim", est, budgetOf(t, a))
	}
	if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
		t.Fatalf("pressure maintenance: %v", err)
	}
	if len(prov.reqs) == 0 {
		t.Fatal("no summary request reached the provider")
	}
	req := prov.reqs[0]
	if got, want := len(req.Messages), len(view)+1; got != want {
		t.Fatalf("summary request carries %d messages, want %d (whole view + appended instruction)", got, want)
	}
	if req.Messages[0].Role != provider.RoleSystem {
		t.Fatalf("request[0] role = %q, want system", req.Messages[0].Role)
	}
	last := req.Messages[len(req.Messages)-1].Content
	if !strings.HasPrefix(strings.TrimSpace(last), "Compact the preceding conversation prefix") {
		t.Fatalf("last request message is not the appended compaction instruction: %q", truncateForLog(last))
	}
	// The D tail rides verbatim: the newest view message sits right before the
	// instruction.
	tailMsg := req.Messages[len(req.Messages)-2]
	if tailMsg.Content != view[len(view)-1].Content {
		t.Fatalf("D tail missing from the summary request: got %q, want %q", truncateForLog(tailMsg.Content), truncateForLog(view[len(view)-1].Content))
	}
}

// TestFullSendTrimDropsOldestUnitsToFit pins 拍板③: an over-budget full-send
// input drops whole oldest units until the request fits, keeping the system
// prefix, the protected compaction summary (merge source) and the newest
// unit, and the trimmed input prices within the safe budget.
func TestFullSendTrimDropsOldestUnitsToFit(t *testing.T) {
	const window = 40_000
	prov := &fullSendStubProvider{window: window}
	a := agentOverForceWindow(t, prov, foldableSessionOverForce(2), window)
	budget, enforced := a.safeSummaryPromptTokenLimit()
	if !enforced || budget <= 0 {
		t.Fatalf("safe budget = %d enforced=%v, want an enforced positive budget", budget, enforced)
	}
	// Build the view: system + big old units + a protected compaction summary
	// + a small newest user message. Total must price over the budget.
	big := strings.Repeat("x", 2200)
	view := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	for i := 0; i < 60; i++ {
		view = append(view,
			provider.Message{Role: provider.RoleAssistant, Content: fmt.Sprintf("old assistant block %d: %s", i, big)},
			provider.Message{Role: provider.RoleUser, Content: fmt.Sprintf("old user turn %d", i)},
		)
	}
	view = append(view,
		provider.Message{Role: provider.RoleUser, Content: summaryTagOpen + "\nstanding facts: keep the API stable\n" + summaryTagClose},
		provider.Message{Role: provider.RoleUser, Content: "newest user turn"},
	)
	if a.estimatedRequestTokens(a.summaryRequest(view, "")) <= budget {
		t.Fatal("fixture input already fits the budget; enlarge it to reproduce the over-window shape")
	}
	mode := SummaryInputCachePrefix
	got := a.preFoldFullSendInput(view, "", &mode)
	if mode != SummaryInputCachePrefix {
		t.Fatalf("inputMode flipped to %q, want unchanged replay label", mode)
	}
	if tokens := a.estimatedRequestTokens(a.summaryRequest(got, "")); tokens > budget {
		t.Fatalf("trimmed input still over budget: %d > %d", tokens, budget)
	}
	if got[0].Role != provider.RoleSystem || got[0].Content != "sys" {
		t.Fatalf("system prefix lost: %q", truncateForLog(got[0].Content))
	}
	last := got[len(got)-1]
	if last.Content != "newest user turn" {
		t.Fatalf("newest unit not kept: %q", truncateForLog(last.Content))
	}
	foundSummary := false
	for _, m := range got {
		if strings.Contains(m.Content, "standing facts: keep the API stable") {
			foundSummary = true
		}
	}
	if !foundSummary {
		t.Fatal("protected compaction summary was trimmed away")
	}
	if len(got) >= len(view) {
		t.Fatalf("no trim happened: kept %d of %d messages", len(got), len(view))
	}
}

// TestFullSendTrimFloorKeepsSystemSummaryNewest pins the codex single-item
// floor: when even system + protected summary + newest unit exceeds the
// budget, the maximally trimmed form goes out (never the untrimmed view).
func TestFullSendTrimFloorKeepsSystemSummaryNewest(t *testing.T) {
	const window = 8_000
	prov := &fullSendStubProvider{window: window}
	a := agentOverForceWindow(t, prov, foldableSessionOverForce(2), window)
	huge := strings.Repeat("y", 60_000)
	view := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: summaryTagOpen + "\nold digest\n" + summaryTagClose},
		{Role: provider.RoleUser, Content: "newest " + huge},
	}
	mode := SummaryInputCachePrefix
	got := a.preFoldFullSendInput(view, "", &mode)
	if mode != SummaryInputCachePrefix {
		t.Fatalf("inputMode flipped to %q", mode)
	}
	// Floor form: system + protected summary + newest unit.
	if len(got) != 3 {
		t.Fatalf("floor form carries %d messages, want 3 (system + protected summary + newest unit)", len(got))
	}
	if got[len(got)-1].Content != view[2].Content {
		t.Fatalf("newest unit not kept intact on the floor form")
	}
}

// TestFullSendSlimRoutingForImageOnlyView pins the 任务 1040 discipline on the
// full-send lane: an image-only over-budget view routes to the transcript
// form (which drops images for real) instead of a text elision that cannot
// shed them, and the input region itself stays untrimmed.
func TestFullSendSlimRoutingForImageOnlyView(t *testing.T) {
	const window = 8_000
	prov := &fullSendStubProvider{window: window}
	a := agentOverForceWindow(t, prov, foldableSessionOverForce(2), window)
	view := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	for i := 0; i < 4; i++ {
		view = append(view, provider.Message{
			Role:   provider.RoleUser,
			Content: fmt.Sprintf("shot %d", i),
			Images:  []string{"https://example.invalid/shot.png", "https://example.invalid/shot2.png"},
		})
	}
	mode := SummaryInputCachePrefix
	got := a.preFoldFullSendInput(view, "", &mode)
	if mode != SummaryInputSlim {
		t.Fatalf("inputMode = %q, want SummaryInputSlim (image-only routing)", mode)
	}
	if len(got) != len(view) {
		t.Fatalf("image-only region was reshaped: %d of %d messages", len(got), len(view))
	}
}

func truncateForLog(s string) string {
	if len(s) <= 80 {
		return s
	}
	return s[:80] + "…"
}

// TestFullSendHookInflatedInputRejectedOnAutomaticLane pins the 任务719/#9572
// guard in its tl1770 shape: a hook that inflates the summary input past the
// safe budget is REJECTED on the automatic lane (no provider request, blocked
// receipt) — the 拍板③ head-trim exists for the natural view's overflow, not
// as an extension escape hatch.
func TestFullSendHookInflatedInputRejectedOnAutomaticLane(t *testing.T) {
	const window = 20_000
	client := &fakeDispatchClient{interceptFn: func(ev protocol.InterceptEvent, raw json.RawMessage) (protocol.InterceptResult, error) {
		if ev != protocol.EventCompactionPrepare {
			return protocol.InterceptResult{Decision: protocol.DecisionContinue}, nil
		}
		var payload dispatch.CompactionPreparePayload
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("decode compaction.prepare payload: %v", err)
		}
		payload.Messages = []protocol.ProviderMessage{{Role: protocol.ProviderRoleUser, Content: strings.Repeat("inflate ", window)}}
		return replaceWith(t, payload), nil
	}}
	prov := &fullSendStubProvider{window: window}
	a := agentOverForceWindow(t, prov, foldableSessionOverForce(40), window)
	a.svc.extensions = newExtDispatcher(client, true, nil, extension.PointCompactionPrepare)

	var rejected *ContextMaintenanceReceipt
	a.svc.sink = event.FuncSink(func(e event.Event) {
		if e.Kind == event.ContextMaintenanceEvent && e.Maintenance != nil && e.Maintenance.Status == "blocked" {
			rejected = &ContextMaintenanceReceipt{Status: e.Maintenance.Status, Reason: e.Maintenance.Reason}
		}
	})
	if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
		t.Fatalf("pressure maintenance error = %v, want the rejection absorbed with an admissible view", err)
	}
	if len(prov.reqs) != 0 {
		t.Fatalf("summary requests = %d, want none for an inflated extension replacement", len(prov.reqs))
	}
	if rejected == nil || !strings.Contains(rejected.Reason, "prepared summary request") {
		t.Fatalf("blocked receipt = %+v, want the summary-budget rejection", rejected)
	}
}

// budgetOf reads the same safe prompt budget preFoldFullSendInput trims
// against, so the fixture guard names the one ruler.
func budgetOf(t *testing.T, a *Agent) int {
	t.Helper()
	budget, enforced := a.safeSummaryPromptTokenLimit()
	if !enforced {
		budget = a.summaryInputBudget("")
	}
	return budget
}
