package agent

// 任务978：压缩触发线滞后于模型窗口 + 投影失效回退全量（full 档）的涨量治理。
// 根因定性（2026-10-18 用户裁决）：真根因不是触发评估时机本身，而是投影失效
// 回退全量态下视图体积可无限涨——0.8 线等到 canonical 很大才让重建启动，摘要
// 请求随体积变大、越窗 400/超时概率上升。本件修法（实验开关
// REASONIX_EXPERIMENT_TL978_EARLY_FOLD，默认关=逐字节现状）：
// ① 回退态触发线前移 0.8→0.6 窗口（fallbackEarlyFoldRatio）；
// ② 回退态同 turn 重试增长门 5%→1%（maintenanceRetryGrowthRatioFallback）。
// 最大回归红线：投影有效（健康）会话的 0.8 线与 5% 门槛一个字都不许动。

import (
	"context"
	"errors"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl978Window 让换算读数留有带宽：前移线=24000、原线=32000、hard=39744。
const tl978Window = 40000

// tl978Session 造一个投影缺失（fresh）+ 可折叠（assistant 大文本为主）的会话，
// canonical 估算落在 [floor, ceil) 带内；带外即 fixture 失败（Fatal，不静默跳过）。
// 换算口径：未校准时 tokens = chars × 0.25（fallbackTokPerChar），
// "word "×4000 = 20000 chars = 5000 tokens/条。
func tl978Session(t *testing.T, units int, tailTokens int) *Session {
	t.Helper()
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "standing constraint: keep the public API stable"},
	}
	for range units {
		msgs = append(msgs,
			provider.Message{Role: provider.RoleAssistant, Content: strings.Repeat("word ", 4000)},
			provider.Message{Role: provider.RoleUser, Content: "continue"},
		)
	}
	if tailTokens > 0 {
		msgs = append(msgs, provider.Message{Role: provider.RoleAssistant, Content: strings.Repeat("word ", tailTokens*4/5)})
	}
	return &Session{Messages: msgs}
}

// tl978Agent 构造 0.8 触发线的 agent（生产默认 compactRatio）。
func tl978Agent(t *testing.T, prov provider.Provider, sess *Session) *Agent {
	t.Helper()
	return New(prov, tool.NewRegistry(), sess, Options{
		ContextWindow: tl978Window,
		CompactRatio:  0.8,
		RecentKeep:    2,
		ArchiveDir:    t.TempDir(),
	}, event.Discard)
}

// withEarlyFold 开着跑 fn，结束后恢复原值；开关关闭的前提取证在此统一做。
func withEarlyFold(t *testing.T, fn func(t *testing.T)) {
	t.Helper()
	old := earlyFoldEnabled
	earlyFoldEnabled = true
	defer func() { earlyFoldEnabled = old }()
	fn(t)
}

// tl978BandGuard 钉住 fixture 前提：canonical 落在 [前移线, 原线) 带内。
func tl978BandGuard(t *testing.T, a *Agent, sess *Session) int {
	t.Helper()
	est := canonicalEstimate(a, sess)
	moved := int(float64(a.compactTrigger()) * fallbackEarlyFoldRatio)
	if est < moved || est >= a.compactTrigger() {
		t.Fatalf("fixture canonical = %d tokens, want in [%d, %d) — not exercised", est, moved, a.compactTrigger())
	}
	return est
}

// 验收① 主对照：同一带内 fixture，开关关（现状 0.8 线）不触发压缩、开关开
// （前移 0.6 线）触发——这就是「修前/修后实测阈值读数」的可执行形态。
func TestTask978EarlyFoldMovesFallbackTriggerLine(t *testing.T) {
	build := func(t *testing.T) (*Agent, *Session, *fakeProvider) {
		sess := tl978Session(t, 5, 3000) // 5×5000 + 3000 ≈ 28000 tokens
		prov := &fakeProvider{reply: "digest"}
		a := tl978Agent(t, prov, sess)
		tl978BandGuard(t, a, sess)
		return a, sess, prov
	}

	t.Run("off_keeps_legacy_line", func(t *testing.T) {
		if earlyFoldEnabled {
			t.Fatal("test requires the default-off state; another test leaked the flag")
		}
		a, _, prov := build(t)
		if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
			t.Fatalf("prepare = %v", err)
		}
		if len(prov.got) != 0 {
			t.Fatalf("summary request issued at est below the legacy 0.8 line — switch-off must be byte-identical status quo (readings: moved line would be %d)", int(float64(a.compactTrigger())*fallbackEarlyFoldRatio))
		}
		if r := a.sess.compactionState.LastReceipt; r != nil {
			t.Fatalf("receipt recorded (%s) — compaction must not have run below the legacy line", r.Status)
		}
	})

	t.Run("on_fires_at_moved_line", func(t *testing.T) {
		withEarlyFold(t, func(t *testing.T) {
			a, _, prov := build(t)
			if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
				t.Fatalf("prepare = %v", err)
			}
			if len(prov.got) == 0 {
				t.Fatalf("no summary request at est above the moved 0.6 line — early fold did not fire (moved line = %d)", int(float64(a.compactTrigger())*fallbackEarlyFoldRatio))
			}
			r := a.sess.compactionState.LastReceipt
			if r == nil || r.Status != "applied" {
				t.Fatalf("receipt = %+v, want an applied fold", r)
			}
			view := a.estimatedVisibleRequestTokens(a.modelVisibleMessages())
			if view >= a.compactTrigger() {
				t.Fatalf("view = %d tokens still above the trigger %d after the fold", view, a.compactTrigger())
			}
		})
	})
}

// 验收② 回归红线：投影有效（健康）会话在原线以下绝不被前移线打扰——
// 开关开着也一样。这是「正常小/中会话不得被反复压缩」的自证。
func TestTask978HealthyProjectionNeverEarlyFolds(t *testing.T) {
	withEarlyFold(t, func(t *testing.T) {
		sess := tl978Session(t, 7, 0) // ~35000，过 0.8 线、低于 hard——先压一次拿到有效投影
		prov := &fakeProvider{reply: "digest"}
		a := tl978Agent(t, prov, sess)
		if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
			t.Fatalf("seed fold = %v", err)
		}
		if r := a.sess.compactionState.LastReceipt; r == nil || r.Status != "applied" {
			t.Fatalf("seed fold receipt = %+v, want applied", r)
		}
		if !projectionValid(a.sess.compactionState, tl978Snapshot(t, a)) {
			t.Fatal("seed fold did not leave a valid projection — fixture broken")
		}
		seedVersion := a.currentProjectionVersion()

		// 把视图推到前移线与原线之间：投影有效 ⇒ 即便开关开着也必须走原 0.8 线。
		conv := a.sess.conversation
		per := strings.Repeat("word ", 1600) // 400 tokens/条
		for a.estimatedVisibleRequestTokens(a.modelVisibleMessages()) < int(float64(a.compactTrigger())*fallbackEarlyFoldRatio)+1000 {
			conv.Add(provider.Message{Role: provider.RoleAssistant, Content: per})
		}
		view := a.estimatedVisibleRequestTokens(a.modelVisibleMessages())
		moved := int(float64(a.compactTrigger()) * fallbackEarlyFoldRatio)
		if view < moved || view >= a.compactTrigger() {
			t.Fatalf("fixture view = %d, want in [%d, %d) — not exercised", view, moved, a.compactTrigger())
		}

		prov.got = nil
		if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
			t.Fatalf("prepare = %v", err)
		}
		if len(prov.got) != 0 {
			t.Fatalf("valid-projection session compacted below the legacy line — the moved trigger leaked into healthy sessions (view=%d, moved=%d, legacy=%d)", view, moved, a.compactTrigger())
		}
		if a.currentProjectionVersion() != seedVersion {
			t.Fatalf("projection version moved %d -> %d without cause", seedVersion, a.currentProjectionVersion())
		}
	})
}

// 验收③ 1029 显式兼容：前移线触发后摘要永久失败 ⇒ 降级链必须照旧落地一个
// 可发送视图（< hard），失败可见、不互堵、不死循环——前移只改「何时开始」，
// 不改失败后的换道语义。
func TestTask978EarlyFoldKeepsRescueChainIntact(t *testing.T) {
	withEarlyFold(t, func(t *testing.T) {
		sess := tl978Session(t, 5, 3000)
		a := tl978Agent(t, &fakeProvider{streamErr: errors.New("provider down")}, sess)
		tl978BandGuard(t, a, sess)
		hard := a.hardInputCeiling()

		err := prepareContext(context.Background(), a, CompactionTriggerPressure)
		if err != nil && !errors.Is(err, ErrCompactionRequired) {
			t.Fatalf("prepare = %v, want success or the fail-visible ErrCompactionRequired", err)
		}
		view := a.estimatedVisibleRequestTokens(a.modelVisibleMessages())
		if view >= hard {
			t.Fatalf("view = %d tokens at or above hard ceiling %d after the failed fold — degradation chain blocked (1029 regression)", view, hard)
		}
		if got := len(sess.Messages); got < 12 {
			t.Fatalf("canonical messages = %d — rescue must project, never delete", got)
		}
	})
}

// 验收④ 1116 兼容组合：回退全量 + 涨量序列。投影失效会话在开关开时随涨量
// 更早重建并收敛到线下；全程不落入「全量越涨越压不动」死循环。
func TestTask978FallbackGrowthConvergesWithMovedLine(t *testing.T) {
	withEarlyFold(t, func(t *testing.T) {
		sess := tl978Session(t, 3, 0) // ~15000，先落在原线之下
		prov := &fakeProvider{reply: "digest"}
		a := tl978Agent(t, prov, sess)
		moved := int(float64(a.compactTrigger()) * fallbackEarlyFoldRatio)

		if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
			t.Fatalf("prepare below lines = %v", err)
		}
		if len(prov.got) != 0 {
			t.Fatal("summary issued below the moved line — fixture broken")
		}

		// 涨过前移线（但仍低于原线）：开关开的回退态必须在此处就重建收敛。
		conv := a.sess.conversation
		unit := provider.Message{Role: provider.RoleAssistant, Content: strings.Repeat("word ", 4000)}
		for canonicalEstimate(a, sess) < moved+500 {
			conv.Add(unit)
		}
		if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
			t.Fatalf("prepare at moved line = %v", err)
		}
		if r := a.sess.compactionState.LastReceipt; r == nil || r.Status != "applied" {
			t.Fatalf("receipt = %+v, want an applied fold at the moved line (canonical=%d, moved=%d)", r, canonicalEstimate(a, sess), moved)
		}
		view := a.estimatedVisibleRequestTokens(a.modelVisibleMessages())
		if view >= a.compactTrigger() {
			t.Fatalf("view = %d did not converge below the trigger %d after rebuild", view, a.compactTrigger())
		}
	})
}

// 验收⑤ 改法 B 读数对照：同 turn 重试增长门——投影健康态维持 5% 原门槛；
// 回退全量态在开关开时降到 1%（1M 窗从 50K 降到 10K tokens 的放行差）。
func TestTask978RetryGateGrowthThreshold(t *testing.T) {
	base := maintenanceRetryGrowthRatio * float64(tl978Window)

	t.Run("healthy_projection_keeps_5pct", func(t *testing.T) {
		withEarlyFold(t, func(t *testing.T) {
			sess := tl978Session(t, 7, 0)
			a := tl978Agent(t, &fakeProvider{reply: "digest"}, sess)
			if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
				t.Fatalf("seed fold = %v", err)
			}
			if !projectionValid(a.sess.compactionState, tl978Snapshot(t, a)) {
				t.Fatal("seed fold did not leave a valid projection — fixture broken")
			}
			// 投影已有效：伪造一份收据，让 retry 门有基准可读。
			a.sess.compactionState.LastReceipt = &ContextMaintenanceReceipt{Status: "applied", InputTokens: 10000}
			due := a.maintenanceRetryDue(a.sess.compactionState.LastReceipt, 10000+int(base)-1)
			if due {
				t.Fatalf("retry due below the 5%% growth gate (%.0f tokens) — healthy sessions must keep the original gate", base)
			}
		})
	})

	t.Run("fallback_state_lowers_to_1pct", func(t *testing.T) {
		withEarlyFold(t, func(t *testing.T) {
			sess := tl978Session(t, 5, 3000) // 投影缺失 ⇒ 回退态
			a := tl978Agent(t, &fakeProvider{reply: "digest"}, sess)
			a.sess.compactionState.LastReceipt = &ContextMaintenanceReceipt{Status: "failed", InputTokens: 10000}
			onePct := int(0.01 * float64(tl978Window))
			if !a.maintenanceRetryDue(a.sess.compactionState.LastReceipt, 10000+onePct) {
				t.Fatalf("retry not due at +1%% window in fallback state — the lowered gate did not apply")
			}
			if a.maintenanceRetryDue(a.sess.compactionState.LastReceipt, 10000+onePct-1) {
				t.Fatalf("retry due below +1%% window — gate looser than specified")
			}
		})
	})

	t.Run("switch_off_keeps_5pct_everywhere", func(t *testing.T) {
		if earlyFoldEnabled {
			t.Fatal("test requires the default-off state; another test leaked the flag")
		}
		sess := tl978Session(t, 5, 3000)
		a := tl978Agent(t, &fakeProvider{reply: "digest"}, sess)
		a.sess.compactionState.LastReceipt = &ContextMaintenanceReceipt{Status: "failed", InputTokens: 10000}
		if a.maintenanceRetryDue(a.sess.compactionState.LastReceipt, 10000+int(base)-1) {
			t.Fatalf("switch-off retry due below the 5%% gate — status quo drifted")
		}
	})
}

func tl978Snapshot(t *testing.T, a *Agent) []provider.Message {
	t.Helper()
	msgs, _ := a.sess.conversation.snapshotMessagesVersion()
	return msgs
}
