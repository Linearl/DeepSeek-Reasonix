package main

import (
	"reasonix/internal/agent"
)

// tl1657: pending-card reconciliation for a compaction that started before the
// frontend could hear about it. The 20261117 09:40 incident: a resume-triggered
// pressure compaction opened its pass at 09:40:28, but the relaunching webview
// had not attached its event subscription yet — CompactionStarted never built a
// card, every CompactionProgress then landed on "no pending card (dropped)", and
// the user watched a minutes-long compaction with zero tokens/tps readout.
// Wails events have no replay, so the frontend reconciles by polling: the tab
// refresh batch calls CompactionStateForTab and synthesizes the pending card
// when Running is true. A pass that already sealed reports Running=false and
// builds nothing — terminal history is not a live card (and its Done/failed
// outcome, if also missed, stays in the transcript/log, not on the wire).

// TabCompactionState mirrors agent.CompactionLiveState in wire shape: the
// same readout the CompactionProgress events carry (tokens = summarizer
// output estimate, fold N/M = chunked-pass progress).
type TabCompactionState struct {
	Running      bool   `json:"running"`
	Trigger      string `json:"trigger"`
	Pass         int    `json:"pass"`
	Tokens       int    `json:"tokens"`
	TokensPerSec int    `json:"tokensPerSec"`
	Done         int    `json:"done"`
	Total        int    `json:"total"`
}

// compactionLiveProbe is the narrow surface the real Controller exposes
// (internal/agent CompactionLiveState); a fake or an older controller without
// the capability simply reports "nothing running" — the pre-tl1657 shape.
type compactionLiveProbe interface {
	CompactionLiveState() agent.CompactionLiveState
}

// CompactionStateForTab reports the tab's in-flight compaction pass, if any.
// Empty tabID answers for the active tab (same convention as ContextUsage).
func (a *App) CompactionStateForTab(tabID string) TabCompactionState {
	_, ctrl := a.tabAndCtrlForUsage(tabID)
	probe, ok := ctrl.(compactionLiveProbe)
	if !ok {
		return TabCompactionState{}
	}
	st := probe.CompactionLiveState()
	return TabCompactionState{
		Running:      st.Running,
		Trigger:      st.Trigger,
		Pass:         st.Pass,
		Tokens:       st.Tokens,
		TokensPerSec: st.TokensPerSec,
		Done:         st.Done,
		Total:        st.Total,
	}
}
