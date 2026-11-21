package agent

import (
	"reasonix/internal/tool"
)

// tl1728 conditional-offer discoverability support.
//
// The provider-visible allowlist (SetProviderVisibleTools) is static, but two
// tool families only conditionally join the live provider schema:
//
//  1. tool.ContextualTool implementations — each turn's Schemas() call drops
//     the tool unless its ProviderVisible(ctx) holds (background-job family,
//     goal family, plan-mode opt-outs).
//  2. providerSurfaceExtras (multi_edit / restart_update / session_hub) —
//     registered and allowed only while their experiment switch is on.
//
// Filtering both families out of use_capability action=list as well left them
// undiscoverable on a bare list scan even in the very turns where the model
// needed them ("this session cannot read background output"). offerCondition
// is the single list-side decision: true = keep the row and attach the
// tri-lingual offer-condition note. The execute-side gate
// (contextualToolGateOutcome) is untouched — listing is discoverability, not
// a callability promise (non-goal: the offer mechanism itself does not move).
//
// Note format follows the trash note precedent (internal/agent/trash_tools.go):
// one string, segments joined with the full-width '｜' — en｜简体｜繁體.

// conditionalOfferNotes carries the per-tool offer-condition copy for the
// tools whose condition is worth stating precisely. A ContextualTool missing
// here still stays listed via conditionalOfferFallbackNote, so a newly added
// contextual tool can never silently regress to invisible-with-no-explanation.
var conditionalOfferNotes = map[string]string{
	// Background-job family: offered only when the turn context carries a job
	// manager (jobs.FromContext); child agents without one are shadowed via
	// jobs.WithoutManager.
	"bash_output": offerNoteBackgroundJobs,
	"kill_shell":  offerNoteBackgroundJobs,
	"wait":        offerNoteBackgroundJobs,
	// complete_step: plan-mode opt-out (planmode.Active hides it while
	// planning).
	"complete_step": offerNoteCompleteStep,
	// Goal family: the host binds the creator/recorder per context; plain host
	// turns always have a creator, goal turns a recorder.
	"create_goal": offerNoteCreateGoal,
	"update_goal": offerNoteUpdateGoal,
	// Planning-round only.
	"submit_plan": offerNoteSubmitPlan,
	// Research-nudge rounds only.
	"extend_research_budget": offerNoteResearchBudget,
	// providerSurfaceExtras: registered and allowed only behind their own
	// experiment switch (restart to apply).
	"multi_edit":     offerNoteMultiEdit,
	"restart_update": offerNoteRestartUpdate,
	"session_hub":    offerNoteSessionHub,
}

const offerNoteBackgroundJobs = "Offered only in turns whose context carries a background-job manager; absent in child agents without one." +
	"｜仅在本轮上下文绑定了后台任务管理器时被 offer；无管理器的子代理回合不出现。" +
	"｜僅在本輪上下文綁定了背景工作管理器時被 offer；無管理器的子代理回合不出現。"

const offerNoteCompleteStep = "Offered only outside plan mode; hidden while planning (todo_write covers task state during planning)." +
	"｜仅在计划模式之外被 offer；规划期间隐藏（规划期用 todo_write 记任务状态）。" +
	"｜僅在計劃模式之外被 offer；規劃期間隱藏（規劃期用 todo_write 記任務狀態）。"

const offerNoteCreateGoal = "Offered only in plain host turns (goal creation works without goal mode); shadowed away in sub-agent and isolated child contexts." +
	"｜仅在普通宿主回合被 offer（无目标模式也可立 goal）；子代理与隔离子上下文不出现。" +
	"｜僅在普通宿主回合被 offer（無目標模式也可立 goal）；子代理與隔離子上下文不出現。"

const offerNoteUpdateGoal = "Offered only in goal turns whose context carries the goal recorder; calls elsewhere fail closed." +
	"｜仅在被绑定 goal 记录器的 goal 回合被 offer；其他回合执行会 fail-closed。" +
	"｜僅在被綁定 goal 記錄器的 goal 回合被 offer；其他回合執行會 fail-closed。"

const offerNoteSubmitPlan = "Offered only while a planning turn is armed; absent in execution turns." +
	"｜仅在装载了 planning 回合时被 offer；执行回合不出现。" +
	"｜僅在裝載了 planning 回合時被 offer；執行回合不出現。"

const offerNoteResearchBudget = "Offered only in turns nudged into research mode with a budget extender bound." +
	"｜仅在被引导进入研究模式且绑定了预算扩展器的回合被 offer。" +
	"｜僅在被引導進入研究模式且綁定了預算擴展器的回合被 offer。"

const offerNoteMultiEdit = "Registered and offered only while the experimental_multi_edit_injection switch is on (restart to apply)." +
	"｜仅在 experimental_multi_edit_injection 实验开关开启时注册并被 offer（重启生效）。" +
	"｜僅在 experimental_multi_edit_injection 實驗開關開啟時註冊並被 offer（重啟生效）。"

const offerNoteRestartUpdate = "Registered and offered only while the experimental_autonomous_update switch is on (restart to apply)." +
	"｜仅在 experimental_autonomous_update 实验开关开启时注册并被 offer（重启生效）。" +
	"｜僅在 experimental_autonomous_update 實驗開關開啟時註冊並被 offer（重啟生效）。"

const offerNoteSessionHub = "Registered and offered only while the experimental_capability_groups switch is on and the collaboration surface is enabled (restart to apply)." +
	"｜仅在 experimental_capability_groups 实验开关开启且协作面启用时注册并被 offer（重启生效）。" +
	"｜僅在 experimental_capability_groups 實驗開關開啟且協作面啟用時註冊並被 offer（重啟生效）。"

// conditionalOfferFallbackNote keeps an unannotated ContextualTool listed with
// a truthful generic statement instead of dropping it or shipping an empty
// note — new contextual tools cannot silently regress to either failure mode.
const conditionalOfferFallbackNote = "Conditionally offered: this tool joins the provider schema only when the current workflow context enables it; calls elsewhere fail closed." +
	"｜条件 offer 型：仅当当前工作流上下文启用时进入工具面；其余情况执行 fail-closed。" +
	"｜條件 offer 型：僅當當前工作流上下文啟用時進入工具面；其餘情況執行 fail-closed。"

// offerCondition reports whether name is a conditional-offer tool and, if so,
// which tri-lingual note the capability list must attach. The mechanism face
// is the ContextualTool assertion (never misses a newly added one); the notes
// map only refines the copy. Experiment-switch extras (multi_edit family) are
// not ContextualTool — they are conditional via the switch, so the map is the
// authority for them.
func (t *UseCapabilityTool) offerCondition(name string) (string, bool) {
	if t.registry != nil {
		if entry, ok := t.registry.Get(name); ok {
			if _, conditional := entry.(tool.ContextualTool); conditional {
				if note, hit := conditionalOfferNotes[name]; hit {
					return note, true
				}
				return conditionalOfferFallbackNote, true
			}
		}
	}
	if note, hit := conditionalOfferNotes[name]; hit {
		return note, true
	}
	return "", false
}
