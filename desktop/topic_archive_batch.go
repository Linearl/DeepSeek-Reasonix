package main

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"reasonix/internal/agent"
)

// tl1240: batch topic archive endpoint. The UI multi-select (batch manage mode)
// submits all selected topics in one bridge call. Design (research report
// tl1240前置, route C): the two global mutation gates are taken ONCE for the
// whole batch, the directory target scan is shared across topics, and the
// fallback runtime / transient-blank sweep / project-tree notification run once
// at the end — per-topic work is only the teardown+move tail, so the batch cost
// is ≈ single + (N-1)×(teardown+move). Lock semantics are unchanged: TryLock or
// busy, no queuing (the B-route async worker pool stays a phase-two item).

type desktopBatchTopicArchiveItem struct {
	TopicID string `json:"topicId"`
	OK      bool   `json:"ok"`
	// Forceable marks failures from the same family TrashTopicForce can clear
	// (active-work gate, session-lease-busy) so the frontend can hint the
	// per-topic force path; see markTopicArchiveForceable (tl 1103).
	Forceable bool   `json:"forceable"`
	Error     string `json:"error,omitempty"`
}

type desktopBatchTopicArchiveResult struct {
	Items     []desktopBatchTopicArchiveItem `json:"items"`
	Succeeded int                            `json:"succeeded"`
	Failed    int                            `json:"failed"`
	TotalMs   int64                          `json:"totalMs"`
}

// BatchTrashTopics archives several topics under one acquisition of the global
// mutation gates. Per-topic failures are isolated: a topic that fails before
// its durable commit point (cleanup-pending) is reported in Items and the batch
// continues with the remaining topics — the single-topic path's rollback rules
// apply unchanged per topic. A fully-busy acquisition declines the whole batch
// with errTopicArchiveBusy, same as the single-topic endpoint.
func (a *App) BatchTrashTopics(topicIDs []string) (desktopBatchTopicArchiveResult, error) {
	ordered := normalizedBatchTopicIDs(topicIDs)
	if len(ordered) == 0 {
		return desktopBatchTopicArchiveResult{}, fmt.Errorf("topicIDs is required")
	}
	started := time.Now()
	result := desktopBatchTopicArchiveResult{Items: make([]desktopBatchTopicArchiveItem, 0, len(ordered))}
	trace := topicArchiveTrace{phase: "start"}
	defer func() {
		slog.Debug("desktop: batch topic archive timing", "phase", trace.phase,
			"total_ms", time.Since(started).Milliseconds(), "requested", len(ordered),
			"succeeded", result.Succeeded, "failed", result.Failed,
			"target_count", trace.targetCount, "runtime_count", trace.runtimeCount)
	}()
	trace.phase = "runtime_lock"
	releaseRuntime, ok := a.tryLockRuntimeMutation("batch-trash-topics")
	if !ok {
		return result, errTopicArchiveBusy
	}
	defer releaseRuntime()
	trace.phase = "removal_lock"
	if !a.sessionRemovalMu.TryLock() {
		return result, errTopicArchiveBusy
	}
	defer a.sessionRemovalMu.Unlock()
	// Per-topic active-work gate FIRST, before any scan: the runtime part of
	// the scan dereferences live tab controllers, and a gated-off topic must
	// never be touched (same phase order as the single-topic path).
	trace.phase = "active_work_check"
	survivors := make([]string, 0, len(ordered))
	for _, topicID := range ordered {
		if a.topicHasActiveRuntimeWork(topicID) {
			result.Failed++
			result.Items = append(result.Items, batchTopicArchiveFailureItem(topicID, errTopicHasActiveWork))
			continue
		}
		survivors = append(survivors, topicID)
	}
	if len(survivors) == 0 {
		return result, nil
	}
	// Merged target scan: one pass over the session directories for all
	// surviving topics; runtime tab bindings merge per topic afterwards.
	trace.phase = "target_scan"
	index := memoizedTopicSessionDirIndex()
	targetsByTopic := make(map[string][]topicTrashTarget, len(survivors))
	for _, topicID := range survivors {
		targets, err := a.topicTrashTargetsFromDirIndexes(topicID, index)
		if err != nil {
			result.Failed++
			result.Items = append(result.Items, batchTopicArchiveFailureItem(topicID, err))
			continue
		}
		targets, err = a.appendTopicRuntimeTrashTargets(topicID, targets)
		if err != nil {
			result.Failed++
			result.Items = append(result.Items, batchTopicArchiveFailureItem(topicID, err))
			continue
		}
		targetsByTopic[topicID] = targets
	}
	batchChangedDirs := map[string]bool{}
	var batchFallback fallbackRuntimeTarget
	for _, topicID := range survivors {
		item := desktopBatchTopicArchiveItem{TopicID: topicID}
		// Topics already reported as failed by the active-work gate or the
		// scan have no entry here — their failure item is already appended.
		targets, ok := targetsByTopic[topicID]
		if !ok {
			continue
		}
		topicTrace := topicArchiveTrace{phase: "start"}
		fallback, changedDirs, err := a.commitTopicArchiveTargets(topicID, targets, &topicTrace, false)
		trace.targetCount += topicTrace.targetCount
		trace.runtimeCount += topicTrace.runtimeCount
		trace.phase = topicTrace.phase
		if err != nil {
			result.Failed++
			result.Items = append(result.Items, batchTopicArchiveFailureItem(topicID, err))
			continue
		}
		item.OK = true
		result.Succeeded++
		for _, dir := range changedDirs {
			batchChangedDirs[dir] = true
		}
		// Mirror the single-path tail: every fallback workspace whose sessions
		// were removed joins the sweep set; the last fallback that actually
		// emptied the tab strip decides the fallback runtime target.
		if fallback.workspaceRoot != "" {
			batchChangedDirs[desktopSessionDir(fallback.workspaceRoot)] = true
		}
		if fallback.needs {
			batchFallback = fallback
		}
		result.Items = append(result.Items, item)
	}
	if result.Succeeded == 0 {
		// Nothing changed — stay silent like the single path, which never
		// reaches its notify tail after a failure.
		return result, nil
	}
	if batchFallback.needs && batchFallback.workspaceRoot == "" {
		batchChangedDirs[desktopSessionDir(globalWorkspaceRoot())] = true
	}
	changedDirs := make([]string, 0, len(batchChangedDirs))
	for dir := range batchChangedDirs {
		changedDirs = append(changedDirs, dir)
	}
	// Remove abandoned transient blanks before fallback construction registers
	// the project (same ordering rationale as the single-topic path).
	trace.phase = "discard_existing_blanks"
	a.discardUnusedTransientBlankSessions(changedDirs, "")
	if batchFallback.needs {
		trace.phase = "open_fallback"
		batchFallback.topicID = ""
		if err := a.openFallbackRuntime(batchFallback); err != nil {
			// Runtime construction errors can include provider configuration
			// details, so keep this recovery diagnostic value-free.
			slog.Warn("desktop: open fallback after batch topic archive failed")
		}
	}
	keepPath := ""
	a.mu.RLock()
	if tab := a.tabs[a.activeTabID]; tab != nil {
		keepPath = tab.SessionPath
	}
	a.mu.RUnlock()
	trace.phase = "discard_unused_blanks"
	a.discardUnusedTransientBlankSessions(changedDirs, keepPath)
	trace.phase = "notify"
	if len(changedDirs) > 0 {
		a.emitProjectTreeChangedForSessionDirs(changedDirs...)
	} else {
		a.emitProjectTreeMetadataChanged()
	}
	result.TotalMs = time.Since(started).Milliseconds()
	return result, nil
}

func batchTopicArchiveFailureItem(topicID string, err error) desktopBatchTopicArchiveItem {
	// tl1258: the lease-busy family gets the complete per-topic reason (holder
	// classification, pid, acquire time, next step) — the bare sentinel text
	// gave every busy topic the same "in use" line with nothing to act on.
	// This must run BEFORE friendlySessionFileError: the wrapped lease error's
	// Unwrap chain contains agent.ErrSessionLeaseHeld, which that rewriter
	// would collapse into the generic sentinel and drop the holder info.
	// Active-work refusals keep their own message below: it already names the
	// next step ("wait for the session to finish…").
	if errors.Is(err, errSessionBusyElsewhere) || errors.Is(err, errSessionBusyZombie) || errors.Is(err, agent.ErrSessionLeaseHeld) {
		return desktopBatchTopicArchiveItem{TopicID: topicID, Forceable: true, Error: sessionLeaseBusyDetail(err)}
	}
	marked := markTopicArchiveForceable(friendlySessionFileError(err))
	item := desktopBatchTopicArchiveItem{TopicID: topicID}
	var forceable *topicArchiveForceableError
	if errors.As(marked, &forceable) {
		item.Forceable = true
		item.Error = forceable.cause.Error()
		return item
	}
	if marked == nil {
		item.Error = "unknown archive failure"
		return item
	}
	item.Error = marked.Error()
	return item
}

// normalizedBatchTopicIDs trims, drops empties and dedupes the batch input,
// preserving first-seen order.
func normalizedBatchTopicIDs(topicIDs []string) []string {
	seen := map[string]bool{}
	ordered := make([]string, 0, len(topicIDs))
	for _, topicID := range topicIDs {
		topicID = strings.TrimSpace(topicID)
		if topicID == "" || seen[topicID] {
			continue
		}
		seen[topicID] = true
		ordered = append(ordered, topicID)
	}
	return ordered
}

// memoizedTopicSessionDirIndex returns a per-directory index getter that builds
// each directory's topic index at most once per call. The batch endpoint uses
// it so N topics × M directories costs M index builds instead of N×M.
func memoizedTopicSessionDirIndex() func(dir string) (topicSessionDirIndex, error) {
	return memoizedTopicSessionDirIndexWith(topicSessionIndexForDir)
}

// memoizedTopicSessionDirIndexWith memoizes an arbitrary per-directory index
// builder; the injectable builder exists so tests can count real index builds.
// Note the cache is keyed by the raw dir string: knownSessionDirs may list the
// same directory twice under different registration keys, and each distinct
// string gets its own entry (mirroring the per-call behavior it wraps).
func memoizedTopicSessionDirIndexWith(build func(dir string) (topicSessionDirIndex, error)) func(dir string) (topicSessionDirIndex, error) {
	cache := map[string]topicSessionDirIndex{}
	return func(dir string) (topicSessionDirIndex, error) {
		if index, ok := cache[dir]; ok {
			return index, nil
		}
		index, err := build(dir)
		if err != nil {
			return topicSessionDirIndex{}, err
		}
		cache[dir] = index
		return index, nil
	}
}
