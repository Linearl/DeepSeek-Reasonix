package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/store"
)

func removeDesktopSessionArtifactsWithGuard(path string, guard *agent.SessionRemovalGuard) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if guard == nil {
		return fmt.Errorf("session removal guard is required")
	}
	defer guard.Release()
	dir := filepath.Dir(path)
	if err := invalidateTopicDirMarkers(dir); err != nil {
		return err
	}
	defer invalidateTopicSessionIndexForPath(path)
	// tl1442-ext: the full store inventory, counted in the same key format as
	// every other deletion chain, plus the hard form of the stem-prefix
	// catch-all sweep (the /clear chain has no bucket — unlisted shapes are
	// removed outright instead of moved).
	var report agent.TrashMoveReport
	stem := strings.TrimSuffix(filepath.Base(path), store.SessionTranscriptSuffix)
	for _, artifact := range sessionOwnedArtifactPaths(path) {
		if strings.TrimSpace(artifact) == "" {
			continue
		}
		if info, err := os.Lstat(artifact); err == nil {
			if info.IsDir() {
				report.Dirs++
			} else {
				report.Files++
			}
			report.Categories = append(report.Categories, agent.StemRelativeSuffix(filepath.Base(artifact), stem))
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.RemoveAll(artifact); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	swept, err := sweepSessionTrashResidueHard(path, &report)
	if err != nil {
		return err
	}
	if swept > 0 {
		slog.Warn("desktop: session teardown swept stem-prefixed stragglers not in the sidecar inventory — extend store.SessionTrashSidecars",
			"path", filepath.Base(path), "names", strings.Join(report.Stragglers, ","))
	}
	agent.LogTrashMoveCounts("desktop: session artifacts removed", dir, filepath.Base(path), &report)
	if err := guard.RemoveSidecarsAndRelease(); err != nil {
		return err
	}
	if err := removeSessionDisplay(dir, path); err != nil {
		return err
	}
	if err := removeSessionPlannerDisplay(dir, path); err != nil {
		return err
	}
	if err := agent.DeleteSubagentsByParent(dir, agent.BranchID(path)); err != nil {
		return err
	}
	return agent.ClearCleanupPending(path)
}
