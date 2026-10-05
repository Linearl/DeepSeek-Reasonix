package config

import (
	"strings"
	"testing"
)

// Same failure mode as the autopilot keys next door: the user-config renderer writes
// a fixed key set, so a preference it does not list is dropped on save even though
// Settings reported success - the switch then reads back off and cannot be turned on.
// Both experiment switches shipped that way until 2026-09-15 (task 81's
// restart-and-update and task 123's session monitor never reached config.toml).
func TestExperimentalSwitchesRoundTripThroughRender(t *testing.T) {
	c := &Config{}
	c.Desktop.ExperimentalRestartUpdate = true
	c.Desktop.ExperimentalAutonomousUpdate = true
	c.Desktop.ExperimentalSessionMonitor = true
	c.Desktop.ExperimentalSplitView = true
	c.Desktop.ExperimentalFeedback = true
	c.Desktop.ExperimentalParallelFullAccess = true
	c.Desktop.ExperimentalTodoSidebar = true
	c.Desktop.ExperimentalPromptHistoryPicker = true
	c.Desktop.ExperimentalPathRules = true
	c.Desktop.ExperimentalTraceAsState = true
	c.Desktop.ExperimentalDream = true
	c.Agent.ExperimentalSessionCollab = true
	c.Desktop.ExperimentalAutonomousIdleTerminate = true
	c.Agent.ExperimentalAutonomousIdleTerminate = true
	c.Desktop.ExperimentalLoopStreakNote = true
	c.Agent.ExperimentalLoopStreakNote = true
	c.Desktop.ExperimentalEventWaitRecheck = true
	c.Agent.ExperimentalEventWaitRecheck = true
	c.Desktop.ExperimentalOrphanLeaseReclaim = true
	c.Agent.ExperimentalOrphanLeaseReclaim = true
	c.Desktop.ExperimentalRecoveryOrphanSweep = true
	c.Desktop.ExperimentalLifecycleNoiseGate = true

	c.Desktop.ExperimentalModelCapabilityFilter = true
	c.Agent.ExperimentalModelCapabilityFilter = true
	c.Desktop.ExperimentalSessionCollab = true
	// Task 439: the built-in zcode task bus switch must survive the render.
	c.Desktop.ExperimentalZcodeTaskBus = true

	out := RenderTOMLForScope(c, RenderScopeUser)
	for _, want := range []string{
		"experimental_restart_update = true",
		"experimental_autonomous_update = true",
		"experimental_session_monitor = true",
		"experimental_split_view = true",
		"experimental_feedback = true",
		"experimental_parallel_full_access = true",
		"experimental_todo_sidebar = true",
		"experimental_prompt_history_picker = true",
		"experimental_path_rules = true",
		"experimental_trace_as_state = true",
		"experimental_dream = true",
		"experimental_session_collab = true",
		"experimental_autonomous_idle_terminate = true",
		"experimental_loop_streak_note = true",
		"experimental_event_wait_recheck = true",
		"experimental_orphan_lease_reclaim = true",
		"experimental_recovery_orphan_sweep = true",
		"experimental_model_capability_filter = true",
		"experimental_zcode_task_bus = true",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered user config is missing %q\n---\n%s", want, out)
		}
	}
}

// Unlike autopilot (which stays out of an untouched config), these two are rendered
// unconditionally: turning one back off has to be recorded, otherwise the file keeps
// the stale true and the switch springs back on at the next load.
func TestExperimentalSwitchesRenderWhenOff(t *testing.T) {
	c := &Config{}
	out := RenderTOMLForScope(c, RenderScopeUser)
	for _, want := range []string{
		"experimental_restart_update = false",
		"experimental_autonomous_update = false",
		"experimental_session_monitor = false",
		"experimental_split_view = false",
		"experimental_feedback = false",
		"experimental_parallel_full_access = false",
		"experimental_todo_sidebar = false",
		"experimental_path_rules = false",
		"experimental_trace_as_state = false",
		"experimental_dream = false", // [agent] and desktop mirror both render this key
		"experimental_session_collab = false",
		"experimental_autonomous_idle_terminate = false",
		"experimental_loop_streak_note = false",
		"experimental_event_wait_recheck = false",
		"experimental_orphan_lease_reclaim = false",
		"experimental_recovery_orphan_sweep = false",
		"experimental_model_capability_filter = false",
		"experimental_zcode_task_bus = false",
		"trace_as_state = false",
		"stalled_intent_nudge = false",
		"readiness_catch_up = false",
		"plan_research_gate = false",
		"read_only_task_background = false",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("a disabled switch should still render %q\n---\n%s", want, out)
		}
	}
}

func TestExperimentalDreamRoundTripThroughRender(t *testing.T) {
	c := &Config{}
	c.Agent.ExperimentalDream = true
	out := RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(out, "experimental_dream = true") {
		t.Fatalf("rendered user config is missing experimental_dream = true\n---\n%s", out)
	}
}

// 任务 506: tab-strip adaptive compression is a plain default-off desktop
// bool. An untouched config must render false (ships off, 铁律 2), enabling
// must render true, and flipping back off must stay recorded — otherwise the
// next save re-renders the stale true and the switch springs back on.
func TestTask506TabCompressRoundTrip(t *testing.T) {
	out := RenderTOMLForScope(&Config{}, RenderScopeUser)
	if !strings.Contains(out, "experimental_tab_compress = false") {
		t.Fatalf("tab compress ships off: missing false render\n---\n%s", out)
	}

	on := &Config{}
	if err := on.SetExperimentalTabCompress(true); err != nil {
		t.Fatalf("set tab compress: %v", err)
	}
	if !on.Desktop.ExperimentalTabCompress {
		t.Fatal("the setter must flip the desktop field")
	}
	out = RenderTOMLForScope(on, RenderScopeUser)
	if !strings.Contains(out, "experimental_tab_compress = true") {
		t.Fatalf("enable must render true\n---\n%s", out)
	}

	off := &Config{}
	if err := off.SetExperimentalTabCompress(false); err != nil {
		t.Fatalf("set tab compress off: %v", err)
	}
	out = RenderTOMLForScope(off, RenderScopeUser)
	if !strings.Contains(out, "experimental_tab_compress = false") {
		t.Fatalf("explicit off must survive the render\n---\n%s", out)
	}
}

// Task 265 lab intake: the intake switches ship ON via nil-means-on pointers.
// An untouched config must render true (existing behaviour, zero regression),
// an explicit off must render false (and survive the next load), and an
// explicit true renders the same as nil.
func TestTask265NilMeansOnSwitchesRoundTrip(t *testing.T) {
	// Untouched: every intake switch renders true.
	out := RenderTOMLForScope(&Config{}, RenderScopeUser)
	for _, want := range []string{
		"experimental_compaction_parallel = true",
		"experimental_context_budget = true",
		"experimental_research_budget = true",
		"experimental_question_search = true",
		"experimental_subagent_tps = true",
		"experimental_completion_summary = true",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("nil pointer must render on: missing %q\n---\n%s", want, out)
		}
	}

	// Explicit off: recorded and rendered false, so it survives the reload.
	off := &Config{}
	if err := off.SetExperimentalCompactionParallel(false); err != nil {
		t.Fatalf("set compaction parallel: %v", err)
	}
	if err := off.SetExperimentalContextBudget(false); err != nil {
		t.Fatalf("set context budget: %v", err)
	}
	if err := off.SetExperimentalResearchBudget(false); err != nil {
		t.Fatalf("set research budget: %v", err)
	}
	if err := off.SetExperimentalQuestionSearch(false); err != nil {
		t.Fatalf("set question search: %v", err)
	}
	if err := off.SetExperimentalSubagentPolicy(false); err != nil {
		t.Fatalf("set subagent policy: %v", err)
	}
	if err := off.SetExperimentalSubagentTps(false); err != nil {
		t.Fatalf("set subagent tps: %v", err)
	}
	if err := off.SetExperimentalCompletionSummary(false); err != nil {
		t.Fatalf("set completion summary: %v", err)
	}
	if off.CompactionParallelEnabled() || off.ContextBudgetEnabled() || off.ResearchBudgetEnabled() {
		t.Fatal("explicitly off agent switches must read back disabled")
	}
	if !off.SubagentPolicyIntakeEnabled() {
		if got := off.DefaultSubagentPolicy(); got != "light" {
			t.Fatalf("off intake must force a light default, got %q", got)
		}
	} else {
		t.Fatal("explicitly off subagent policy intake must read back disabled")
	}
	if off.DesktopQuestionSearchEnabled() || off.DesktopSubagentTpsEnabled() || off.DesktopCompletionSummaryEnabled() {
		t.Fatal("explicitly off desktop switches must read back disabled")
	}
	out = RenderTOMLForScope(off, RenderScopeUser)
	for _, want := range []string{
		"experimental_compaction_parallel = false",
		"experimental_context_budget = false",
		"experimental_research_budget = false",
		"experimental_question_search = false",
		"experimental_subagent_policy = false",
		"experimental_subagent_tps = false",
		"experimental_completion_summary = false",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("explicit off must survive the render: missing %q\n---\n%s", want, out)
		}
	}

	// Quick commands (task 262) is a plain default-off bool: it must render
	// false on an untouched config and true after enabling.
	out = RenderTOMLForScope(&Config{}, RenderScopeUser)
	if !strings.Contains(out, "experimental_quick_commands = false") {
		t.Fatalf("quick commands ships off: missing false render\n---\n%s", out)
	}
	on := &Config{}
	if err := on.SetExperimentalQuickCommands(true); err != nil {
		t.Fatalf("set quick commands: %v", err)
	}
	out = RenderTOMLForScope(on, RenderScopeUser)
	if !strings.Contains(out, "experimental_quick_commands = true") {
		t.Fatalf("quick commands enable must render true\n---\n%s", out)
	}

	// Full access (task 257) is a plain default-off bool: it must render
	// false on an untouched config and true after enabling.
	out = RenderTOMLForScope(&Config{}, RenderScopeUser)
	if !strings.Contains(out, "experimental_full_access = false") {
		t.Fatalf("full access ships off: missing false render\n---\n%s", out)
	}
	full := &Config{}
	if err := full.SetExperimentalFullAccess(true); err != nil {
		t.Fatalf("set full access: %v", err)
	}
	out = RenderTOMLForScope(full, RenderScopeUser)
	if !strings.Contains(out, "experimental_full_access = true") {
		t.Fatalf("full access enable must render true\n---\n%s", out)
	}
}

func TestExperimentalSessionCollabRoundTripThroughRender(t *testing.T) {
	c := &Config{}
	c.Agent.ExperimentalSessionCollab = true
	out := RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(out, "experimental_session_collab = true") {
		t.Fatalf("rendered user config is missing experimental_session_collab = true\n---\n%s", out)
	}
}

func TestStalledIntentNudgeRoundTripThroughRender(t *testing.T) {
	c := &Config{}
	c.Agent.StalledIntentNudge = true
	c.Agent.StalledIntentNudgeLimit = 2
	out := RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(out, "stalled_intent_nudge = true") {
		t.Fatalf("rendered user config is missing stalled_intent_nudge = true\n---\n%s", out)
	}
	if !strings.Contains(out, "stalled_intent_nudge_limit = 2") {
		t.Fatalf("rendered user config is missing stalled_intent_nudge_limit = 2\n---\n%s", out)
	}
}

func TestReadinessCatchUpRoundTripThroughRender(t *testing.T) {
	c := &Config{}
	c.Agent.ReadinessCatchUp = true
	c.Agent.ReadinessCatchUpLimit = 2
	out := RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(out, "readiness_catch_up = true") {
		t.Fatalf("rendered user config is missing readiness_catch_up = true\n---\n%s", out)
	}
	if !strings.Contains(out, "readiness_catch_up_limit = 2") {
		t.Fatalf("rendered user config is missing readiness_catch_up_limit = 2\n---\n%s", out)
	}
}

func TestTraceAsStateRoundTripThroughRender(t *testing.T) {
	c := &Config{}
	c.Agent.TraceAsState = true
	out := RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(out, "trace_as_state = true") {
		t.Fatalf("rendered user config is missing trace_as_state = true\n---\n%s", out)
	}
}

func TestPlanResearchGateRoundTripThroughRender(t *testing.T) {
	c := &Config{}
	c.Agent.PlanResearchGate = true
	c.Agent.PlanResearchGateLimit = 2
	c.Agent.ReadOnlyTaskBackground = true
	out := RenderTOMLForScope(c, RenderScopeUser)
	for _, want := range []string{
		"plan_research_gate = true",
		"plan_research_gate_limit = 2",
		"read_only_task_background = true",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered user config is missing %q\n---\n%s", want, out)
		}
	}
}

// Task 265 audit-3 M1: the intake switch must not rewrite the render face.
// With a stored tier + the switch explicitly off, saving any other setting
// (which re-renders the user config) previously dropped subagent_policy
// entirely — the "saving a switch deletes the key" family of 2026-09-15.
func TestSubagentPolicySurvivesRenderWhenIntakeOff(t *testing.T) {
	c := &Config{}
	c.Agent.SubagentPolicy = "balanced"
	if err := c.SetExperimentalSubagentPolicy(false); err != nil {
		t.Fatalf("set subagent policy off: %v", err)
	}
	// Consumer view: forced light while off (the intended gate).
	if got := c.DefaultSubagentPolicy(); got != "light" {
		t.Fatalf("intake off must force light at the consumer, got %q", got)
	}
	// Render face: the stored tier survives untouched.
	out := RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(out, `subagent_policy = "balanced"`) {
		t.Fatalf("stored tier dropped from the render while the switch is off\n---\n%s", out)
	}
	// Re-opening the switch restores the stored value with no data repair.
	if err := c.SetExperimentalSubagentPolicy(true); err != nil {
		t.Fatalf("set subagent policy on: %v", err)
	}
	if got := c.DefaultSubagentPolicy(); got != "balanced" {
		t.Fatalf("stored tier must come back after re-enabling, got %q", got)
	}
}
