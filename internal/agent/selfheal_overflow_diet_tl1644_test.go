package agent

// tl1644 验收矩阵（20261116 装机重启折叠 719 条复发，正反例先于合入）。
//
// 现场：包96 版本切换重启 → LoadProjectionSidecar 校验 fail-closed（盘上
// canonical 前缀被静默 rewrite 改写，内存/盘上形态分叉）→ no_projection
// fallback → prepareOnce 按 canonical 全量占用估 1.21M ≥ 硬顶 999,744 →
// kickOverflowDiet → 折叠 719 条。而重启前最后一次真实请求实账仅 668,863
// （投影 v6=521K+尾部，正常服务）——失效是暂态，折叠是误杀。
//
// 判定标准（任务书）：压缩正常工作时 selfheal 不应触发。
//
// | #  | 态                                                       | 期望                                              | 钉 |
// |----|----------------------------------------------------------|---------------------------------------------------|----|
// | A1 | 反例：投影失效+估算≥硬顶+无可信实账锚                      | diet 触发：折叠+armed/applied slog+repairlog+备份   | TestTL1644DietTriggersWithoutTrustedAnchor |
// | A2 | 正例：投影失效+估算≥硬顶+可信锚基值<硬顶（tl1644 现场）    | defer：零折叠+deferred slog；宽限内重复 kick 仍 defer | TestTL1644DietDeferredByAnchorBelowCeiling |
// | A3 | 正例：健康压缩轮（投影有效+压缩装成清锚+估算<线）          | 零 defer 零 diet，repairlog 零新行                  | TestTL1644HealthyCompactionRoundNeverDietsOrDefers |
// | A4 | 反例：投影失效+估算≥硬顶+锚基值也≥硬顶（真超窗）           | diet 照常触发（兜底保留）+armed slog 在场            | TestTL1644DietProceedsWhenAnchorAtOrAboveCeiling |
// | A5 | 反例：宽限期满（rebuild 迟迟不落地）                        | 同一 episode 的 kick 兜底接管，折叠执行              | TestTL1644DietDeferExpiresIntoFold |
// | B1 | 观测：LoadProjectionSidecar body 校验失败丢弃              | WARN slog 带 mismatch 原因+stored/current hash 读数  | TestTL1644SidecarDropLogsMismatchReadings |
// | B2 | 观测：kickOverflowDiet 武装瞬间                            | armed slog 带 reason/estimate/ceiling/anchor 读数    | TestTL1644DietArmedLogsReadings |
// | C1 | 纯函数：projectionInvalidReason 各失配分支          | 原因命名逐分支正确且与 valid 判定同真同假            | TestTL1644ProjectionContentInvalidReason |
//
// non-goals 保持：不删 selfheal 兜底（A1/A4/A5 证明保留）、不动压缩阈值参数、
// 不改 pre-repair 备份机制。

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// preRepairGlob globs the session's pre-repair backup directories (1114 fixture shape).
func preRepairGlob(path string) string {
	return strings.TrimSuffix(path, ".jsonl") + ".pre-repair-*"
}

// waitForRebuildFlagDown waits until the post-invalidation rebuild goroutine
// has left its in-flight flag, so the TempDir cleanup does not race a
// goroutine still writing derived files. Not the 813 acceptance helper
// (waitForRebuildSettled): this fixture has no summary model, so the rebuild
// can never restore a projection — only its goroutine needs to settle.
func waitForRebuildFlagDown(t *testing.T, a *Agent) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !a.sess.rebuildPending.Load() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Log("background rebuild still pending at deadline; continuing")
}

// captureDefaultLogger swaps the default slog logger for one writing into a
// buffer and returns a restore func (the desktop module has the same pattern).
func captureDefaultLogger(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	return &buf, func() { slog.SetDefault(prev) }
}

// setTrustedAnchor installs an in-memory usage anchor whose base measurement is
// baseTokens (provider name "" matches the provider-less test agent).
func setTrustedAnchor(a *Agent, baseTokens int64) {
	a.sess.output.contextBaseline.Store(&contextBaselineState{
		providerName: a.requestProviderName(),
		promptTokens: int(baseTokens),
		coveredChars: 1,
	})
}

// A1 反例腿：无锚 + 估算≥硬顶 ⇒ diet 照常触发，armed slog 在场，折叠落地。
func TestTL1644DietTriggersWithoutTrustedAnchor(t *testing.T) {
	path := imageDominantSchemaOneSession(t, t.TempDir(), "tl1644_noanchor.jsonl")
	a := agentOnDisk1114(t, path)
	buf, restore := captureDefaultLogger(t)
	defer restore()

	if _, ok := a.trustedContextBaselineBase(); ok {
		t.Fatal("fixture setup: the fixture must start with no trusted anchor")
	}
	a.observeProjectionFallback(a.hardInputCeiling() + 1)
	if !waitForDietSettled1114(t, a) {
		t.Fatal("overflow diet never settled")
	}

	records, err := ListSessionRepairRecordsFor(path)
	if err != nil || len(records) != 1 {
		t.Fatalf("the no-anchor episode must fold exactly once: %d records err=%v", len(records), err)
	}
	logged := buf.String()
	if !strings.Contains(logged, "overflow diet armed") {
		t.Fatalf("the armed reading must be logged on trigger: %s", logged)
	}
	if !strings.Contains(logged, "overflow diet applied") {
		t.Fatalf("the applied WARN must stay on the trigger path: %s", logged)
	}
}

// A2 正例腿：可信锚基值 < 硬顶 ⇒ defer（零折叠），宽限内重复 kick 仍 defer。
// tl1644 现场：锚 668,863 < 999,744，失效前视图可服务，折叠是误杀。
func TestTL1644DietDeferredByAnchorBelowCeiling(t *testing.T) {
	path := imageDominantSchemaOneSession(t, t.TempDir(), "tl1644_defer.jsonl")
	a := agentOnDisk1114(t, path)
	buf, restore := captureDefaultLogger(t)
	defer restore()

	// 失效前视图的实账基值：远低于硬顶（现场 668,863/1M 的缩比形态）。
	setTrustedAnchor(a, 60_000)

	est := a.hardInputCeiling() + 500_000 // fallback 全量占用：远超硬顶
	a.kickOverflowDiet("no_projection", est)
	if a.sess.dietPending.Load() {
		t.Fatal("an anchor below the ceiling must defer the diet, not arm it")
	}
	if records, _ := ListSessionRepairRecordsFor(path); len(records) != 0 {
		t.Fatalf("a deferred diet must not write a repair record: %+v", records)
	}
	if backupDirs, err := filepath.Glob(preRepairGlob(path)); err != nil || len(backupDirs) != 0 {
		t.Fatalf("a deferred diet must not create a backup dir: %v err=%v", backupDirs, err)
	}
	logged := buf.String()
	if !strings.Contains(logged, "overflow diet deferred") {
		t.Fatalf("the defer must be visible with its readings: %s", logged)
	}
	if !strings.Contains(logged, "anchored_view_tokens=60000") {
		t.Fatalf("the defer log must carry the anchor base reading: %s", logged)
	}

	// 宽限内的重复 kick（fallback 每 5 分钟节流重发）仍然 defer，零折叠。
	a.kickOverflowDiet("no_projection", est)
	if a.sess.dietPending.Load() {
		t.Fatal("a kick inside the defer grace must stay deferred")
	}
	if records, _ := ListSessionRepairRecordsFor(path); len(records) != 0 {
		t.Fatalf("repeated kicks inside the grace must not fold: %+v", records)
	}
}

// A3 正例腿：健康压缩轮——投影有效+压缩装成（tl1600② 清锚）+估算<触发线 ⇒
// 零 defer 零 diet，repairlog 零新行。压缩正常工作时 selfheal 不应触发。
func TestTL1644HealthyCompactionRoundNeverDietsOrDefers(t *testing.T) {
	path := imageDominantSchemaOneSession(t, t.TempDir(), "tl1644_healthy.jsonl")
	a := agentOnDisk1114(t, path)

	msgs := a.sess.conversation.Snapshot()
	covered := len(msgs) - 4
	a.sess.compactionMu.Lock()
	a.sess.compactionState = CompactionState{
		SchemaVersion:     compactionStateSchemaCurrent,
		TranscriptVersion: a.sess.conversation.TranscriptVersion(),
		Projection: ContextProjection{
			Messages:          []provider.Message{formatSummaryMessage("digest of the covered prefix")},
			CoveredCount:      covered,
			CoveredPrefixHash: coveredPrefixHash(msgs, covered),
			PinnedContextHash: pinnedContextCoverageHash(msgs, covered),
			ProjectionVersion: 7,
		},
		PromptCacheKey: a.currentPromptCacheKeyLocked(),
	}
	if err := a.persistCompactionStateLocked(); err != nil {
		a.sess.compactionMu.Unlock()
		t.Fatalf("persist fixture projection: %v", err)
	}
	a.sess.compactionMu.Unlock()
	// tl1600②：压缩装成即清锚——下一轮可信采样才重新落锚。
	a.clearContextBaseline()

	// 压缩后的视图估算远低于触发线（现场 521K/1M 的缩比形态）；维护检查循环。
	viewEst := a.estimatedVisibleRequestTokens(a.modelVisibleMessages())
	for i := 0; i < 5; i++ {
		a.observeProjectionFallback(viewEst)
		a.kickOverflowDiet("no_projection", viewEst)
	}
	if a.sess.dietPending.Load() {
		t.Fatal("a healthy compaction round must never arm the overflow diet")
	}
	a.sess.compactionMu.Lock()
	deferred := a.sess.compaction.overflowDietDeferredAt
	a.sess.compactionMu.Unlock()
	if !deferred.IsZero() {
		t.Fatal("a healthy compaction round must never enter the diet-defer state")
	}
	if records, _ := ListSessionRepairRecordsFor(path); len(records) != 0 {
		t.Fatalf("a healthy compaction round must not write a repair record: %+v", records)
	}
}

// A4 反例腿：锚基值也≥硬顶（真超窗）⇒ 不 defer，兜底照常折叠。
func TestTL1644DietProceedsWhenAnchorAtOrAboveCeiling(t *testing.T) {
	path := imageDominantSchemaOneSession(t, t.TempDir(), "tl1644_over.jsonl")
	a := agentOnDisk1114(t, path)
	buf, restore := captureDefaultLogger(t)
	defer restore()

	setTrustedAnchor(a, int64(a.hardInputCeiling())+10_000)
	a.kickOverflowDiet("no_projection", a.hardInputCeiling()+500_000)
	if !waitForDietSettled1114(t, a) {
		t.Fatal("a genuinely over-window episode never settled")
	}
	logged := buf.String()
	if strings.Contains(logged, "overflow diet deferred") {
		t.Fatalf("an anchor at/above the ceiling must not defer: %s", logged)
	}
	if !strings.Contains(logged, "overflow diet armed") {
		t.Fatalf("the armed reading must be logged: %s", logged)
	}
	if records, _ := ListSessionRepairRecordsFor(path); len(records) != 1 {
		t.Fatalf("the genuinely over-window episode must fold: %d records", len(records))
	}
}

// A5 反例腿：宽限期满（rebuild 迟迟不落地）⇒ 同一 episode 的 kick 兜底接管。
func TestTL1644DietDeferExpiresIntoFold(t *testing.T) {
	path := imageDominantSchemaOneSession(t, t.TempDir(), "tl1644_grace.jsonl")
	a := agentOnDisk1114(t, path)

	setTrustedAnchor(a, 60_000)
	a.kickOverflowDiet("no_projection", a.hardInputCeiling()+500_000)
	if a.sess.dietPending.Load() {
		t.Fatal("fixture setup: the first kick must defer")
	}
	// 把首次 defer 拨回宽限之前：模拟 rebuild 长期不落地。
	a.sess.compactionMu.Lock()
	a.sess.compaction.overflowDietDeferredAt = time.Now().Add(-2 * overflowDietAnchorGrace)
	a.sess.compactionMu.Unlock()

	a.kickOverflowDiet("no_projection", a.hardInputCeiling()+500_000)
	if !waitForDietSettled1114(t, a) {
		t.Fatal("the post-grace kick never settled")
	}
	if records, _ := ListSessionRepairRecordsFor(path); len(records) != 1 {
		t.Fatalf("past the grace the 兜底 must fold exactly once: %d records", len(records))
	}
}

// B1 观测钉：body 校验失败丢弃必须带失配原因与 hash 读数落 WARN。
func TestTL1644SidecarDropLogsMismatchReadings(t *testing.T) {
	path := imageDominantSchemaOneSession(t, t.TempDir(), "tl1644_drop.jsonl")
	a := agentOnDisk1114(t, path)

	// 装一个对当前 canonical 有效的投影并落盘（模拟重启前健康 applied）。
	msgs := a.sess.conversation.Snapshot()
	covered := len(msgs) - 4
	a.sess.compactionMu.Lock()
	a.sess.compactionState = CompactionState{
		SchemaVersion:     compactionStateSchemaCurrent,
		TranscriptVersion: a.sess.conversation.TranscriptVersion(),
		Projection: ContextProjection{
			Messages:          []provider.Message{formatSummaryMessage("digest of the covered prefix")},
			CoveredCount:      covered,
			CoveredPrefixHash: coveredPrefixHash(msgs, covered),
			PinnedContextHash: pinnedContextCoverageHash(msgs, covered),
			ProjectionVersion: 7,
		},
		PromptCacheKey: a.currentPromptCacheKeyLocked(),
	}
	if err := a.persistCompactionStateLocked(); err != nil {
		a.sess.compactionMu.Unlock()
		t.Fatalf("persist fixture projection: %v", err)
	}
	a.sess.compactionMu.Unlock()

	// 篡改盘上侧车的 covered_prefix_hash（模拟现场「盘上前缀被改写后重启」：
	// 装投影时的 hash 与重启后 canonical 的 hash 不再相等）。
	sidecarPath := ContextStatePath(path)
	blob, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	var sidecar map[string]any
	if err := json.Unmarshal(blob, &sidecar); err != nil {
		t.Fatalf("unmarshal sidecar: %v", err)
	}
	proj, _ := sidecar["projection"].(map[string]any)
	if proj == nil {
		t.Fatal("fixture setup: sidecar has no projection object")
	}
	proj["covered_prefix_hash"] = "deadbeefdeadbeefdeadbeefdeadbeef"
	fixed, err := json.Marshal(sidecar)
	if err != nil {
		t.Fatalf("marshal sidecar: %v", err)
	}
	if err := os.WriteFile(sidecarPath, fixed, 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}

	buf, restore := captureDefaultLogger(t)
	defer restore()
	a.LoadProjectionSidecar(path)

	// Assert the drop readings immediately: the post-invalidation rebuild
	// goroutine may legitimately reinstall a fresh projection (checkpointState
	// returns to "applied") moments later — that is the tl1644 recovery path,
	// not a failed drop. The readings below are the drop's own audit trail.
	a.sess.compactionMu.Lock()
	dropped := len(a.sess.compactionState.Projection.Messages) == 0
	a.sess.compactionMu.Unlock()
	if !dropped {
		t.Fatal("the tampered body must fail closed (projection body dropped)")
	}
	logged := buf.String()
	if !strings.Contains(logged, "projection sidecar body dropped") {
		t.Fatalf("the drop must be logged: %s", logged)
	}
	if !strings.Contains(logged, "mismatch=hash_mismatch") {
		t.Fatalf("the log must name the failed criterion: %s", logged)
	}
	if !strings.Contains(logged, "stored_prefix_hash=deadbeef") || !strings.Contains(logged, "current_prefix_hash=") {
		t.Fatalf("the log must carry both hash readings: %s", logged)
	}

	// Let the rebuild goroutine settle so the TempDir cleanup does not race it.
	waitForRebuildFlagDown(t, a)
}

// B2 观测钉：armed slog 必须带触发原因、估算、硬顶与锚读数。
func TestTL1644DietArmedLogsReadings(t *testing.T) {
	path := imageDominantSchemaOneSession(t, t.TempDir(), "tl1644_armed.jsonl")
	a := agentOnDisk1114(t, path)
	buf, restore := captureDefaultLogger(t)
	defer restore()

	a.kickOverflowDiet("no_projection", a.hardInputCeiling()+123_456)
	if !waitForDietSettled1114(t, a) {
		t.Fatal("the armed kick never settled")
	}
	logged := buf.String()
	for _, want := range []string{
		"msg=\"session: overflow diet armed after projection fallback\"",
		"reason=no_projection",
		"estimate_tokens=",
		"hard_ceiling=",
		"anchor_trusted=false",
	} {
		if !strings.Contains(logged, want) {
			t.Fatalf("armed log missing %q in:\\n%s", want, logged)
		}
	}
}

// C1 纯函数钉：失配原因逐分支命名正确，且与 projectionContentValid 同真同假。
func TestTL1644ProjectionContentInvalidReason(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "hello"},
		{Role: provider.RoleAssistant, Content: "hi"},
	}
	mk := func() CompactionState {
		return CompactionState{
			SchemaVersion: compactionStateSchemaCurrent,
			Projection: ContextProjection{
				Messages:          []provider.Message{formatSummaryMessage("digest")},
				CoveredCount:      2,
				CoveredPrefixHash: coveredPrefixHash(msgs, 2),
				PinnedContextHash: pinnedContextCoverageHash(msgs, 2),
				ProjectionVersion: 1,
			},
		}
	}

	if got := projectionInvalidReason(mk(), msgs); got != "" {
		t.Fatalf("a matching projection must read valid: %q", got)
	}
	if !projectionContentValid(mk(), msgs) {
		t.Fatal("lockstep: valid wrapper must agree with the empty reason")
	}

	drift := mk()
	drift.Projection.CoveredPrefixHash = "deadbeefdeadbeefdeadbeefdeadbeef"
	if got := projectionInvalidReason(drift, msgs); got != projectionFailHashMismatch {
		t.Fatalf("hash drift must name the criterion: %q", got)
	}

	noHash := mk()
	noHash.Projection.CoveredPrefixHash = ""
	if got := projectionInvalidReason(noHash, msgs); got != projectionFailLegacyNoHash {
		t.Fatalf("missing hash must name the criterion: %q", got)
	}

	badCount := mk()
	badCount.Projection.CoveredCount = len(msgs) + 1
	if got := projectionInvalidReason(badCount, msgs); got != projectionFailCoveredOverflow {
		t.Fatalf("out-of-range coverage must name the criterion: %q", got)
	}

	empty := mk()
	empty.Projection.Messages = nil
	if got := projectionInvalidReason(empty, msgs); got != projectionFailBodyEmpty {
		t.Fatalf("an empty body must name the criterion: %q", got)
	}

	// v4 pinned hash 失配：往 covered 前缀塞一条 pinned revision，锚 hash 未跟踪。
	pinned := mk()
	withPinned := append([]provider.Message(nil), msgs[:2]...)
	withPinned = append(withPinned, provider.Message{
		Role:    provider.RoleUser,
		Origin:  provider.MessageOriginHost,
		Content: "<pinned_context_revision>fixture</pinned_context_revision>",
	})
	if !containsPinnedContextRevision(withPinned) {
		t.Fatal("fixture setup: the host-authored marker must be recognized as a pinned revision")
	}
	pinned.Projection.CoveredCount = len(withPinned)
	pinned.Projection.CoveredPrefixHash = coveredPrefixHash(msgs, len(withPinned))
	pinned.Projection.PinnedContextHash = ""
	if got := projectionInvalidReason(pinned, withPinned); got != projectionFailPinned {
		t.Fatalf("pinned hash mismatch must name the criterion: %q", got)
	}
}
