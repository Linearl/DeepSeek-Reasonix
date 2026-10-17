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
	c.Sandbox.ExperimentalBashHeavyGuard = true             // task 575：815 起登记 optimisticParallel 家族子键（原豁免键，门禁要求仍被渲染）
	c.Sandbox.ExperimentalParallelWriterReadOnlyBash = true // task 573：豁免键，门禁要求仍被渲染
	c.Agent.ExperimentalDream = true
	// 任务 517：B1/B2/B3 合并为单键，legacy 键渲染面保持（迁移后读 false）。
	// 任务 722：细粒度子开关置非 nil——条件渲染的键在 all-on 夹具下必须
	// 真的被渲染表发射（反向门禁）。
	c.Agent.ExperimentalSafetyCostControl = true
	c.Agent.SafetyIdleTerminate = boolPtr(true)
	c.Agent.SafetyLoopStreakNote = boolPtr(true)
	c.Agent.SafetyEventWaitRecheck = boolPtr(true)
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
	c.Desktop.ExperimentalOutputStyleUI = true
	c.Desktop.ExperimentalCacheTuning = true
	c.Agent.ExperimentalActiveTabResident = true
	// ui
	c.Desktop.ExperimentalTabCompress = true
	c.Desktop.ExperimentalHeartbeatBackground = true // 任务 742：心跳任务后台化（可选档）
	c.Desktop.ExperimentalTodoSidebar = true
	c.Desktop.ExperimentalPromptHistoryPicker = true
	c.Desktop.ExperimentalRestartUpdate = true
	c.Desktop.ExperimentalAutonomousUpdate = true
	c.Desktop.ExperimentalSessionWall = true
	c.Desktop.ExperimentalSubagentPanel = true
	c.Desktop.ExperimentalSubagentDetail = true
	// 任务 826：左右边栏 hover 展开（默认关，可选档）。
	c.Desktop.ExperimentalSidebarHoverLeft = true
	c.Desktop.ExperimentalSidebarHoverRight = true
	// 任务 940：composer 权限选择器形态二选一（默认关，可选档）。
	c.Desktop.ExperimentalApprovalSelect = true
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
	// 任务 846：配置数据目录可更改（storage 组，未稳定档，默认关）。
	c.Desktop.ExperimentalHomePointer = true
	// infra（785 退役 S1 常驻底座开关条目）
	c.Agent.ExperimentalRuntimeReuse = true
	c.Desktop.ExperimentalZcodeTaskBus = true
	c.Desktop.ExperimentalModelCapabilityFilter = true
	c.Desktop.ExperimentalPathRules = true
	c.Agent.ExperimentalOrphanHandling = true
	c.Agent.ExperimentalOrphanLeaseReclaim = true
	c.Agent.ExperimentalRecoveryOrphanSweep = true
	c.Desktop.ExperimentalLocalServer = true
	// tool-opt（任务 603/915：字段 *bool 条件渲染——all-on 夹具必须置非 nil 才发射）
	c.Agent.ExperimentalToolOptimizations = boolPtr(true)
	// 任务 943：族拆两键——注入键同为 *bool 条件渲染，all-on 夹具置非 nil。
	c.Agent.ExperimentalMultiEditInjection = boolPtr(true)
	// 豁免键（非表A，但属于实验室族，门禁要求它们仍被渲染）
	c.Agent.ExperimentalPreapproveManagedPaths = true
	c.Agent.ExperimentalCascadeApproval = true
	c.Agent.ExperimentalFallbackModel = true
	// task 789：证据门宽松开关（默认关）。815 登记窗口：设置页入口落地，
	// 自豁免表转正入表（editEvidenceRelaxed，未稳定档）。
	c.Agent.ExperimentalEditEvidenceRelaxed = true
	// 任务 707：压缩模型指定（上下文治理卡，可选档）。注册表 renderKeys 含
	// compact_model（labSpecialKeys），两键都要被渲染表发射（反向门禁）。
	c.Agent.ExperimentalCompactModel = true
	c.Agent.CompactModel = "minimax/m3"
	c.Agent.ExperimentalCollabBackgroundDelivery = true
	c.Serve.ExperimentalGCChildSession = true         // 任务 540：豁免键，serve 域开关
	c.Agent.ExperimentalGeneralPurposeSubagent = true // 任务 632：豁免键，门禁要求仍被渲染
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
// register must stay exactly 推荐 16 / 可选 27 / 未稳定 16 / 已退役 0 = 59（任务 943
// multiEditInjection 与任务 940 approvalSelect 先后入表后，两件各 +1 可选）.
// Any lab addition/removal moves these numbers ON PURPOSE (update 表A first).
func TestLabFeatureTierCountsMatchTableA(t *testing.T) {
	want := map[LabTier]int{
		// 任务 783④：toolOptimizations（编辑读回 readBack）随 22.5 交付
		// 转正，未稳定→推荐（前端注册表同步升档）——推荐 15→16、未稳定
		// 14→13，总数不变 51。
		LabTierRecommended: 16,
		// 任务 517：B1/B2/B3（可选×3）合并为 safetyCostControl（可选×1），
		// 可选 20→18、总数 48→46。任务 705（sessionCollabAutoFold，超长跨
		// 会话消息自动折叠，默认关）按可选档入表（18→19、47→48）。任务 707
		// （compactModel，压缩模型指定，默认关）按任务书建议可选档入表
		// （19→20、48→49）。
		// （19→20、48→49）。任务 704（trajectoryView，轨迹视图，默认关）按
		// 可选档入表（20→21、49→50）。任务 742（heartbeatBackground，心跳
		// 任务后台化，默认关）按 550 口径可选档入表（21→22、50→51）。
		LabTierOptional: 22,
		// 任务 545（sessionCwdFollow）+ 任务 504（tabModeTint）为 562 表A 快照后
		// 新增的默认关实验项，按未稳定档登记；任务 551 将 B9
		// （modelCapabilityFilter）退役移出表A——xlsx 侧待同步。
		// 任务 603（toolOptimizations，「工具优化」族首件）同为快照后新增，
		// 用户定档未稳定。任务 677（collabGroupView，群聊入口开关，409 交付
		// 漏挂铁律 2 开关）同为快照后新增，用户定档未稳定（46→47）。
		// 任务 783④：toolOptimizations 转正（14→13）。任务 785：S1 常驻底座开关
		// 随底座退役出表（13→12，总数 51→50）。任务 790：
		// acpListen（ACP 常驻监听，默认关）入表未稳定档（12→13，总数 50→51）。
		// 任务 846：homePointer（配置数据目录可更改，默认关）入表未稳定档
		// （13→14，总数 54→55）。任务 815：editEvidenceRelaxed（编辑证据门
		// 宽松，任务 789 交付、815 登记窗口补设置页入口后转正）入表未稳定档
		// （14→15，总数 55→56）。任务 686：workflowPanel（工作流可视化面板，
		// 685 引擎未落地为前端 fixture 预览）入表未稳定档（15→16，总数
		// 56→57）。
		LabTierUnstable: 16,
		LabTierRetired:  0,
	}
	// 任务 801：decisionPatterns（决策模式提取+固化，默认关）按 550 口径可选
	// 档入表（22→23、总数 51→52）。任务 826：sidebarHoverLeft/Right（左右
	// 边栏 hover 展开+延时折叠，默认关）按 550 口径可选档入表（23→25、
	// 总数 52→54）。任务 943：multiEditInjection（multi_edit 直注，工具优化
	// 族拆分独立卡）按可选档入表（25→26、总数 57→58；前端注册表同步）。
	// 任务 940：approvalSelect（composer 权限选择器形态二选一，默认关=按钮
	// 组）按 550 口径可选档入表（26→27、总数 58→59；与 943 先后入表各 +1）。
	want[LabTierOptional] = 27
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
	if len(labFeatureTiers) != 59 {
		t.Errorf("labFeatureTiers has %d entries, 表A(同步后) has 59", len(labFeatureTiers))
	}
}
