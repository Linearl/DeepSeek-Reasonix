package control

import (
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/evidence"
	"reasonix/internal/goaleval"
	"reasonix/internal/provider"
	"reasonix/internal/tool"

	_ "reasonix/internal/tool/builtin"
)

// --- 纯函数层（切片1）---

func TestStopReviewContinuationRulesTable(t *testing.T) {
	t.Run("every layer-1 rule fires on its own fact", func(t *testing.T) {
		cases := []struct {
			name string
			f    stopReviewFacts
			want string
		}{
			{"todos_pending", stopReviewFacts{TodosPending: []evidence.TodoStepMatch{{Content: "x", Status: "pending"}}}, "todos_pending"},
			{"readiness_gap", stopReviewFacts{ReadinessMissing: []string{"verification"}}, "readiness_gap"},
			{"plan_steps_pending", stopReviewFacts{PlanStepsPending: []evidence.TodoStepMatch{{Content: "step", Status: "pending", StepID: "plan_step_01"}}}, "plan_steps_pending"},
			{"goal_in_pursuit", stopReviewFacts{GoalInPursuit: true}, "goal_in_pursuit"},
		}
		for _, tc := range cases {
			rules := stopReviewContinuationRules(tc.f)
			if len(rules) != 1 || rules[0].Name != tc.want {
				t.Fatalf("%s: rules = %v, want exactly [%s]", tc.name, stopReviewRuleNames(rules), tc.want)
			}
		}
	})

	t.Run("empty facts release the stop", func(t *testing.T) {
		if rules := stopReviewContinuationRules(stopReviewFacts{}); len(rules) != 0 {
			t.Fatalf("empty facts produced rules %v, want release", stopReviewRuleNames(rules))
		}
	})

	t.Run("rules stack when several facts hold", func(t *testing.T) {
		f := stopReviewFacts{
			TodosPending:     []evidence.TodoStepMatch{{Content: "x", Status: "pending"}},
			ReadinessMissing: []string{"review"},
			GoalInPursuit:    true,
		}
		rules := stopReviewContinuationRules(f)
		if len(rules) != 3 {
			t.Fatalf("stacked facts produced %d rules (%v), want 3", len(rules), stopReviewRuleNames(rules))
		}
	})
}

// TestStopReviewOneWayIronLaw pins the structural form of the one-way law: the
// review's decision surface is (continue rules | release) and nothing else —
// no input combination can produce a stop, and the layer-② progress evidence
// never feeds a rule (it is a release-side trace fact only).
func TestStopReviewOneWayIronLaw(t *testing.T) {
	todoSets := [][]evidence.TodoStepMatch{
		nil,
		{{Content: "a", Status: "pending"}},
		{{Content: "a", Status: "in_progress"}},
	}
	missingSets := [][]string{nil, {"verification"}, {"project_check", "signoff"}}
	for _, todos := range todoSets {
		for _, missing := range missingSets {
			for _, plan := range []bool{false, true} {
				for _, goal := range []bool{false, true} {
					for _, output := range []int{0, 3, 42} {
						f := stopReviewFacts{
							TodosPending:         todos,
							ReadinessMissing:     missing,
							GoalInPursuit:        goal,
							TurnOutputSignatures: output,
						}
						if plan {
							f.PlanStepsPending = stopReviewPlanStepsOf(f.TodosPending)
						}
						rules := stopReviewContinuationRules(f)
						for _, r := range rules {
							if strings.Contains(strings.ToLower(r.Name), "stop") {
								t.Fatalf("iron law violated: rule %q reads as a stop verdict", r.Name)
							}
						}
						// The rule set must be independent of the output fact:
						// layer ② releases, it never adds or removes a rule.
						fNoOutput := f
						fNoOutput.TurnOutputSignatures = 0
						a := stopReviewRuleNames(stopReviewContinuationRules(f))
						b := stopReviewRuleNames(stopReviewContinuationRules(fNoOutput))
						if strings.Join(a, ",") != strings.Join(b, ",") {
							t.Fatalf("output evidence changed the verdict: %v vs %v", a, b)
						}
					}
				}
			}
		}
	}
}

// TestStopReviewIdleProtection pins layer ②'s release side: zero output and
// no pending anything releases the stop, and output alone never holds one.
func TestStopReviewIdleProtection(t *testing.T) {
	idle := stopReviewFacts{}
	if rules := stopReviewContinuationRules(idle); len(rules) != 0 {
		t.Fatalf("idle stop was held by %v", stopReviewRuleNames(rules))
	}
	productive := stopReviewFacts{TurnOutputSignatures: 7}
	if rules := stopReviewContinuationRules(productive); len(rules) != 0 {
		t.Fatalf("output alone held the stop: %v", stopReviewRuleNames(rules))
	}
}

func TestStopReviewPlanStepsOf(t *testing.T) {
	// The caller passes evidence.IncompleteTodos's output — every row is
	// unfinished by construction — so the filter matches on the plan_step_
	// StepID identity alone; status is the upstream guarantee, not this
	// function's job.
	todos := []evidence.TodoStepMatch{
		{Content: "phase", Status: "pending"},
		{Content: "seeded", Status: "pending", StepID: "plan_step_02"},
	}
	steps := stopReviewPlanStepsOf(todos)
	if len(steps) != 1 || steps[0].StepID != "plan_step_02" {
		t.Fatalf("plan step filter = %+v, want only the seeded step", steps)
	}
}

func TestStopReviewOptionsNormalize(t *testing.T) {
	zero := normalizeStopReviewOptions(StopReviewOptions{})
	if zero.Disabled {
		t.Fatal("zero options must keep the review enabled (缺陷补齐默认开)")
	}
	if zero.MaxContinuations != DefaultStopReviewMaxContinuations {
		t.Fatalf("zero options ceiling = %d, want default %d", zero.MaxContinuations, DefaultStopReviewMaxContinuations)
	}
	custom := normalizeStopReviewOptions(StopReviewOptions{Disabled: true, MaxContinuations: 2})
	if !custom.Disabled || custom.MaxContinuations != 2 {
		t.Fatalf("explicit options mangled: %+v", custom)
	}
	negative := normalizeStopReviewOptions(StopReviewOptions{MaxContinuations: -3})
	if negative.MaxContinuations != DefaultStopReviewMaxContinuations {
		t.Fatalf("negative ceiling = %d, want default", negative.MaxContinuations)
	}
}

// --- 运行时半区（切片2：非 Goal 会话生效）---

// stopReviewTestHarness wires the shared scripted-provider shape the goal
// tests use, plus notice collection keyed by Code.
type stopReviewTestHarness struct {
	prov    *scriptedTurns
	c       *Controller
	notices []event.Event
	done    chan event.Event
}

func newStopReviewHarness(t *testing.T, turns [][]provider.Chunk, seedTodos []evidence.TodoItem, opts StopReviewOptions) *stopReviewTestHarness {
	t.Helper()
	todoWrite, ok := tool.LookupBuiltin("todo_write")
	if !ok {
		t.Fatal("todo_write builtin not registered")
	}
	reg := tool.NewRegistry()
	reg.Add(todoWrite)
	prov := &scriptedTurns{turns: turns}
	ag := agent.New(prov, reg, agent.NewSession(""), agent.Options{}, event.Discard)
	if len(seedTodos) > 0 {
		ag.SeedTodoState(seedTodos)
	}
	h := &stopReviewTestHarness{prov: prov, done: make(chan event.Event, 1)}
	c := New(Options{
		Runner:     ag,
		Executor:   ag,
		StopReview: opts,
		Sink: event.FuncSink(func(e event.Event) {
			switch e.Kind {
			case event.Notice:
				h.notices = append(h.notices, e)
			case event.TurnDone:
				select {
				case h.done <- e:
				default:
				}
			}
		}),
	})
	h.c = c
	return h
}

func (h *stopReviewTestHarness) noticeByCode(code string) *event.Event {
	for i := range h.notices {
		if h.notices[i].Code == code {
			return &h.notices[i]
		}
	}
	return nil
}

// TestStopReviewNonGoalPendingTodosContinues is the acceptance anchor 非 Goal
// 会话有待办未完成时轮末不静默停: a plain session whose turn ends with a
// pending canonical todo gets exactly one visible review-driven continuation,
// and once the continuation clears the list the session stops cleanly.
func TestStopReviewNonGoalPendingTodosContinues(t *testing.T) {
	h := newStopReviewHarness(t, flattenTurns(
		[][]provider.Chunk{textTurn("started the report but it is not done.")},
		[][]provider.Chunk{
			{toolCallChunk("t1", "todo_write", `{"todos":[{"content":"write the report","status":"completed"}]}`), {Type: provider.ChunkDone}},
			textTurn("finished the report."),
		},
	), []evidence.TodoItem{{Content: "write the report", Status: "in_progress"}}, StopReviewOptions{})

	h.c.Submit("write the report")
	<-h.done

	// Provider-call accounting follows the goal tests' convention: every
	// scripted element is one provider invocation, and a tool-call round plus
	// its text round are two. Original turn (text only) + continuation turn
	// (todo_write call + text) = 3.
	if h.prov.call != 3 {
		t.Fatalf("provider calls = %d, want 3 (original turn + continuation's tool round and text round)", h.prov.call)
	}
	n := h.noticeByCode(event.NoticeCodeStopReviewContinued)
	if n == nil {
		t.Fatalf("missing %s notice; notices = %+v", event.NoticeCodeStopReviewContinued, h.notices)
	}
	if !strings.Contains(n.Text, "1/") || !strings.Contains(n.Text, "canonical todo list") {
		t.Fatalf("continued notice should name the reason and the N/M counter: %q (detail %q)", n.Text, n.Detail)
	}
	if !strings.Contains(n.Detail, "rules=todos_pending") {
		t.Fatalf("continued notice detail should carry rules=, got %q", n.Detail)
	}
	if h.c.stopReviewStreak != 0 {
		t.Fatalf("streak = %d after a clean release, want 0", h.c.stopReviewStreak)
	}
	for _, td := range h.c.executor.CanonicalTodoState() {
		if td.Status != "completed" {
			t.Fatalf("todo %q = %s after the continuation, want completed", td.Content, td.Status)
		}
	}
}

// TestStopReviewCappedStopsWithReport pins 到顶转停+报告: with a ceiling of 1
// and todos that never clear, the review continues once, then stops with the
// capped notice carrying the pending list — and never spins.
func TestStopReviewCappedStopsWithReport(t *testing.T) {
	h := newStopReviewHarness(t, flattenTurns(
		[][]provider.Chunk{textTurn("still working on it.")},
		[][]provider.Chunk{textTurn("still working on it again.")},
	), []evidence.TodoItem{{Content: "write the report", Status: "pending"}}, StopReviewOptions{MaxContinuations: 1})

	h.c.Submit("write the report")
	<-h.done

	if h.prov.call != 2 {
		t.Fatalf("provider calls = %d, want 2 (original + one continuation, then capped)", h.prov.call)
	}
	n := h.noticeByCode(event.NoticeCodeStopReviewCapped)
	if n == nil {
		t.Fatalf("missing %s notice; notices = %+v", event.NoticeCodeStopReviewCapped, h.notices)
	}
	if !strings.Contains(n.Text, "1/1") || !strings.Contains(n.Text, "write the report") {
		t.Fatalf("capped notice should carry the ceiling and the pending list: %q", n.Text)
	}
	if !strings.Contains(n.Detail, "cause=stop_review") || !strings.Contains(n.Detail, "continuation=1/1") {
		t.Fatalf("capped notice detail should carry cause and budget: %q", n.Detail)
	}
	if h.c.stopReviewStreak != 0 {
		t.Fatalf("streak = %d after the capped stop, want 0", h.c.stopReviewStreak)
	}
}

// TestStopReviewDisabledKeepsOldBehavior is the master-switch control pin: the
// layer off means the old free-stop behavior byte-for-byte — one turn, no
// stop_review notices, pending todos just sit there.
func TestStopReviewDisabledKeepsOldBehavior(t *testing.T) {
	h := newStopReviewHarness(t,
		[][]provider.Chunk{textTurn("answered.")},
		[]evidence.TodoItem{{Content: "write the report", Status: "pending"}},
		StopReviewOptions{Disabled: true},
	)

	h.c.Submit("write the report")
	<-h.done

	if h.prov.call != 1 {
		t.Fatalf("provider calls = %d, want 1 with the review disabled", h.prov.call)
	}
	if n := h.noticeByCode(event.NoticeCodeStopReviewContinued); n != nil {
		t.Fatalf("disabled review emitted %s: %q", event.NoticeCodeStopReviewContinued, n.Text)
	}
	if n := h.noticeByCode(event.NoticeCodeStopReviewCapped); n != nil {
		t.Fatalf("disabled review emitted %s: %q", event.NoticeCodeStopReviewCapped, n.Text)
	}
}

// TestStopReviewReleasesCleanCompletion is the 不误停对照钉: a session that
// ends with everything cleared stops on its own — no continuation, no notice.
func TestStopReviewReleasesCleanCompletion(t *testing.T) {
	h := newStopReviewHarness(t,
		[][]provider.Chunk{
			{toolCallChunk("t1", "todo_write", `{"todos":[{"content":"write the report","status":"completed"}]}`), {Type: provider.ChunkDone}},
			textTurn("done."),
		},
		[]evidence.TodoItem{{Content: "write the report", Status: "in_progress"}},
		StopReviewOptions{},
	)

	h.c.Submit("write the report")
	<-h.done

	// The clearing turn is one tool round plus its text round.
	if h.prov.call != 2 {
		t.Fatalf("provider calls = %d, want 2 for a clean stop", h.prov.call)
	}
	if n := h.noticeByCode(event.NoticeCodeStopReviewContinued); n != nil {
		t.Fatalf("clean stop got a continuation notice: %q", n.Text)
	}
	if n := h.noticeByCode(event.NoticeCodeStopReviewCapped); n != nil {
		t.Fatalf("clean stop got a capped notice: %q", n.Text)
	}
}

// TestStopReviewStreakResetsOnUserTurn pins the segment boundary: a real user
// turn always opens a fresh continuation budget.
func TestStopReviewStreakResetsOnUserTurn(t *testing.T) {
	h := newStopReviewHarness(t,
		[][]provider.Chunk{textTurn("answered.")},
		nil,
		StopReviewOptions{},
	)
	h.c.mu.Lock()
	h.c.stopReviewStreak = 3
	h.c.mu.Unlock()

	h.c.Submit("hello")
	<-h.done

	// The reset runs inside the orchestrated turn, strictly before TurnDone is
	// emitted, so the receive above orders this read after it.
	if h.c.stopReviewStreak != 0 {
		t.Fatalf("streak = %d after a real user turn, want 0", h.c.stopReviewStreak)
	}
}

// --- Goal 衔接（切片3：③门与判定下沉）---

// TestStopReviewGoalEvaluatorSkippedWhenRulesContinue pins 判定层③'s gate: a
// goal turn whose rules said CONTINUE (todos pending, no update_goal report)
// must not consult the LLM evaluator at all — 规则说继续就禁止 LLM 否决（单向
// fail-open）. The goal still completes through the report path once the model
// actually clears the todos, so the four kept goal behaviors stay intact.
func TestStopReviewGoalEvaluatorSkippedWhenRulesContinue(t *testing.T) {
	todoWrite, ok := tool.LookupBuiltin("todo_write")
	if !ok {
		t.Fatal("todo_write builtin not registered")
	}
	reg := goalRegistry()
	reg.Add(todoWrite)
	eval := &fakeGoalEvaluator{outcome: goaleval.OutcomeComplete, reason: "would wrongly complete a goal with pending todos"}
	prov := &scriptedTurns{turns: flattenTurns(
		[][]provider.Chunk{textTurn("progress, but the todo is still open.")},
		[][]provider.Chunk{
			{toolCallChunk("t1", "todo_write", `{"todos":[{"content":"ship the fix","status":"completed"}]}`), {Type: provider.ChunkDone}},
			{toolCallChunk("ug1", "update_goal", `{"status":"complete","reason":""}`), {Type: provider.ChunkDone}},
			textTurn("all done."),
		},
	)}
	ag := agent.New(prov, reg, agent.NewSession(""), agent.Options{}, event.Discard)
	ag.SeedTodoState([]evidence.TodoItem{{Content: "ship the fix", Status: "in_progress"}})

	done := make(chan event.Event, 1)
	c := New(Options{
		Runner:        ag,
		Executor:      ag,
		GoalEvaluator: eval,
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.TurnDone {
				select {
				case done <- e:
				default:
				}
			}
		}),
	})

	c.Submit("/goal ship the fix")
	<-done

	// 判定层③: the first goal turn ended with todos pending and no report —
	// the rules said continue, so the evaluator must never have been consulted
	// (pre-1460 this shape ran the evaluator on every such turn).
	if eval.calls != 0 {
		t.Fatalf("evaluator calls = %d, want 0 while the rules said continue", eval.calls)
	}
	// The goal loop still ran (the sunk 继续判定 via goal_in_pursuit): original
	// turn + continuation's three scripted rounds.
	if prov.call != 4 {
		t.Fatalf("provider calls = %d, want 4 (goal turn + continuation's todo_write/report/text)", prov.call)
	}
	if got := c.GoalStatus(); got != GoalStatusComplete {
		t.Fatalf("GoalStatus() = %q, want complete via the report path", got)
	}
}
