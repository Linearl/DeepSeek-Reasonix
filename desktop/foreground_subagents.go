package main

import (
	"sort"
	"strings"
	"time"

	"reasonix/internal/event"
)

// Task 557: the capsule's running-work data plane had two inputs — the active
// controller's background jobs and the other tabs' runtime jobs. A foreground
// (synchronous) sub-agent is neither: it blocks inside the parent turn and is
// never registered as a job, so the capsule badge counted zero while one ran.
//
// This file adds the third source: a process-local registry of running
// foreground sub-agents per tab, maintained from the content-free lifecycle
// telemetry the controller already emits (emitSubagentLifecycle). Lifecycle
// start phases (child_created/child_resume/child_running) add or refresh an
// entry; terminal phases remove it; the turn boundary is the reliable
// end-signal backstop — a foreground sub-agent cannot outlive its turn, so
// TurnStarted/TurnDone clear the tab's entries and an interrupted or
// abnormally ended turn can never leave the count hanging (task 510: stale
// state is worse than no state).
//
// Dedupe is by construction, not by matching keys: background sub-agents
// (task run_in_background, backgrounded fleets) are already represented by
// their job row, and their lifecycle events carry Background=true (set at the
// emit sites in internal/agent), so they never enter this registry.

// ForegroundSubagentView is one running foreground sub-agent for the desktop
// contract. Title is the owning tab's display title (empty when the tab is
// unknown, e.g. a rebound runtime); the frontend shows it as the row origin.
// ParentToolCallID (任务 667) is the dispatch-batch key: children of one
// parallel dispatch share it. The capsule UI ignores it; get_session_status's
// work detail surfaces it as "batch".
type ForegroundSubagentView struct {
	TabID            string `json:"tabId"`
	Title            string `json:"title,omitempty"`
	Ref              string `json:"ref"`
	Name             string `json:"name,omitempty"`
	// Description is the dispatch line the parent model passed with the call
	// (tl1209). Panels prefer it over Name: same-profile children ("ask",
	// "task") are otherwise indistinguishable.
	Description      string `json:"description,omitempty"`
	StartedAt        int64  `json:"startedAt"` // unix milliseconds
	ParentToolCallID string `json:"parentToolCallId,omitempty"`
	// LastActivityAt（tl1447）是子代理最后一次可观测活动的时刻（unix ms）：
	// 登记时（lifecycle 活相）打起点，此后每个带 ref 的进度事件刷新。运行行
	// 的静默时长 = now - LastActivityAt，越阈值即心跳警示徽章（异常先发声）。
	LastActivityAt int64 `json:"lastActivityAt,omitempty"`
}

// foregroundSubagentEntry is the registry record for one running foreground
// sub-agent, keyed by ref within its tab's set.
type foregroundSubagentEntry struct {
	name             string
	description      string
	startedAt        int64 // unix milliseconds
	parentToolCallID string
	lastActivity     int64 // unix milliseconds; tl1447 活跃性判定面
}

// foregroundLifecycleAlivePhases are the lifecycle phases that mean "this ref
// is (still) running in the foreground". Anything else — including unknown
// future phases — is never guessed at; the turn-boundary clears are the
// backstop that keeps the count from hanging.
var foregroundLifecycleAlivePhases = map[string]bool{
	"child_created": true,
	"child_resume":  true,
	"child_running": true,
}

// foregroundLifecycleTerminalPhases remove a ref from the registry. Removal
// runs regardless of the Background flag: an unknown ref removal is a no-op,
// and a terminal for a previously-tracked ref must always land.
var foregroundLifecycleTerminalPhases = map[string]bool{
	"child_completed": true,
	"child_partial":   true,
	"child_failed":    true,
	"child_cancelled": true,
}

// noteSubagentLifecycle maintains the foreground registry from one lifecycle
// transition. tabID is the emitting sink's current binding — the tab the
// parent turn (or its rebound runtime) is displayed in.
func (a *App) noteSubagentLifecycle(tabID string, info event.SubagentLifecycleInfo) {
	if a == nil || strings.TrimSpace(tabID) == "" || strings.TrimSpace(info.Ref) == "" {
		return
	}
	switch {
	case foregroundLifecycleAlivePhases[info.Phase]:
		if info.Background {
			// Job-owned child: the background job row already represents it.
			// Counting it here too would double the badge (task 557 dedupe).
			return
		}
		startedAt := info.StartUnixMs
		if startedAt == 0 {
			startedAt = time.Now().UnixMilli()
		}
		a.foregroundSubagentsMu.Lock()
		if a.foregroundSubagents == nil {
			a.foregroundSubagents = make(map[string]map[string]foregroundSubagentEntry)
		}
		refs := a.foregroundSubagents[tabID]
		if refs == nil {
			refs = make(map[string]foregroundSubagentEntry)
			a.foregroundSubagents[tabID] = refs
		}
		refs[info.Ref] = foregroundSubagentEntry{name: info.Skill, description: info.Description, startedAt: startedAt, parentToolCallID: info.ParentToolCallID, lastActivity: startedAt}
		a.foregroundSubagentsMu.Unlock()
	case foregroundLifecycleTerminalPhases[info.Phase]:
		a.removeForegroundSubagent(tabID, info.Ref)
	}
}

// noteSubagentActivity（tl1447）刷新一个运行中前台子代理的最后活跃时刻。
// 调用源是 tabEventSink.Emit：子代理进度管道的每个事件（ToolProgress，带
// SubagentRef）都是可观测活动——reasoning/text/notice 预览与阶段变迁都会
// 走它，静默时长因此覆盖「长工具调用不产预览」的真实静默。仅刷新既有行：
// 后台子代理不进本登记表（job 行代表它），无主 ref 天然 no-op，所以本函数
// 永远不凭一个活动事件造行（造行是 lifecycle 活相的特权）。
func (a *App) noteSubagentActivity(tabID, ref string) {
	if a == nil || strings.TrimSpace(tabID) == "" || strings.TrimSpace(ref) == "" {
		return
	}
	now := time.Now().UnixMilli()
	a.foregroundSubagentsMu.Lock()
	if refs := a.foregroundSubagents[tabID]; refs != nil {
		if entry, ok := refs[ref]; ok {
			entry.lastActivity = now
			refs[ref] = entry
		}
	}
	a.foregroundSubagentsMu.Unlock()
}

// removeForegroundSubagent drops one ref from a tab's set, pruning the tab
// entry when the set drains so the registry never grows per tab.
func (a *App) removeForegroundSubagent(tabID, ref string) {
	a.foregroundSubagentsMu.Lock()
	defer a.foregroundSubagentsMu.Unlock()
	refs := a.foregroundSubagents[tabID]
	if refs == nil {
		return
	}
	delete(refs, ref)
	if len(refs) == 0 {
		delete(a.foregroundSubagents, tabID)
	}
}

// clearForegroundSubagents drops every entry for one tab. Called on the
// turn boundary (TurnStarted defensively, TurnDone authoritatively): a
// foreground sub-agent cannot outlive its parent turn, so whatever survived
// there is residue from an interrupted or abnormally ended turn.
func (a *App) clearForegroundSubagents(tabID string) {
	if a == nil || strings.TrimSpace(tabID) == "" {
		return
	}
	a.foregroundSubagentsMu.Lock()
	delete(a.foregroundSubagents, tabID)
	a.foregroundSubagentsMu.Unlock()
}

// moveForegroundSubagents carries a rebound runtime's running foreground
// sub-agents to the new binding. Detach (tab closed with the session kept
// running) and reattach rebind the same controller sink to a different tab;
// without the move, the old tab's entries would linger with no turn left to
// clear them.
func (a *App) moveForegroundSubagents(fromTabID, toTabID string) {
	if a == nil || fromTabID == "" || toTabID == "" || fromTabID == toTabID {
		return
	}
	a.foregroundSubagentsMu.Lock()
	defer a.foregroundSubagentsMu.Unlock()
	refs, ok := a.foregroundSubagents[fromTabID]
	if !ok {
		return
	}
	delete(a.foregroundSubagents, fromTabID)
	target := a.foregroundSubagents[toTabID]
	if target == nil {
		a.foregroundSubagents[toTabID] = refs
		return
	}
	for ref, entry := range refs {
		target[ref] = entry
	}
}

// RunningSubagents returns every process-local running foreground sub-agent,
// across visible and detached runtimes — the same cross-tab breadth the
// background runtime list (task 440) gives jobs. The current tab's capsule
// merges them next to its jobs; other tabs' entries carry their tab title as
// the row origin.
func (a *App) RunningSubagents() []ForegroundSubagentView {
	if a == nil {
		return []ForegroundSubagentView{}
	}
	a.foregroundSubagentsMu.RLock()
	type staged struct {
		tabID string
		entry foregroundSubagentEntry
		ref   string
	}
	stagedEntries := make([]staged, 0, len(a.foregroundSubagents))
	for tabID, refs := range a.foregroundSubagents {
		for ref, entry := range refs {
			stagedEntries = append(stagedEntries, staged{tabID: tabID, entry: entry, ref: ref})
		}
	}
	a.foregroundSubagentsMu.RUnlock()

	// Titles resolve outside the registry lock: a.mu and the registry mutex
	// are never held together. Same title fallback as BackgroundRuntimes.
	titles := make(map[string]string, len(a.tabs)+len(a.detachedSessions))
	a.mu.RLock()
	for _, tab := range a.tabs {
		if tab == nil {
			continue
		}
		title := strings.TrimSpace(tab.TopicTitle)
		if title == "" {
			title = strings.TrimSpace(tab.Label)
		}
		titles[tab.ID] = title
	}
	for _, tab := range a.detachedSessions {
		if tab == nil {
			continue
		}
		title := strings.TrimSpace(tab.TopicTitle)
		if title == "" {
			title = strings.TrimSpace(tab.Label)
		}
		titles[tab.ID] = title
	}
	a.mu.RUnlock()

	out := make([]ForegroundSubagentView, 0, len(stagedEntries))
	for _, item := range stagedEntries {
		lastActivity := item.entry.lastActivity
		if lastActivity == 0 {
			// 旧登记行无活跃面（升级窗口）：退化为起点，静默从起点起算——
			// 宁可早示警也不谎报「刚活跃过」。
			lastActivity = item.entry.startedAt
		}
		out = append(out, ForegroundSubagentView{
			TabID:            item.tabID,
			Title:            titles[item.tabID],
			Ref:              item.ref,
			Name:             item.entry.name,
			Description:      item.entry.description,
			StartedAt:        item.entry.startedAt,
			ParentToolCallID: item.entry.parentToolCallID,
			LastActivityAt:   lastActivity,
		})
	}
	// Deterministic order: start time, then tab, then ref — map iteration is
	// random and the capsule preserves snapshot order within groups.
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt != out[j].StartedAt {
			return out[i].StartedAt < out[j].StartedAt
		}
		if out[i].TabID != out[j].TabID {
			return out[i].TabID < out[j].TabID
		}
		return out[i].Ref < out[j].Ref
	})
	return out
}
