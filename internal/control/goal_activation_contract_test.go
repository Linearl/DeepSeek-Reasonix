package control

import (
	"strings"
	"testing"
	"time"

	"reasonix/internal/tool"
)

// tl1472 验收第一条（架构专家新增）：状态一致性契约测试。goal 状态升格为对外
// 契约——牌=armed+running ⇒ 下轮必须自动续；牌=disarmed ⇒ 必须不续；
// budget_limited ⇒ 停且 cause 可见。防「同一语义两处维护」腐化（宪法 III）。

func contractContinueReport(reason string) *goalTurnReport {
	return &goalTurnReport{status: GoalStatusRunning, reason: reason, nextAction: "next step"}
}

func TestContractArmedRunningMustAutoContinue(t *testing.T) {
	g := &goalMachine{goal: "ship it", status: GoalStatusRunning, scopeID: newGoalScopeID()}
	epoch := g.continuationToken()
	res := g.advance(goalAdvanceInput{
		report:        contractContinueReport("working on it"),
		expectedEpoch: &epoch,
	})
	if !res.cont {
		t.Fatalf("armed+running must auto-continue: got cont=false (halted=%q notice=%q)", res.halted, res.notice)
	}
	if res.halted != "" {
		t.Fatalf("armed goal must not report a halt: %q", res.halted)
	}
	if _, ok := g.admitContinuation(res); !ok {
		t.Fatalf("armed+running continuation must be admitted")
	}
	if g.activationValue() != GoalActivationArmed {
		t.Fatalf("armed goal must stay armed after a continue advance: %q", g.activationValue())
	}
}

func TestContractDisarmedMustNotContinue(t *testing.T) {
	g := &goalMachine{goal: "ship it", status: GoalStatusRunning, scopeID: newGoalScopeID()}
	path, _, ok := g.disarm(goalActivationActorUser, "user_message", nil)
	if !ok {
		// statePath empty → persistence disabled; the disarm itself still ran.
		t.Logf("disarm persistence skipped (no state path, path=%q)", path)
	}
	if got := g.activationValue(); got != GoalActivationDisarmed {
		t.Fatalf("disarm must flip the switch: got %q", got)
	}
	if got := g.phase(); got != GoalPhaseActive {
		t.Fatalf("disarmed goal must stay active (状态牌与续跑解耦): got phase %q", got)
	}
	epoch := g.continuationToken()
	res := g.advance(goalAdvanceInput{
		report:        contractContinueReport("still working"),
		expectedEpoch: &epoch,
	})
	if res.cont {
		t.Fatalf("牌=disarmed ⇒ 必须不续: got cont=true")
	}
	if res.halted != "disarmed" {
		t.Fatalf("disarmed halt must be named: halted=%q", res.halted)
	}
	if _, ok := g.admitContinuation(res); ok {
		t.Fatalf("disarmed continuation must be refused at admission")
	}
	// The goal object survives: text, phase, scope intact.
	if g.goalText() != "ship it" || g.phase() != GoalPhaseActive {
		t.Fatalf("disarmed goal must stay intact: goal=%q phase=%q", g.goalText(), g.phase())
	}
	// A terminal decision still applies to a disarmed goal (user-takeover turn
	// can legitimately complete it).
	epoch2 := g.continuationToken()
	res2 := g.advance(goalAdvanceInput{
		report:        &goalTurnReport{status: GoalStatusComplete, reason: "done"},
		expectedEpoch: &epoch2,
	})
	if g.status != GoalStatusComplete {
		t.Fatalf("disarmed goal must still accept a complete report: status=%q", g.status)
	}
	if res2.cont {
		t.Fatalf("terminal advance must not continue")
	}
	if got := g.phase(); got != GoalPhaseComplete {
		t.Fatalf("completed goal phase: got %q", got)
	}
}

func TestContractBudgetLimitedStopsWithCause(t *testing.T) {
	t.Run("autopilot_terminal", func(t *testing.T) {
		g := &goalMachine{goal: "ship it", status: GoalStatusRunning, autopilot: true, tokensLimit: 100, tokensUsed: 100, scopeID: newGoalScopeID()}
		g.setActivationLocked(true, goalActivationActorUser, "install")
		epoch := g.continuationToken()
		res := g.advance(goalAdvanceInput{
			report:        contractContinueReport("keep going"),
			expectedEpoch: &epoch,
		})
		if res.cont {
			t.Fatalf("budget_limited ⇒ 必须停: got cont=true")
		}
		if g.status != GoalStatusBudgetExhausted {
			t.Fatalf("autopilot budget exhaustion must be terminal: status=%q", g.status)
		}
		if got := g.phase(); got != GoalPhaseBudgetLimited {
			t.Fatalf("budget phase mapping: got %q", got)
		}
		if res.cause != stopCauseBudgetSpend || g.lastStopCause != stopCauseBudgetSpend {
			t.Fatalf("budget stop must carry a visible cause: res=%q lastStopCause=%q", res.cause, g.lastStopCause)
		}
		if g.activationValue() != GoalActivationDisarmed {
			t.Fatalf("预算耗尽必须 disarm（disarm 全集④）: %q", g.activationValue())
		}
	})
	t.Run("interactive_safe_pause", func(t *testing.T) {
		g := &goalMachine{goal: "ship it", status: GoalStatusRunning, tokensLimit: 100, tokensUsed: 100, scopeID: newGoalScopeID()}
		g.setActivationLocked(true, goalActivationActorUser, "install")
		epoch := g.continuationToken()
		res := g.advance(goalAdvanceInput{
			report:        contractContinueReport("keep going"),
			expectedEpoch: &epoch,
		})
		if res.cont {
			t.Fatalf("interactive budget pause must stop the loop: got cont=true")
		}
		if g.status != GoalStatusBlocked || g.stopCause != stopCauseBudgetSpend {
			t.Fatalf("interactive budget pause keeps resume semantics: status=%q stopCause=%q", g.status, g.stopCause)
		}
		if got := g.phase(); got != GoalPhaseBudgetLimited {
			t.Fatalf("interactive spend pause maps to budget_limited: got %q", got)
		}
	})
	t.Run("wall_clock", func(t *testing.T) {
		g := &goalMachine{goal: "ship it", status: GoalStatusRunning, autopilot: true, scopeID: newGoalScopeID()}
		g.setActivationLocked(true, goalActivationActorUser, "install")
		g.deadline = time.Now().Add(-time.Minute)
		epoch := g.continuationToken()
		res := g.advance(goalAdvanceInput{
			report:        contractContinueReport("keep going"),
			expectedEpoch: &epoch,
		})
		if res.cont || g.status != GoalStatusTimeLimitReached {
			t.Fatalf("wall-clock budget must stop terminal: cont=%v status=%q", res.cont, g.status)
		}
		if got := g.phase(); got != GoalPhaseBudgetLimited {
			t.Fatalf("wall-clock budget maps to budget_limited: got %q", got)
		}
		if res.cause != goalCauseTimeLimit {
			t.Fatalf("wall-clock cause: got %q", res.cause)
		}
	})
}

func TestContractPhaseMappingTable(t *testing.T) {
	cases := []struct {
		status    string
		stopCause string
		want      string
	}{
		{GoalStatusRunning, "", GoalPhaseActive},
		{GoalStatusBlocked, "", GoalPhaseBlocked},
		{GoalStatusBlocked, stopCauseManual, GoalPhasePaused},
		{GoalStatusBlocked, stopCauseBudgetSpend, GoalPhaseBudgetLimited},
		{GoalStatusBlocked, stopCauseEvaluator, GoalPhasePaused},
		{GoalStatusBlocked, stopCauseRecoveryPaused, GoalPhasePaused},
		{GoalStatusComplete, "", GoalPhaseComplete},
		{GoalStatusBudgetExhausted, "", GoalPhaseBudgetLimited},
		{GoalStatusTimeLimitReached, "", GoalPhaseBudgetLimited},
		{GoalStatusStopped, "", GoalPhaseBlocked},
		{GoalStatusFailed, "", GoalPhaseBlocked},
		{GoalStatusCancelled, "", GoalPhaseBlocked},
		{"", "", ""},
	}
	for _, tc := range cases {
		g := &goalMachine{goal: "g", status: tc.status, stopCause: tc.stopCause}
		if got := g.phaseLocked(); got != tc.want {
			t.Errorf("phase(%q, stopCause=%q) = %q, want %q", tc.status, tc.stopCause, got, tc.want)
		}
	}
}

func TestContractNoProgressDisarmsAfterN(t *testing.T) {
	g := &goalMachine{goal: "ship it", status: GoalStatusRunning, scopeID: newGoalScopeID()}
	g.setActivationLocked(true, goalActivationActorUser, "install")
	for i := 0; i < goalNoProgressDisarmTurns-1; i++ {
		epoch := g.continuationToken()
		res := g.advance(goalAdvanceInput{
			report:        contractContinueReport("no visible progress"),
			expectedEpoch: &epoch,
		})
		if !res.cont {
			t.Fatalf("turn %d below the N threshold must continue", i+1)
		}
		if g.activationValue() != GoalActivationArmed {
			t.Fatalf("turn %d must not disarm yet: %q", i+1, g.activationValue())
		}
	}
	epoch := g.continuationToken()
	res := g.advance(goalAdvanceInput{
		report:        contractContinueReport("still no progress"),
		expectedEpoch: &epoch,
	})
	if res.cont {
		t.Fatalf("连续 %d 轮无进展必须 disarm 停续（disarm 全集⑤）", goalNoProgressDisarmTurns)
	}
	if res.halted != "no_progress" {
		t.Fatalf("no-progress halt must be named: halted=%q", res.halted)
	}
	if g.activationValue() != GoalActivationDisarmed || g.phase() != GoalPhaseActive {
		t.Fatalf("no-progress disarm keeps the object: activation=%q phase=%q", g.activationValue(), g.phase())
	}
}

func TestContractTerminalTransitionsDisarm(t *testing.T) {
	t.Run("complete", func(t *testing.T) {
		g := &goalMachine{goal: "ship it", status: GoalStatusRunning, scopeID: newGoalScopeID()}
		g.setActivationLocked(true, goalActivationActorUser, "install")
		epoch := g.continuationToken()
		g.advance(goalAdvanceInput{
			report:        &goalTurnReport{status: GoalStatusComplete, reason: "done"},
			expectedEpoch: &epoch,
		})
		if g.activationValue() != GoalActivationDisarmed {
			t.Fatalf("completed goal must disarm（disarm 全集③）: %q", g.activationValue())
		}
	})
	t.Run("manual_pause_then_resume_re_arms", func(t *testing.T) {
		g := &goalMachine{goal: "ship it", status: GoalStatusRunning, scopeID: newGoalScopeID()}
		g.setActivationLocked(true, goalActivationActorUser, "install")
		g.pauseFor(stopCauseManual, "paused", nil)
		if g.activationValue() != GoalActivationDisarmed {
			t.Fatalf("paused goal must disarm: %q", g.activationValue())
		}
		if got := g.phase(); got != GoalPhasePaused {
			t.Fatalf("manual pause phase: got %q", got)
		}
		if _, _, _, resumed := g.resume(nil); !resumed {
			t.Fatalf("paused goal must resume")
		}
		if g.activationValue() != GoalActivationArmed {
			t.Fatalf("/goal resume must re-arm（人类直接请求）: %q", g.activationValue())
		}
	})
	t.Run("stop_disarms", func(t *testing.T) {
		g := &goalMachine{goal: "ship it", status: GoalStatusRunning, scopeID: newGoalScopeID()}
		g.setActivationLocked(true, goalActivationActorUser, "install")
		g.stop(GoalStatusStopped, goalCauseStopRequested, nil)
		if g.activationValue() != GoalActivationDisarmed {
			t.Fatalf("explicit stop must disarm（disarm 全集②）: %q", g.activationValue())
		}
	})
}

func TestContractHumanChannelSetRegressionZeroChange(t *testing.T) {
	// 人通道原语义回归钉：set → running+armed+续跑准入；同文重复 set 幂等；
	// clear → 清空。set 的返回值是「是否持久化」，不是「是否安装」。
	g := &goalMachine{}
	g.set("write the report", "", nil)
	if g.goalText() != "write the report" || g.status != GoalStatusRunning {
		t.Fatalf("human set semantics unchanged: goal=%q status=%q", g.goalText(), g.status)
	}
	if g.activationValue() != GoalActivationArmed || g.phase() != GoalPhaseActive {
		t.Fatalf("human set must install armed+active: %q/%q", g.activationValue(), g.phase())
	}
	epoch := g.continuationToken()
	res := g.advance(goalAdvanceInput{report: contractContinueReport("working"), expectedEpoch: &epoch})
	if !res.cont {
		t.Fatalf("human-set goal must auto-continue (原语义零变化)")
	}
	g.set("write the report", "", nil)
	if g.goalText() != "write the report" || g.status != GoalStatusRunning {
		t.Fatalf("identical set must be a no-op: goal=%q status=%q", g.goalText(), g.status)
	}
	g.set("", "", nil)
	if g.goalText() != "" || g.status != GoalStatusStopped {
		t.Fatalf("clear semantics unchanged: goal=%q status=%q", g.goalText(), g.status)
	}
	if g.activationValue() != GoalActivationDisarmed {
		t.Fatalf("cleared machine must read disarmed: %q", g.activationValue())
	}
}

func TestContractModelCreateChannelPins(t *testing.T) {
	t.Run("create_when_no_goal", func(t *testing.T) {
		g := &goalMachine{}
		accepted, refusal, _, _, _ := g.createByModel("refactor the parser", "refactor parser for speed", nil)
		if !accepted {
			t.Fatalf("model create with no existing goal must be accepted: %+v", refusal)
		}
		if g.status != GoalStatusRunning || g.phase() != GoalPhaseActive {
			t.Fatalf("model create installs active: status=%q phase=%q", g.status, g.phase())
		}
		if g.activationValue() != GoalActivationArmed {
			t.Fatalf("模型 create 即 armed: %q", g.activationValue())
		}
		if got := g.summaryText(); got != "refactor parser for speed" {
			t.Fatalf("explicit summary stored: %q", got)
		}
	})
	t.Run("refuse_when_live_goal_with_cause", func(t *testing.T) {
		g := &goalMachine{}
		g.set("user goal", "", nil)
		accepted, refusal, _, _, _ := g.createByModel("model goal", "model summary", nil)
		if accepted {
			t.Fatalf("占位锁：已有目标时模型立新必须拒绝")
		}
		if refusal.cause != "goal_occupied" {
			t.Fatalf("refusal must name the cause: %+v", refusal)
		}
		if !strings.Contains(refusal.detail, "user goal") || !strings.Contains(refusal.detail, "active") {
			t.Fatalf("refusal must be readable (existing goal + phase): %q", refusal.detail)
		}
		if g.goalText() != "user goal" {
			t.Fatalf("禁静默替换: goal=%q", g.goalText())
		}
	})
	t.Run("completed_goal_replaceable", func(t *testing.T) {
		g := &goalMachine{}
		g.set("old goal", "", nil)
		epoch := g.continuationToken()
		g.advance(goalAdvanceInput{
			report:        &goalTurnReport{status: GoalStatusComplete, reason: "done"},
			expectedEpoch: &epoch,
		})
		if g.phase() != GoalPhaseComplete {
			t.Fatalf("setup: expected complete, got %q", g.phase())
		}
		accepted, refusal, _, _, _ := g.createByModel("new goal", "new summary", nil)
		if !accepted {
			t.Fatalf("仅 completed 可替换：refusal=%+v", refusal)
		}
		if g.goalText() != "new goal" || g.phase() != GoalPhaseActive || g.activationValue() != GoalActivationArmed {
			t.Fatalf("replacement must install active+armed: goal=%q phase=%q activation=%q", g.goalText(), g.phase(), g.activationValue())
		}
	})
	t.Run("empty_text_refused", func(t *testing.T) {
		g := &goalMachine{}
		accepted, refusal, _, _, _ := g.createByModel("  ", "", nil)
		if accepted || refusal.cause != "empty_goal" {
			t.Fatalf("empty goal text must refuse: accepted=%v cause=%q", accepted, refusal.cause)
		}
	})
}

func TestContractBlockedReportMinTurns(t *testing.T) {
	g := &goalMachine{goal: "ship it", status: GoalStatusRunning, scopeID: newGoalScopeID()}
	g.setActivationLocked(true, goalActivationActorUser, "install")
	scope := g.scopeID
	epoch := g.continuationToken()
	recorder := g.newTurnRecorder(scope, epoch)
	_, err := recorder.RecordGoalReport(tool.GoalReport{Status: "blocked", Reason: "only the user can unblock"})
	if err == nil {
		t.Fatalf("turn 1 blocked must be refused (最小轮次门槛)")
	} else if !strings.Contains(err.Error(), "at least") {
		t.Fatalf("refusal must explain the threshold: %v", err)
	}
	// Simulate one completed goal turn: advance once with a continue report.
	if res := g.advance(goalAdvanceInput{report: contractContinueReport("working"), expectedEpoch: &epoch}); !res.cont {
		t.Fatalf("setup: first turn should continue")
	}
	// The advance moved the epoch, so the second turn binds a fresh recorder.
	recorder2 := g.newTurnRecorder(scope, g.continuationToken())
	if _, err := recorder2.RecordGoalReport(tool.GoalReport{Status: "blocked", Reason: "only the user can unblock"}); err != nil {
		t.Fatalf("turn 2 blocked must be accepted: %v", err)
	}
}
