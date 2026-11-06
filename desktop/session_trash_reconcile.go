package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"reasonix/internal/agent"
)

func reconcileDesktopTrashSessionArtifacts(dir, sessionPath, key string) error {
	// Hold the removal guard across the whole move so no runtime can acquire
	// the session (or save into it) while its artifacts are relocated; the
	// lock sidecars are deleted atomically with the guard release.
	guard, err := acquireSessionRemovalGuardForOp("cleanup_pending_reconcile", sessionPath)
	if err != nil {
		return err
	}
	return trashSessionArtifactsWithGuard(dir, sessionPath, key, guard)
}

func trashSessionArtifactsWithGuard(dir, sessionPath, key string, guard *agent.SessionRemovalGuard) error {
	if guard == nil {
		return errSessionBusyElsewhere
	}
	// tl1429b: the transcript is already gone — a previous archive moved it
	// and the caller (the cleanup-pending reconcile) is re-trashing residue.
	// When NO bucket holds the transcript yet (the unique-bucket form), a
	// fresh allocation here minted a second, transcript-less record (the
	// package-78 double trash entry) while the first one stayed stuck behind
	// its own missing meta — tear the leftover sidecars instead. A bucket
	// that DOES hold the transcript keeps the flow below: remaining sidecars
	// move into it so a later restore brings them back (the committed
	// partial-move form).
	if _, statErr := os.Stat(sessionPath); os.IsNotExist(statErr) {
		bucketPath := filepath.Join(sessionTrashPath(dir), key, key)
		if _, bucketErr := os.Stat(bucketPath); os.IsNotExist(bucketErr) {
			removeSessionOwnedResidue(sessionPath, key)
			_ = agent.ClearCleanupPending(sessionPath)
			invalidateTopicSessionIndexForPath(sessionPath)
			_ = guard.RemoveSidecarsAndRelease()
			return nil
		}
	}
	defer guard.Release()
	itemDir := filepath.Join(sessionTrashPath(dir), key)
	if info, err := os.Stat(itemDir); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("session trash target is not a directory: %s", key)
		}
		trashPath := filepath.Join(itemDir, key)
		if trashInfo, err := os.Stat(trashPath); err == nil && !trashInfo.IsDir() {
			_, liveErr := os.Stat(sessionPath)
			if liveErr != nil && !os.IsNotExist(liveErr) {
				return liveErr
			}
			if liveErr == nil {
				discardable, err := liveSessionContentDiscardable(sessionPath)
				if err != nil {
					return err
				}
				if discardable {
					return removeDesktopSessionArtifactsWithGuard(sessionPath, guard)
				}
				matches, err := trashSessionMatchesLive(sessionPath, trashPath)
				if err != nil {
					return err
				}
				if matches {
					return removeDesktopSessionArtifactsWithGuard(sessionPath, guard)
				}
				itemDir, err = reserveUniqueSessionTrashItemDir(dir, key)
				if err != nil {
					return err
				}
			}
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
	} else if os.IsNotExist(err) {
		if err := os.MkdirAll(itemDir, 0o755); err != nil {
			return err
		}
	} else {
		return err
	}
	defer invalidateTopicSessionIndexForPath(sessionPath)
	if err := invalidateTopicDirMarkers(dir); err != nil {
		return err
	}
	// tl1442-ext: same full-inventory counted move + catch-all sweep as the
	// interactive delete, so a cleanup-pending completion reconciles against
	// the same counting log and leaves the same zero residue.
	report, err := moveSessionTrashArtifactsToBucket(dir, sessionPath, key, itemDir)
	if err != nil {
		return err
	}
	if err := guard.RemoveSidecarsAndRelease(); err != nil {
		return err
	}
	meta := trashedSessionMeta{Key: key, DeletedAt: time.Now().UnixMilli()}
	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(itemDir, sessionTrashMetaFile), b, 0o644); err != nil {
		return err
	}
	if err := agent.ClearCleanupPending(sessionPath); err != nil {
		return err
	}
	logSessionTrashMove(dir, key, &report)
	return nil
}
