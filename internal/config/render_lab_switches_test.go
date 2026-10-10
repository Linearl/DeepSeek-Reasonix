package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// TestRenderTOMLRoundTripsLabSwitches pins the 81/123 lost-save rule for the
// whole lab surface (task 561): every experimental switch the settings page
// can write must survive RenderTOML → decode. A key missing from the render
// table is silently dropped on the next save — the renderer writes a fixed
// key set, so an unlisted key flips straight back to its default. The lab
// grouping/merging (task 561 M1-M8) is entry-level only and never changes
// these keys, so this test must stay green across UI regroupings.
func TestRenderTOMLRoundTripsLabSwitches(t *testing.T) {
	orig := Default()

	// automation（自动化）
	orig.Desktop.Autopilot = true                   // autopilot (M8 standalone)
	orig.Agent.ExperimentalSessionCollab = true     // sessionCollab (M8 standalone)
	orig.Agent.ExperimentalFullAccess = true        // fullAccess (M8 standalone)
	orig.Sandbox.OptimisticWrite = true             // optimisticParallel (M8 standalone)
	orig.Agent.ExperimentalDream = true             // dream
	orig.Agent.ExperimentalSafetyCostControl = true // 任务 517 合并键（B1/B2/B3）
	// 任务 722：细粒度子开关显式覆盖往返（true/false 各半，nil 键不落盘）。
	orig.Agent.SafetyIdleTerminate = boolPtr(true)
	orig.Agent.SafetyLoopStreakNote = boolPtr(false)
	orig.Agent.SafetyEventWaitRecheck = boolPtr(true)
	orig.Agent.ExperimentalSubagentPolicy = boolPtr(true) // M4 (*bool, nil means on)

	// efficiency（提效）
	orig.Agent.ExperimentalCompactionParallel = boolPtr(true) // M3 (*bool, nil means on)
	orig.Agent.ExperimentalContextBudget = boolPtr(true)      // M3
	orig.Agent.ExperimentalResearchBudget = boolPtr(true)     // M3
	orig.Agent.ExperimentalProactiveCompact = true            // M3
	orig.Agent.ExperimentalColdCacheCompact = true            // M3
	orig.Desktop.ExperimentalCacheTuning = true               // M3
	orig.Agent.ExperimentalHighSpeedModel = true              // M2
	orig.Agent.CollabInboxMerge = "same_sender"               // messageMerge light half 1
	orig.Agent.CollabGuidanceMerge = true                     // messageMerge light half 2
	orig.Desktop.ExperimentalQuickCommands = true             // quickCommands
	orig.Agent.TraceAsState = true                            // traceAsState ([agent] authoritative; task-60 field has no Experimental prefix)
	orig.Desktop.ExperimentalOutputStyleUI = true             // outputStyle

	// ui（界面）
	orig.Desktop.ExperimentalTabCompress = true                // tabCompress
	orig.Desktop.ExperimentalTodoSidebar = true                // todoSidebar
	orig.Desktop.ExperimentalPromptHistoryPicker = true        // promptHistoryPicker
	orig.Desktop.ExperimentalSessionWall = true                // sessionWall
	orig.Desktop.ExperimentalSubagentPanel = true              // M4
	orig.Desktop.ExperimentalSubagentDetail = true             // M4
	orig.Desktop.ExperimentalCompletionSummary = boolPtr(true) // completionSummary (*bool, nil means on)
	orig.Agent.ExperimentalAutoLoadOlder = true                // autoLoadOlder ([agent] authoritative after task 473)
	orig.Desktop.ExperimentalSplitView = true                  // splitView
	orig.Agent.ExperimentalComposerDraft = true                // draftPersistence
	orig.Agent.ExperimentalSelectionActions = true             // selectionActions
	orig.Desktop.ExperimentalQuestionSearch = boolPtr(true)    // questionSearch (*bool, nil means on)
	orig.Agent.ExperimentalOpenCodeGoUsage = true              // opencodeGoUsage
	orig.Desktop.ExperimentalRestartUpdate = true              // M6
	orig.Desktop.ExperimentalFeedback = true                   // M6

	// observability（可观测性）/ dev-debug（开发调试）
	orig.Desktop.ExperimentalSessionMonitor = true     // monitoring (M8 standalone, member 1)
	orig.Agent.ExperimentalPerfMonitor = true          // monitoring (M8 standalone, member 2)
	orig.Agent.ExperimentalPerfMonitor = true          // monitoring ([agent] authoritative after task 473)
	orig.Desktop.ExperimentalCDPDebugPort = true       // M5
	orig.Desktop.ExperimentalLifecycleNoiseGate = true // M5

	// storage（存储）
	orig.SessionStorage = "dual_write_read_v4" // M7 (sessionStorage)
	orig.EventsAutoRotation = "auto"           // M7 (eventsRotation)

	// infra（基础设施）
	orig.Agent.ExperimentalRuntimeReuse = true            // runtimeReuse
	orig.Agent.ExperimentalBaseProcess = true             // baseProcess
	orig.Desktop.ExperimentalZcodeTaskBus = true          // zcodeTaskBus
	orig.Desktop.ExperimentalPathRules = true             // pathRules
	orig.Agent.ExperimentalOrphanHandling = true          // orphanHandling
	orig.Desktop.ExperimentalLocalServer = true           // localServer
	orig.Desktop.ExperimentalModelCapabilityFilter = true // modelCapabilityFilter (retired, read-only in M2)

	rendered := RenderTOML(orig)

	// Render-table presence: every lab key must be physically emitted (the
	// fixed-key-set rule). Conditional blocks (autopilot, optimistic_write)
	// must light up because the fields above are set.
	for _, key := range []string{
		"autopilot = true",
		"optimistic_write = true",
		"session_storage = \"dual_write_read_v4\"",
		"events_auto_rotation = \"auto\"",
		"collab_inbox_merge = \"same_sender\"",
		"collab_guidance_merge = true",
		"trace_as_state = true",
		"experimental_restart_update = true",
		"experimental_session_monitor = true",
		"experimental_split_view = true",
		"experimental_feedback = true",
		"experimental_todo_sidebar = true",
		"experimental_subagent_panel = true",
		"experimental_prompt_history_picker = true",
		"experimental_session_wall = true",
		"experimental_tab_compress = true",
		"experimental_subagent_detail = true",
		"experimental_question_search = true",
		"experimental_subagent_tps", // rendered from the nil-means-on helper; presence only
		"experimental_completion_summary = true",
		"experimental_quick_commands = true",
		"experimental_cdp_debug_port = true",
		"experimental_output_style_ui = true",
		"experimental_zcode_task_bus = true",
		"experimental_path_rules = true",
		"experimental_local_server = true",
		"experimental_cache_tuning = true",
		"trace_as_state = true", // task 473: [agent] spelling (renamed from experimental_trace_as_state)
		"experimental_dream = true",
		"experimental_session_collab = true",
		"experimental_auto_load_older = true",
		"experimental_perf_monitor = true",
		"experimental_safety_cost_control = true",
		// 任务 722：子开关条件渲染——非 nil 才落盘，true/false 逐键往返。
		"safety_idle_terminate = true",
		"safety_loop_streak_note = false",
		"safety_event_wait_recheck = true",
		// 任务 517：legacy B1/B2/B3 键保持渲染（迁移后读 false），旧配置往返面不缩。
		"experimental_autonomous_idle_terminate = false",
		"experimental_loop_streak_note = false",
		"experimental_event_wait_recheck = false",
		"experimental_orphan_handling = true",
		// (task 551: experimental_model_capability_filter retired from the render
		// face — accepted on read, never rendered; see render_coverage_test.)
		"experimental_runtime_reuse = true",
		"experimental_full_access = true",
		"experimental_subagent_policy = true",
		"experimental_compaction_parallel = true",
		"experimental_context_budget = true",
		"experimental_research_budget = true",
		"experimental_high_speed_model = true",
		"experimental_proactive_compact = true",
		"experimental_cold_cache_compact = true",
		"experimental_composer_draft = true",
		"experimental_selection_actions = true",
		"experimental_opencode_go_usage = true",
		"experimental_base_process = true",
	} {
		if !strings.Contains(rendered, key) {
			t.Errorf("render table lost %q — a settings save would silently drop it (81/123 lesson)\n---\n%s", key, rendered)
		}
	}

	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v\n---\n%s", err, rendered)
	}

	// Round-trip: each switch reads back with the value we stored.
	if !got.Desktop.Autopilot {
		t.Error("autopilot did not round-trip")
	}
	if !got.Sandbox.OptimisticWrite {
		t.Error("sandbox.optimistic_write did not round-trip")
	}
	if SessionStorageMode(&got) != "dual_write_read_v4" {
		t.Errorf("session_storage = %q, want dual_write_read_v4", SessionStorageMode(&got))
	}
	if EventsAutoRotationMode(&got) != "auto" {
		t.Errorf("events_auto_rotation = %q, want auto", EventsAutoRotationMode(&got))
	}
	if NormalizeCollabInboxMerge(got.Agent.CollabInboxMerge) != "same_sender" {
		t.Errorf("collab_inbox_merge = %q, want same_sender", got.Agent.CollabInboxMerge)
	}
	if !got.Agent.CollabGuidanceMerge {
		t.Error("collab_guidance_merge did not round-trip")
	}
	if !got.Agent.TraceAsState {
		t.Error("trace_as_state (agent, post-473 single key) did not round-trip")
	}
	for name, ok := range map[string]bool{
		"agent.experimental_session_collab":          got.Agent.ExperimentalSessionCollab,
		"agent.experimental_full_access":             got.Agent.ExperimentalFullAccess,
		"agent.experimental_dream":                   got.Agent.ExperimentalDream,
		"agent.experimental_safety_cost_control":     got.Agent.ExperimentalSafetyCostControl,
		"agent.safety_idle_terminate":                got.Agent.SafetyIdleTerminate != nil && *got.Agent.SafetyIdleTerminate,
		"agent.safety_loop_streak_note":              got.Agent.SafetyLoopStreakNote != nil && !*got.Agent.SafetyLoopStreakNote,
		"agent.safety_event_wait_recheck":            got.Agent.SafetyEventWaitRecheck != nil && *got.Agent.SafetyEventWaitRecheck,
		"agent.experimental_proactive_compact":       got.Agent.ExperimentalProactiveCompact,
		"agent.experimental_cold_cache_compact":      got.Agent.ExperimentalColdCacheCompact,
		"agent.experimental_high_speed_model":        got.Agent.ExperimentalHighSpeedModel,
		"agent.experimental_composer_draft":          got.Agent.ExperimentalComposerDraft,
		"agent.experimental_selection_actions":       got.Agent.ExperimentalSelectionActions,
		"agent.experimental_opencode_go_usage":       got.Agent.ExperimentalOpenCodeGoUsage,
		"agent.experimental_runtime_reuse":           got.Agent.ExperimentalRuntimeReuse,
		"agent.experimental_base_process":            got.Agent.ExperimentalBaseProcess,
		"agent.experimental_orphan_handling":         got.Agent.ExperimentalOrphanHandling,
		"desktop.experimental_cache_tuning":          got.Desktop.ExperimentalCacheTuning,
		"desktop.experimental_quick_commands":        got.Desktop.ExperimentalQuickCommands,
		"desktop.experimental_output_style_ui":       got.Desktop.ExperimentalOutputStyleUI,
		"desktop.experimental_tab_compress":          got.Desktop.ExperimentalTabCompress,
		"desktop.experimental_todo_sidebar":          got.Desktop.ExperimentalTodoSidebar,
		"desktop.experimental_prompt_history_picker": got.Desktop.ExperimentalPromptHistoryPicker,
		"desktop.experimental_session_wall":          got.Desktop.ExperimentalSessionWall,
		"desktop.experimental_subagent_panel":        got.Desktop.ExperimentalSubagentPanel,
		"desktop.experimental_subagent_detail":       got.Desktop.ExperimentalSubagentDetail,
		// (retired from render — task 473 single-key fold.)
		"desktop.experimental_split_view":      got.Desktop.ExperimentalSplitView,
		"desktop.experimental_session_monitor": got.Desktop.ExperimentalSessionMonitor,
		"agent.experimental_perf_monitor":      got.Agent.ExperimentalPerfMonitor,
		// (retired from render — task 473 single-key fold.)
		"desktop.experimental_restart_update":       got.Desktop.ExperimentalRestartUpdate,
		"desktop.experimental_feedback":             got.Desktop.ExperimentalFeedback,
		"desktop.experimental_cdp_debug_port":       got.Desktop.ExperimentalCDPDebugPort,
		"desktop.experimental_lifecycle_noise_gate": got.Desktop.ExperimentalLifecycleNoiseGate,
		"desktop.experimental_zcode_task_bus":       got.Desktop.ExperimentalZcodeTaskBus,
		"desktop.experimental_path_rules":           got.Desktop.ExperimentalPathRules,
		"desktop.experimental_local_server":         got.Desktop.ExperimentalLocalServer,
		// (desktop.experimental_model_capability_filter retired from render — task 551.)
	} {
		if !ok {
			t.Errorf("%s did not round-trip through the render table (81/123 lesson)", name)
		}
	}
	// Nil-means-on switches: the pointer must come back set to true.
	for name, ptr := range map[string]*bool{
		"agent.experimental_subagent_policy":      got.Agent.ExperimentalSubagentPolicy,
		"agent.experimental_compaction_parallel":  got.Agent.ExperimentalCompactionParallel,
		"agent.experimental_context_budget":       got.Agent.ExperimentalContextBudget,
		"agent.experimental_research_budget":      got.Agent.ExperimentalResearchBudget,
		"desktop.experimental_question_search":    got.Desktop.ExperimentalQuestionSearch,
		"desktop.experimental_completion_summary": got.Desktop.ExperimentalCompletionSummary,
	} {
		if ptr == nil || !*ptr {
			t.Errorf("%s did not round-trip (nil or false)", name)
		}
	}
	// Helper agreement: the nil-means-on readers must see "on".
	if !got.CompactionParallelEnabled() || !got.ContextBudgetEnabled() || !got.ResearchBudgetEnabled() || !got.SubagentPolicyIntakeEnabled() {
		t.Error("nil-means-on helpers disagree with the round-tripped pointers")
	}
	if !got.DesktopQuestionSearchEnabled() || !got.DesktopSubagentTpsEnabled() || !got.DesktopCompletionSummaryEnabled() {
		t.Error("desktop nil-means-on helpers disagree with the round-tripped pointers")
	}
}
