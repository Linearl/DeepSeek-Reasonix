package main

// tl1404 归档终局兜底。阶梯:正常归档 → 强制归档(撕 lease+save 锁)→ 终局
// 兜底(进程级清场)。tl1383/tl1392 治的是「已知根因」;本文件保证归档路径
// 没有无终局的死局:任何形态(缺文件残留活体 / holder unknown+save 锁长持 /
// fork·构建循环占门禁 / 运行中 runtime / 未知新根因)都必须在有限预算内归档
// 成功。核心约束:归档操作对用户必须总能成功;兜底代价(丢运行中 turn)在
// 强制归档确认文案里向用户预告,每个破坏性动作留 slog(who/why/清了什么)
// 供事后归因。
//
// 边界(有意不越):真实占用——一个活着的、可指认的其它 Reasonix 进程持有
// lease——永不静默抢锁(tl1367 line 1 契约);那种形态有明确的关闭路径,
// 不是死局。本进程名下的全部状态才是终局的清场范围。

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/store"
)

// terminalArchiveBudget bounds one terminal fallback attempt chain. The whole
// force-archive call must stay inside the user-visible ceiling (10s), so the
// terminal leaves headroom for the failed force attempt ahead of it.
const terminalArchiveBudget = 8 * time.Second

// terminalArchiveGatePollInterval paces the bounded gate wait and the retry
// loop. Small enough to converge fast, large enough not to spin the CPU.
const terminalArchiveGatePollInterval = 25 * time.Millisecond

// terminalArchiveFallbackRuns counts terminal-fallback entries. Observation
// counter for desktop.log forensics, and the seam the「可正常归档绝不触发兜底」
// acceptance pin asserts against.
var terminalArchiveFallbackRuns atomic.Int64

// errTopicArchiveTerminalTimeout is the honest failure when even the terminal
// fallback could not converge inside its budget (e.g. the runtime gates stayed
// hot for the whole window). Deliberately NOT in the forceable family: the
// frontend must not offer the force button again — the copy points at retry
// and the restart-then-archive fallback.
var errTopicArchiveTerminalTimeout = errors.New("the archive could not finish within its safety window — retry in a moment; if it keeps failing, restart Reasonix and archive before the session opens")

// errTopicArchiveExternalHolder is an INTERNAL sentinel: the terminal clear-down
// finished and the OS lease lock is still held by something outside this
// process — real occupation the terminal must never grab (tl1367 line 1; the
// identity record may be torn, so the lock itself is the arbiter). It wraps the
// original refusal to stop the retry loop immediately (an external holder does
// not free up inside the budget) and keeps the whole error chain reachable via
// Unwrap; the caller renders its own copy from the original refusal.
var errTopicArchiveExternalHolder = errors.New("terminal fallback stopped: the lease lock is held outside this process")

// terminalFallbackEligible reports whether a force-archive refusal may be
// escalated into the terminal fallback. The excluded form is REAL occupation:
// a refusal whose lease error names a LIVE foreign pid running this
// application (tl1367 line 1 — a working window of this app is never grabbed,
// silently or loudly; the user closes that window themselves). Everything
// else — identity-missing residue, self-held locks, zombie records, busy
// gates, active work in this window — is either this process's own state or
// an unnameable leftover, and the terminal owns it.
func terminalFallbackEligible(err error) bool {
	if err == nil {
		return false
	}
	var leaseErr *agent.SessionLeaseError
	if errors.As(err, &leaseErr) && leaseErr != nil && leaseErr.Info != nil && leaseErr.Info.PID > 0 && leaseErr.Info.PID != os.Getpid() {
		if desktopLeaseAlive(leaseErr.Info.PID) && agent.ProcessLooksLikeSelf(leaseErr.Info.PID) {
			return false
		}
	}
	return true
}

// terminalFallbackReason renders the WHY for the fallback's slog entry: the
// refusal class, never a path.
func terminalFallbackReason(err error) string {
	switch {
	case errors.Is(err, errTopicArchiveTerminalTimeout):
		return "terminal budget exhausted"
	case errors.Is(err, errTopicArchiveBusy):
		return "archive gates busy"
	case errors.Is(err, errTopicHasActiveWork):
		return "topic has active runtime work"
	case sessionLeaseHolderIdentityMissing(err):
		return "lease holder identity missing"
	case errors.Is(err, agent.ErrSessionLeaseHeld):
		return "session lease held"
	default:
		return "archive refused"
	}
}

// terminalTopicArchive is the final rung of the archive ladder: a bounded
// chain of terminal-mode archive attempts until the budget runs out. Each
// destructive step inside logs who/why/what (see the call sites); this entry
// logs the WHO (pid+topic), the WHY (the refusing class), and the outcome.
func (a *App) terminalTopicArchive(topicID string, cause error) error {
	terminalArchiveFallbackRuns.Add(1)
	started := time.Now()
	deadline := started.Add(terminalArchiveBudget)
	slog.Warn("desktop: topic archive entering terminal fallback",
		"topic", topicID, "pid", os.Getpid(),
		"reason", terminalFallbackReason(cause),
		"budget_ms", terminalArchiveBudget.Milliseconds())
	var lastErr error
	for attempt := 0; attempt == 0 || time.Now().Before(deadline); attempt++ {
		if attempt > 0 {
			time.Sleep(terminalArchiveGatePollInterval)
		}
		err := a.trashTopicBounded(topicID, true, true, deadline)
		if err == nil {
			slog.Warn("desktop: topic archive terminal fallback completed",
				"topic", topicID, "pid", os.Getpid(),
				"attempts", attempt+1, "elapsed_ms", time.Since(started).Milliseconds())
			return nil
		}
		lastErr = err
		if errors.Is(err, errTopicArchiveTerminalTimeout) || errors.Is(err, errTopicArchiveExternalHolder) {
			// The budget is gone, or the holder is provably outside this
			// process — neither frees up inside the budget; retrying burns the
			// user's time for nothing.
			break
		}
	}
	slog.Error("desktop: topic archive terminal fallback exhausted its budget",
		"topic", topicID, "pid", os.Getpid(),
		"reason", terminalFallbackReason(lastErr), "last_error", lastErr.Error())
	return lastErr
}

// forceReleaseTopicTabLeases releases the session lease of every in-process
// tab (visible or detached) holding key, regardless of runtime activity.
// Terminal-archive-only: the ordinary sweep protects active work; the
// terminal has already canceled it (snapshot stage) and the user accepted the
// lost-turn cost in the force confirm copy. Caller holds the archive gates
// (same locking shape as reclaimTopicSelfLeasesLocked).
func (a *App) forceReleaseTopicTabLeases(key string) {
	a.mu.Lock()
	released := 0
	for _, tabs := range []map[string]*WorkspaceTab{a.tabs, a.detachedSessions} {
		for _, tab := range tabs {
			if tab == nil || tab.sessionLeaseRuntimeKey() != key {
				continue
			}
			tab.releaseSessionLeaseForKey(key)
			released++
		}
	}
	a.mu.Unlock()
	if released > 0 {
		slog.Warn("desktop: terminal archive released in-process tab lease holders",
			"key_holders_released", released)
	}
}

// terminalArchiveOwnershipReclaim tears down THIS process's lease state for
// one target before a guard retry: tab-bound leases first (unbinding the
// tab), then whatever remains registered at the agent layer (unbound objects,
// owner entries, identity records, lock files). Both halves leave slog trails.
func (a *App) terminalArchiveOwnershipReclaim(key string) {
	a.forceReleaseTopicTabLeases(key)
	if agent.ForceReleaseInProcessSessionLease(key) {
		slog.Warn("desktop: terminal archive force-released the session's in-process lease state",
			"session_base", filepath.Base(key))
	}
}

// terminalArchiveGuardAfterReclaim is the single arbitration point of the
// terminal ownership path: clear this process's lease state for the key, take
// one guard retry, and decide what a still-failing guard means.
//
//   - guard taken            → (guard, nil)
//   - bypassable refusal     → (nil, nil): the OS lease lock is FREE (only
//     the save lock is contested — a wedged saver or a foreign save handle);
//     the trash step proceeds unguarded, tearing whatever residue remains.
//   - non-bypassable refusal → (nil, err): the OS lease lock is still held
//     with no in-process state left to release — a live EXTERNAL holder the
//     terminal must never grab (tl1367 line 1; the identity record may be
//     torn, so the lock itself is the arbiter). The original refusal is
//     wrapped in errTopicArchiveExternalHolder so the retry loop stops
//     immediately while the whole error chain stays reachable.
func (a *App) terminalArchiveGuardAfterReclaim(sessionPath, key string, cause error) (*agent.SessionRemovalGuard, error) {
	a.terminalArchiveOwnershipReclaim(key)
	guard, err := acquireSessionRemovalGuardForOp("terminal_archive", sessionPath)
	if err == nil {
		return guard, nil
	}
	// tl1429: the old input here was SessionLeaseHeldByOtherRuntime, whose
	// lock probe reports true for BOTH a foreign process and an in-process
	// handle whose registry entry is the only trace (the self-lock form:
	// holder record pid-less, no lease object, no tab). The user-side
	// 2026-11-04 17:55 incident burned the whole terminal budget on exactly
	// that misclassification ("held outside this process" for a lock this
	// process itself held). The registry arbitrates now: a booked handle is
	// this process's own state — ForceReleaseInProcessSessionLease above
	// already tears it down, and a still-failing guard then degrades to the
	// guard-less trash below; only an unbooked lock stays "outside".
	if agent.SessionLeaseHeldOutsideProcess(key) {
		slog.Warn("desktop: terminal fallback keeps the refusal: the lease lock is held outside this process",
			"session_base", filepath.Base(sessionPath))
		return nil, fmt.Errorf("%w: %w", errTopicArchiveExternalHolder, cause)
	}
	slog.Warn("desktop: terminal archive could not take the removal guard; trashing without it",
		"session_base", filepath.Base(sessionPath), "reason", terminalFallbackReason(err))
	return nil, nil
}

// trashSessionArtifactsTerminal is the guard-less terminal trash: mutual
// exclusion yields to "the archive must succeed" — a guard that still cannot
// be taken after the in-process clear-down names a holder this process can
// never release (a foreign process, or a wedged foreign handle). The trash
// bucket is always allocated under a unique name, which skips every refusal
// branch the guarded version negotiates (duplicate-copy probing included).
// Session-owned sidecars and lock files are torn best-effort afterwards. A
// vanished transcript takes the direct pass: nothing to move, no bucket (an
// empty trash bucket is tl1363 zombie-entry debris), just residue teardown.
func trashSessionArtifactsTerminal(dir, sessionPath, key string) error {
	defer invalidateTopicSessionIndexForPath(sessionPath)
	if sessionTrashTargetFileMissing(sessionPath) {
		slog.Warn("desktop: terminal archive tearing a missing-file target's residue",
			"session_base", filepath.Base(sessionPath))
		removeSessionOwnedResidue(sessionPath, key)
		return agent.ClearCleanupPending(sessionPath)
	}
	itemDir, err := reserveUniqueSessionTrashItemDir(dir, key)
	if err != nil {
		return err
	}
	if err := invalidateTopicDirMarkers(dir); err != nil {
		return err
	}
	slog.Warn("desktop: terminal archive trashing a session without the removal guard",
		"session_base", filepath.Base(sessionPath))
	// tl1442-ext: same full-inventory counted move + catch-all sweep as the
	// guarded paths; the residue tear below stays as the guard-less backstop.
	report, err := moveSessionTrashArtifactsToBucket(dir, sessionPath, key, itemDir)
	if err != nil {
		return err
	}
	removeSessionOwnedResidue(sessionPath, key)
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

// removeSessionOwnedResidue tears every session-owned leftover the move did
// not take: the sidecars of a vanished transcript (meta/event/lock residue of
// the tl1363 zombie-entry family — the archive used to fake-succeed on such
// topics and leave the residue alive) and the lock/identity files a missing
// guard could not delete atomically. Best-effort with a per-failure log: a
// sidecar whose handle is still held by another process stays until that
// process exits (Windows deletes only under FILE_SHARE_DELETE handles); the
// slog line is the attribution trail (what was cleared, what was blocked).
func removeSessionOwnedResidue(sessionPath, key string) {
	removed, blocked := 0, 0
	tear := func(path string, kind string) {
		if path == "" {
			return
		}
		if err := os.RemoveAll(path); err == nil {
			removed++
		} else if !os.IsNotExist(err) {
			blocked++
			slog.Warn("desktop: topic archive could not tear a session-owned residue file",
				"kind", kind, "err", err)
		}
	}
	for _, artifact := range sessionTrashArtifacts(sessionPath, key) {
		tear(artifact.src, filepath.Base(artifact.src))
	}
	tear(store.SessionLeaseInfo(sessionPath), "lease_identity")
	tear(store.SessionLeaseLock(sessionPath), "lease_lock")
	tear(store.SessionLockFile(sessionPath), "save_lock")
	if removed+blocked > 0 {
		slog.Info("desktop: topic archive tore session-owned residue",
			"removed", removed, "blocked", blocked, "session_base", filepath.Base(sessionPath))
	}
}
