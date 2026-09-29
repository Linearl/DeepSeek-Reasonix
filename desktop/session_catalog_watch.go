package main

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
	"reasonix/internal/sessioncatalog"
)

// Directory notifications admit only dirty roots. The periodic audit remains
// authoritative after dropped notifications and on unsupported filesystems.
func (a *App) watchSessionCatalog(ctx context.Context, catalog *sessioncatalog.Catalog) {
	watcher, _ := fsnotify.NewWatcher()
	var events <-chan fsnotify.Event
	var failures <-chan error
	if watcher != nil {
		defer watcher.Close()
		events, failures = watcher.Events, watcher.Errors
	}
	targets := map[string]sessioncatalog.DirectoryTarget{}
	watched := map[string]bool{}
	dirty := map[string]bool{}
	var batch *time.Timer
	var batchReady <-chan time.Time
	armBatch := func() {
		if len(dirty) > 0 && batchReady == nil {
			batch = time.NewTimer(250 * time.Millisecond)
			batchReady = batch.C
		}
	}
	defer func() {
		if batch != nil {
			batch.Stop()
		}
	}()
	syncMetadata := func() {
		if err := a.syncSessionCatalogMetadataBounded(ctx, catalog); err != nil && !errors.Is(err, context.Canceled) {
			slog.Debug("desktop: refresh session catalog metadata", "err", err)
		}
	}
	refreshTargets := func() {
		targets = refreshCatalogWatchTargets(watcher, targets, a.sessionCatalogTargets(), watched, dirty)
	}
	refreshTargets()
	armBatch()
	metadata := time.NewTicker(30 * time.Second)
	audit := time.NewTicker(5 * time.Minute)
	defer metadata.Stop()
	defer audit.Stop()
	maintenance := 0
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				events = nil
				clear(watched)
				continue
			}
			key := filepath.Dir(event.Name)
			if _, exists := targets[filepath.Clean(event.Name)]; exists {
				key = filepath.Clean(event.Name)
				if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
					delete(watched, key)
				}
			}
			if _, exists := targets[key]; exists {
				dirty[key] = true
			}
			armBatch()
		case _, ok := <-failures:
			if !ok {
				failures = nil
			}
			for key := range targets {
				dirty[key] = true
			}
			armBatch()
		case <-metadata.C:
			syncMetadata()
			refreshTargets()
			armBatch()
		case <-audit.C:
			refreshTargets()
			for key := range targets {
				dirty[key] = true
			}
			armBatch()
			// Retention maintenance has a separate, bounded admission budget.
			// Never sweep every workspace on one ordinary refresh tick.
			all := a.sessionCatalogTargets()
			if len(all) > 0 {
				a.sweepExcessRecoveryCopies(catalog, all[maintenance%len(all)])
				maintenance++
			}
		case <-batchReady:
			batchReady = nil
			for key := range dirty {
				delete(dirty, key)
				target, exists := targets[key]
				if !exists || ctx.Err() != nil {
					continue
				}
				if len(migrateLegacySessionsIntoGlobalTopics(target.Path)) > 0 {
					syncMetadata()
				}
				catalog.RequestReconcile(target)
			}
		}
	}
}

func refreshCatalogWatchTargets(watcher *fsnotify.Watcher, current map[string]sessioncatalog.DirectoryTarget, targets []sessioncatalog.DirectoryTarget, watched, dirty map[string]bool) map[string]sessioncatalog.DirectoryTarget {
	next := map[string]sessioncatalog.DirectoryTarget{}
	for _, target := range targets {
		key := filepath.Clean(target.Path)
		next[key] = target
		if _, known := current[key]; !known {
			dirty[key] = true
		}
		if watcher != nil && !watched[key] {
			watched[key] = watcher.Add(key) == nil
		}
		if !watched[key] {
			dirty[key] = true
		}
	}
	for key := range current {
		if _, exists := next[key]; !exists {
			if watcher != nil && watched[key] {
				_ = watcher.Remove(key)
			}
			delete(watched, key)
			delete(dirty, key)
		}
	}
	return next
}
