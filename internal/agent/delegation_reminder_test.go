package agent

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// Task 850 (816 P1-②): the efficiency tier's mid-turn delegation reminder.
// The turn-opening guidance fades across a long turn (816 R1), so the host
// re-tops it ONCE when the turn is long, zero-dispatch, and explicitly on
// the efficiency tier — never on economy/balanced/unset (the behavioral
// gradient between the tiers must be host-visible, not just prose).

type delegationNoticeSink struct {
	notices []event.Event
}

func (s *delegationNoticeSink) Emit(e event.Event) {
	if e.Kind == event.Notice {
		s.notices = append(s.notices, e)
	}
}

func newDelegationReminderAgent(t *testing.T, sink *delegationNoticeSink, tier SubagentPolicy) *Agent {
	t.Helper()
	reg := tool.NewRegistry()
	var calls int32
	reg.Add(fakeTool{name: "bash", calls: &calls})
	a := New(nil, reg, NewSession(""), Options{}, sink)
	if tier != "" {
		a.SetSubagentPolicyTier(tier)
	}
	return a
}

func runDelegationReminderRound(t *testing.T, a *Agent, batch []provider.ToolCall) {
	t.Helper()
	// beginRunTurn owns this initialization in a real turn; the harness
	// builds the minimal per-turn state by hand.
	if a.turn.seenTodoProgress == nil {
		a.turn.seenTodoProgress = make(map[string]struct{})
	}
	if _, err := a.handleToolRound(context.Background(), &a.turn, 0, "", "", batch, nil); err != nil {
		t.Fatalf("handleToolRound errored: %v", err)
	}
}

func delegationReminderNotices(sink *delegationNoticeSink) []event.Event {
	var out []event.Event
	for _, e := range sink.notices {
		if e.Code == event.NoticeCodeDelegationReminder {
			out = append(out, e)
		}
	}
	return out
}

func conversationDelegationReminders(a *Agent) int {
	n := 0
	for _, m := range a.sess.conversation.Snapshot() {
		if m.Origin == provider.MessageOriginHost && strings.Contains(m.Content, "delegation reminder") {
			n++
		}
	}
	return n
}

func longInlineBatch(n int) []provider.ToolCall {
	batch := make([]provider.ToolCall, n)
	for i := range batch {
		batch[i] = provider.ToolCall{Name: "bash"}
	}
	return batch
}

// TestDelegationReminderFiresOnceForEfficiencyLongTurn: the efficiency tier
// past the step threshold with zero dispatches gets exactly one transient
// reminder — visible as a notice and appended once at the context tail. A
// second long round in the SAME turn must not stack another one.
func TestDelegationReminderFiresOnceForEfficiencyLongTurn(t *testing.T) {
	sink := &delegationNoticeSink{}
	a := newDelegationReminderAgent(t, sink, SubagentPolicyAggressive)

	runDelegationReminderRound(t, a, longInlineBatch(delegationReminderMinSteps))

	if got := len(delegationReminderNotices(sink)); got != 1 {
		t.Fatalf("delegation reminder notices = %d, want exactly one", got)
	}
	if got := conversationDelegationReminders(a); got != 1 {
		t.Fatalf("reminder messages in conversation = %d, want exactly one", got)
	}
	if !a.turn.delegationReminderSent {
		t.Fatal("per-turn flag must latch after the reminder fires")
	}

	// Same turn again: the latch holds, no second nag.
	runDelegationReminderRound(t, a, longInlineBatch(3))
	if got := len(delegationReminderNotices(sink)); got != 1 {
		t.Fatalf("second round produced %d reminders, want still one (once per turn)", got)
	}
	if got := conversationDelegationReminders(a); got != 1 {
		t.Fatalf("second round produced %d conversation reminders, want still one", got)
	}

	// A fresh turn zeroes the latch with the runtime, like every per-turn counter.
	a.turn = turnRuntime{}
	if a.turn.delegationReminderSent {
		t.Fatal("a fresh turn must re-arm the reminder latch")
	}
}

// TestDelegationReminderSilentForOtherTiers: economy (light), balanced, and
// a never-set tier run the identical long zero-dispatch turn with NO reminder
// — the nudge is the efficiency tier's behavioral half, and the economy tier
// must stay free to spend nothing on dispatches (验收：经济档不触发).
func TestDelegationReminderSilentForOtherTiers(t *testing.T) {
	for _, tc := range []struct {
		name string
		tier SubagentPolicy // "" = never set
	}{
		{"economy", SubagentPolicyLight},
		{"balanced", SubagentPolicyBalanced},
		{"unset", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &delegationNoticeSink{}
			a := newDelegationReminderAgent(t, sink, tc.tier)
			runDelegationReminderRound(t, a, longInlineBatch(delegationReminderMinSteps))
			if got := len(delegationReminderNotices(sink)); got != 0 {
				t.Fatalf("%s tier got %d delegation reminders, want zero", tc.name, got)
			}
			if got := conversationDelegationReminders(a); got != 0 {
				t.Fatalf("%s tier got %d conversation reminders, want zero", tc.name, got)
			}
		})
	}
}

// TestDelegationReminderSilentWhenDispatched: a long turn that already
// dispatched has no zero-dispatch problem to point at — no reminder.
func TestDelegationReminderSilentWhenDispatched(t *testing.T) {
	sink := &delegationNoticeSink{}
	a := newDelegationReminderAgent(t, sink, SubagentPolicyAggressive)
	batch := append([]provider.ToolCall{{Name: "task"}}, longInlineBatch(delegationReminderMinSteps-1)...)
	var calls int32
	a.svc.tools.Add(fakeTool{name: "task", calls: &calls})

	runDelegationReminderRound(t, a, batch)

	if a.turn.taskDispatches != 1 {
		t.Fatalf("taskDispatches = %d, want 1 (dispatch must be counted first)", a.turn.taskDispatches)
	}
	if got := len(delegationReminderNotices(sink)); got != 0 {
		t.Fatalf("dispatching turn got %d reminders, want zero", got)
	}
}

// TestDelegationReminderSilentBelowThreshold: a short zero-dispatch turn is
// below the threshold — the reminder must not fire on ordinary rounds.
func TestDelegationReminderSilentBelowThreshold(t *testing.T) {
	sink := &delegationNoticeSink{}
	a := newDelegationReminderAgent(t, sink, SubagentPolicyAggressive)

	runDelegationReminderRound(t, a, longInlineBatch(delegationReminderMinSteps-1))

	if got := len(delegationReminderNotices(sink)); got != 0 {
		t.Fatalf("short turn got %d reminders, want zero", got)
	}
	if got := conversationDelegationReminders(a); got != 0 {
		t.Fatalf("short turn got %d conversation reminders, want zero", got)
	}
}

// TestDelegationTierGradientContract pins the 验收-5 mechanism gradient on
// top of the 820 metrics: the SAME simulated scenario recorded under all
// three tiers yields three distinguishable Policy fingerprints with
// identical counts (the metrics can see a tier gradient), and the guidance
// texts form the prose gradient — economy carries the conservative clause,
// efficiency carries the dispatch mandate, balanced carries neither extreme.
// The REAL behavior gradient (dispatches vs steps per tier in production) is
// read off the 820 Policy × TaskDispatches/MainChainToolSteps stream.
func TestDelegationTierGradientContract(t *testing.T) {
	scenario := longInlineBatch(6)

	for _, tier := range []SubagentPolicy{SubagentPolicyLight, SubagentPolicyBalanced, SubagentPolicyAggressive} {
		sink := &delegationMetricsSink{}
		a := New(nil, tool.NewRegistry(), NewSession(""), Options{}, sink)
		a.SetSubagentPolicyTier(tier)
		a.turn.turnInput = "same scenario"
		if b := a.executeBatch(context.Background(), &a.turn, scenario); b.err != nil {
			t.Fatalf("%s scenario errored: %v", tier, b.err)
		}
		a.emitTurnShadows(a.turn.turnInput)
		if len(sink.audits) != 1 {
			t.Fatalf("%s: %d fingerprints, want one", tier, len(sink.audits))
		}
		got := sink.audits[0]
		if got.Policy != string(tier) {
			t.Fatalf("%s: fingerprint Policy = %q, tiers must stay distinguishable", tier, got.Policy)
		}
		if got.MainChainToolSteps != len(scenario) {
			t.Fatalf("%s: MainChainToolSteps = %d, want %d (same scenario, same count)", tier, got.MainChainToolSteps, len(scenario))
		}
	}

	// Prose gradient: the tiers must not blur into each other.
	economy := SubagentPolicyGuidance(SubagentPolicyLight)
	balanced := SubagentPolicyGuidance(SubagentPolicyBalanced)
	efficiency := SubagentPolicyGuidance(SubagentPolicyAggressive)
	if !strings.Contains(economy, "Do quick, small tasks yourself") || strings.Contains(economy, "Do NOT inline") {
		t.Fatal("economy must carry the conservative clause and not the dispatch mandate")
	}
	if strings.Contains(balanced, "Do quick, small tasks yourself") || strings.Contains(balanced, "Do NOT inline") {
		t.Fatal("balanced must carry neither extreme (zero-change contract)")
	}
	if !strings.Contains(efficiency, "Do NOT inline") || strings.Contains(efficiency, "Do quick, small tasks yourself") {
		t.Fatal("efficiency must carry the dispatch mandate and not the conservative clause")
	}
}
