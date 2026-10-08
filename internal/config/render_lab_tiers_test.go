package config

import (
	"strings"
	"testing"
)

// allLabSwitchesOn builds a config with every lab-family switch lit, so the
// render table emits every lab key (conditional blocks like autopilot and
// optimistic_write light up with the switch on). Mirrors the setup of
// TestRenderTOMLRoundTripsLabSwitches plus the family/exempt keys.
func allLabSwitchesOn() *Config {
	c := Default()
	// automation
	c.Desktop.Autopilot = true
	c.Desktop.ExperimentalAutopilotAskTimeout = true
	c.Agent.ExperimentalSessionCollab = true
	c.Agent.ExperimentalFullAccess = true
	c.Desktop.ExperimentalParallelFullAccess = true
	c.Sandbox.OptimisticWrite = true
	c.Sandbox.ExperimentalBashHeavyGuard = true // task 575：豁免键，门禁要求仍被渲染
	c.Sandbox.ExperimentalParallelWriterReadOnlyBash = true // task 573：豁免键，门禁要求仍被渲染
	c.Agent.ExperimentalDream = true
	c.Agent.ExperimentalAutonomousIdleTerminate = true
	c.Agent.ExperimentalLoopStreakNote = true
	c.Agent.ExperimentalSubagentPolicy = boolPtr(true)
	// efficiency
	c.Agent.ExperimentalContextBudget = boolPtr(true)
	c.Agent.ExperimentalResearchBudget = boolPtr(true)
	c.Agent.ExperimentalProactiveCompact = true
	c.Agent.ExperimentalColdCacheCompact = true
	c.Agent.CollabInboxMerge = "same_sender"
	c.Agent.CollabGuidanceMerge = true
	c.Desktop.ExperimentalQuickCommands = true
	c.Agent.ExperimentalHighSpeedModel = true
	c.Agent.ExperimentalCompactionParallel = boolPtr(true)
	c.Agent.TraceAsState = true
	c.Desktop.ExperimentalTraceAsState = true
	c.Agent.ExperimentalEventWaitRecheck = true
	c.Desktop.ExperimentalOutputStyleUI = true
	c.Desktop.ExperimentalCacheTuning = true
	c.Agent.ExperimentalActiveTabResident = true
	// ui
	c.Desktop.ExperimentalTabCompress = true
	c.Desktop.ExperimentalTodoSidebar = true
	c.Desktop.ExperimentalPromptHistoryPicker = true
	c.Desktop.ExperimentalRestartUpdate = true
	c.Desktop.ExperimentalAutonomousUpdate = true
	c.Desktop.ExperimentalSessionWall = true
	c.Desktop.ExperimentalSubagentPanel = true
	c.Desktop.ExperimentalSubagentDetail = true
	c.Desktop.ExperimentalCompletionSummary = boolPtr(true)
	c.Desktop.ExperimentalAutoLoadOlder = true
	c.Desktop.ExperimentalSplitView = true
	c.Agent.ExperimentalComposerDraft = true
	c.Agent.ExperimentalSelectionActions = true
	c.Desktop.ExperimentalQuestionSearch = boolPtr(true)
	c.Agent.ExperimentalOpenCodeGoUsage = true
	c.Desktop.ExperimentalFeedback = true
	c.Desktop.ExperimentalFeedbackNudge = true
	// observability / dev-debug
	c.Desktop.ExperimentalSessionMonitor = true
	c.Agent.ExperimentalPerfMonitor = true
	c.Desktop.ExperimentalPerfMonitor = true
	c.Agent.ExperimentalHeapHighProfile = true
	c.Desktop.ExperimentalCDPDebugPort = true
	c.Desktop.ExperimentalLifecycleNoiseGate = true
	// storage
	c.SessionStorage = "dual_write_read_v4"
	c.EventsAutoRotation = "auto"
	// infra
	c.Agent.ExperimentalRuntimeReuse = true
	c.Agent.ExperimentalBaseProcess = true
	c.Desktop.ExperimentalZcodeTaskBus = true
	c.Desktop.ExperimentalModelCapabilityFilter = true
	c.Desktop.ExperimentalPathRules = true
	c.Agent.ExperimentalOrphanHandling = true
	c.Agent.ExperimentalOrphanLeaseReclaim = true
	c.Agent.ExperimentalRecoveryOrphanSweep = true
	c.Desktop.ExperimentalLocalServer = true
	// tool-opt（任务 603）
	c.Agent.ExperimentalToolOptimizations = true
	// 豁免键（非表A，但属于实验室族，门禁要求它们仍被渲染）
	c.Agent.ExperimentalPreapproveManagedPaths = true
	c.Agent.ExperimentalCascadeApproval = true
	c.Agent.ExperimentalFallbackModel = true
	c.Agent.ExperimentalCollabBackgroundDelivery = true
	c.Serve.ExperimentalGCChildSession = true // 任务 540：豁免键，serve 域开关
	return c
}

// renderedLabKeys extracts the set of lab switch keys physically emitted by
// the render table for the all-on config.
func renderedLabKeys(t *testing.T) map[string]bool {
	t.Helper()
	rendered := RenderTOML(allLabSwitchesOn())
	keys := map[string]bool{}
	for _, line := range strings.Split(rendered, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		eq := strings.Index(line, " = ")
		if eq <= 0 {
			continue
		}
		key := line[:eq]
		if strings.HasPrefix(key, "experimental_") || labSpecialKeys[key] {
			keys[key] = true
		}
	}
	return keys
}

// TestLabRenderKeysAllTaggedWithTier is the 任务562 gate (the tier counterpart
// of the 81/123 render-table defense): every lab switch the render table emits
// must be tagged with a tier in labFeatureTiers, or explicitly exempted in
// labNonFeatureKeys with a reason. A NEW experimental switch that skips the
// tag fails here — the three-tier badges cannot silently drift from the
// render table.
func TestLabRenderKeysAllTaggedWithTier(t *testing.T) {
	rendered := renderedLabKeys(t)
	if len(rendered) == 0 {
		t.Fatal("render table emitted no lab keys — the extraction broke, fix the gate before trusting it")
	}

	tagged := map[string]bool{}
	for _, f := range labFeatureTiers {
		for _, key := range f.renderKeys {
			if tagged[key] {
				t.Errorf("lab key %q is registered twice in labFeatureTiers (features must not share render keys)", key)
			}
			tagged[key] = true
		}
	}
	for key := range labNonFeatureKeys {
		if tagged[key] {
			t.Errorf("exempt key %q is also registered in labFeatureTiers — it cannot be both feature-tagged and exempt", key)
		}
	}

	// 门禁：渲染表发射的每个实验室键都必须已标档位（或显式豁免）。
	for key := range rendered {
		if !tagged[key] {
			if _, exempt := labNonFeatureKeys[key]; exempt {
				continue
			}
			t.Errorf("门禁失败（任务562）：渲染表新出现实验室键 %q，但 labFeatureTiers 未登记档位——"+
				"新增实验项必须标档位（加入对应表A特性的 renderKeys），若确非实验室 tab 特性，"+
				"请在 labNonFeatureKeys 附理由豁免（81/123 渲染表防线的档位对位）", key)
		}
	}

	// 反向：注册表的每个键（含豁免键）都必须真的被渲染表发射——特性被改名/
	// 删除后注册表要跟着演进，不能留下幽灵档位。
	for _, f := range labFeatureTiers {
		if len(f.renderKeys) == 0 {
			t.Errorf("lab feature %q registers no render keys — a tier without a switch cannot be displayed", f.id)
		}
		for _, key := range f.renderKeys {
			if !rendered[key] {
				t.Errorf("lab feature %q registers render key %q but the render table never emits it (renamed or removed key — update labFeatureTiers)", f.id, key)
			}
		}
	}
	for key := range labNonFeatureKeys {
		if !rendered[key] {
			t.Errorf("exempt lab key %q is no longer emitted by the render table — drop it from labNonFeatureKeys", key)
		}
	}
}

// TestLabFeatureTierCountsMatchTableA pins the xlsx 表A distribution: the tier
// register must stay exactly 推荐 15 / 可选 20 / 未稳定 10 / 已退役 1 = 46.
// Any lab addition/removal moves these numbers ON PURPOSE (update 表A first).
func TestLabFeatureTierCountsMatchTableA(t *testing.T) {
	want := map[LabTier]int{
		LabTierRecommended: 15,
		LabTierOptional:    20,
		// 任务 545（sessionCwdFollow）+ 任务 504（tabModeTint）为 562 表A 快照后
		// 新增的默认关实验项，按未稳定档登记；任务 551 将 B9
		// （modelCapabilityFilter）退役移出表A——xlsx 侧待同步。
		// 任务 603（toolOptimizations，「工具优化」族首件）同为快照后新增，
		// 用户定档未稳定。
		LabTierUnstable: 13,
		LabTierRetired:  0,
	}
	got := map[LabTier]int{}
	seen := map[string]bool{}
	for _, f := range labFeatureTiers {
		got[f.tier]++
		if seen[f.id] {
			t.Errorf("duplicate lab feature id %q in labFeatureTiers", f.id)
		}
		seen[f.id] = true
		switch f.tier {
		case LabTierRecommended, LabTierOptional, LabTierUnstable, LabTierRetired:
		default:
			t.Errorf("lab feature %q has unknown tier %q", f.id, f.tier)
		}
	}
	for tier, n := range want {
		if got[tier] != n {
			t.Errorf("tier %q count = %d, xlsx 表A says %d (任务562 验收：档位数量与表A完全一致)", tier, got[tier], n)
		}
	}
	if len(labFeatureTiers) != 48 {
		t.Errorf("labFeatureTiers has %d entries, 表A(同步后) has 48", len(labFeatureTiers))
	}
}
