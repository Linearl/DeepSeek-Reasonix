package control

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/goaleval"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1459 stage 2 pins: a Goal never leaves running in silence.

type goalTransitionCase struct {
	name     string
	setup    func(g *goalMachine)
	input    goalAdvanceInput
	wantCode string
}

func runningGoalMachine() *goalMachine {
	return &goalMachine{goal: "ship the release", status: GoalStatusRunning, scopeID: "goal-test", turnsLimit: unlimitedGoalTurns}
}

// goalFSMTransitionCases enumerates every advance() branch that leaves running.
// A new terminal branch must get a case here; the AST guard below fails first
// if the branch forgets its cause.
func goalFSMTransitionCases() []goalTransitionCase {
	return []goalTransitionCase{
		{name: "autopilot time limit", setup: func(g *goalMachine) {
			g.autopilot, g.deadline = true, time.Now().Add(-time.Minute)
		}, wantCode: event.NoticeCodeGoalStopped},
		{name: "report blocked", input: goalAdvanceInput{report: &goalTurnReport{status: GoalStatusBlocked, reason: "needs credentials"}}, wantCode: event.NoticeCodeGoalBlocked},
		{name: "evaluator blocked", input: goalAdvanceInput{evaluator: &goalEvaluatorVerdict{outcome: goaleval.OutcomeBlocked, reason: "waiting on review"}}, wantCode: event.NoticeCodeGoalBlocked},
		{name: "report complete", input: goalAdvanceInput{report: &goalTurnReport{status: GoalStatusComplete}}, wantCode: event.NoticeCodeGoalComplete},
		{name: "token budget interactive", setup: func(g *goalMachine) { g.tokensLimit, g.tokensUsed = 10, 10 }, wantCode: event.NoticeCodeGoalPaused},
		{name: "token budget autopilot", setup: func(g *goalMachine) {
			g.tokensLimit, g.tokensUsed, g.autopilot = 10, 10, true
		}, wantCode: event.NoticeCodeGoalStopped},
		{name: "evaluator failed interactive", input: goalAdvanceInput{evaluatorFailed: "timeout"}, wantCode: event.NoticeCodeGoalPaused},
		{name: "evaluator failed autopilot exhausted", setup: func(g *goalMachine) {
			g.autopilot, g.evaluatorFailTurns = true, maxAutopilotEvaluatorFails
		}, input: goalAdvanceInput{evaluatorFailed: "timeout"}, wantCode: event.NoticeCodeGoalStopped},
		{name: "pause cause interactive", input: goalAdvanceInput{pauseCause: stopCauseBudgetSpend, pauseReason: "spend boundary"}, wantCode: event.NoticeCodeGoalPaused},
		{name: "pause cause autopilot recovery", setup: func(g *goalMachine) { g.autopilot = true },
			input: goalAdvanceInput{pauseCause: stopCauseRecoveryPaused, pauseReason: "recovery paused"}, wantCode: event.NoticeCodeGoalStopped},
		{name: "repeated check-only gap completes partial", setup: func(g *goalMachine) {
			g.progressEvidence, g.noProgressTurns = []string{"sig"}, 2
			g.lastContinuationReason = clipGoalReason("readiness missing: checks pending")
		}, input: goalAdvanceInput{readiness: agent.ReadinessResult{Missing: []string{"verification"}, Reason: "checks pending"}}, wantCode: event.NoticeCodeGoalComplete},
	}
}

func TestGoalStatusTransitionsDeclareACause(t *testing.T) {
	for _, tc := range goalFSMTransitionCases() {
		t.Run(tc.name, func(t *testing.T) {
			g := runningGoalMachine()
			if tc.setup != nil {
				tc.setup(g)
			}
			res := g.advance(tc.input)
			if g.status == GoalStatusRunning {
				t.Fatalf("case did not leave running (status %q) — fix the table setup", g.status)
			}
			if res.cause == "" || res.notice == "" || res.cont {
				t.Fatalf("left running without cause/notice: cause=%q notice=%q cont=%v", res.cause, res.notice, res.cont)
			}
			if g.lastStopCause != res.cause {
				t.Fatalf("lastStopCause = %q, want %q", g.lastStopCause, res.cause)
			}
			if code, _ := goalNoticeCodeFor(g.status, g.stopCause); code != tc.wantCode {
				t.Fatalf("notice code = %q, want %q", code, tc.wantCode)
			}
		})
	}
	// A continue step keeps running and must not invent a cause.
	g := runningGoalMachine()
	res := g.advance(goalAdvanceInput{report: &goalTurnReport{status: GoalStatusRunning, reason: "more", nextAction: "next"}})
	if !res.cont || res.cause != "" || g.lastStopCause != "" {
		t.Fatalf("continue step: cont=%v cause=%q lastStopCause=%q", res.cont, res.cause, g.lastStopCause)
	}
}

// TestGoalStatusTransitionsDeclareACauseASTGuard is the "new branch without a
// cause is red" half: every assignment of a non-running GoalStatus constant in
// the Goal machine must be paired with a cause in the same switch case, or a
// lastStopCause write in the same function outside a switch.
func TestGoalStatusTransitionsDeclareACauseASTGuard(t *testing.T) {
	files, err := filepath.Glob("goal*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var offenders []string
	checked := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			fnHasLastStop := assignsTo(fn.Body, "lastStopCause")
			inCase := map[ast.Node]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				clause, ok := n.(*ast.CaseClause)
				if !ok {
					return true
				}
				for _, pos := range nonRunningStatusAssignments(clause) {
					inCase[pos] = true
					checked++
					if !assignsTo(clause, "cause") && !assignsTo(clause, "lastStopCause") {
						offenders = append(offenders, fset.Position(pos.Pos()).String()+" (switch case without cause)")
					}
				}
				return true
			})
			for _, pos := range nonRunningStatusAssignments(fn.Body) {
				if inCase[pos] {
					continue
				}
				checked++
				if !fnHasLastStop {
					offenders = append(offenders, fset.Position(pos.Pos()).String()+" ("+fn.Name.Name+" sets a non-running status without lastStopCause)")
				}
			}
		}
	}
	if checked < 15 {
		t.Fatalf("guard inspected only %d status assignments; the scan is broken", checked)
	}
	if len(offenders) > 0 {
		t.Fatalf("Goal status left running without a declared cause:\n%s", strings.Join(offenders, "\n"))
	}
}

// nonRunningStatusAssignments returns `g.status = GoalStatusX` (X != Running)
// assignments under n, including multi-assignments.
func nonRunningStatusAssignments(n ast.Node) []ast.Node {
	var out []ast.Node
	ast.Inspect(n, func(node ast.Node) bool {
		as, ok := node.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != len(as.Rhs) {
			return true
		}
		for i, lhs := range as.Lhs {
			sel, ok := lhs.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "status" {
				continue
			}
			if id, ok := as.Rhs[i].(*ast.Ident); ok && strings.HasPrefix(id.Name, "GoalStatus") && id.Name != "GoalStatusRunning" {
				out = append(out, as)
			}
		}
		return true
	})
	return out
}

func assignsTo(n ast.Node, name string) bool {
	found := false
	ast.Inspect(n, func(node ast.Node) bool {
		as, ok := node.(*ast.AssignStmt)
		if !ok {
			return !found
		}
		for _, lhs := range as.Lhs {
			switch v := lhs.(type) {
			case *ast.Ident:
				if v.Name == name {
					found = true
				}
			case *ast.SelectorExpr:
				if v.Sel.Name == name {
					found = true
				}
			}
		}
		return !found
	})
	return found
}

type goalNoticeRecorder struct {
	mu      sync.Mutex
	notices []event.Event
}

func (r *goalNoticeRecorder) sink() event.Sink {
	return event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice && strings.HasPrefix(e.Code, "goal_") {
			r.mu.Lock()
			r.notices = append(r.notices, e)
			r.mu.Unlock()
		}
	})
}

func (r *goalNoticeRecorder) codes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.notices))
	for _, e := range r.notices {
		out = append(out, e.Code)
	}
	return out
}

func (r *goalNoticeRecorder) last(t *testing.T) event.Event {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.notices) == 0 {
		t.Fatal("no goal_* notice emitted")
	}
	return r.notices[len(r.notices)-1]
}

func goalNoticeController(t *testing.T) (*Controller, *goalNoticeRecorder) {
	t.Helper()
	rec := &goalNoticeRecorder{}
	ag := agent.New(nil, nil, agent.NewSession(""), agent.Options{}, event.Discard)
	c := New(Options{Executor: ag, Sink: rec.sink()})
	c.SetGoal("ship the release")
	return c, rec
}

func TestGoalHostStopsEmitCausedNotices(t *testing.T) {
	for _, cause := range []string{goalCauseDurability, goalCauseStopRequested, goalCauseCancelled} {
		t.Run(cause, func(t *testing.T) {
			c, rec := goalNoticeController(t)
			c.stopGoal(GoalStatusStopped, cause, "checkpoint tool transcript: conflict")
			e := rec.last(t)
			if e.Code != event.NoticeCodeGoalStopped || !strings.Contains(e.Detail, "cause="+cause) || e.Text == "" {
				t.Fatalf("notice = code %q detail %q text %q", e.Code, e.Detail, e.Text)
			}
			if got := c.GoalRuntime().LastStopCause; got != cause {
				t.Fatalf("LastStopCause = %q, want %q", got, cause)
			}
			// A second stop on a stopped Goal is not a transition: no notice.
			before := len(rec.codes())
			c.stopGoal(GoalStatusStopped, cause, "")
			if after := len(rec.codes()); after != before {
				t.Fatalf("stopping a stopped Goal emitted %d extra notices", after-before)
			}
		})
	}
	t.Run("clear", func(t *testing.T) {
		c, rec := goalNoticeController(t)
		c.SetGoal("")
		if e := rec.last(t); e.Code != event.NoticeCodeGoalCleared || !strings.Contains(e.Detail, "cause="+goalCauseCleared) {
			t.Fatalf("clear notice = %+v", e)
		}
		if got := c.GoalRuntime().LastStopCause; got != goalCauseCleared {
			t.Fatalf("LastStopCause after clear = %q", got)
		}
	})
	t.Run("pause", func(t *testing.T) {
		c, rec := goalNoticeController(t)
		c.PauseGoal()
		if e := rec.last(t); e.Code != event.NoticeCodeGoalPaused || !strings.Contains(e.Detail, "cause="+stopCauseManual) {
			t.Fatalf("pause notice = %+v", e)
		}
	})
	t.Run("resume clears the cause", func(t *testing.T) {
		c, _ := goalNoticeController(t)
		c.PauseGoal()
		c.ResumeGoal()
		if got := c.GoalRuntime().LastStopCause; got != "" {
			t.Fatalf("LastStopCause after resume = %q, want empty", got)
		}
	})
}

func TestGoalAdvanceNoticeCarriesCodeAndCause(t *testing.T) {
	rec := &goalNoticeRecorder{}
	ag := agent.New(nil, nil, agent.NewSession(""), agent.Options{}, event.Discard)
	c := New(Options{Executor: ag, Sink: rec.sink()})
	c.SetGoal("ship the release")
	// tl1472 blocked 最小轮次门槛：先走完一轮（第 1 轮），blocked 在第 2 轮申报。
	c.goals.advance(goalAdvanceInput{report: &goalTurnReport{status: GoalStatusRunning, reason: "work"}})
	scope, _, _ := c.goals.deliveryScope()
	recorder := c.goals.newTurnRecorder(scope, c.goals.continuationToken())
	c.goalUsageTee.setActiveRecorder(recorder)
	if _, err := recorder.RecordGoalReport(tool.GoalReport{Status: "blocked", Reason: "needs credentials"}); err != nil {
		t.Fatal(err)
	}
	newTurnOrchestrator(c).advanceGoalAfterTurn(context.Background(), recorder.epoch, nil)
	e := rec.last(t)
	if e.Code != event.NoticeCodeGoalBlocked || !strings.Contains(e.Detail, "cause="+goalCauseReportBlocked) || !strings.HasPrefix(e.Text, "goal blocked: needs credentials") {
		t.Fatalf("advance notice = code %q detail %q text %q", e.Code, e.Detail, e.Text)
	}
}

func TestUpdateGoalRejectionNamesTheReason(t *testing.T) {
	cases := []struct {
		name  string
		after func(c *Controller)
		want  string
	}{
		{"stopped", func(c *Controller) { c.stopGoal(GoalStatusStopped, goalCauseDurability, "") }, "the goal is stopped (cause: durability_failure)"},
		{"cleared", func(c *Controller) { c.SetGoal("") }, "the goal is no longer set (the goal was cleared)"},
		{"replaced", func(c *Controller) { c.SetGoal("a different goal") }, "replaced by a new goal"},
		{"moved on", func(c *Controller) {
			c.goals.advance(goalAdvanceInput{report: &goalTurnReport{status: GoalStatusRunning, reason: "more", nextAction: "x"}})
		}, "the goal moved on during this turn"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := goalNoticeController(t)
			scope, _, _ := c.goals.deliveryScope()
			recorder := c.goals.newTurnRecorder(scope, c.goals.continuationToken())
			tc.after(c)
			_, err := recorder.RecordGoalReport(tool.GoalReport{Status: "complete"})
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "no goal state was changed") {
				t.Fatalf("rejection = %v, want it to name %q", err, tc.want)
			}
		})
	}
}

func TestNonGoalTurnTellsUpdateGoalWhy(t *testing.T) {
	updateGoal, ok := tool.LookupBuiltin("update_goal")
	if !ok {
		t.Fatal("update_goal builtin missing")
	}
	args := json.RawMessage(`{"status":"complete"}`)

	plain := New(Options{Executor: agent.New(nil, nil, agent.NewSession(""), agent.Options{}, event.Discard), Sink: event.Discard})
	ctx := plain.bindTurnScope(context.Background(), nil)
	if reason := tool.GoalTurnUnavailableReason(ctx); reason != "" {
		t.Fatalf("plain chat carries a goal reason %q", reason)
	}

	c, _ := goalNoticeController(t)
	c.stopGoal(GoalStatusStopped, goalCauseDurability, "checkpoint conflict")
	ctx = c.bindTurnScope(context.Background(), nil)
	_, err := updateGoal.Execute(ctx, args)
	if err == nil || !strings.Contains(err.Error(), "only available while an active goal turn") ||
		!strings.Contains(err.Error(), "cause: durability_failure") || !strings.Contains(err.Error(), "resume or restart") {
		t.Fatalf("update_goal refusal = %v", err)
	}
}

// TestDurabilityCancelStopsGoalVisibly replays one incident round end to end:
// the durable sink fails mid-turn, the turn is cancelled, the Goal stops — and
// now the user gets a goal_stopped notice naming durability_failure, and the
// model's next update_goal refusal says why.
func TestDurabilityCancelStopsGoalVisibly(t *testing.T) {
	var c *Controller
	reg := goalRegistry()
	reg.Add(poisonLedgerTool{c: &c})
	prov := &scriptedTurns{turns: [][]provider.Chunk{
		{toolCallChunk("p1", "poison_ledger", `{}`), {Type: provider.ChunkDone}},
		textTurn("unreachable"),
	}}
	ag := agent.New(prov, reg, agent.NewSession(""), agent.Options{}, event.Discard)
	rec := &goalNoticeRecorder{}
	done := make(chan struct{}, 4)
	c = New(Options{
		Runner:   ag,
		Executor: ag,
		Sink: event.FuncSink(func(e event.Event) {
			rec.sink().Emit(e)
			if e.Kind == event.TurnDone {
				done <- struct{}{}
			}
		}),
	})
	c.Submit("/goal finish the constitution")
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("goal turn never finished")
	}
	if got := c.GoalStatus(); got != GoalStatusStopped {
		t.Fatalf("GoalStatus = %q, want stopped", got)
	}
	e := rec.last(t)
	if e.Code != event.NoticeCodeGoalStopped || !strings.Contains(e.Detail, "cause="+goalCauseDurability) || !strings.Contains(e.Text, "durable save failed") {
		t.Fatalf("incident-shape notice = code %q detail %q text %q", e.Code, e.Detail, e.Text)
	}
	t.Logf("incident-shape notice: code=%s text=%q detail=%q", e.Code, e.Text, e.Detail)
}
