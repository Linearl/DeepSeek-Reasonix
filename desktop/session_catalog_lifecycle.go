package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"reasonix/internal/history"
	"reasonix/internal/sessioncatalog"
	"reasonix/internal/taskcatalog"
)

func (a *App) runSessionCatalog(ctx context.Context) {
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
	// Directory scans are independent and internally batched/resumable; keep
	// startup work bounded so a large project set cannot starve the UI.
	var reconcileGroup errgroup.Group
	reconcileGroup.SetLimit(4)
	for _, target := range targets {
		if ctx.Err() != nil || a.shuttingDown.Load() {
			return
		}
		reconcileGroup.Go(func() error {
			if migrated := migrateLegacySessionsIntoGlobalTopics(target.Path); len(migrated) > 0 {
				_ = a.syncSessionCatalogMetadataBounded(ctx, catalog)
			}
			if err := catalog.ReconcileDirectory(ctx, target); err != nil && !errors.Is(err, context.Canceled) {
				slog.Debug("desktop: reconcile session catalog directory", "dir", target.Path, "err", err)
			}
			return nil
		})
	}
	_ = reconcileGroup.Wait()
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
