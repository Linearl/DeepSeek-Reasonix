package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"reasonix/internal/history"
	"reasonix/internal/sessioncatalog"
	"reasonix/internal/taskcatalog"
)

func (a *App) runSessionCatalog(ctx context.Context) {
	// tl1208: 先预热 topic 注册唯一归属索引（重建先于直查的 boot 面），
	// 随后 Open 注入的 TopicOwner 钩子与侧栏/壳对账直查都有暖索引可问。
	topicRegistryWarmup()
	path := sessioncatalog.DefaultPath()
	freshGeneration := false
	if strings.TrimSpace(path) != "" {
		_, statErr := os.Stat(path)
		freshGeneration = errors.Is(statErr, os.ErrNotExist)
	}
	targets := a.sessionCatalogTargets()
	history.RegisterCatalogRoots(historyCatalogRoots(targets))
	projects := loadProjectsFile()
	taskcatalog.RegisterSharedProject(globalWorkspaceRoot(), projects.GlobalTitle)
	for _, project := range projects.Projects {
		taskcatalog.RegisterSharedProject(project.Root, projectDisplayName(project))
	}
	catalog, err := sessioncatalog.Open(ctx, sessioncatalog.Options{
		Path: path,
		OnRevision: func(revision uint64, roots []string, reason string) {
			a.emitProjectTreeChangedV2(revision, roots, reason)
		},
		// tl1208: 会话目录列表直查 topic 注册唯一归属——跨 scope 空壳让位
		// 先问注册表，再回落 1197 内容承载查询。
		TopicOwner: func(topicID string) (string, string, bool) {
			if entry, ok := topicRegistryOwnerOf(topicID); ok {
				return entry.Scope, entry.Root, true
			}
			return "", "", false
		},
	})
	if err != nil {
		slog.Warn("desktop: open session catalog", "err", err)
		return
	}
	if ctx.Err() != nil || a.shuttingDown.Load() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		_ = catalog.Close(closeCtx)
		closeCancel()
		return
	}
	a.sessionCatalog.Store(catalog)
	if err := a.syncSessionCatalogMetadataBounded(ctx, catalog); err != nil && !errors.Is(err, context.Canceled) {
		slog.Warn("desktop: sync session catalog metadata", "err", err)
	}
	select {
	case <-a.tabsRestoredSignal():
	case <-ctx.Done():
		return
	}
	// Restored tabs can reveal a project absent from the initial registry.
	targets = a.sessionCatalogTargets()
	history.RegisterCatalogRoots(historyCatalogRoots(targets))
	a.indexRestoredSessionPaths(ctx, catalog)
	// tl1526: the startup wave runs under a hard budget. Directory scans are
	// internally batched and resumable (scan_cursor), so whatever the budget
	// cannot cover degrades to the async reconcile queue instead of delaying
	// the rest of the load chain (retarget, inventory, refresh loop).
	_ = runStartupReconcileWave(ctx, sessionCatalogStartupReconcileBudget, targets,
		func(waveCtx context.Context, target sessioncatalog.DirectoryTarget) error {
			if migrated := migrateLegacySessionsIntoGlobalTopics(target.Path); len(migrated) > 0 {
				_ = a.syncSessionCatalogMetadataBounded(waveCtx, catalog)
			}
			return catalog.ReconcileDirectory(waveCtx, target)
		},
		func(target sessioncatalog.DirectoryTarget) {
			catalog.RequestReconcile(target)
		})
	if ctx.Err() != nil || a.shuttingDown.Load() {
		return
	}
	if freshGeneration {
		catalog.MarkRepairReason("generation_upgrade")
	}
	a.retargetOpenTabsToContinuations()
	// Task 550 ②: three-source topic inventory reconcile at load time. Tabs
	// are restored and the catalog projection is open, so tab ↔ topic-index ↔
	// session-file disagreements are now listable; the summary is logged once
	// and the full list stays queryable via GetTopicInventoryMismatches.
	logTopicInventorySummary(a.reconcileTopicInventory(ctx))
	a.runSessionCatalogRefreshLoop(ctx, catalog)
}

// Task 389: the 30s full-target reconcile/migrate/sweep loop burned idle CPU
// (upstream #11181); this is the single-point port of the upstream 1.38.12
// catalog rework (#10603) — fsnotify-driven dirty-directory refreshes with
// periodic metadata/audit fallbacks instead of the unconditional loop.
func (a *App) runSessionCatalogRefreshLoop(ctx context.Context, catalog *sessioncatalog.Catalog) {
	a.watchSessionCatalog(ctx, catalog)
}

// sessionCatalogStartupReconcileBudget bounds the startup directory-reconcile
// wave (tl1526; the 2026-11-09 incident measured one directory at 131s). var
// only for test injection; production code never rewrites it.
var sessionCatalogStartupReconcileBudget = 120 * time.Second

// runStartupReconcileWave reconciles each target under one shared budget with
// a 4-way scan concurrency. Targets the budget never reached, and targets
// whose scan ended on the budget, go through onDeferred exactly once so the
// caller can hand them to the async reconcile queue; the return value is the
// deferred count. Budget-out degrades instead of blocking: a scan interrupted
// by the deadline resumes from its stored scan_cursor on the queued pass.
func runStartupReconcileWave(ctx context.Context, budget time.Duration, targets []sessioncatalog.DirectoryTarget,
	reconcile func(context.Context, sessioncatalog.DirectoryTarget) error,
	onDeferred func(sessioncatalog.DirectoryTarget)) int {
	waveCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	var wave errgroup.Group
	wave.SetLimit(4)
	deferred := 0
	var deferredMu sync.Mutex
	countDeferred := func(target sessioncatalog.DirectoryTarget) {
		deferredMu.Lock()
		deferred++
		deferredMu.Unlock()
		onDeferred(target)
	}
	for _, target := range targets {
		if ctx.Err() != nil || waveCtx.Err() != nil {
			countDeferred(target)
			continue
		}
		target := target
		wave.Go(func() error {
			err := reconcile(waveCtx, target)
			if err == nil {
				return nil
			}
			// Only budget-caused termination defers: a scan that reports its own
			// deadline while the wave budget is still alive keeps the pre-budget
			// semantics (debug-only, no re-queue).
			if waveCtx.Err() != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)) {
				countDeferred(target)
				return nil
			}
			if !errors.Is(err, context.Canceled) {
				slog.Debug("desktop: reconcile session catalog directory", "dir", target.Path, "err", err)
			}
			return nil
		})
	}
	_ = wave.Wait()
	if deferred > 0 {
		slog.Warn("desktop: startup session catalog reconcile budget exhausted; remaining directories deferred to the async reconcile queue",
			"budget", budget.String(), "deferred", deferred,
			"mode", "deferred_to_async_queue")
	}
	return deferred
}
