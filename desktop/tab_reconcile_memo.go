package main

import (
	"path/filepath"
	"strings"

	"reasonix/internal/agent"
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
// binding (changed=false) — pure confirmation cost.
//
// Task 691: field telemetry (desktop.log 2026-10-09) shows the remaining
// faces — one tab switch firing 30-70 full reconciles through the OTHER
// callers (reconciledSessionPathForTab, activeSessionDir,
// ensureTabControllerWorkspace, memory/settings reads), ~150-250ms of
// confirmed-nothing each under disk pressure, stacking into the 0.9-2.0s
// "switch-tab:ancillary context" leg. The memo therefore moved from the
// effort read into reconcileTabWithPinnedSessionMeta itself: every caller now
// skips while nothing that could move a binding has changed, and the memo
// stores the reconcile's (path, ok) outcome so all callers see the same
// answer a fresh reconcile would have re-derived. The flows stay the
// authoritative healers — any input that could move a binding (the tab's own
// binding fields, the build generation, the project registry files, or now
// also the session file and its branch-meta sidecar, so an externally edited
// or moved session re-heals) invalidates the memo and the next caller
// re-reconciles once.
//
// Value semantics: the memo stores only the reconcile's own outcome, never
// derived display data. The provider entry in currentProviderEntryForTab is
// still resolved fresh from the (task-609) config snapshot and the tab's
// current model/effort fields, so an effort or model change is visible on the
// very next read regardless of the memo.

// tabReconcileMemoKey is the fingerprint of every input the session-binding
// reconcile can read on behalf of one tab. Field-for-field: the tab state
// reconcileTabWithPinnedSessionMeta mutates (and therefore proves current
// by matching), the build generation session rebinds bump, and the registry
// files knownSessionDirs/legacyMigrationTargetForDir read. Task 691 adds the
// session file and its branch-meta sidecar — the reconcile resolves the tab's
// session path against disk, so a moved, deleted, or externally rewritten
// session must re-heal.
type tabReconcileMemoKey struct {
	sessionPath   string
	scope         string
	workspaceRoot string
	topicID       string
	buildGen      uint64
	projectsStamp configFileStamp
	orgStamp      configFileStamp
	sessionStamp  configFileStamp
	metaStamp     configFileStamp
}

// tabReconcileMemo is the stored outcome of one full reconcile plus the
// input fingerprint it was true under.
type tabReconcileMemo struct {
	key  tabReconcileMemoKey
	path string
	ok   bool
}

// tabReconcileMemoKeyOf snapshots the key inputs for tab. Callers must not
// hold App.mu.
func (a *App) tabReconcileMemoKeyOf(tab *WorkspaceTab) tabReconcileMemoKey {
	a.mu.RLock()
	sessionPath := canonicalTabSessionPath(tab.SessionPath)
	key := tabReconcileMemoKey{
		sessionPath:   sessionPath,
		scope:         tab.Scope,
		workspaceRoot: tab.WorkspaceRoot,
		topicID:       tab.TopicID,
		buildGen:      tab.buildGeneration,
	}
	a.mu.RUnlock()
	key.projectsStamp = configFileStampOf(filepath.Join(desktopConfigDir(), desktopProjectsFile))
	key.orgStamp = configFileStampOf(filepath.Join(desktopConfigDir(), desktopProjectOrganizationFile))
	if sessionPath != "" {
		key.sessionStamp = configFileStampOf(sessionPath)
		if metaPath := agent.BranchMetaPath(sessionPath); metaPath != "" {
			key.metaStamp = configFileStampOf(metaPath)
		}
	}
	return key
}

// tabReconcileMemoResultFor reports the memoized reconcile outcome for tab,
// and whether that outcome may be served (fresh) instead of re-reconciling.
func (a *App) tabReconcileMemoResultFor(tab *WorkspaceTab) (path string, ok bool, fresh bool) {
	if tab == nil || strings.TrimSpace(tab.ID) == "" {
		return "", false, false
	}
	key := a.tabReconcileMemoKeyOf(tab)
	a.mu.RLock()
	defer a.mu.RUnlock()
	if tab.reconcileMemo.key != key {
		return "", false, false
	}
	return tab.reconcileMemo.path, tab.reconcileMemo.ok, true
}

// storeTabReconcileResult records that a full session reconcile just ran for
// tab, stamping it with the post-reconcile input fingerprint and outcome.
func (a *App) storeTabReconcileResult(tab *WorkspaceTab, path string, ok bool) {
	if tab == nil || strings.TrimSpace(tab.ID) == "" {
		return
	}
	key := a.tabReconcileMemoKeyOf(tab)
	a.mu.Lock()
	tab.reconcileMemo = tabReconcileMemo{key: key, path: path, ok: ok}
	a.mu.Unlock()
}
