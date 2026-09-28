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
// Capacity is tunable (task 196fix2, reviewed 2026-09-28): the original
// capacity-1 design assumed one large file saved repeatedly, but the 0928
// reading showed three sessions interleaving their saves — every neighbour
// Put evicted the entry the next save needed, so the cache scored zero hits
// and every save replayed 122 MB under the lock. The built-in default is now
// 3 (covers the primary session plus two companions) and the host pushes a
// configured value through SetSessionGraphCacheCapacity (0 in the file means
// "use the default"; the reader/setter bound the range to 1..16).
// tailTruncated window states are never admitted: the write-path contract
// forbids them anywhere near a save.
const (
	sessionGraphCacheCapacityDefault = 3
	sessionGraphCacheCapacityMin     = 1
	sessionGraphCacheCapacityMax     = 16
)

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
	// sessionGraphCacheCapacityVar holds the tunable LRU capacity (task
	// 196fix2). The zero value means "never pushed" and reads as the built-in
	// default, so CLI/serve hosts and tests behave sanely before any push.
	sessionGraphCacheCapacityVar atomic.Int64
)

// SetSessionGraphCacheCapacity pushes the tunable LRU capacity (task 196fix2).
// Invalid values are clamped rather than refused: this is the backstop behind
// the config-layer setter, and a silently rejected update would leave the gate
// on the previous capacity with no signal.
func SetSessionGraphCacheCapacity(capacity int) {
	if capacity < sessionGraphCacheCapacityMin {
		capacity = sessionGraphCacheCapacityDefault
	}
	if capacity > sessionGraphCacheCapacityMax {
		capacity = sessionGraphCacheCapacityMax
	}
	sessionGraphCacheCapacityVar.Store(int64(capacity))
}

// currentSessionGraphCacheCapacity resolves the effective capacity: an
// unpromoted zero reads as the built-in default.
func currentSessionGraphCacheCapacity() int {
	if v := int(sessionGraphCacheCapacityVar.Load()); v >= sessionGraphCacheCapacityMin && v <= sessionGraphCacheCapacityMax {
		return v
	}
	return sessionGraphCacheCapacityDefault
}

// sessionGraphCacheKey normalizes the cache key at the boundary: callers pass
// either a raw or a canonical session path, and on Windows those diverge by
// case (C:\Users\... vs c:\users\...). Normalizing here keeps all five call
// sites consistent without touching each one. Task 239 audit fix.
func sessionGraphCacheKey(logPath string) string {
	if logPath == "" {
		return ""
	}
	return canonicalSessionSavePath(logPath)
}

func sessionGraphCacheGet(logPath string) *sessionDAGState {
	key := sessionGraphCacheKey(logPath)
	if key == "" {
		return nil
	}
	sessionGraphCacheMu.Lock()
	defer sessionGraphCacheMu.Unlock()
	entry, ok := sessionGraphCache[key]
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
	key := sessionGraphCacheKey(logPath)
	if key == "" {
		return
	}
	sessionGraphCacheMu.Lock()
	defer sessionGraphCacheMu.Unlock()
	sessionGraphCache[key] = &sessionGraphCacheEntry{state: st, lastUsed: time.Now()}
	capacity := currentSessionGraphCacheCapacity()
	for len(sessionGraphCache) > capacity {
		// Task 239 M1-1: deterministic tie-break — when timestamps are equal,
		// compare keys so map iteration order cannot change the eviction victim.
		oldestKey := ""
		var oldestUsed time.Time
		for key, entry := range sessionGraphCache {
			if oldestKey == "" || entry.lastUsed.Before(oldestUsed) ||
				(entry.lastUsed.Equal(oldestUsed) && key < oldestKey) {
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
// diagnostics can prove cache behaviour instead of inferring it (task 196fix2
// feeds them into the "dag state for save" log line for the on-device
// hits>0 self-verification).
func SessionGraphCacheStats() (hits, misses, evictions uint64) {
	return sessionGraphCacheHits.Load(), sessionGraphCacheMisses.Load(), sessionGraphCacheEvictions.Load()
}

// SessionGraphCacheCapacityNow reports the effective LRU capacity (task
// 196fix2) — the host-side setter test proves a config push landed, and the
// "dag state for save" log line carries the same value for on-device checks.
func SessionGraphCacheCapacityNow() int {
	return currentSessionGraphCacheCapacity()
}
