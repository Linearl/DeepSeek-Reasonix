package agent

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"reasonix/internal/store"
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

// Task 499 (2026-10-05): the count cap alone let the cached set grow without
// bound in bytes — the 10-05 incident held 7.6 GB working set / 14.6 GB heap
// with the graph states of closed sessions still pinned here (entries hold
// references shared with the owning Session, so every entry outlives its
// runtime). Two byte ceilings bound the retained set, both pushed from config
// (dag_graph_cache_max_mb / dag_graph_cache_entry_max_mb, 0 in the file means
// "use the default"):
//
//   - total cap (default 2 GiB): the sum of cached entries is evicted
//     LRU-until-under, so the worst retained set is exactly the cap;
//   - per-entry cap (default 1 GiB): a single state above it is refused
//     admission entirely, so one monster session cannot pin the whole budget
//     after its session is gone. The known-worst real log (728 MB, task 196)
//     stays well under and keeps its cache hits.
//
// The accounted bytes are st.size — the replayed log byte count every
// replayFrom refreshes — not a deep walk: replay/snapshot payload dominates
// the state (the node map holds full messages), Go object overhead puts the
// resident footprint above it, so the cap errs conservative in the right
// direction. Eviction is a pure cache miss to every consumer (save and load
// paths both fall through to a correct full replay), so the caps trade
// replay time, never correctness.
const (
	sessionGraphCacheMaxBytesDefault       = int64(2) << 30
	sessionGraphCacheEntryMaxBytesDefault  = int64(1) << 30
	sessionGraphCacheMaxBytesMin           = int64(16) << 20
	sessionGraphCacheMaxBytesMax           = int64(64) << 30
	sessionGraphCacheEntryMaxBytesMin      = int64(16) << 20
	sessionGraphCacheEntryMaxBytesMaxValue = int64(64) << 30
)

type sessionGraphCacheEntry struct {
	state    *sessionDAGState
	lastUsed time.Time
	// approxBytes is the entry's accounted share of the byte budget: the
	// state's replayed log bytes at Put time. Saves re-Put on every pass, so
	// in-place growth between saves is re-accounted within one save.
	approxBytes int64
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
	// Task 499 byte ceilings: zero means "never pushed" and reads as the
	// built-in default, same convention as the capacity.
	sessionGraphCacheMaxBytesVar      atomic.Int64
	sessionGraphCacheEntryMaxBytesVar atomic.Int64
	sessionGraphCacheBytes            int64 // guarded by sessionGraphCacheMu
	sessionGraphCacheEntryRefusals    atomic.Uint64
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

// SetSessionGraphCacheMaxBytes pushes the total byte ceiling (task 499).
// Clamped rather than refused: this is the backstop behind the config-layer
// setter, same convention as SetSessionGraphCacheCapacity.
func SetSessionGraphCacheMaxBytes(maxBytes int64) {
	if maxBytes < sessionGraphCacheMaxBytesMin {
		maxBytes = sessionGraphCacheMaxBytesDefault
	}
	if maxBytes > sessionGraphCacheMaxBytesMax {
		maxBytes = sessionGraphCacheMaxBytesMax
	}
	sessionGraphCacheMaxBytesVar.Store(maxBytes)
}

// SetSessionGraphCacheEntryMaxBytes pushes the per-entry admission ceiling
// (task 499), clamped like the total.
func SetSessionGraphCacheEntryMaxBytes(maxBytes int64) {
	if maxBytes < sessionGraphCacheEntryMaxBytesMin {
		maxBytes = sessionGraphCacheEntryMaxBytesDefault
	}
	if maxBytes > sessionGraphCacheEntryMaxBytesMaxValue {
		maxBytes = sessionGraphCacheEntryMaxBytesMaxValue
	}
	sessionGraphCacheEntryMaxBytesVar.Store(maxBytes)
}

// currentSessionGraphCacheMaxBytes resolves the effective total ceiling.
func currentSessionGraphCacheMaxBytes() int64 {
	if v := sessionGraphCacheMaxBytesVar.Load(); v >= sessionGraphCacheMaxBytesMin && v <= sessionGraphCacheMaxBytesMax {
		return v
	}
	return sessionGraphCacheMaxBytesDefault
}

// currentSessionGraphCacheEntryMaxBytes resolves the effective per-entry
// ceiling, clamped to the total so a hand-tuned entry cap above the total can
// never defeat the total (worst retained set stays exactly the total cap).
func currentSessionGraphCacheEntryMaxBytes() int64 {
	total := currentSessionGraphCacheMaxBytes()
	v := sessionGraphCacheEntryMaxBytesVar.Load()
	if v < sessionGraphCacheEntryMaxBytesMin || v > sessionGraphCacheEntryMaxBytesMaxValue {
		v = sessionGraphCacheEntryMaxBytesDefault
	}
	if v > total {
		v = total
	}
	return v
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
	// Task 499: the accounted size is the replayed log byte count — every
	// replayFrom refreshes st.size, and every save re-Puts, so the number
	// tracks the live graph within one save of growth.
	size := st.size
	if entryMax := currentSessionGraphCacheEntryMaxBytes(); size > entryMax {
		// A single state above the per-entry ceiling is refused admission:
		// the next save/load for it falls through to a correct full replay,
		// and no closed monster session pins the whole byte budget.
		sessionGraphCacheEntryRefusals.Add(1)
		slog.Debug("session: dag graph cache entry refused (over entry byte cap)",
			"path", key, "log_bytes", size, "entry_cap_bytes", entryMax)
		return
	}
	sessionGraphCacheMu.Lock()
	defer sessionGraphCacheMu.Unlock()
	if old, ok := sessionGraphCache[key]; ok && old != nil {
		// Replacement: the old entry's share leaves the budget before the new
		// one enters, so re-putting a grown state cannot double-count.
		sessionGraphCacheBytes -= old.approxBytes
	}
	sessionGraphCache[key] = &sessionGraphCacheEntry{state: st, lastUsed: time.Now(), approxBytes: size}
	sessionGraphCacheBytes += size
	sessionGraphCacheEvictOverbudgetLocked(key)
}

// sessionGraphCacheEvictOverbudgetLocked enforces both ceilings after a Put.
// Eviction is LRU (task 239 tie-break preserved) and never takes the
// just-put entry, so the entry the next save needs cannot be evicted by its
// own Put; the loop stops when only that entry remains, which bounds the
// worst retained set at the total cap (a lone over-cap entry is refused
// upstream by the per-entry ceiling).
func sessionGraphCacheEvictOverbudgetLocked(justPut string) {
	capacity := currentSessionGraphCacheCapacity()
	maxBytes := currentSessionGraphCacheMaxBytes()
	for len(sessionGraphCache) > capacity || (sessionGraphCacheBytes > maxBytes && len(sessionGraphCache) > 1) {
		oldestKey := ""
		var oldestUsed time.Time
		for key, entry := range sessionGraphCache {
			if key == justPut && len(sessionGraphCache) > 1 {
				continue
			}
			if oldestKey == "" || entry.lastUsed.Before(oldestUsed) ||
				(entry.lastUsed.Equal(oldestUsed) && key < oldestKey) {
				oldestKey, oldestUsed = key, entry.lastUsed
			}
		}
		if oldestKey == "" {
			break
		}
		if entry := sessionGraphCache[oldestKey]; entry != nil {
			sessionGraphCacheBytes -= entry.approxBytes
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

// SessionGraphCacheByteStats reports the byte-side state of the cache (task
// 499): accounted bytes, live entries, the effective ceilings, and how many
// oversized single states were refused admission. The "dag state for save"
// log line carries bytesNow and the total cap so an on-device reading can
// attribute evictions to byte pressure instead of guessing.
func SessionGraphCacheByteStats() (bytesNow, totalCap, entryCap int64, entries int, entryRefusals uint64) {
	sessionGraphCacheMu.Lock()
	defer sessionGraphCacheMu.Unlock()
	return sessionGraphCacheBytes,
		currentSessionGraphCacheMaxBytes(), currentSessionGraphCacheEntryMaxBytes(),
		len(sessionGraphCache),
		sessionGraphCacheEntryRefusals.Load()
}

// Task 499 ②: the cache had no invalidation point anywhere — a closed tab, a
// detached runtime release, a deleted session all left the fully replayed
// graph pinned here forever, which is the retention half of the 10-05 memory
// bloat (14.6 GB heap). Hosts call InvalidateSessionGraph when a session's
// runtime is torn down; the next open replays fresh, which is exactly the
// pre-cache behaviour for a session nobody holds.
var sessionGraphCacheInvalidations atomic.Uint64

// InvalidateSessionGraph drops the cached graph for one session (the session
// path, not the log path — the same input the save path takes). It reports
// the accounted bytes freed and whether an entry existed. Dropping an entry
// is always correct: a live Session keeps its own state reference, and any
// fresh reader falls through to a full replay (the memory/time trade the
// task prescribes for closed sessions).
func InvalidateSessionGraph(sessionPath string) (freedBytes int64, ok bool) {
	logPath := store.SessionEventLog(sessionPath)
	key := sessionGraphCacheKey(logPath)
	if key == "" {
		return 0, false
	}
	sessionGraphCacheMu.Lock()
	defer sessionGraphCacheMu.Unlock()
	entry, exists := sessionGraphCache[key]
	if !exists || entry == nil {
		return 0, false
	}
	freed := entry.approxBytes
	delete(sessionGraphCache, key)
	sessionGraphCacheBytes -= freed
	sessionGraphCacheInvalidations.Add(1)
	return freed, true
}

// SessionGraphCacheInvalidations reports the cumulative invalidate calls that
// dropped an entry (task 499 on-device evidence: desktop close paths must
// move this counter).
func SessionGraphCacheInvalidations() uint64 {
	return sessionGraphCacheInvalidations.Load()
}
