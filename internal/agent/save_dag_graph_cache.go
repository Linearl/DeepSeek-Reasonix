package agent

import (
	"sync"
	"sync/atomic"
	"time"
)

// Task 196: an in-process cache of fully replayed session graphs, keyed by the
// canonical event-log path. A fresh Session instance has no graph of its own,
// so without this cache its first save re-read the whole log under the
// save-path lock (measured: 20-30s on a 728 MB log). Entries hold references
// shared with the owning Session, never copies.
//
// Capacity is deliberately 1 (reviewed 2026-09-21): a second large graph would
// sit resident for its own sake, against the memory-governance targets, while
// the dominant pattern - one large file saved repeatedly - is already covered.
// tailTruncated window states are never admitted: the write-path contract
// forbids them anywhere near a save.
const sessionGraphCacheCapacity = 1

type sessionGraphCacheEntry struct {
	state    *sessionDAGState
	lastUsed time.Time
}

var (
	sessionGraphCacheMu        sync.Mutex
	sessionGraphCache          = map[string]*sessionGraphCacheEntry{}
	sessionGraphCacheHits      atomic.Uint64
	sessionGraphCacheMisses    atomic.Uint64
	sessionGraphCacheEvictions atomic.Uint64
)

func sessionGraphCacheGet(logPath string) *sessionDAGState {
	sessionGraphCacheMu.Lock()
	defer sessionGraphCacheMu.Unlock()
	entry, ok := sessionGraphCache[logPath]
	if !ok || entry == nil {
		sessionGraphCacheMisses.Add(1)
		return nil
	}
	entry.lastUsed = time.Now()
	sessionGraphCacheHits.Add(1)
	return entry.state
}

func sessionGraphCachePut(logPath string, st *sessionDAGState) {
	if st == nil || st.tailTruncated {
		return
	}
	sessionGraphCacheMu.Lock()
	defer sessionGraphCacheMu.Unlock()
	sessionGraphCache[logPath] = &sessionGraphCacheEntry{state: st, lastUsed: time.Now()}
	for len(sessionGraphCache) > sessionGraphCacheCapacity {
		oldestKey := ""
		var oldestUsed time.Time
		for key, entry := range sessionGraphCache {
			if oldestKey == "" || entry.lastUsed.Before(oldestUsed) {
				oldestKey, oldestUsed = key, entry.lastUsed
			}
		}
		if oldestKey == "" {
			break
		}
		delete(sessionGraphCache, oldestKey)
		sessionGraphCacheEvictions.Add(1)
	}
}

// SessionGraphCacheStats returns the hit/miss/eviction counters so tests and
// diagnostics can prove cache behaviour instead of inferring it.
func SessionGraphCacheStats() (hits, misses, evictions uint64) {
	return sessionGraphCacheHits.Load(), sessionGraphCacheMisses.Load(), sessionGraphCacheEvictions.Load()
}
