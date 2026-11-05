package main

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/control"
)

type topicArchiveRemovalOwnership struct {
	guard      *agent.SessionRemovalGuard
	restoreTab *WorkspaceTab
}

type topicArchiveOwnershipBatch struct {
	entries []*topicArchiveRemovalOwnership
	byPath  map[string]*topicArchiveRemovalOwnership
}

func takeTopicArchiveSessionLease(tab *WorkspaceTab, sessionPath string) *agent.SessionLease {
	if tab == nil {
		return nil
	}
	key := sessionRuntimeKey(sessionPath)
	tab.sessionLeaseMu.Lock()
	defer tab.sessionLeaseMu.Unlock()
	lease := tab.sessionLease
	if lease == nil || sessionRuntimeKey(lease.Path()) != key {
		return nil
	}
	tab.sessionLease = nil
	tab.storeSessionLeaseRuntimeKey("")
	return lease
}

func topicArchiveLeaseOwners(removed []removedSessionRuntime) map[string]*WorkspaceTab {
	owners := make(map[string]*WorkspaceTab, len(removed))
	for _, item := range removed {
		if item.tab == nil || item.readOnly || item.sessionPath == "" {
			continue
		}
		key := sessionRuntimeKey(item.sessionPath)
		if key != "" && item.tab.sessionLeaseRuntimeKey() == key {
			owners[key] = item.tab
		}
	}
	return owners
}

// sessionTrashTargetFileMissing reports whether the trash target's transcript
// is absent from disk. tl1383: the zombie-entry form (tl1363 family) — a
// restored tab or stale binding pointing at a lost or already-trashed session.
// Such a target has nothing to protect and nothing to move.
func sessionTrashTargetFileMissing(sessionPath string) bool {
	if strings.TrimSpace(sessionPath) == "" {
		return false
	}
	_, err := os.Stat(sessionPath)
	return os.IsNotExist(err)
}

// acquireTopicArchiveOwnership takes removal ownership for every target. The
// regular path refuses (returns the error) when a guard cannot be taken. The
// tl1404 terminal path (terminal=true) instead clear-downs: this process's
// lease state for the key is force-released (tab-bound objects, unbound
// orphans, owner entries, identity records — each with a slog trail), the
// guard is retried once, and a guard that STILL cannot be taken degrades to a
// nil-guard entry the trash step handles via the terminal guard-less flow.
func acquireTopicArchiveOwnership(a *App, targets []topicTrashTarget, removed []removedSessionRuntime, terminal bool) (*topicArchiveOwnershipBatch, error) {
	batch := &topicArchiveOwnershipBatch{byPath: make(map[string]*topicArchiveRemovalOwnership, len(targets))}
	localOwners := topicArchiveLeaseOwners(removed)
	// Acquire historical/non-runtime sessions first. If another process owns
	// any target, the archive aborts before a local runtime lease is touched.
	for _, target := range targets {
		key := sessionRuntimeKey(target.sessionPath)
		if localOwners[key] != nil {
			continue
		}
		if sessionTrashTargetFileMissing(target.sessionPath) {
			// tl1383: no transcript on disk to protect or move — refusing to
			// archive a session whose file does not exist protects nobody and
			// wedges the topic forever ("给不存在的会话解占用" is a dead-end
			// refusal). Tear any leftover lease record through the
			// free-lock-arbitrated primitive and record a nil guard so the
			// trash step skips straight to marker cleanup. A lease whose
			// handle is still live keeps its lock, so a real writer of a
			// not-yet-created file is never stripped.
			if cleared, err := agent.ClearStaleSessionLeaseInfo(key); err == nil && cleared {
				slog.Info("desktop: topic archive tore a leftover lease record for a missing session file",
					"session_base", filepath.Base(target.sessionPath))
			}
			batch.add(target, nil, nil)
			continue
		}
		guard, err := acquireSessionRemovalGuard(target.sessionPath)
		if err != nil && terminal {
			// tl1404 terminal: clear this process's lease state, retry the
			// guard once, and let the OS lease lock arbitrate what remains —
			// external holder = keep the refusal, contested save lock only =
			// degrade to unguarded trash (see the helper's contract).
			guard, err = a.terminalArchiveGuardAfterReclaim(target.sessionPath, key, err)
			if err != nil {
				batch.release()
				return nil, err
			}
			if guard == nil {
				batch.add(target, nil, nil)
				continue
			}
		}
		if err != nil {
			batch.release()
			return nil, err
		}
		batch.add(target, guard, nil)
	}
	// Convert local runtime leases without releasing the lease lock. Converted
	// guards can be restored if marker publication or generation validation
	// fails before detach commits.
	for _, target := range targets {
		key := sessionRuntimeKey(target.sessionPath)
		tab := localOwners[key]
		if tab == nil {
			continue
		}
		lease := takeTopicArchiveSessionLease(tab, target.sessionPath)
		if lease == nil {
			// No lease available (e.g. tab in sessionRuntimeFailed after
			// a failed restore). Fall back to non-runtime guard acquisition
			// so the archive can proceed without a live lease.
			guard, err := acquireSessionRemovalGuard(target.sessionPath)
			if err != nil && terminal {
				guard, err = a.terminalArchiveGuardAfterReclaim(target.sessionPath, key, err)
				if err != nil {
					batch.rollback()
					return nil, err
				}
				if guard == nil {
					batch.add(target, nil, nil)
					continue
				}
			}
			if err != nil {
				batch.rollback()
				return nil, err
			}
			batch.add(target, guard, nil)
			continue
		}
		guard, err := lease.TryConvertToRemovalGuard()
		if err != nil {
			// Lease conversion failed (stale authority, mismatched generation,
			// etc.). Release the lease and fall back to a non-runtime guard
			// instead of aborting the entire archive.
			tab.adoptSessionLease(lease)
			if errors.Is(err, agent.ErrSessionLeaseHeld) {
				if terminal {
					// tl1404 terminal: the conversion lost a lock this process
					// cannot name. Clear the in-process state, retry once, and
					// let the OS lease lock arbitrate (see the helper).
					guard2, err2 := a.terminalArchiveGuardAfterReclaim(target.sessionPath, key, err)
					if err2 != nil {
						batch.rollback()
						return nil, err2
					}
					if guard2 == nil {
						batch.add(target, nil, nil)
						continue
					}
					batch.add(target, guard2, nil)
					continue
				}
				batch.rollback()
				// tl1367-fix defect A: keep the lease error flowing (wrapped, so
				// both the classification sentinel and the holder Info stay
				// reachable through Unwrap/errors.As). The bare sentinel here
				// used to sever the errors.As chain — the refusal log degraded
				// to holder_class=unknown pid=0 and the frontend lost the
				// holder block.
				return nil, &sessionLeaseHeldBusyError{cause: err}
			}
			slog.Warn("desktop: topic archive lease conversion failed, falling back to non-runtime guard",
				"session", target.sessionPath, "err", err)
			guard2, err2 := acquireSessionRemovalGuard(target.sessionPath)
			if err2 != nil && terminal {
				guard2, err2 = a.terminalArchiveGuardAfterReclaim(target.sessionPath, key, err2)
				if err2 != nil {
					batch.rollback()
					return nil, err2
				}
				if guard2 == nil {
					batch.add(target, nil, nil)
					continue
				}
			}
			if err2 != nil {
				batch.rollback()
				return nil, err2
			}
			batch.add(target, guard2, nil)
			continue
		}
		batch.add(target, guard, tab)
	}
	return batch, nil
}

func (b *topicArchiveOwnershipBatch) add(target topicTrashTarget, guard *agent.SessionRemovalGuard, tab *WorkspaceTab) {
	entry := &topicArchiveRemovalOwnership{guard: guard, restoreTab: tab}
	b.entries = append(b.entries, entry)
	b.byPath[sessionRuntimeKey(target.sessionPath)] = entry
}

func (b *topicArchiveOwnershipBatch) take(sessionPath string) *agent.SessionRemovalGuard {
	if b == nil {
		return nil
	}
	entry := b.byPath[sessionRuntimeKey(sessionPath)]
	if entry == nil {
		return nil
	}
	guard := entry.guard
	entry.guard = nil
	return guard
}

func (b *topicArchiveOwnershipBatch) rollback() {
	if b == nil {
		return
	}
	for _, entry := range slices.Backward(b.entries) {
		if entry.guard == nil {
			continue
		}
		if entry.restoreTab != nil {
			lease, err := entry.guard.RestoreSessionLease()
			if err != nil {
				slog.Warn("desktop: restoring topic archive session ownership failed")
				entry.guard.Release()
			} else {
				entry.restoreTab.adoptSessionLease(lease)
			}
		} else {
			entry.guard.Release()
		}
		entry.guard = nil
	}
}

func (b *topicArchiveOwnershipBatch) release() {
	if b == nil {
		return
	}
	for _, entry := range b.entries {
		if entry.guard != nil {
			entry.guard.Release()
			entry.guard = nil
		}
	}
}

func delayedDesktopTopicTrash(dir, sessionPath, key string, guard *agent.SessionRemovalGuard, destroys []control.SessionDestroyHandle) {
	waitAllDestroyHandles(destroys)
	if err := trashSessionArtifactsWithGuard(dir, sessionPath, key, guard); err != nil {
		slog.Warn("desktop: delayed topic archive cleanup remains pending")
	}
	finishDestroyHandles(destroys)
}
