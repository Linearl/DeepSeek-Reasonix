package agent

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// 任务 707 验收①（铁律 2）：开关关（CompactModel 空，即装配侧
// CompactModelLive 的关形态）时压缩完全走对话目的地——provider 选择、用量
// 归因（ModelRef + pricing）与 707 之前逐字节同形。
func TestCompactionSummaryDefaultsToConversationDestination(t *testing.T) {
	pricing := &provider.Pricing{Currency: "USD", Input: 3}
	home := &destCountProvider{Provider: &fakeProvider{reply: "home digest", promptTokens: 42}}
	sink := &recordSink{}
	sess := foldableSessionOverForce(3)
	a := New(home, nil, sess, Options{
		ModelRef:      "fake/a",
		Pricing:       pricing,
		ContextWindow: 5000,
		ArchiveDir:    t.TempDir(),
	}, sink)

	summary, _, err := a.summarize(context.Background(), sess.Messages[1:], "")
	if err != nil {
		t.Fatalf("summarize = %v", err)
	}
	if !strings.Contains(summary, "home digest") {
		t.Fatalf("summary = %q, want the conversation destination's reply", summary)
	}
	if got := home.calls.Load(); got != 1 {
		t.Fatalf("conversation destination served %d summary requests, want 1", got)
	}
	events := sink.kinds(event.Usage)
	if len(events) != 1 {
		t.Fatalf("usage events = %d, want exactly 1 compaction event", len(events))
	}
	if events[0].ModelRef != "fake/a" || events[0].UsageSource != event.UsageSourceCompaction {
		t.Fatalf("usage event = ref %q source %q, want the conversation ref + compaction source", events[0].ModelRef, events[0].UsageSource)
	}
	if events[0].Pricing != pricing {
		t.Fatal("usage event lost the construction pricing")
	}
}

// 任务 707 验收②：开关开 + 选定压缩模型后，触发压缩时实际调用指定模型——
// 对话目的地零调用，用量事件归因到压缩模型（ref + 专属 pricing），解析走
// 既有 resolver 缝（与 148 覆盖同链）。
func TestCompactionSummaryServedByConfiguredCompactModel(t *testing.T) {
	compactPricing := &provider.Pricing{Currency: "USD", Input: 0.5}
	home := &destCountProvider{Provider: &fakeProvider{reply: "home digest", promptTokens: 42}}
	econ := &destCountProvider{Provider: &fakeProvider{reply: "econ digest", promptTokens: 42}}
	resolver := &fakeModelResolver{providers: map[string]provider.Provider{
		"econ/m3": econ,
	}}
	sink := &recordSink{}
	sess := foldableSessionOverForce(3)
	a := New(home, nil, sess, Options{
		ModelRef:            "fake/a",
		ModelResolver:       resolver,
		CompactModel:        "econ/m3",
		CompactModelPricing: compactPricing,
		ContextWindow:       5000,
		ArchiveDir:          t.TempDir(),
	}, sink)

	summary, _, err := a.summarize(context.Background(), sess.Messages[1:], "")
	if err != nil {
		t.Fatalf("summarize = %v", err)
	}
	if !strings.Contains(summary, "econ digest") {
		t.Fatalf("summary = %q, want the compact model's reply", summary)
	}
	if got := econ.calls.Load(); got != 1 {
		t.Fatalf("compact model served %d summary requests, want 1", got)
	}
	if got := home.calls.Load(); got != 0 {
		t.Fatalf("conversation destination served %d summary requests, want 0", got)
	}
	if resolver.last.Ref != "econ/m3" {
		t.Fatalf("resolver saw ref %q, want econ/m3 (the armed compact ref)", resolver.last.Ref)
	}
	events := sink.kinds(event.Usage)
	if len(events) != 1 {
		t.Fatalf("usage events = %d, want exactly 1 compaction event", len(events))
	}
	if events[0].ModelRef != "econ/m3" || events[0].UsageSource != event.UsageSourceCompaction {
		t.Fatalf("usage event = ref %q source %q, want the compact ref + compaction source", events[0].ModelRef, events[0].UsageSource)
	}
	if events[0].Pricing != compactPricing {
		t.Fatal("usage event did not carry the compact model's pricing")
	}
	if ref, ok := a.compactModelResolvedForTest(); !ok || ref != "econ/m3" {
		t.Fatalf("resolved cache = %q ok=%v, want econ/m3 cached", ref, ok)
	}

	// Second summary rides the cache: same destination, no re-resolve needed
	// (assert via behavior — resolve seam still points at the armed ref).
	if _, _, err := a.summarize(context.Background(), sess.Messages[1:], ""); err != nil {
		t.Fatalf("second summarize = %v", err)
	}
	if got := econ.calls.Load(); got != 2 {
		t.Fatalf("compact model served %d summary requests after two passes, want 2", got)
	}
}

// 任务 707 验收③：压缩模型解析失败（provider 未配置 / resolver 未接线）时
// 优雅回退对话模型——压缩照常完成，且失败不缓存（下一次摘要重试解析缝）。
func TestCompactionSummaryFallsBackWhenCompactModelUnresolved(t *testing.T) {
	home := &destCountProvider{Provider: &fakeProvider{reply: "home digest", promptTokens: 42}}
	resolver := &fakeModelResolver{err: &providerResolveError{}}
	sess := foldableSessionOverForce(3)
	a := New(home, nil, sess, Options{
		ModelRef:      "fake/a",
		ModelResolver: resolver,
		CompactModel:  "ghost/model",
		ContextWindow: 5000,
		ArchiveDir:    t.TempDir(),
	}, event.Discard)

	summary, _, err := a.summarize(context.Background(), sess.Messages[1:], "")
	if err != nil {
		t.Fatalf("summarize = %v, want the graceful conversation-model fallback", err)
	}
	if !strings.Contains(summary, "home digest") {
		t.Fatalf("summary = %q, want the conversation destination's reply", summary)
	}
	if got := home.calls.Load(); got != 1 {
		t.Fatalf("conversation destination served %d summary requests, want 1", got)
	}
	if _, cached := a.compactModelResolvedForTest(); cached {
		t.Fatal("a failed resolve must not poison the cache")
	}

	// Resolver-less hosts (CLI one-shots, sub-agents) take the same fallback.
	bare := New(home, nil, sess, Options{
		ModelRef:      "fake/a",
		CompactModel:  "econ/m3",
		ContextWindow: 5000,
		ArchiveDir:    t.TempDir(),
	}, event.Discard)
	if _, _, err := bare.summarize(context.Background(), sess.Messages[1:], ""); err != nil {
		t.Fatalf("resolver-less summarize = %v, want the fallback", err)
	}
}

type providerResolveError struct{}

func (*providerResolveError) Error() string { return "unknown provider ref" }

// 任务 707 验收⑥（638 联动硬约束）：经济模型写出的压缩产物在切回/切走对话
// 模型后仍有效——投影不因切模型回退全量（有效性=内容哈希，模型无关），切模
// 型零摘要调用，缓存命名空间按 638 rebind 跟随对话目的地。
func TestCompactModelProjectionSurvivesConversationModelSwitch(t *testing.T) {
	home := &destCountProvider{Provider: &fakeProvider{reply: "home digest"}}
	econ := &destCountProvider{Provider: &fakeProvider{reply: "econ digest"}}
	other := &destCountProvider{Provider: &namedFakeProvider{name: "other", fakeProvider: fakeProvider{reply: "other digest"}}}
	resolver := &fakeModelResolver{providers: map[string]provider.Provider{
		"econ/m3": econ,
		"other/b": other,
	}}
	sess := foldableSessionOverForce(6)
	a := New(home, nil, sess, Options{
		ModelRef:      "fake/a",
		ModelResolver: resolver,
		CompactModel:  "econ/m3",
		ContextWindow: 5000,
		CompactRatio:  0.5, CompactForceRatio: 0.5, RecentKeep: 2,
		ArchiveDir: t.TempDir(),
	}, event.Discard)

	// The fold runs on the compact model and installs the projection.
	if err := prepareContext(context.Background(), a, CompactionTriggerManual); err != nil {
		t.Fatalf("manual compact = %v", err)
	}
	if got := econ.calls.Load(); got != 1 {
		t.Fatalf("compact model served %d summary requests, want 1", got)
	}
	if got := home.calls.Load(); got != 0 {
		t.Fatalf("conversation destination served %d summary requests, want 0", got)
	}
	before := projectionTokens(a)
	if before == 0 {
		t.Fatalf("no projection landed; receipt = %+v", a.sess.compactionState.LastReceipt)
	}

	// Hot-switch the CONVERSATION model: the compact-model digest must keep
	// governing the view — no full-canonical fallback, no re-summary.
	if !a.SetSessionModelOverride("other/b", ModelOverrideExtras{}) {
		t.Fatal("override declined; the resolver seam is not wired")
	}
	if _, err := a.contextManager().Prepare(context.Background(), ContextPreparePolicy{Trigger: CompactionTriggerPressure}); err != nil {
		t.Fatalf("prepare after switch: %v", err)
	}
	if econ.calls.Load() != 1 || home.calls.Load() != 0 || other.calls.Load() != 0 {
		t.Fatalf("model switch triggered summary calls: econ=%d home=%d other=%d, want all unchanged",
			econ.calls.Load(), home.calls.Load(), other.calls.Load())
	}
	if after := projectionTokens(a); after != before {
		t.Fatalf("projection changed across the switch: before=%d after=%d", before, after)
	}
	snap := a.ContextMaintenanceSnapshot()
	if !snap.ProjectionValid {
		t.Fatal("snapshot reports the compact-model projection invalid after a conversation-model switch")
	}
	if snap.ProjectedTokens >= snap.CanonicalTokens {
		t.Fatalf("view did not stay folded: projected=%d canonical=%d", snap.ProjectedTokens, snap.CanonicalTokens)
	}
}
