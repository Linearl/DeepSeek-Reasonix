package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// P18-R1 (2026-10-04): serve the load path from the task-196 session graph
// cache. The cache already held fully replayed DAG states (put by every load
// and save), but only the SAVE path ever consulted it — every load (tab open,
// history search, prompt history, topic-title derivation, timestamp backfill)
// re-decoded the whole event log, and on a multi-hundred-MB session that is a
// 25-30s decode that the DAG investigation measured running twice a minute
// while holding the save-path lock. Serving a byte-identical log from the
// cached state turns those loads into an O(materialize) pass (measured ~5ms
// for 3900 messages).
//
// Correctness contract, mirroring the save path's own reuse guards
// (dagStateForSave): the cached state is trusted only when
//   - the log header's generation still matches the state's (a rewrite/heal
//     that kept the size would otherwise pose as an append), and
//   - the log's current size equals the state's lastGoodEnd (append-only
//     contract: nothing new to replay; growth falls through to a full
//     replay, and the save path is the one that extends cached states), and
//   - the state is neither damaged nor a truncated tail window.
// Readers that hold the save-path lock (LoadSession) use the cached state
// directly; lockless callers (LoadSessionUserMessages) take the save-path
// lock for the hit window — the state is extended in place under that same
// lock by dagStateForSave, so a lockless materialize could race a concurrent
// save. The hit-window hold is an O(materialize) pass, far below the
// full-decode hold it replaces.
//
// Rollback: REASONIX_DAG_LOAD_CACHE=0 restores the exact pre-P18 behavior.
// SessionDAGLoadCacheStats exposes hit/miss counters for on-device proof.

var (
	sessionDAGLoadCacheHits   atomic.Uint64
	sessionDAGLoadCacheMisses atomic.Uint64
)

// SessionDAGLoadCacheStats returns the load-path cache hit/miss counters so an
// on-device reading can prove the fast path fired (P18 acceptance evidence).
func SessionDAGLoadCacheStats() (hits, misses uint64) {
	return sessionDAGLoadCacheHits.Load(), sessionDAGLoadCacheMisses.Load()
}

// dagLoadCacheEnabled reads the P18-R1 gate. Default on; REASONIX_DAG_LOAD_
// CACHE=0 restores the pre-P18 always-replay load path (env-switch precedent:
// REASONIX_DAG_FOLD_CHECKPOINT).
func dagLoadCacheEnabled() bool {
	return os.Getenv("REASONIX_DAG_LOAD_CACHE") != "0"
}

// cachedSessionLoadResult materializes the selected head from the cached graph
// when the cache and the bytes on disk still agree. The caller MUST hold the
// save-path lock for path (LoadSession already does; LoadSessionUserMessages
// takes it for the hit window): the cached state is extended in place under
// that lock by dagStateForSave. hasher may be nil where the caller does not
// need a transcript digest. ok=false means "fall through to the ordinary full
// replay" — every guard failure is a normal miss, not an error.
func cachedSessionLoadResult(path string, hasher *sessionTranscriptHasher) (sessionLoadResult, bool) {
	if !dagLoadCacheEnabled() {
		return sessionLoadResult{}, false
	}
	logPath := store.SessionEventLog(path)
	if logPath == "" {
		return sessionLoadResult{}, false
	}
	st := sessionGraphCacheGet(logPath)
	if st == nil || st.tailTruncated || st.damaged {
		sessionDAGLoadCacheMisses.Add(1)
		return sessionLoadResult{}, false
	}
	header, ok, err := readSessionDAGHeader(path)
	if err != nil || !ok || header.generation != st.generation {
		sessionDAGLoadCacheMisses.Add(1)
		return sessionLoadResult{}, false
	}
	info, err := os.Stat(logPath)
	if err != nil || info.IsDir() || info.Size() < st.lastGoodEnd {
		sessionDAGLoadCacheMisses.Add(1)
		return sessionLoadResult{}, false
	}
	// lastGoodEnd stops at the last complete entry; the writer's trailing
	// newline sits beyond it. Only whitespace may live in [lastGoodEnd, size)
	// for the cached state to be the honest whole transcript.
	if info.Size() > st.lastGoodEnd && !logTailWhitespaceOnly(logPath, st.lastGoodEnd, info.Size()) {
		sessionDAGLoadCacheMisses.Add(1)
		return sessionLoadResult{}, false
	}
	startedAt := time.Now()
	headID := st.selectedHead()
	msgs, times := st.materialize(headID)
	if hasher != nil {
		hasher.addAll(msgs)
	}
	sessionDAGLoadCacheHits.Add(1)
	slog.Debug("session: load from dag graph cache",
		"path", canonicalSessionSavePath(path),
		"messages", len(msgs),
		"log_bytes", info.Size(),
		"materialize_ms", time.Since(startedAt).Milliseconds())
	return sessionLoadResult{
		msgs: msgs, times: times, fromEvents: true, damaged: st.damaged, dag: true,
		head:      HeadRef{HeadID: headID, LeafID: st.heads[headID].leaf, LogGeneration: st.generation, LogOffset: st.lastGoodEnd},
		headCount: len(st.heads),
		state:     st,
		openTurn:  st.heads[headID].openTurn,
		events:    loadHeadEvents(st, headID),
	}, true
}

// loadSessionUserMessagesCached is the P18-R1 fast path for
// LoadSessionUserMessages: the user-turn projection computed from a cached
// graph, taken under the save-path lock for the hit window. ok=false falls
// through to the ordinary full replay (which builds and caches a fresh state).
func loadSessionUserMessagesCached(path string) ([]SessionUserMessage, bool) {
	if !dagLoadCacheEnabled() {
		return nil, false
	}
	if store.SessionEventLog(path) == "" {
		return nil, false
	}
	unlock := lockSessionSavePath(path)
	defer unlock()
	res, ok := cachedSessionLoadResult(path, nil)
	if !ok {
		return nil, false
	}
	out := make([]SessionUserMessage, 0, len(res.msgs))
	for i, m := range res.msgs {
		if m.Role != provider.RoleUser || IsPinnedContextRevision(m) {
			continue
		}
		at := time.Time{}
		if i < len(res.times) {
			at = res.times[i]
		}
		if m.CreatedAt > 0 {
			at = time.UnixMilli(m.CreatedAt)
		}
		out = append(out, SessionUserMessage{Message: m, At: at})
	}
	return out, true
}

// logTailWhitespaceOnly reports whether the bytes in [from, to) of logPath are
// all whitespace — the writer's trailing newline(s) after the last complete
// entry. Anything else (or an implausibly large gap) is a real append the
// cached state has not seen: the caller must fall through to a full replay.
func logTailWhitespaceOnly(logPath string, from, to int64) bool {
	if to-from > 4096 {
		return false
	}
	f, err := os.Open(logPath)
	if err != nil {
		return false
	}
	defer f.Close()
	tail := make([]byte, to-from)
	if _, err := f.ReadAt(tail, from); err != nil {
		return false
	}
	for _, b := range tail {
		switch b {
		case '\n', '\r', ' ', '\t':
		default:
			return false
		}
	}
	return true
}

// sessionLogIdentity is a cheap identity for "have this session's event-log
// bytes changed", used by the P18-R2 guards: the log's size plus a digest of
// its bounded head window. Hashing a multi-hundred-MB log whole would
// reintroduce the decode the guards exist to avoid; append-only logs change
// size on every write, and same-size rewrites rotate the header generation —
// the pair (size, head window) catches both cheaply.
type sessionLogIdentity struct {
	Size       int64
	HeadDigest string
}

func readSessionLogIdentity(logPath string) (sessionLogIdentity, bool) {
	info, err := os.Stat(logPath)
	if err != nil || info.IsDir() {
		return sessionLogIdentity{}, false
	}
	f, err := os.Open(logPath)
	if err != nil {
		return sessionLogIdentity{}, false
	}
	defer f.Close()
	head := make([]byte, 4096)
	n, _ := f.Read(head)
	sum := sha256.Sum256(head[:n])
	return sessionLogIdentity{
		Size:       info.Size(),
		HeadDigest: hex.EncodeToString(sum[:8]) + ":" + strconv.FormatInt(info.Size(), 10),
	}, true
}
