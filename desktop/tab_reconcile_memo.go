package main

import (
	"path/filepath"
	"strings"
)

// Task 639: the effort read path (currentProviderEntryForTab) ran a full
// session-binding reconcile on EVERY call — a settled tab paid, per tab
// switch, the whole disk walk behind resolveSessionBinding (knownSessionDirs
// with its loadProjectsFile reads, per-dir validateSessionPath Lstat +
// EvalSymlinks, LoadBranchMeta sidecar reads) plus the binding apply's
// pinned/topic file reads. Field telemetry (task 639: switch-tab ancillary
// effort at 1188-1401ms with a 25s outlier under controller-build disk
// pressure) shows that walk, not the config snapshot (task 609), is what is
// left of the read cost.
//
// The reconcile is defensive healing: it moves a tab onto the session's saved
// binding. Once applied, re-running it on the next read re-derives the same
// binding (changed=false) — pure confirmation cost. This memo makes the read
// path skip that confirmation while nothing that could move a binding has
// changed: the tab's own binding fields (session path, scope, workspace root,
// topic), the build generation (bumped by session rebinds), and the project
// registry files (desktop-projects.json + its organization sidecar, stamped
// like the task-609 config snapshot). A mismatch re-runs the full reconcile
// once and re-stamps, so a moved session still heals on the next read.
//
// Scope: the memo lives ONLY on the read path in currentProviderEntryForTab.
// Every other reconcileTabWithPinnedSessionMeta caller (session open, rebind
// flows, reconciledSessionPathForTab) keeps the full reconcile, so those
// flows remain the authoritative healers — and any binding change they apply
// lands in the tab fields this memo keys on.
//
// Value semantics: the memo stores no derived data — skipping the reconcile
// just leaves the tab fields at their last reconciled state. The provider
// entry itself is always resolved fresh from the (task-609) config snapshot
// and the tab's current model/effort fields, so an effort or model change is
// visible on the very next read regardless of the memo.

// tabReconcileMemoKey is the fingerprint of every input the session-binding
// reconcile can read on behalf of one tab. Field-for-field: the tab state
// reconcileTabWithPinnedSessionMeta mutates (and therefore proves current
// by matching), the build generation session rebinds bump, and the registry
// files knownSessionDirs/legacyMigrationTargetForDir read.
type tabReconcileMemoKey struct {
	sessionPath   string
	scope         string
	workspaceRoot string
	topicID       string
	buildGen      uint64
	projectsStamp configFileStamp
	orgStamp      configFileStamp
}

// tabReconcileMemoKeyOf snapshots the key inputs for tab. Callers must not
// hold App.mu.
func (a *App) tabReconcileMemoKeyOf(tab *WorkspaceTab) tabReconcileMemoKey {
	a.mu.RLock()
	key := tabReconcileMemoKey{
		sessionPath:   canonicalTabSessionPath(tab.SessionPath),
		scope:         tab.Scope,
		workspaceRoot: tab.WorkspaceRoot,
		topicID:       tab.TopicID,
		buildGen:      tab.buildGeneration,
	}
	a.mu.RUnlock()
	key.projectsStamp = configFileStampOf(filepath.Join(desktopConfigDir(), desktopProjectsFile))
	key.orgStamp = configFileStampOf(filepath.Join(desktopConfigDir(), desktopProjectOrganizationFile))
	return key
}

// reconcileReadFreshFor reports whether tab's binding memo still matches the
// live inputs, i.e. whether the read path may skip the session reconcile.
func (a *App) reconcileReadFreshFor(tab *WorkspaceTab) bool {
	if tab == nil || strings.TrimSpace(tab.ID) == "" {
		return false
	}
	key := a.tabReconcileMemoKeyOf(tab)
	a.mu.RLock()
	defer a.mu.RUnlock()
	return tab.reconcileMemo == key
}

// storeTabReconcileRead records that a full session reconcile just ran for
// tab, stamping it with the post-reconcile input fingerprint.
func (a *App) storeTabReconcileRead(tab *WorkspaceTab) {
	if tab == nil || strings.TrimSpace(tab.ID) == "" {
		return
	}
	key := a.tabReconcileMemoKeyOf(tab)
	a.mu.Lock()
	tab.reconcileMemo = key
	a.mu.Unlock()
}

// invalidateTabReconcileRead drops tab's memo so the next read re-reconciles.
// Reserved for flows that move a binding through a channel the key cannot see
// (none today); callers must not hold App.mu.
func (a *App) invalidateTabReconcileRead(tab *WorkspaceTab) {
	if tab == nil {
		return
	}
	a.mu.Lock()
	tab.reconcileMemo = tabReconcileMemoKey{}
	a.mu.Unlock()
}
