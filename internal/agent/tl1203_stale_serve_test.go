package agent

import (
	"errors"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// tl1203 回归钉：压缩后可见视图回退全量的行为退役。竞品无一家有此行为
// （codex 物理压缩 / zcode 直接改存储 / DSH 快照投影不存全量事件流），投影
// 失效时最后有效投影继续服务（stale fold + 未覆盖 canonical 尾部逐字拼接），
// 同时后台重建，重建成功后切换——绝不把请求打回 canonical 全量。
//
// 触发源矩阵（用户令 20261027）：切模型 / 400·摘要失败 / 超窗，三类注入后
// 均不得出现「回退全量」状态。切模型+内容匹配的 rebind 路径由任务638 测试
// 钉住（TestModelHotSwitchKeepsProjectedViewAndRebinds）；此处钉内容漂移下的
// stale 服务路径。

// staleServeFixture boots the hot-switch fixture with a real window so the
// compaction trigger and the forced rebuild are armed (the bare fixture's
// window-independent provider leaves compactTrigger at zero). The 0.5 ratio
// keeps trigger (1000) and ceiling (1744) far apart for the pressure fixture.
func staleServeFixture(t *testing.T, window int) *hotSwitchFixture {
	t.Helper()
	fx := newHotSwitchFixture(t)
	fx.agent.contextWindow = window
	fx.agent.compactRatio = 0.5
	return fx
}

// driftCoveredPrefix rewrites a covered canonical turn: the covered-prefix
// hash rejects the fold from this moment (content_mismatch), while the stale
// projection body stays in memory serving the wire.
func driftCoveredPrefix(t *testing.T, fx *hotSwitchFixture) {
	t.Helper()
	if len(fx.agent.sess.compactionState.Projection.Messages) == 0 {
		t.Fatal("fixture setup: no projection body to serve stale")
	}
	fx.agent.sess.conversation.Messages[1].Content = "task-one-EDITED"
}

// armFoldPressure appends one large uncovered turn so the canonical estimate
// lands between the fold trigger (0.5×2000 = 1000) and the hard input
// ceiling (2000-256 = 1744) of the 2000-token fixture window: enough
// pressure for the forced rebuild to fold, not enough to arm the 1114 diet.
// The append is uncovered, so the projection stays valid until driftCoveredPrefix.
func armFoldPressure(t *testing.T, fx *hotSwitchFixture) {
	t.Helper()
	a := fx.agent
	a.sess.conversation.Add(provider.Message{Role: provider.RoleUser, Content: strings.Repeat("bulk-body-", 300)})
	est := a.estimatedVisibleRequestTokens(a.sess.conversation.Messages)
	if est <= a.compactTrigger() || est >= a.hardInputCeiling() {
		t.Fatalf("fixture setup: canonical est %d must sit in (%d, %d)", est, a.compactTrigger(), a.hardInputCeiling())
	}
}

// waitForRebuildIdle polls until no rebuild episode is in flight.
func waitForRebuildIdle(t *testing.T, a *Agent) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !a.sess.rebuildPending.Load() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("rebuild episode never settled")
}

// ① 验收1：投影失效（运行中 content_mismatch）时请求组包仍走投影视图——
// wire 是 sys+digest+未覆盖尾部，绝不是 canonical 全量；失效状态照报
// （ProjectionValid=false），占比读数停在投影视图（不暴涨）。
func TestTL1203StaleFoldServesWireOnContentDrift(t *testing.T) {
	fx := staleServeFixture(t, 100_000)
	a := fx.agent

	driftCoveredPrefix(t, fx)
	wire := a.modelVisibleMessages()
	assertProjectedWire(t, wire, fx.canonical)

	if snap := a.ContextMaintenanceSnapshot(); snap.ProjectionValid {
		t.Fatal("content drift must keep reporting the projection invalid")
	} else if snap.ProjectedTokens >= snap.CanonicalTokens {
		t.Fatalf("stale view must stay folded: projected=%d canonical=%d", snap.ProjectedTokens, snap.CanonicalTokens)
	}
}

// ② 验收3：旧投影服务期间新消息逐字注入——拼接语义与有效投影完全一致
// （covered digest + 未覆盖 canonical 尾部，逐字节相等）。
func TestTL1203StaleFoldInjectsNewMessagesVerbatim(t *testing.T) {
	fx := staleServeFixture(t, 100_000)
	a := fx.agent
	driftCoveredPrefix(t, fx)

	// Two new turns appended after the fold point: they must reach the wire
	// byte-for-byte, exactly as they would under a valid projection.
	newUser := provider.Message{Role: provider.RoleUser, Content: "fresh-user-turn"}
	newReply := provider.Message{Role: provider.RoleAssistant, Content: "fresh-assistant-turn"}
	a.sess.conversation.Add(newUser)
	a.sess.conversation.Add(newReply)

	wire := a.modelVisibleMessages()
	want := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		wire[1], // the stale digest, shape-checked below
		fx.canonical[5],
		fx.canonical[6],
		newUser,
		newReply,
	}
	if len(wire) != len(want) {
		t.Fatalf("wire len = %d, want the 6-message stale view (digest + verbatim tail)", len(wire))
	}
	if !isCompactionSummary(wire[1]) {
		t.Fatalf("wire[1] must be the stale digest, got %+v", wire[1])
	}
	for i := range wire {
		if i == 1 {
			continue
		}
		if wire[i].Role != want[i].Role || wire[i].Content != want[i].Content {
			t.Fatalf("wire[%d] = %s/%q, want %s/%q (verbatim injection)",
				i, wire[i].Role, wire[i].Content, want[i].Role, want[i].Content)
		}
	}
}

// ③ 验收2：触发源矩阵——超窗与摘要失败（400 形态）注入后均不出现回退全量。
// 切模型（内容漂移下）由 TestModelHotSwitchStillFailsClosedOnContentDrift 钉住。
func TestTL1203TriggerSourcesNeverFallBackToFull(t *testing.T) {
	t.Run("over_window", func(t *testing.T) {
		// The canonical transcript outgrows the hard input ceiling; the stale
		// fold still serves (the tail view is the body-less stopgap only).
		fx := staleServeFixture(t, 1_000)
		a := fx.agent
		driftCoveredPrefix(t, fx)

		wire := a.modelVisibleMessages()
		assertProjectedWire(t, wire, fx.canonical)
		for _, m := range wire {
			if strings.Contains(m.Content, "truncated to fit the context window") {
				t.Fatalf("over-window stale serving must not carry a truncation marker: %+v", m)
			}
		}
	})

	t.Run("summary_failure", func(t *testing.T) {
		// The forced rebuild episode fails (every summary request errors, the
		// 400 shape): the stale fold keeps serving through the failure.
		fx := staleServeFixture(t, 2_000)
		a := fx.agent
		armFoldPressure(t, fx)
		fx.home.streamErr = errors.New("summary down")
		driftCoveredPrefix(t, fx)

		if wire := a.modelVisibleMessages(); len(wire) != 5 {
			t.Fatalf("stale wire len = %d, want 5 (digest + tail + bulk turn)", len(wire))
		}
		waitForRebuildIdle(t, a)
		wire := a.modelVisibleMessages()
		if len(wire) >= len(fx.canonical) {
			t.Fatalf("wire len = %d, still a projected view, not the %d-message canonical", len(wire), len(fx.canonical))
		}
		if a.ContextMaintenanceSnapshot().ProjectionValid {
			t.Fatal("a failed rebuild must leave the projection invalid, serving stale")
		}
	})
}

// ④ 强制重建接线：stale 态首读 arm 恰好一次后台重建（压力门槛够不着的形态），
// 重建成功后投影恢复有效、armed 标记清零、视图切回新投影。armFoldPressure 把
// canonical est 顶进触发线与硬顶之间：fold 有压力、1114 diet 不进竞争。
func TestTL1203StaleServingArmsSingleForcedRebuild(t *testing.T) {
	fx := staleServeFixture(t, 2_000)
	a := fx.agent
	armFoldPressure(t, fx)
	driftCoveredPrefix(t, fx)

	if a.sess.staleRebuildArmed.Load() {
		t.Fatal("fixture setup: armed flag must start clear")
	}
	if wire := a.modelVisibleMessages(); len(wire) != 5 {
		t.Fatalf("stale wire len = %d, want 5 (digest + tail + bulk turn)", len(wire))
	}
	if !waitForProjection(t, a) {
		a.sess.compactionMu.Lock()
		defer a.sess.compactionMu.Unlock()
		t.Fatalf("forced rebuild never landed: msgs=%d pending=%v",
			len(a.sess.compactionState.Projection.Messages), a.sess.rebuildPending.Load())
	}

	// The new projection is valid: the wire serves it again and the episode
	// re-arms for any future invalidation.
	if wire := a.modelVisibleMessages(); len(wire) >= len(fx.canonical) {
		t.Fatalf("post-rebuild wire len = %d, want a projected view", len(wire))
	}
	if a.sess.staleRebuildArmed.Load() {
		t.Fatal("staleRebuildArmed must clear once a valid projection serves again")
	}
}
