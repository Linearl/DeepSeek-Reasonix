package main

import (
	"errors"
	"sort"

	"reasonix/internal/checkpoint"
	"reasonix/internal/control"
)

type CheckpointMeta struct {
	Turn               int      `json:"turn"`
	Prompt             string   `json:"prompt"`
	Files              []string `json:"files"`     // stable preview of cumulative files RestoreCode would affect from this turn
	FileCount          int      `json:"fileCount"` // full cumulative file count, including entries omitted from Files
	FilesTruncated     bool     `json:"filesTruncated,omitempty"`
	TurnFileCount      int      `json:"turnFileCount"` // files changed during this turn only
	Time               int64    `json:"time"`          // unix milliseconds
	CanCode            bool     `json:"canCode"`
	CanConversation    bool     `json:"canConversation"`
	Coverage           string   `json:"coverage,omitempty"`
	CoverageGaps       []string `json:"coverageGaps,omitempty"`
	ExpiredFilePayload bool     `json:"expiredFilePayload,omitempty"`
	ActiveWriters      int      `json:"activeWriters,omitempty"`
	Legacy             bool     `json:"legacy,omitempty"`
	CanUndoFiles       bool     `json:"canUndoFiles,omitempty"`
	DisabledReason     string   `json:"disabledReason,omitempty"`
}

// RewindPlanView is the desktop-facing prepare result.
type RewindPlanView struct {
	PlanID             string   `json:"planId"`
	Turn               int      `json:"turn"`
	Scope              string   `json:"scope"`
	Coverage           string   `json:"coverage,omitempty"`
	CoverageGaps       []string `json:"coverageGaps,omitempty"`
	Legacy             bool     `json:"legacy,omitempty"`
	ExpiredFilePayload bool     `json:"expiredFilePayload,omitempty"`
	CanFiles           bool     `json:"canFiles"`
	CanConversation    bool     `json:"canConversation"`
	DisabledReason     string   `json:"disabledReason,omitempty"`
	Conflicts          []string `json:"conflicts,omitempty"`
	Files              []string `json:"files,omitempty"`
	FileCount          int      `json:"fileCount"`
	ActiveWriters      int      `json:"activeWriters,omitempty"`
	Path               string   `json:"path,omitempty"`
	ConversationAction string   `json:"conversationAction,omitempty"`
	OK                 bool     `json:"ok"`
	Error              string   `json:"error,omitempty"`
}

// RewindResultView is the desktop-facing commit/undo result.
type RewindResultView struct {
	OK                 bool     `json:"ok"`
	TransactionID      string   `json:"transactionId,omitempty"`
	UndoAvailable      bool     `json:"undoAvailable"`
	Written            []string `json:"written,omitempty"`
	Deleted            []string `json:"deleted,omitempty"`
	ConversationOK     bool     `json:"conversationOk,omitempty"`
	ConversationForked bool     `json:"conversationForked,omitempty"`
	OperationID        string   `json:"operationId,omitempty"`
	Branch             string   `json:"branch,omitempty"`
	Partial            bool     `json:"partial,omitempty"`
	TabID              string   `json:"tabId,omitempty"`
	Tab                *TabMeta `json:"tab,omitempty"`
	Error              string   `json:"error,omitempty"`
	Conflicts          []string `json:"conflicts,omitempty"`
	Coverage           string   `json:"coverage,omitempty"`
}

const checkpointFilePreviewLimit = 60

// Checkpoints lists the session's rewind points, oldest first, for the rewind UI.
func (a *App) Checkpoints() []CheckpointMeta {
	return a.CheckpointsForTab("")
}

func (a *App) CheckpointsForTab(tabID string) []CheckpointMeta {
	a.mu.RLock()
	var ctrl control.SessionAPI
	if tab := a.tabByIDLocked(tabID); tab != nil {
		ctrl = tab.Ctrl
	}
	a.mu.RUnlock()
	if ctrl == nil {
		return []CheckpointMeta{}
	}
	metas := ctrl.Checkpoints()
	out := make([]CheckpointMeta, 0, len(metas))
	for _, m := range metas {
		gaps := make([]string, 0, len(m.CoverageGaps))
		for _, g := range m.CoverageGaps {
			if g.Detail != "" {
				gaps = append(gaps, g.Reason+": "+g.Detail)
			} else {
				gaps = append(gaps, g.Reason)
			}
		}
		cov := string(m.Coverage)
		meta := CheckpointMeta{
			Turn:               m.Turn,
			Prompt:             m.Prompt,
			Files:              m.Paths,
			TurnFileCount:      len(m.Paths),
			Time:               m.Time.UnixMilli(),
			CanCode:            len(m.Paths) > 0 && m.CanUndoFiles,
			CanConversation:    ctrl.CheckpointHasBoundary(m.Turn),
			Coverage:           cov,
			CoverageGaps:       gaps,
			ExpiredFilePayload: m.ExpiredFilePayload,
			ActiveWriters:      len(m.ActiveWriters),
			Legacy:             m.Legacy,
			CanUndoFiles:       m.CanUndoFiles,
			DisabledReason:     m.DisabledReason,
		}
		out = append(out, meta)
	}
	// RestoreCode(turn) reverts every file touched in this turn or any later one, so
	// a turn can rewind code even when it changed no files itself — as long as a
	// later turn did. Propagate CanCode backwards over the oldest-first list.
	// Also propagate the cumulative unique file count so the UI shows how many
	// files RestoreCode would actually affect from this turn.
	hasCodeAfter := false
	canCodeAfter := true
	codeFileSet := make(map[string]bool, len(metas)*2)
	codeFilePreview := []string{}
	//nolint:modernize // slices.Backward yields element copies; this body writes through the index.
	for i := len(out) - 1; i >= 0; i-- {
		if len(out[i].Files) > 0 {
			hasCodeAfter = true
			if !out[i].CanUndoFiles {
				canCodeAfter = false
			}
		}
		for _, f := range out[i].Files {
			if codeFileSet[f] {
				continue
			}
			codeFileSet[f] = true
			codeFilePreview = insertCheckpointFilePreview(codeFilePreview, f, checkpointFilePreviewLimit)
		}
		out[i].CanCode = hasCodeAfter && canCodeAfter
		out[i].FileCount = len(codeFileSet)
		out[i].Files = append([]string{}, codeFilePreview...)
		out[i].FilesTruncated = out[i].FileCount > len(out[i].Files)
	}
	return out
}

func insertCheckpointFilePreview(preview []string, path string, limit int) []string {
	if limit <= 0 || path == "" {
		return preview
	}
	idx := sort.SearchStrings(preview, path)
	if idx < len(preview) && preview[idx] == path {
		return preview
	}
	if len(preview) < limit {
		preview = append(preview, "")
		copy(preview[idx+1:], preview[idx:])
		preview[idx] = path
		return preview
	}
	if idx >= limit {
		return preview
	}
	copy(preview[idx+1:], preview[idx:limit-1])
	preview[idx] = path
	return preview
}

// ToolResultForTab returns the full arguments and output for one tool call that
// were elided from the frontend's in-memory items[] for memory efficiency. The
// caller (frontend ToolCard) loads this on demand when the user expands a
// collapsed tool card. Returns nil when the tool ID is not found.
func (a *App) ToolResultForTab(tabID, toolID string) *control.ToolResultData {
	a.mu.RLock()
	var ctrl control.SessionAPI
	if tab := a.tabByIDLocked(tabID); tab != nil {
		ctrl = tab.Ctrl
	}
	a.mu.RUnlock()
	if ctrl == nil {
		return nil
	}
	return ctrl.ToolResult(toolID)
}

// Rewind restores the session to the start of turn. scope is "code",
// "conversation", or "both" (anything else is treated as "both"). The frontend
// re-reads History after this resolves.
func (a *App) Rewind(turn int, scope string) error {
	return a.RewindForTab("", turn, scope)
}

// RewindForTab rewinds the requested tab instead of resolving the active tab at
// execution time, which may have changed after frontend confirmation.
// Compatibility wrapper over the structured fork-first path. Conversation
// rewind opens the fork as a new tab; it never retargets the source controller.
func (a *App) RewindForTab(tabID string, turn int, scope string) error {
	result := a.CommitRewindForTab(tabID, "", turn, scope)
	if result.OK {
		return nil
	}
	return errors.New(nonEmptyStr(result.Error, "rewind failed"))
}

// PreviewRewindForTab returns a structured precheck without mutating state.
func (a *App) PreviewRewindForTab(tabID string, turn int, scope string) RewindPlanView {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if a.tabIsReadOnly(tab) {
		return RewindPlanView{OK: false, Error: readOnlyChannelErr().Error()}
	}
	if ctrl == nil {
		return RewindPlanView{OK: false, Error: "no controller"}
	}
	s := control.RewindBoth
	switch scope {
	case "code":
		s = control.RewindCode
	case "conversation":
		s = control.RewindConversation
	}
	plan, err := ctrl.PrepareRewind(turn, s)
	view := rewindPlanToView(plan, scope)
	if err != nil {
		view.OK = false
		view.Error = err.Error()
		return view
	}
	view.OK = true
	return view
}

// PreviewWorkspaceFileRevertForTab prepares a single-file session-owned revert.
func (a *App) PreviewWorkspaceFileRevertForTab(tabID, path string) RewindPlanView {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if a.tabIsReadOnly(tab) {
		return RewindPlanView{OK: false, Error: readOnlyChannelErr().Error(), Path: path}
	}
	if ctrl == nil {
		return RewindPlanView{OK: false, Error: "no controller", Path: path}
	}
	plan, err := ctrl.PrepareFileRevert(path)
	view := rewindPlanToView(plan, "code")
	view.Path = path
	if err != nil {
		view.OK = false
		view.Error = err.Error()
		return view
	}
	view.OK = plan.CanFiles || len(plan.Conflicts) > 0
	return view
}

// CommitWorkspaceFileRevertForTab commits a single-file revert.
// resolution is "keep_current" or "overwrite_checkpoint".
func (a *App) CommitWorkspaceFileRevertForTab(tabID, planID, resolution string) RewindResultView {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if a.tabIsReadOnly(tab) {
		return RewindResultView{OK: false, Error: readOnlyChannelErr().Error()}
	}
	if ctrl == nil {
		return RewindResultView{OK: false, Error: "no controller"}
	}
	res := checkpoint.ConflictResolution("")
	switch resolution {
	case "keep_current":
		res = checkpoint.ResolveKeepCurrent
	case "overwrite_checkpoint":
		res = checkpoint.ResolveOverwriteCheckpoint
	}
	result, err := ctrl.CommitFileRevert(planID, res)
	view := rewindResultToView(result)
	if err != nil {
		view.OK = false
		if view.Error == "" {
			view.Error = err.Error()
		}
	}
	return view
}

func rewindPlanToView(plan checkpoint.RewindPlan, scope string) RewindPlanView {
	gaps := make([]string, 0, len(plan.CoverageGaps))
	for _, g := range plan.CoverageGaps {
		if g.Detail != "" {
			gaps = append(gaps, g.Reason+": "+g.Detail)
		} else {
			gaps = append(gaps, g.Reason)
		}
	}
	return RewindPlanView{
		PlanID:             plan.PlanID,
		Turn:               plan.Turn,
		Scope:              scope,
		Coverage:           string(plan.Coverage),
		CoverageGaps:       gaps,
		Legacy:             plan.Legacy,
		ExpiredFilePayload: plan.ExpiredFilePayload,
		CanFiles:           plan.CanFiles,
		CanConversation:    plan.CanConversation,
		DisabledReason:     plan.DisabledReason,
		Conflicts:          conflictStrings(plan),
		Files:              plan.Files,
		FileCount:          plan.FileCount,
		ActiveWriters:      len(plan.ActiveWriters),
		Path:               plan.Path,
		ConversationAction: plan.ConversationAction,
	}
}

func conflictStrings(plan checkpoint.RewindPlan) []string {
	out := make([]string, 0, len(plan.Conflicts))
	for _, c := range plan.Conflicts {
		if c.Path != "" {
			out = append(out, c.Path+": "+c.Reason)
		} else {
			out = append(out, c.Reason)
		}
	}
	return out
}

func rewindResultToView(result checkpoint.RewindResult) RewindResultView {
	conflicts := make([]string, 0, len(result.Conflicts))
	for _, c := range result.Conflicts {
		if c.Path != "" {
			conflicts = append(conflicts, c.Path+": "+c.Reason)
		} else {
			conflicts = append(conflicts, c.Reason)
		}
	}
	txID := result.TransactionID
	if result.OperationID != "" {
		txID = result.OperationID
	}
	return RewindResultView{
		OK:                 result.OK,
		TransactionID:      txID,
		OperationID:        nonEmptyStr(result.OperationID, result.TransactionID),
		UndoAvailable:      result.UndoAvailable,
		Written:            result.Written,
		Deleted:            result.Deleted,
		ConversationOK:     result.ConversationOK || result.ConversationForked,
		ConversationForked: result.ConversationForked,
		Branch:             result.Branch,
		Partial:            result.Partial,
		Error:              result.Error,
		Conflicts:          conflicts,
		Coverage:           string(result.Coverage),
	}
}

func nonEmptyStr(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}

// Fork branches the conversation at the start of turn into a new session tab
