package agent

import (
	"errors"
	"os"
	"sync"
)

// A log that cannot be replayed used to be re-refused on every open, and each
// refusal costs a full replay before it gives up: the desktop log for 2026-09-20
// shows three `refusing replay` stalls (6s, 15s, 326ms) wrapped around a single
// tab switch, all from the same oversized session being retried by the LRU and
// the sidebar. The refusal is a property of the bytes on disk, so it can be
// answered from memory until those bytes change.
//
// The key carries the log size and mtime, so growing the log invalidates the
// entry by construction: only the exact state that was refused stays refused.
type sessionReplayRefusalKey struct {
	path      string
	logSize   int64
	modTimeNS int64
}

// sessionReplayRefusalLimit bounds the cache. Refusals are rare (they need a log
// past the replay caps) and a session that changes is retried, so a small FIFO is
// enough and keeps the memory cost of the guard flat.
const sessionReplayRefusalLimit = 64

var (
	sessionReplayRefusalMu    sync.Mutex
	sessionReplayRefusalOrder []sessionReplayRefusalKey
	sessionReplayRefusalCache = map[sessionReplayRefusalKey]error{}
)

// rememberSessionReplayRefusal records that this exact log state cannot be
// replayed. Only limit refusals are cached: those are deterministic for a given
// file state, whereas a damaged or still-being-written log deserves a retry.
func rememberSessionReplayRefusal(logPath string, err error) {
	var limitErr *SessionReplayLimitError
	if !errors.As(err, &limitErr) {
		return
	}
	info, statErr := os.Stat(logPath)
	if statErr != nil {
		return
	}
	key := sessionReplayRefusalKey{path: logPath, logSize: info.Size(), modTimeNS: info.ModTime().UnixNano()}
	sessionReplayRefusalMu.Lock()
	defer sessionReplayRefusalMu.Unlock()
	if _, seen := sessionReplayRefusalCache[key]; !seen {
		sessionReplayRefusalOrder = append(sessionReplayRefusalOrder, key)
		for len(sessionReplayRefusalOrder) > sessionReplayRefusalLimit {
			evicted := sessionReplayRefusalOrder[0]
			sessionReplayRefusalOrder = sessionReplayRefusalOrder[1:]
			delete(sessionReplayRefusalCache, evicted)
		}
	}
	sessionReplayRefusalCache[key] = err
}

// cachedSessionReplayRefusal returns the refusal recorded for this exact log
// state, if there is one, so the caller can fail fast instead of replaying.
func cachedSessionReplayRefusal(logPath string) (error, bool) {
	info, err := os.Stat(logPath)
	if err != nil {
		return nil, false
	}
	key := sessionReplayRefusalKey{path: logPath, logSize: info.Size(), modTimeNS: info.ModTime().UnixNano()}
	sessionReplayRefusalMu.Lock()
	defer sessionReplayRefusalMu.Unlock()
	cached, ok := sessionReplayRefusalCache[key]
	return cached, ok
}

// forgetSessionReplayRefusal drops any refusal for this path. Tests use it to
// start from a clean process state; production relies on the size/mtime key.
func forgetSessionReplayRefusal(logPath string) {
	sessionReplayRefusalMu.Lock()
	defer sessionReplayRefusalMu.Unlock()
	for _, key := range sessionReplayRefusalOrder {
		if key.path == logPath {
			delete(sessionReplayRefusalCache, key)
		}
	}
	kept := sessionReplayRefusalOrder[:0]
	for _, key := range sessionReplayRefusalOrder {
		if key.path != logPath {
			kept = append(kept, key)
		}
	}
	sessionReplayRefusalOrder = kept
}
