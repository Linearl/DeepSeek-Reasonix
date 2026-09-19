package main

import (
	"errors"
	"io"
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
// touch. It is also the ceiling on how much of an append the cache is willing to
// absorb incrementally before re-reading the tail.
var historyTimeOverlayTailBudget int64 = agent.SessionUserTimesTailBudgetBytes

// historyTimeOverlayMaxEntries bounds one cache entry's map. The map grows by
// appends, and a session that runs for hours would otherwise accumulate every
// id it ever saw; past this point a full tail re-read replaces it.
const historyTimeOverlayMaxEntries = 4096

const historyTimeOverlayCacheEntries = 8

type historyTimeOverlayCacheEntry struct {
	// head is the signature of the log's opening bytes: the schema-2 header
	// carries the generation, so a rotation (which changes what the tail means)
	// invalidates the entry even when the new file happens to be larger.
	head string
	// size is the byte offset the entry's times were scanned up to. Growth beyond
	// it is absorbed incrementally as long as the append stays inside the budget.
	size int64
	// times holds the newest known created-at per message id.
	times map[string]int64
	// complete reports whether a scan reached the start of the log, i.e. whether
	// an id missing from the map means "this session never recorded that message's
	// time" rather than "the budget ran out".
	complete bool
}

var historyTimeOverlayCache = struct {
	mu      sync.Mutex
	entries map[string]historyTimeOverlayCacheEntry
	order   []string
	hits    int
	misses  int
	extends int
}{entries: map[string]historyTimeOverlayCacheEntry{}}

// historyTimeOverlayLogIdentity identifies one generation of a session's event
// log: its size plus the bytes it opens with. Size alone is not a generation
// guard (a rotating log can be replaced by a shorter one and then grow back past
// the cached size), and mtime alone is not either (every append moves it, which
// is exactly the case we want to survive).
func historyTimeOverlayLogIdentity(sessionPath string) (size int64, head string, ok bool) {
	logPath := store.SessionEventLog(sessionPath)
	if logPath == "" {
		return 0, "", false
	}
	f, err := os.Open(logPath)
	if err != nil {
		return 0, "", false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return 0, "", false
	}
	buf := make([]byte, 128)
	n, err := f.ReadAt(buf, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, "", false
	}
	return info.Size(), string(buf[:n]), true
}

// persistedUserTimesForWindow returns the id→createdAt map for the session's
// newest user messages, reading at most historyTimeOverlayTailBudget bytes from
// the tail of the event log. A nil map means "no timestamps available": the
// caller keeps its own fallback instead of paying for a full DAG replay.
//
// 2026-09-19 regression fix: the cache used to be keyed on size+mtime, so a
// running turn — which appends to the event log continuously — invalidated it on
// every read and every tab switch re-scanned the whole tail window (measured
// 48-115 ms per switch on the 116 MB session in the logs). The log is
// append-only, so the cached map stays valid as the file grows: the fix extends
// it with just the appended range and only falls back to a fresh tail read when
// the append is too large, the head changed (rotation), or the map hit its cap.
func persistedUserTimesForWindow(sessionPath string) map[string]int64 {
	size, head, ok := historyTimeOverlayLogIdentity(sessionPath)
	if !ok {
		return nil
	}
	cacheKey := agent.CanonicalSessionPath(sessionPath)

	historyTimeOverlayCache.mu.Lock()
	cached, found := historyTimeOverlayCache.entries[cacheKey]
	if found && cached.head == head && size >= cached.size {
		if size == cached.size {
			historyTimeOverlayCache.hits++
			historyTimeOverlayCache.mu.Unlock()
			return cached.times
		}
		appended := size - cached.size
		if appended <= historyTimeOverlayTailBudget && len(cached.times) < historyTimeOverlayMaxEntries {
			historyTimeOverlayCache.mu.Unlock()
			startedAt := time.Now()
			delta, err := agent.LoadSessionUserTimesRange(sessionPath, cached.size)
			if err == nil {
				merged := make(map[string]int64, len(cached.times)+len(delta))
				for id, at := range cached.times {
					merged[id] = at
				}
				for id, at := range delta {
					merged[id] = at
				}
				historyTimeOverlayCache.mu.Lock()
				historyTimeOverlayCache.extends++
				historyTimeOverlayCache.entries[cacheKey] = historyTimeOverlayCacheEntry{
					head: head, size: size, times: merged, complete: cached.complete,
				}
				historyTimeOverlayCache.mu.Unlock()
				historyTimeOverlayLog("debug", "history time overlay cache extended",
					sessionPath, len(delta), time.Since(startedAt))
				return merged
			}
			// Fall through to a full tail read when the range scan fails.
			historyTimeOverlayCache.mu.Lock()
		}
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
		head: head, size: size, times: times, complete: complete,
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

// historyTimeOverlayStats reports the cache counters for probes and tests:
// hits are exact re-serves, extends are append-absorbing range scans, misses are
// full tail reads.
func historyTimeOverlayStats() (hits, misses, extends int) {
	historyTimeOverlayCache.mu.Lock()
	defer historyTimeOverlayCache.mu.Unlock()
	return historyTimeOverlayCache.hits, historyTimeOverlayCache.misses, historyTimeOverlayCache.extends
}

// resetHistoryTimeOverlayCache clears the cache; tests use it to observe cold
// reads, and a probe can use it to compare cold and warm numbers.
func resetHistoryTimeOverlayCache() {
	historyTimeOverlayCache.mu.Lock()
	defer historyTimeOverlayCache.mu.Unlock()
	historyTimeOverlayCache.entries = map[string]historyTimeOverlayCacheEntry{}
	historyTimeOverlayCache.order = nil
	historyTimeOverlayCache.hits = 0
	historyTimeOverlayCache.misses = 0
	historyTimeOverlayCache.extends = 0
}
