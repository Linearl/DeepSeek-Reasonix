package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/sessioncatalog"
)

type RecoveryLineageMember struct {
	Path            string `json:"path"`
	VersionKind     string `json:"versionKind,omitempty"`
	VersionState    string `json:"versionState,omitempty"`
	ParentVersionID string `json:"parentVersionId,omitempty"`
	Role            string `json:"role"`
	Canonical       bool   `json:"canonical"`
	Turns           int    `json:"turns"`
	Open            bool   `json:"open"`
	Running         bool   `json:"running"`
	VersionNote     string `json:"versionNote,omitempty"`
	Preview         string `json:"preview,omitempty"`
	CreatedAt       int64  `json:"createdAt,omitempty"`
	LastActivityAt  int64  `json:"lastActivityAt,omitempty"`
	// Head fields are set when the version is a head inside one schema-2 log.
	HeadID   string `json:"headId,omitempty"`
	HeadKind string `json:"headKind,omitempty"`
	HeadName string `json:"headName,omitempty"`
	Selected bool   `json:"selected,omitempty"`
}

type RecoveryLineageView struct {
	GroupID         string                  `json:"groupId"`
	State           string                  `json:"state"`
	BranchCount     int                     `json:"branchCount"`
	Unresolved      int                     `json:"unresolved"`
	CleanupEligible int                     `json:"cleanupEligible"`
	Members         []RecoveryLineageMember `json:"members"`
}

type SessionVersionStateView struct {
	ConversationID    string              `json:"conversationId,omitempty"`
	ActiveVersionID   string              `json:"activeVersionId,omitempty"`
	ActivePath        string              `json:"activePath,omitempty"`
	RecoveryVersionID string              `json:"recoveryVersionId,omitempty"`
	CanContinue       bool                `json:"canContinue"`
	RequiresChoice    bool                `json:"requiresChoice"`
	Lineage           RecoveryLineageView `json:"lineage"`
}

// GetSessionVersionState exposes the logical conversation and its physical
// recovery versions without making the physical paths ordinary sessions.
func (a *App) GetSessionVersionState(key ProjectTopicKey) SessionVersionStateView {
	view := a.GetRecoveryLineage(key)
	if view.Members == nil {
		view.Members = []RecoveryLineageMember{}
	}
	out := SessionVersionStateView{Lineage: view, CanContinue: true}
	out.ConversationID = key.TopicID
	for _, member := range view.Members {
		if member.Canonical {
			out.ActivePath = member.Path
			out.ActiveVersionID = agent.BranchID(member.Path)
			if member.HeadID != "" {
				out.ActiveVersionID = member.HeadID
			}
			break
		}
	}
	if key.Path != "" {
		out.ActivePath = key.Path
		if view.State != sessionHeadLineageState {
			out.ActiveVersionID = agent.BranchID(key.Path)
		}
	}
	out.RequiresChoice = view.State == "diverged" && view.Unresolved > 0
	if out.ActivePath != "" {
		for _, member := range view.Members {
			if sameRecoveryLineagePath(member.Path, out.ActivePath) && member.Role == sessioncatalog.RecoveryRoleDiverged {
				out.RecoveryVersionID = agent.BranchID(member.Path)
			}
		}
	}
	return out
}

// ReconcileRecoveryVersions refreshes one logical conversation and applies the
// existing covered-copy sweep. It is idempotent and keeps diverged content.
func (a *App) ReconcileRecoveryVersions(key ProjectTopicKey) error {
	catalog := a.sessionCatalog.Load()
	if catalog == nil {
		return errors.New("session catalog is unavailable")
	}
	topic, ok, err := catalog.GetTopic(a.bootContext(), sessioncatalog.TopicKey{Scope: key.Scope, WorkspaceRoot: key.WorkspaceRoot, TopicID: key.TopicID})
	if err != nil || !ok {
		return errors.New("session version lineage is unavailable")
	}
	_, dir, ok := recoveryLineageSelection(topic, key.Path)
	if !ok {
		return nil
	}
	target := sessioncatalog.DirectoryTarget{Path: dir, Scope: key.Scope, WorkspaceRoot: key.WorkspaceRoot}
	if err := catalog.ReconcileDirectory(a.bootContext(), target); err != nil {
		return err
	}
	a.sweepExcessRecoveryCopies(catalog, target)
	a.emitProjectTreeChangedForSessionDirs(dir)
	return nil
}

// SetActiveSessionVersion selects and opens a recovery version on the existing
// topic tab. It rejects subagent transcripts and preserves the logical topic.
//
// tl1430 semantics: a switch is the adoption of one transcript file as the
// conversation's authoritative main line. Events are append-only - nothing on
// either side of the switch is rewritten, moved, or deleted; the switch itself
// is recorded as a head_switch event on the adopted transcript, the tab hot-
// switches its projection by replaying the new file, and the reopened
// conversation reads the new main line from the persisted preference. Every
// path either really switches or returns an explicit error; a silent no-op is
// forbidden.
func (a *App) SetActiveSessionVersion(req RecoveryPreferenceRequest) error {
	a.sessionVersionActivationMu.Lock()
	defer a.sessionVersionActivationMu.Unlock()
	// tl1430: the in-log head switch left with the tl1280 write-path slow
	// lane. A request naming a head id can only come from a stale caller, and
	// honoring it used to drop the id, reconcile nothing, and report success.
	if strings.TrimSpace(req.HeadID) != "" {
		return errors.New("in-log head switching was retired; switch versions by transcript file")
	}
	if strings.TrimSpace(req.Path) == "" {
		return errors.New("session version is unavailable")
	}
	meta, ok, err := agent.LoadBranchMeta(req.Path)
	if err != nil || !ok {
		return errors.New("session version is unavailable")
	}
	if meta.EffectiveVersionKind() == agent.VersionSubagent {
		return errors.New("subagent transcripts cannot become the active conversation version")
	}
	catalog := a.sessionCatalog.Load()
	if catalog == nil {
		return errors.New("session catalog is unavailable")
	}
	// tl1430 direction D: the versions panel is path-driven and used to
	// hardcode scope=global with an empty topic id, so the lineage gate could
	// never pass for a conversation the catalog files elsewhere. Resolve the
	// owning topic from the catalog filing when the caller does not name one.
	scope, workspaceRoot, topicID := strings.TrimSpace(req.Scope), strings.TrimSpace(req.WorkspaceRoot), strings.TrimSpace(req.TopicID)
	if topicID == "" {
		resolveCtx, cancel := a.catalogReadContext()
		record, found, lookupErr := catalog.GetSession(resolveCtx, req.Path)
		cancel()
		if lookupErr != nil || !found || strings.TrimSpace(record.LogicalTopicID) == "" {
			return errors.New("the selected version does not belong to a known conversation")
		}
		scope, workspaceRoot = normalizeDesktopTopicScope(record.Scope, record.WorkspaceRoot)
		topicID = strings.TrimSpace(record.LogicalTopicID)
		req.Scope, req.WorkspaceRoot, req.TopicID = scope, workspaceRoot, topicID
	}
	topic, ok, err := catalog.GetTopic(a.bootContext(), sessioncatalog.TopicKey{Scope: scope, WorkspaceRoot: workspaceRoot, TopicID: topicID})
	if err != nil || !ok {
		return errors.New("recovery lineage is unavailable")
	}
	groupID, _, ok := recoveryLineageSelection(topic, req.Path)
	if !ok || groupID == "" {
		return errors.New("selected version is outside the recovery lineage")
	}
	memberFound := false
	// The line giving way (the from side of the head_switch marker) is the
	// line the user is actually on before the switch: the open tab's current
	// transcript when one holds this topic, else the group's preferred/
	// canonical member, else the group's sole normal (non-recovered)
	// transcript - the flat-form main line - else the topic representative
	// if it belongs to this group. The representative alone is a sort
	// artifact in a diverged lineage and may name the very row being
	// activated; it must never masquerade as the incumbent main line
	// (tl1430 test-caught defect).
	memberPreferred := ""
	memberNormal := ""
	for _, member := range topic.Sessions {
		if !recoveryRecordBelongsToGroup(member, groupID) {
			continue
		}
		if sameRecoveryLineagePath(member.Path, req.Path) && member.RecoveryRole != sessioncatalog.RecoveryRoleCoveredCopy {
			memberFound = true
		}
		// The explicit choice surfaces at filing level as the preferred role
		// (promoteCanonicalLeaves translates meta.RecoveryPreferred into
		// RecoveryRolePreferred); the bare record flag and the ordinary-list
		// folding marker (RecoveryCanonical) are not the user's line.
		if memberPreferred == "" && (member.RecoveryPreferred || member.RecoveryRole == sessioncatalog.RecoveryRolePreferred) {
			memberPreferred = member.Path
		}
		if memberNormal == "" && !member.Recovered && member.RecoveryRole != sessioncatalog.RecoveryRoleCoveredCopy {
			memberNormal = member.Path
		}
	}
	if !memberFound {
		return errors.New("selected version is outside the recovery lineage")
	}
	a.mu.RLock()
	var tabID string
	var tabFromPath string
	for _, tab := range a.runtimeTabsLocked() {
		if tab == nil || tab.TopicID != req.TopicID || tab.Scope != req.Scope ||
			(req.Scope == "project" && tab.WorkspaceRoot != req.WorkspaceRoot) {
			continue
		}
		tabID = tab.ID
		if candidate := strings.TrimSpace(tab.currentSessionPath()); candidate != "" && recoveryTopicHasGroupMember(topic, groupID, candidate) {
			tabFromPath = candidate
		}
		break
	}
	a.mu.RUnlock()
	fromPath := tabFromPath
	if fromPath == "" {
		fromPath = memberPreferred
	}
	if fromPath == "" {
		fromPath = memberNormal
	}
	if fromPath == "" {
		if representative := strings.TrimSpace(topic.RepresentativePath); representative != "" && recoveryTopicHasGroupMember(topic, groupID, representative) {
			fromPath = representative
		}
	}
	if fromPath == "" {
		// A lineage whose main transcript is gone (fork-only): record the
		// logical group identity as the from side instead of inventing a
		// path for a file that does not exist.
		fromPath = groupID
	}
	// "Already the main line" answers against the two authorities for the
	// line the user interacts with: the open tab's transcript and the
	// explicit preference. The catalog's diverged-group canonical marker and
	// the representative are ordinary-list folding artifacts - they may name
	// the very row being activated while the user's line is elsewhere, so
	// they must never trigger a false already-there refusal (tl1430
	// test-caught defect).
	if (tabFromPath != "" && sameRecoveryLineagePath(tabFromPath, req.Path)) ||
		(memberPreferred != "" && sameRecoveryLineagePath(memberPreferred, req.Path)) {
		return errors.New("this version is already the conversation's main line")
	}
	if tabID != "" {
		// tl1430 hot-switch gating: an idle tab rehydrates onto the new main
		// line in place (no close/reopen); a tab with work in flight stays on
		// its line and the user gets an explicit refusal instead of a silent
		// or disruptive swap.
		a.mu.RLock()
		runningTab := a.tabs[tabID]
		a.mu.RUnlock()
		if runningTab != nil && runningTab.Ctrl != nil {
			if state := controllerRuntimeState(runningTab.Ctrl); state.ActiveWork() {
				return errors.New("the current main line is still working; wait for its turn to finish, then switch again")
			}
		}
		if _, err := a.ResumeSessionForTabExact(tabID, req.Path); err != nil {
			return err
		}
	}
	if err := a.ChooseRecoveryBranch(req); err != nil {
		return err
	}
	// tl1430: record the switch as appended history on the adopted transcript.
	// The from side stays untouched (append-only); a failure here leaves the
	// switch itself in effect but must surface, never disappear silently.
	if err := agent.AppendSessionHeadSwitchEvent(req.Path, fromPath, req.Path); err != nil {
		return fmt.Errorf("the switch succeeded but recording the head_switch event failed: %w", err)
	}
	a.emitRuntimeEvent("session:active-version-changed", sessionRecoveryEvent{
		ConversationID: req.TopicID, ActiveVersionID: agent.BranchID(req.Path),
		RecoveryVersionID: agent.BranchID(req.Path), Scope: req.Scope,
		WorkspaceRoot: req.WorkspaceRoot, TopicID: req.TopicID,
		CanContinue: true, RequiresChoice: false,
	})
	return nil
}

// recoveryAuthorityMainPath resolves a conversation's authoritative main line
// from the same two authorities the already-main gate above reads (tl1415c A):
// the open tab's current transcript when one holds this topic, else the
// group's explicit preferred member. The panel's chain inventory (Selected,
// recovery_chains.go) reads THIS resolver instead of re-deriving its own
// answer, so the visual main line and the system main line can no longer
// split — the split the user实测 reported as "下面那个是主线为何还能切换
// 到此版本" (tab on a copy file, panel still marking the scan origin).
//
// Returns "" when no authority resolves here: the path is not filed in the
// catalog, or the group has no tab on it and no explicit preference. Callers
// keep their pre-tl1415c fallback (meta preferred → scan-origin main) for
// that shape — unfiled paths are exactly the cases the catalog cannot speak
// for, and inventing a verdict would be the second source of truth again.
func (a *App) recoveryAuthorityMainPath(catalog *sessioncatalog.Catalog, mainPath string) string {
	resolveCtx, cancel := a.catalogReadContext()
	record, found, err := catalog.GetSession(resolveCtx, mainPath)
	cancel()
	if err != nil || !found || strings.TrimSpace(record.LogicalTopicID) == "" {
		return ""
	}
	scope, workspaceRoot := normalizeDesktopTopicScope(record.Scope, record.WorkspaceRoot)
	topicID := strings.TrimSpace(record.LogicalTopicID)
	topic, ok, err := catalog.GetTopic(a.bootContext(), sessioncatalog.TopicKey{Scope: scope, WorkspaceRoot: workspaceRoot, TopicID: topicID})
	if err != nil || !ok {
		return ""
	}
	groupID, _, ok := recoveryLineageSelection(topic, mainPath)
	if !ok || groupID == "" {
		return ""
	}
	// Same tab rule as the switch gate: the tab holding this topic whose
	// current transcript belongs to this group names the line the user is
	// actually on — authority #1.
	a.mu.RLock()
	authPath := ""
	for _, tab := range a.runtimeTabsLocked() {
		if tab == nil || tab.TopicID != topicID || tab.Scope != scope ||
			(scope == "project" && tab.WorkspaceRoot != workspaceRoot) {
			continue
		}
		if candidate := strings.TrimSpace(tab.currentSessionPath()); candidate != "" && recoveryTopicHasGroupMember(topic, groupID, candidate) {
			authPath = candidate
		}
		break
	}
	a.mu.RUnlock()
	if authPath != "" {
		return authPath
	}
	// Authority #2: the explicit preference at filing level (same predicate as
	// memberPreferred in SetActiveSessionVersion).
	for _, member := range topic.Sessions {
		if !recoveryRecordBelongsToGroup(member, groupID) {
			continue
		}
		if member.RecoveryPreferred || member.RecoveryRole == sessioncatalog.RecoveryRolePreferred {
			return member.Path
		}
	}
	return ""
}

// RetrySessionRecovery re-arms a pending recovery version after its lease
// owner has gone away, then routes through the same validated activation path.
func (a *App) RetrySessionRecovery(req RecoveryPreferenceRequest) error {
	meta, ok, err := agent.LoadBranchMeta(req.Path)
	if err != nil || !ok || meta.EffectiveVersionKind() != agent.VersionRecovery {
		return errors.New("session recovery version is unavailable")
	}
	if err := agent.UpdateBranchMeta(req.Path, false, func(next *agent.BranchMeta) error {
		next.VersionKind = agent.VersionRecovery
		next.VersionState = agent.VersionActive
		return nil
	}); err != nil {
		return err
	}
	if err := a.SetActiveSessionVersion(req); err != nil {
		_ = agent.UpdateBranchMeta(req.Path, false, func(next *agent.BranchMeta) error {
			next.VersionState = agent.VersionPending
			return nil
		})
		return err
	}
	return nil
}

type RecoveryCleanupRequest struct {
	Scope         string `json:"scope"`
	WorkspaceRoot string `json:"workspaceRoot,omitempty"`
	TopicID       string `json:"topicId"`
	Apply         bool   `json:"apply"`
}

type RecoveryPreferenceRequest struct {
	Scope         string `json:"scope"`
	WorkspaceRoot string `json:"workspaceRoot,omitempty"`
	TopicID       string `json:"topicId"`
	Path          string `json:"path"`
	HeadID        string `json:"headId,omitempty"` // a head of the schema-2 log at Path
}

type RecoveryCleanupItem struct {
	Path   string `json:"path"`
	HeadID string `json:"headId,omitempty"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type RecoveryCleanupResult struct {
	Eligible int                   `json:"eligible"`
	Moved    int                   `json:"moved"`
	Busy     int                   `json:"busy"`
	Kept     int                   `json:"kept"`
	DryRun   bool                  `json:"dryRun"`
	Items    []RecoveryCleanupItem `json:"items"`
}

// GetRecoveryLineage lists a conversation's versions: the heads of its
// schema-2 log, the schema-1 recovery copies of its lineage, or both for a
// family whose root was upgraded after copies had been made.
func (a *App) GetRecoveryLineage(key ProjectTopicKey) RecoveryLineageView {
	out := RecoveryLineageView{Members: []RecoveryLineageMember{}}
	if a.catalogRebuilding.Load() {
		out.State = "repairing"
		return out
	}
	catalog := a.sessionCatalog.Load()
	if catalog == nil {
		return out
	}
	topic, ok, err := catalog.GetTopic(a.bootContext(), sessioncatalog.TopicKey{Scope: key.Scope, WorkspaceRoot: key.WorkspaceRoot, TopicID: key.TopicID})
	if err != nil || !ok {
		return out
	}
	out = a.fileRecoveryLineage(catalog, topic, key.Path)
	if heads, ok := a.sessionHeadLineage(topic, key.Path); ok {
		out = mergeHeadLineage(out, heads)
	}
	if key.RecordClassification {
		recordRecoveryLineageClassification(key.Path, out)
	}
	return out
}

func (a *App) fileRecoveryLineage(catalog *sessioncatalog.Catalog, topic sessioncatalog.TopicRecord, selectedPath string) RecoveryLineageView {
	out := RecoveryLineageView{Members: []RecoveryLineageMember{}}
	groupID, directory, ok := recoveryLineageSelection(topic, selectedPath)
	if !ok {
		return out
	}
	groups, err := catalog.ListRecoveryGroups(a.bootContext(), directory)
	if err != nil {
		return out
	}
	groupFound := false
	for _, group := range groups {
		if group.ID == groupID {
			out.State = group.State
			groupFound = true
			break
		}
	}
	if !groupFound {
		return RecoveryLineageView{Members: []RecoveryLineageMember{}}
	}
	out.GroupID = groupID
	_, overlays := a.catalogRuntimeOverlays()
	representativeInGroup := false
	for _, record := range topic.Sessions {
		if recoveryRecordBelongsToGroup(record, groupID) && sameRecoveryLineagePath(record.Path, topic.RepresentativePath) {
			representativeInGroup = true
			break
		}
	}
	for _, record := range topic.Sessions {
		if !recoveryRecordBelongsToGroup(record, groupID) {
			continue
		}
		overlay := overlays[sessionRuntimeKey(record.Path)]
		versionNote := record.CustomTitle
		versionKind := "recovery"
		versionState := "active"
		parentVersionID := record.ParentID
		if meta, ok, err := agent.LoadBranchMeta(record.Path); err == nil && ok {
			versionNote = meta.CustomTitle
			versionKind = string(meta.EffectiveVersionKind())
			versionState = string(meta.EffectiveVersionState())
			parentVersionID = meta.ParentVersionID
		}
		canonical := record.RecoveryCanonical
		if representativeInGroup {
			canonical = sameRecoveryLineagePath(record.Path, topic.RepresentativePath)
		}
		out.Members = append(out.Members, RecoveryLineageMember{
			Path: record.Path, VersionKind: versionKind, VersionState: versionState,
			ParentVersionID: parentVersionID, Role: record.RecoveryRole, Canonical: canonical,
			Turns: record.Turns, Open: overlay.open, Running: overlay.running,
			VersionNote: versionNote, Preview: record.Preview,
			CreatedAt: record.CreatedAt, LastActivityAt: record.LastActivityAt,
		})
		out.BranchCount++
		if record.RecoveryRole == sessioncatalog.RecoveryRoleDiverged {
			out.Unresolved++
		}
		if record.RecoveryRole == sessioncatalog.RecoveryRoleCoveredCopy {
			out.CleanupEligible++
		}
	}
	if out.State == "" {
		out.State = topic.RecoveryState
	}
	if out.State == "preferred" {
		out.Unresolved = 0
	}
	// The lower-level group API historically calls an all-covered lineage
	// "repairing". Expose its stable state so event consumers can clear pending
	// recovery notifications without polling forever.
	if recoveryLineageIsCovered(out) {
		out.State = "covered"
	}
	return out
}

func recordRecoveryLineageClassification(selectedPath string, view RecoveryLineageView) {
	outcome := ""
	switch view.State {
	case "covered", "adopted", "preferred", "diverged":
		outcome = "classified_" + view.State
	default:
		return
	}
	path := ""
	for _, member := range view.Members {
		if sameRecoveryLineagePath(member.Path, selectedPath) {
			path = member.Path
			break
		}
		if path == "" || member.Canonical {
			path = member.Path
		}
	}
	control.RecordRecoveryLifecycle(path, outcome)
}

func recoveryLineageSelection(topic sessioncatalog.TopicRecord, selectedPath string) (string, string, bool) {
	if sessioncatalog.PathIdentityKey(selectedPath) != "" {
		for _, record := range topic.Sessions {
			if !sameRecoveryLineagePath(record.Path, selectedPath) {
				continue
			}
			groupID := record.RecoveryGroupID
			if !record.Recovered {
				groupID = agent.BranchID(record.Path)
			}
			if groupID != "" && recoveryTopicHasGroup(topic, groupID) {
				return groupID, filepath.Dir(record.Path), true
			}
			return "", "", false
		}
		return "", "", false
	}

	groupID, directory := "", ""
	for _, record := range topic.Sessions {
		if !record.Recovered || record.RecoveryGroupID == "" {
			continue
		}
		if groupID != "" && groupID != record.RecoveryGroupID {
			// An older frontend cannot safely choose between multiple groups.
			return "", "", false
		}
		groupID, directory = record.RecoveryGroupID, filepath.Dir(record.Path)
	}
	return groupID, directory, groupID != "" && directory != ""
}

func sameRecoveryLineagePath(left, right string) bool {
	leftKey := sessioncatalog.PathIdentityKey(left)
	return leftKey != "" && leftKey == sessioncatalog.PathIdentityKey(right)
}

func recoveryTopicHasGroup(topic sessioncatalog.TopicRecord, groupID string) bool {
	for _, record := range topic.Sessions {
		if record.Recovered && record.RecoveryGroupID == groupID {
			return true
		}
	}
	return false
}

// recoveryTopicHasGroupMember reports whether one concrete path belongs to the
// lineage group via the same membership rule the group filter uses.
func recoveryTopicHasGroupMember(topic sessioncatalog.TopicRecord, groupID, path string) bool {
	for _, record := range topic.Sessions {
		if recoveryRecordBelongsToGroup(record, groupID) && sameRecoveryLineagePath(record.Path, path) {
			return true
		}
	}
	return false
}

func recoveryRecordBelongsToGroup(record sessioncatalog.SessionRecord, groupID string) bool {
	if record.Recovered {
		return record.RecoveryGroupID == groupID
	}
	return agent.BranchID(record.Path) == groupID
}

func recoveryLineageIsCovered(view RecoveryLineageView) bool {
	if view.State != "repairing" || view.CleanupEligible == 0 {
		return false
	}
	for _, member := range view.Members {
		if member.Role != sessioncatalog.RecoveryRoleNormal && member.Role != sessioncatalog.RecoveryRoleCoveredCopy {
			return false
		}
	}
	return true
}

// ChooseRecoveryBranch changes only the default open target. Diverged content
// remains on disk and is never made cleanup-eligible by this choice.
func (a *App) ChooseRecoveryBranch(req RecoveryPreferenceRequest) error {
	if strings.TrimSpace(req.HeadID) != "" {
		// tl1430: in-log head switching left with the tl1280 slow lane; the
		// leftover branch used to drop the id and report success silently.
		return errors.New("in-log head switching was retired; switch versions by transcript file")
	}
	catalog := a.sessionCatalog.Load()
	if catalog == nil {
		return errors.New("session catalog is unavailable")
	}
	topic, ok, err := catalog.GetTopic(a.bootContext(), sessioncatalog.TopicKey{Scope: req.Scope, WorkspaceRoot: req.WorkspaceRoot, TopicID: req.TopicID})
	if err != nil || !ok {
		return errors.New("recovery lineage is unavailable")
	}
	groupID, dir, ok := recoveryLineageSelection(topic, req.Path)
	if !ok {
		return errors.New("selected branch is outside the recovery lineage")
	}
	groups, err := catalog.ListRecoveryGroups(a.bootContext(), dir)
	if err != nil {
		return errors.New("recovery lineage is unavailable")
	}
	paths := []string{}
	chosen := ""
	foundGroup := false
	for _, group := range groups {
		if group.ID == groupID {
			foundGroup = true
			break
		}
	}
	if !foundGroup {
		return errors.New("recovery lineage is unavailable")
	}
	for _, member := range topic.Sessions {
		if !recoveryRecordBelongsToGroup(member, groupID) {
			continue
		}
		paths = append(paths, member.Path)
		if sameRecoveryLineagePath(member.Path, req.Path) && member.RecoveryRole != sessioncatalog.RecoveryRoleCoveredCopy {
			chosen = member.Path
		}
	}
	if chosen == "" {
		return errors.New("selected branch is outside the recovery lineage")
	}
	if err := func() error {
		defer a.lockRuntimeMutation("choose-recovery-branch")()
		a.sessionRemovalMu.Lock()
		defer a.sessionRemovalMu.Unlock()
		return agent.SetRecoveryPreferred(paths, chosen)
	}(); err != nil {
		return errors.New("could not save the recovery branch choice")
	}
	// The rescan reads session files and rewrites only the catalog projection,
	// so it needs neither barrier; only the preference write above must stay
	// atomic with respect to session removal.
	if err := catalog.ReconcileDirectory(a.bootContext(), sessioncatalog.DirectoryTarget{Path: dir, Scope: req.Scope, WorkspaceRoot: req.WorkspaceRoot}); err != nil {
		return errors.New("the branch choice was saved but the session catalog could not refresh")
	}
	a.emitProjectTreeChangedForSessionDirs(dir)
	return nil
}

// CleanRecoveryLineage performs one backend-owned, revalidated cleanup batch.
// It never purges and never moves diverged content.
func (a *App) CleanRecoveryLineage(req RecoveryCleanupRequest) RecoveryCleanupResult {
	result := RecoveryCleanupResult{DryRun: !req.Apply, Items: []RecoveryCleanupItem{}}
	catalog := a.sessionCatalog.Load()
	if catalog == nil {
		return result
	}
	topic, ok, err := catalog.GetTopic(a.bootContext(), sessioncatalog.TopicKey{Scope: req.Scope, WorkspaceRoot: req.WorkspaceRoot, TopicID: req.TopicID})
	if err != nil || !ok {
		return result
	}
	if heads, ok := a.cleanTopicHeads(req, topic); ok {
		return heads
	}
	canonical, rootID := recoveryCleanupCanonical(topic)
	if canonical == "" || rootID == "" {
		return result
	}
	dir := filepath.Dir(canonical)
	groups, err := catalog.ListRecoveryGroups(a.bootContext(), dir)
	if err != nil {
		return result
	}
	members := []sessioncatalog.SessionRecord{}
	for _, group := range groups {
		if group.ID == rootID {
			members = group.Members
			break
		}
	}
	candidates := []sessioncatalog.SessionRecord{}
	for _, record := range members {
		if record.Path == canonical || record.RecoveryRole != sessioncatalog.RecoveryRoleCoveredCopy {
			continue
		}
		result.Eligible++
		candidates = append(candidates, record)
		result.Items = append(result.Items, RecoveryCleanupItem{Path: record.Path, Status: "eligible"})
	}
	if !req.Apply || len(candidates) == 0 {
		return result
	}
	defer a.lockRuntimeMutation("clean-recovery-lineage")()
	a.sessionRemovalMu.Lock()
	defer a.sessionRemovalMu.Unlock()
	if a.sessionOpenInAnyTab(canonical) || agent.SessionLeaseHeld(canonical) {
		for index := range result.Items {
			result.Items[index].Status = "busy"
			result.Busy++
		}
		return result
	}
	if err := agent.ReparentRecoveryCanonical(canonical, rootID, dir); err != nil {
		for index := range result.Items {
			if errors.Is(err, agent.ErrSessionLeaseHeld) {
				result.Items[index].Status = "busy"
				result.Busy++
			} else {
				result.Items[index].Status = "kept"
				result.Items[index].Error = "recovery branch changed and was kept"
				result.Kept++
			}
		}
		return result
	}
	for index, record := range candidates {
		item := &result.Items[index]
		if a.sessionOpenInAnyTab(record.Path) || agent.SessionLeaseHeld(record.Path) {
			item.Status = "busy"
			result.Busy++
			continue
		}
		if err := agent.TrashRecoveryBranchCoveredBy(record.Path, canonical, dir); err != nil {
			item.Status = "kept"
			if errors.Is(err, agent.ErrSessionLeaseHeld) {
				item.Status = "busy"
				result.Busy++
			} else {
				item.Error = "recovery branch changed and was kept"
				result.Kept++
			}
		} else {
			item.Status = "moved"
			result.Moved++
			a.removeSessionCatalogPath(record.Path, "recovery_lineage_cleaned")
		}
	}
	if result.Moved > 0 {
		a.emitProjectTreeChangedForSessionDirs(dir)
		a.invalidatePromptHistoryCache()
	}
	return result
}

func recoveryCleanupCanonical(topic sessioncatalog.TopicRecord) (canonical, rootID string) {
	for _, record := range topic.Sessions {
		if record.RecoveryCanonical && (record.RecoveryRole == sessioncatalog.RecoveryRoleAdopted || record.RecoveryRole == sessioncatalog.RecoveryRolePreferred) {
			return record.Path, record.RecoveryGroupID
		}
	}
	return "", ""
}
