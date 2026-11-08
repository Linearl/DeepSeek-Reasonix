package main

import (
	"log/slog"
	"strings"
	"time"

	"reasonix/internal/control"
)

// Task 889: the observed reconcile twins (extracted from tabs.go, pure move
// + instrumentation, zero behavior change). reconcileObservation is the split
// of one reconcile pass: memoMs times the settled-skip check itself —
// tabReconcileMemoKeyOf's canonicalTabSessionPath (Lstat + EvalSymlinks) and
// the file stamps, disk work that runs even when the memo serves — while
// walkMs is what a memo miss spent on binding resolve + apply. The effort
// breakdown line carries both, so a slow reconcile_ms splits into "memo key
// computation" (settled path: no phase log ever fired for it before task 889)
// vs "full walk" without cross-reading the phase logs.
type reconcileObservation struct {
	memoMs  time.Duration
	memoHit bool
	walkMs  time.Duration
	// stale (tl1500 刀①) marks the entry-level snapshot fallback: a writer
	// held or queued on a.mu, so the entry answered "binding unchanged" from
	// the tab's own session path without probing (no memo, no walk, no apply).
	stale bool
}

func (a *App) reconcileTabWithPinnedSessionMeta(tab *WorkspaceTab) (string, bool) {
	path, ok, _ := a.reconcileTabWithPinnedSessionMetaObserved(tab, nil)
	return path, ok
}

// reconcileTabWithPinnedSessionMetaObserved is the instrumented twin of
// reconcileTabWithPinnedSessionMeta: same steps in the same order, plus (a)
// a reconcileObservation for the effort breakdown and (b) mark calls that
// append the in-flight phase to the tab's effort-read phase trail
// (effort_phase_trail.go), so the 2s-timeout warn line names the step the
// read stands in instead of only "reconcile". mark is nil for every
// non-effort caller (the plain wrapper above keeps the old signature). The
// slow-phase log now starts at reconcile entry, so its total_ms spans the
// memo check too — the previously unattributed memo-key time no longer falls
// between the lines.
func (a *App) reconcileTabWithPinnedSessionMetaObserved(tab *WorkspaceTab, mark func(phase string)) (path string, ok bool, obs reconcileObservation) {
	markPhase := func(phase string) {
		if mark != nil {
			mark(phase)
		}
	}
	if tab == nil {
		return "", false, obs
	}
	start := time.Now()
	sessionPath, ctrl, scope, workspaceRoot, current, entryFresh := a.readReconcileEntry(tab)
	if !entryFresh {
		// tl1500 刀①: 写锁被持有/排队时不排队应答——「无法探测变更」按
		// 「绑定未变」处理：tab 自身的绑定路径原样作为 settled 结果返回
		// （与 memo 命中同形状），绝不进入走 a.mu 的 memo/apply 后段。无
		// 绑定路径则按无绑定应答。切 tab 绑定/文件/记忆等读者在保存家族
		// 占锁期间由此保持 <100ms 返回（2026-11-08 装机挂死主链）。
		obs.stale = true
		slog.Warn("desktop: session reconcile stale fallback (writer holds a.mu; tl1500 刀①)",
			"tab", tab.ID, "cause", "binding_snapshot_stale")
		a.logSlowSessionReconcile(tab, start, "stale_snapshot", time.Since(start))
		if sessionPath != "" {
			return sessionPath, true, obs
		}
		return "", false, obs
	}
	if current != tab {
		return "", false, obs
	}
	// Task 691: the task-639 memo now gates EVERY caller, not just the effort
	// read. One tab switch was firing 30-70 full reconciles through the other
	// callers (reconciledSessionPathForTab, activeSessionDir, memory/settings
	// reads) — each a confirmed-nothing re-derivation of a settled binding,
	// ~150-250ms under disk pressure, stacking into the switch-tab ancillary
	// and meta legs. A fresh memo serves the stored (path, ok) outcome; any
	// input that could move a binding busts it and the next call re-reconciles
	// once (tab_reconcile_memo.go for the freshness contract).
	markPhase("memo")
	memoPath, memoOK, fresh := a.tabReconcileMemoResultFor(tab)
	obs.memoMs = time.Since(start)
	if fresh {
		obs.memoHit = true
		// Task 889: a slow settled skip (memo key stats + lock waits under
		// congestion) now lands as a phase log instead of staying an
		// unattributed reconcile_ms in the effort breakdown.
		a.logSlowSessionReconcile(tab, start, "memo", obs.memoMs)
		return memoPath, memoOK, obs
	}
	// Task 889: without this line a slow key computation in front of a walk
	// would be swallowed by the walk's own phase log (total_ms includes
	// memo time, phase_ms does not).
	a.logSlowSessionReconcile(tab, start, "memokey", obs.memoMs)
	if a.reconcileReadProbe != nil {
		a.reconcileReadProbe(tab.ID)
	}
	// Task 639: phase timers only — this reconcile is on the effort read
	// path, and the slow log below is what turns a 1.2s sample into a named
	// culprit (path branch, binding resolve, or binding apply).
	walkStart := time.Now()
	defer func() { obs.walkMs = time.Since(walkStart) }()
	if sessionPath != "" {
		branchStart := time.Now()
		markPhase("path")
		resolved, ok := a.reconcileTabWithSessionPathMarked(tab, sessionPath, mark)
		a.logSlowSessionReconcile(tab, start, "path", time.Since(branchStart))
		if ok {
			a.storeTabReconcileResult(tab, resolved, true)
			return resolved, true, obs
		}
	}
	if ctrl == nil {
		a.storeTabReconcileResult(tab, "", false)
		return "", false, obs
	}
	sessionPath = strings.TrimSpace(ctrl.SessionPath())
	if sessionPath == "" {
		a.storeTabReconcileResult(tab, "", false)
		return "", false, obs
	}
	resolveStart := time.Now()
	markPhase("resolve")
	binding, ok := a.resolveSessionBinding(sessionPath)
	resolveMs := time.Since(resolveStart)
	if !ok {
		a.logSlowSessionReconcile(tab, start, "resolve", resolveMs)
		a.storeTabReconcileResult(tab, "", false)
		return "", false, obs
	}
	if scope == "project" && binding.scope != "project" && normalizeProjectRoot(workspaceRoot) != "" {
		if root, ok := safeControllerWorkspaceRoot(ctrl); ok && sameProjectRoot(root, workspaceRoot) {
			a.storeTabReconcileResult(tab, "", false)
			return "", false, obs
		}
	}
	applyStart := time.Now()
	markPhase("apply")
	a.applySessionBindingToTab(tab, binding)
	applyMs := time.Since(applyStart)
	a.logSlowSessionReconcile(tab, start, "apply", applyMs)
	a.storeTabReconcileResult(tab, binding.path, true)
	return binding.path, true, obs
}

func (a *App) reconcileTabWithSessionPath(tab *WorkspaceTab, sessionPath string) (string, bool) {
	return a.reconcileTabWithSessionPathMarked(tab, sessionPath, nil)
}

// readReconcileEntry reads the reconcile inputs (tab identity + binding
// fields). tl1500 刀①: TryRLock first — on success this is the exact locked
// read the old entry did, plus a binding-snapshot publish; when a writer holds
// or queues on a.mu, fresh=false and only the tab's own session path is
// served, letting the caller treat "cannot probe for changes" as "binding
// unchanged" instead of joining the reader convoy. The lock-free field read in
// the stale arm follows the activeWorkspaceRoot precedent (binding reads may
// be served outside a.mu when the probing lock is unavailable).
func (a *App) readReconcileEntry(tab *WorkspaceTab) (sessionPath string, ctrl control.SessionAPI, scope, workspaceRoot string, current *WorkspaceTab, fresh bool) {
	if a.mu.TryRLock() {
		current = a.tabs[tab.ID]
		sessionPath = strings.TrimSpace(tab.SessionPath)
		ctrl = tab.Ctrl
		scope = tab.Scope
		workspaceRoot = tab.WorkspaceRoot
		a.publishBindingSnapshotLocked()
		a.mu.RUnlock()
		return sessionPath, ctrl, scope, workspaceRoot, current, true
	}
	// Stale: caller must skip the memo/walk/apply legs.
	return strings.TrimSpace(tab.SessionPath), nil, "", "", nil, false
}

// reconcileTabWithSessionPathMarked is the instrumented twin of
// reconcileTabWithSessionPath: identical body, with the resolve/apply
// sub-steps announced to the effort-read phase trail when mark is non-nil.
// The split matters during congestion — the binding walk (disk) and the
// apply (App.mu publish, where field samples queued for 90s+) are different
// culprits behind the same reconcile_ms.
func (a *App) reconcileTabWithSessionPathMarked(tab *WorkspaceTab, sessionPath string, mark func(phase string)) (string, bool) {
	if tab == nil || strings.TrimSpace(sessionPath) == "" {
		return "", false
	}
	if mark != nil {
		mark("resolve")
	}
	binding, ok := a.resolveSessionBinding(sessionPath)
	if !ok {
		return "", false
	}
	if mark != nil {
		mark("apply")
	}
	a.applySessionBindingToTab(tab, binding)
	return binding.path, true
}
