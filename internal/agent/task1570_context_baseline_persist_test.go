package agent

import (
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1570：超窗判定实账落盘。20261113 包90 装机现场——重启后 SelfHealOpenSession
// 首轮前必然无实账（任务 1183 锚纯内存），超窗判据退估算误判大会话超窗 →
// Diet → 缓存全 miss → 压缩失败连败 → 再 Diet → 裁剪最早历史。本文件钉住：
// ① trusted usage 采样把锚写进 .jsonl.meta（含 provider/model/时间戳）；
// ② 同值节流不重写；③ Estimated/Unknown 永不落盘；④ 重启后（新 Agent 实例）
// 落盘锚回填为冷启动实账锚；⑤ 有效性边界各拒：超 24h / 换 provider / 换
// model / coveredChars>当前视图 / meta 缺失 / 旧版 meta 无字段。

// tl1570Agent builds the persist fixture: the 1183 fold fixture with a bound
// transcript path and (optionally) a model ref, so the sampling path's
// persistContextBaseline has somewhere to write.
func tl1570Agent(t *testing.T, prov provider.Provider, path, modelRef string) *Agent {
	t.Helper()
	a := New(prov, tool.NewRegistry(), foldableSessionOverForce(2), Options{
		ContextWindow:     5000,
		CompactRatio:      0.5,
		CompactForceRatio: 0.5,
		RecentKeep:        2,
		ArchiveDir:        t.TempDir(),
		ModelRef:          modelRef,
	}, event.Discard)
	if path != "" {
		a.SetSessionPath(path)
	}
	return a
}

func tl1570TranscriptPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "tl1570.jsonl")
}

// tl1570StoreAnchor drives the real sampling entry (storeContextBaseline) the
// way storeLatestRequestUsage would after a trusted response.
func tl1570StoreAnchor(a *Agent, prompt, completion int, coveredChars int64) {
	shape := requestCalibrationShape{
		requestChars: coveredChars,
		compactChars: coveredChars,
	}
	a.sess.output.activeReqShape.Store(&shape)
	a.storeContextBaseline(&provider.Usage{
		PromptTokens:            prompt,
		CompletionTokens:        completion,
		ContextPromptTokens:     prompt,
		ContextCompletionTokens: completion,
	})
}

// 钉①: a trusted sample lands the anchor in the .jsonl.meta sidecar with the
// full measurement identity — provider, model ref, counts, covered chars,
// image price, sampling stamp.
func TestPersistContextBaselineWritesMeta(t *testing.T) {
	path := tl1570TranscriptPath(t)
	a := tl1570Agent(t, &failingSummaryProvider{}, path, "model-a")
	visible := a.modelVisibleMessages()
	covered := int64(charsOfMessages(visible)) - 10
	tl1570StoreAnchor(a, 3000, 120, covered)

	m, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("meta sidecar missing after trusted sample: ok=%v err=%v", ok, err)
	}
	if m.ContextBaseline == nil {
		t.Fatalf("trusted sample left no context_baseline in the meta sidecar")
	}
	rec := m.ContextBaseline
	if rec.ProviderName != "failing-summary" {
		t.Fatalf("provider = %q, want failing-summary", rec.ProviderName)
	}
	if rec.ModelRef != "model-a" {
		t.Fatalf("model_ref = %q, want model-a", rec.ModelRef)
	}
	if rec.PromptTokens != 3000 || rec.CompletionTokens != 120 {
		t.Fatalf("counts = %d/%d, want 3000/120", rec.PromptTokens, rec.CompletionTokens)
	}
	if rec.CoveredChars != covered {
		t.Fatalf("covered_chars = %d, want %d", rec.CoveredChars, covered)
	}
	if rec.SampledAt.IsZero() || time.Since(rec.SampledAt) > time.Minute {
		t.Fatalf("sampled_at = %v, want a fresh stamp", rec.SampledAt)
	}
}

// 钉②: an identical follow-up sample must not rewrite the sidecar (the disk
// stamp stays the first sample's), while a changed measurement does.
func TestPersistContextBaselineThrottlesIdenticalSample(t *testing.T) {
	path := tl1570TranscriptPath(t)
	a := tl1570Agent(t, &failingSummaryProvider{}, path, "model-a")
	visible := a.modelVisibleMessages()
	covered := int64(charsOfMessages(visible)) - 10
	tl1570StoreAnchor(a, 3000, 120, covered)

	first, _, err := LoadBranchMeta(path)
	if err != nil {
		t.Fatalf("meta read: %v", err)
	}
	stamp := first.ContextBaseline.SampledAt

	// Same measurement again (only the sampling stamp differs): throttled.
	time.Sleep(5 * time.Millisecond)
	tl1570StoreAnchor(a, 3000, 120, covered)
	second, _, err := LoadBranchMeta(path)
	if err != nil {
		t.Fatalf("meta read: %v", err)
	}
	if !second.ContextBaseline.SampledAt.Equal(stamp) {
		t.Fatalf("identical sample rewrote the sidecar: stamp %v -> %v", stamp, second.ContextBaseline.SampledAt)
	}

	// A grown measurement persists immediately.
	tl1570StoreAnchor(a, 3200, 120, covered)
	third, _, err := LoadBranchMeta(path)
	if err != nil {
		t.Fatalf("meta read: %v", err)
	}
	if third.ContextBaseline.PromptTokens != 3200 {
		t.Fatalf("changed sample not persisted: prompt = %d, want 3200", third.ContextBaseline.PromptTokens)
	}
}

// 钉③: reconstructed (Estimated) usage never anchors — in memory (1183) and,
// the new leg, never on disk either.
func TestPersistSkipsUntrustedUsage(t *testing.T) {
	path := tl1570TranscriptPath(t)
	a := tl1570Agent(t, &failingSummaryProvider{}, path, "model-a")
	a.sess.output.activeReqShape.Store(&requestCalibrationShape{requestChars: 1000, compactChars: 1000})
	a.storeContextBaseline(&provider.Usage{PromptTokens: 500, Estimated: true})
	// An untrusted sample never reaches the persist leg: the sidecar either
	// does not exist yet, or (if something else created it) carries no anchor.
	m, ok, err := LoadBranchMeta(path)
	if err != nil {
		t.Fatalf("meta read: %v", err)
	}
	if ok && m.ContextBaseline != nil {
		t.Fatalf("estimated usage was persisted as a trusted anchor")
	}
}

// 钉④（节流镜像）: reset() clears the throttle mirror along with the anchor —
// a replaced transcript must never be skipped as "already persisted" because
// its NEW sidecar has never seen the value.
func TestPersistThrottleMirrorResetsWithTranscript(t *testing.T) {
	path := tl1570TranscriptPath(t)
	a := tl1570Agent(t, &failingSummaryProvider{}, path, "model-a")
	visible := a.modelVisibleMessages()
	tl1570StoreAnchor(a, 3000, 120, int64(charsOfMessages(visible))-10)
	if a.sess.output.lastPersistedBaseline.Load() == nil {
		t.Fatalf("precondition: throttle mirror missing after persist")
	}
	a.sess.output.reset()
	if a.sess.output.lastPersistedBaseline.Load() != nil {
		t.Fatalf("reset() left a stale throttle mirror behind")
	}
}

// 验收（端到端重启腿）: the anchor written by process A re-arms process B —
// a brand-new Agent instance on the same transcript reads the sidecar anchor
// through observedContextTokens (and TrustedObservedContextTokens), identical
// to what the live anchor would have reported.
func TestPersistedAnchorSurvivesRestart(t *testing.T) {
	path := tl1570TranscriptPath(t)
	a := tl1570Agent(t, &failingSummaryProvider{}, path, "model-a")
	visible := a.modelVisibleMessages()
	covered := int64(charsOfMessages(visible)) - 10
	tl1570StoreAnchor(a, 3000, 120, covered)

	// "Restart": fresh Agent instance, same transcript, same destination.
	b := tl1570Agent(t, &failingSummaryProvider{}, path, "model-a")
	bVisible := b.modelVisibleMessages()
	if int64(charsOfMessages(bVisible)) < covered {
		t.Fatalf("fixture drift: restarted view %d < covered %d", charsOfMessages(bVisible), covered)
	}
	got, ok := b.observedContextTokens(bVisible)
	if !ok {
		t.Fatalf("restarted agent rejected its persisted anchor — the 20261113 misjudgment shape")
	}
	// coveredChars sat 10 chars under the live view, so the restart read adds
	// the same session-calibrated delta price the live anchor leg would.
	deltaChars := 10
	want := 3000 + 120 + int(float64(deltaChars)*fallbackTokPerChar)
	if got != want {
		t.Fatalf("restarted observed = %d, want anchor+completion+delta = %d", got, want)
	}
	if b.sess.output.contextBaseline.Load() == nil {
		t.Fatalf("validated read-back did not re-arm the in-memory anchor")
	}
	if _, ok := b.TrustedObservedContextTokens(); !ok {
		t.Fatalf("TrustedObservedContextTokens rejected the persisted anchor")
	}
}

// 边界：超 24h 拒。
func TestPersistedAnchorExpiredAfter24h(t *testing.T) {
	path := tl1570TranscriptPath(t)
	a := tl1570Agent(t, &failingSummaryProvider{}, path, "model-a")
	visible := a.modelVisibleMessages()
	covered := int64(charsOfMessages(visible)) - 10
	tl1570StoreAnchor(a, 3000, 120, covered)

	stale := time.Now().Add(-contextBaselinePersistTTL - time.Hour)
	if err := UpdateBranchMeta(path, false, func(m *BranchMeta) error {
		m.ContextBaseline.SampledAt = stale.UTC()
		return nil
	}); err != nil {
		t.Fatalf("backdate meta: %v", err)
	}
	a.sess.output.contextBaseline.Store(nil) // force the disk leg
	if _, ok := a.observedContextTokens(visible); ok {
		t.Fatalf("anchor past the 24h trust window was accepted")
	}
}

// 边界：换 provider 拒（落盘段——锚跟着量它的请求目标走）。
func TestPersistedAnchorRejectsProviderSwitch(t *testing.T) {
	path := tl1570TranscriptPath(t)
	a := tl1570Agent(t, &failingSummaryProvider{}, path, "model-a")
	visible := a.modelVisibleMessages()
	tl1570StoreAnchor(a, 3000, 120, int64(charsOfMessages(visible))-10)

	b := tl1570Agent(t, &namedOutputBudgetProvider{name: "other-provider"}, path, "model-a")
	if _, ok := b.observedContextTokens(b.modelVisibleMessages()); ok {
		t.Fatalf("persisted anchor priced a different provider with the old one's count")
	}
}

// 边界：换 model 拒——同一 provider 实例内热切模型，providerName 看不见，
// model_ref 段负责拒绝。
func TestPersistedAnchorRejectsModelSwitch(t *testing.T) {
	path := tl1570TranscriptPath(t)
	a := tl1570Agent(t, &failingSummaryProvider{}, path, "model-a")
	visible := a.modelVisibleMessages()
	tl1570StoreAnchor(a, 3000, 120, int64(charsOfMessages(visible))-10)

	b := tl1570Agent(t, &failingSummaryProvider{}, path, "model-b")
	if _, ok := b.observedContextTokens(b.modelVisibleMessages()); ok {
		t.Fatalf("persisted anchor priced a hot-switched model with the old one's count")
	}
}

// 边界：视图增长超界拒——盘上 coveredChars 大于当前视图（fold/rewind 落在
// 上次保存之后），按实账重放会把已折叠前缀加回来，压缩永不收敛。
func TestPersistedAnchorRejectsViewSmallerThanCovered(t *testing.T) {
	path := tl1570TranscriptPath(t)
	a := tl1570Agent(t, &failingSummaryProvider{}, path, "model-a")
	visible := a.modelVisibleMessages()
	tl1570StoreAnchor(a, 3000, 120, int64(charsOfMessages(visible))+1)

	b := tl1570Agent(t, &failingSummaryProvider{}, path, "model-a")
	if _, ok := b.observedContextTokens(b.modelVisibleMessages()); ok {
		t.Fatalf("persisted anchor replayed a count larger than the live view")
	}
}

// 边界：meta 缺失（全新会话/从未采样）→ 无锚，保持估算。
func TestPersistedAnchorAbsentMetaKeepsEstimate(t *testing.T) {
	path := tl1570TranscriptPath(t)
	a := tl1570Agent(t, &failingSummaryProvider{}, path, "model-a")
	if _, ok := a.observedContextTokens(a.modelVisibleMessages()); ok {
		t.Fatalf("accepted an anchor from a transcript that never sampled")
	}
}

// 边界：旧版构建写的 meta（无 context_baseline 字段）→ 无锚，保持估算；
// 双向零迁移。
func TestPersistedAnchorLegacyMetaWithoutField(t *testing.T) {
	path := tl1570TranscriptPath(t)
	a := tl1570Agent(t, &failingSummaryProvider{}, path, "model-a")
	if err := UpdateBranchMeta(path, false, func(m *BranchMeta) error {
		m.CustomTitle = "legacy session"
		return nil
	}); err != nil {
		t.Fatalf("seed legacy meta: %v", err)
	}
	a.sess.output.contextBaseline.Store(nil)
	if _, ok := a.observedContextTokens(a.modelVisibleMessages()); ok {
		t.Fatalf("accepted an anchor from a pre-tl1570 meta without the field")
	}
}
