package main

import (
	"log/slog"
	"os"
	"sync"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/store"
)

// historyTimeOverlayFullLookup restores the pre-task-123 behaviour: every
// window that is missing a user timestamp replays the whole session DAG
// (agent.LoadSessionUserMessages) to recover it. That path costs seconds on a
// large session — the measured 4.0 s of a 5.0 s startup — so it is off by
// default and kept only as a code-level retreat: flip it to true to compare or
// to fall back if the bounded tail read ever proves incomplete in a way the
// display cannot tolerate.
var historyTimeOverlayFullLookup = false

// historyTimeOverlayTailBudget caps how much of the event log the tail read may
// touch. Kept as a variable so tests and probes can tighten it.
var historyTimeOverlayTailBudget int64 = agent.SessionUserTimesTailBudgetBytes

const historyTimeOverlayCacheEntries = 8

type historyTimeOverlayCacheEntry struct {
	size    int64
	modTime time.Time
	times   map[string]int64
	// complete reports whether the scan reached the start of the log, i.e.
	// whether an id missing from the map means "this session never recorded
	// that message's time" rather than "the budget ran out".
	complete bool
}

var historyTimeOverlayCache = struct {
	mu      sync.Mutex
	entries map[string]historyTimeOverlayCacheEntry
	order   []string
	hits    int
	misses  int
}{entries: map[string]historyTimeOverlayCacheEntry{}}

// historyTimeOverlayLogIdentity identifies one generation of a session's event
// log. Size plus mtime is the same generation guard the display index uses, and
// is enough here because the log is append-only: an append changes both.
func historyTimeOverlayLogIdentity(sessionPath string) (int64, time.Time, bool) {
	logPath := store.SessionEventLog(sessionPath)
	if logPath == "" {
		return 0, time.Time{}, false
	}
	info, err := os.Stat(logPath)
	if err != nil || info.IsDir() {
		return 0, time.Time{}, false
	}
	return info.Size(), info.ModTime(), true
}

// persistedUserTimesForWindow returns the id→createdAt map for the session's
// newest user messages, reading at most historyTimeOverlayTailBudget bytes from
// the tail of the event log. A nil map means "no timestamps available": the
// caller keeps its own fallback instead of paying for a full DAG replay.
func persistedUserTimesForWindow(sessionPath string) map[string]int64 {
	size, modTime, ok := historyTimeOverlayLogIdentity(sessionPath)
	if !ok {
		return nil
	}
	cacheKey := agent.CanonicalSessionPath(sessionPath)
	historyTimeOverlayCache.mu.Lock()
	if cached, found := historyTimeOverlayCache.entries[cacheKey]; found && cached.size == size && cached.modTime.Equal(modTime) {
		historyTimeOverlayCache.hits++
		historyTimeOverlayCache.mu.Unlock()
		return cached.times
	}
	historyTimeOverlayCache.misses++
	historyTimeOverlayCache.mu.Unlock()

	startedAt := time.Now()
	times, complete := agent.LoadSessionUserTimesTail(sessionPath, historyTimeOverlayTailBudget)
	elapsed := time.Since(startedAt)

	historyTimeOverlayCache.mu.Lock()
	if _, found := historyTimeOverlayCache.entries[cacheKey]; !found {
		historyTimeOverlayCache.order = append(historyTimeOverlayCache.order, cacheKey)
	}
	historyTimeOverlayCache.entries[cacheKey] = historyTimeOverlayCacheEntry{
		size: size, modTime: modTime, times: times, complete: complete,
	}
	for len(historyTimeOverlayCache.order) > historyTimeOverlayCacheEntries {
		oldest := historyTimeOverlayCache.order[0]
		historyTimeOverlayCache.order = historyTimeOverlayCache.order[1:]
		delete(historyTimeOverlayCache.entries, oldest)
	}
	historyTimeOverlayCache.mu.Unlock()

	// Task 123: the tail read is the fast path (measured well under 100 ms for
	// a 146 MiB log), so an incomplete scan is the interesting event — it means
	// the page will show fewer timestamps than the session actually has.
	if !complete {
		historyTimeOverlayLog("info", "history time overlay tail exhausted budget",
			sessionPath, len(times), elapsed)
	}
	return times
}

func historyTimeOverlayLog(level, msg, sessionPath string, entries int, elapsed time.Duration) {
	fields := []any{
		"path", sessionPath,
		"entries", entries,
		"ms", elapsed.Milliseconds(),
	}
	switch level {
	case "info":
		slog.Info("desktop: "+msg, fields...)
	default:
		slog.Debug("desktop: "+msg, fields...)
	}
}

// historyTimeOverlayStats reports the cache counters for probes and tests.
func historyTimeOverlayStats() (hits, misses int) {
	historyTimeOverlayCache.mu.Lock()
	defer historyTimeOverlayCache.mu.Unlock()
	return historyTimeOverlayCache.hits, historyTimeOverlayCache.misses
}
