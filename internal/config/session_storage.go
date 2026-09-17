package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The conversation store has four modes (task 155). Each one is a point on a
// single upgrade path: the write side and the read side of the v3 (legacy
// JSONL) / v4 (framed linear store) pair are decoupled here so the desktop can
// move one dimension at a time instead of jumping straight from v3 to v4.
//
//	v3_only               write v3            read v3        (legacy default)
//	dual_write_read_v3    write v3 + v4       read v3        (shadow compare)
//	dual_write_read_v4    write v3 + v4       read v4        (staged cutover)
//	v4_only               write v3 + v4       read v4        (v3 read-only)
//
// v4_only keeps the legacy transcript readable but freezes it: the v4 store is
// the maintained copy and legacy sessions open read-only (decision
// dec-698697fe47d1778e). Execution still commits the agent transcript, so the
// v3 write side is not deleted here — retiring it is #10291 (execution
// binding), which is why v4_only is a restart-scoped mode.
const (
	SessionStorageV3Only          = "v3_only"
	SessionStorageDualWriteReadV3 = "dual_write_read_v3"
	SessionStorageDualWriteReadV4 = "dual_write_read_v4"
	SessionStorageV4Only          = "v4_only"
)

// Legacy aliases kept readable (and normalized on the next settings write):
// before task 155 the setting was one boolean-ish string.
//
//	legacy -> v3_only            (write v3, read v3)
//	v4     -> dual_write_read_v4 (that is what the old value actually did:
//	                              dual write with reads preferring v4)
const (
	sessionStorageLegacyAlias = "legacy"
	sessionStorageV4Alias     = "v4"
)

// SessionStorageModes lists every mode in upgrade order.
var SessionStorageModes = []string{
	SessionStorageV3Only,
	SessionStorageDualWriteReadV3,
	SessionStorageDualWriteReadV4,
	SessionStorageV4Only,
}

// NormalizeSessionStorageMode maps a stored/env value onto a mode. The boolean
// is false for values that are not modes so callers can refuse a typo instead of
// silently switching directories.
func NormalizeSessionStorageMode(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", sessionStorageLegacyAlias, SessionStorageV3Only:
		return SessionStorageV3Only, true
	case SessionStorageDualWriteReadV3:
		return SessionStorageDualWriteReadV3, true
	case sessionStorageV4Alias, SessionStorageDualWriteReadV4:
		return SessionStorageDualWriteReadV4, true
	case SessionStorageV4Only:
		return SessionStorageV4Only, true
	default:
		return "", false
	}
}

// SessionStorageModeIndex returns the position of a mode on the upgrade path.
func SessionStorageModeIndex(mode string) int {
	for i, candidate := range SessionStorageModes {
		if candidate == mode {
			return i
		}
	}
	return -1
}

// SessionV4WritesEnabledForMode reports whether a mode writes the v4 mirror
// (modes 2-4).
func SessionV4WritesEnabledForMode(mode string) bool {
	normalized, ok := NormalizeSessionStorageMode(mode)
	if !ok {
		return false
	}
	return normalized != SessionStorageV3Only
}

// SessionV4ReadsEnabledForMode reports whether a mode reads v4 (modes 3-4).
func SessionV4ReadsEnabledForMode(mode string) bool {
	normalized, ok := NormalizeSessionStorageMode(mode)
	if !ok {
		return false
	}
	return normalized == SessionStorageDualWriteReadV4 || normalized == SessionStorageV4Only
}

// SessionV3FrozenForMode reports whether a mode freezes the legacy transcript as
// a read-only fallback (mode 4).
func SessionV3FrozenForMode(mode string) bool {
	normalized, ok := NormalizeSessionStorageMode(mode)
	if !ok {
		return false
	}
	return normalized == SessionStorageV4Only
}

// SessionV4WritesEnabled reports whether the v4 mirror is written (modes 2-4).
func SessionV4WritesEnabled(cfg *Config) bool {
	return SessionV4WritesEnabledForMode(SessionStorageMode(cfg))
}

// SessionV4ReadsEnabled reports whether history reads prefer v4 (modes 3-4).
func SessionV4ReadsEnabled(cfg *Config) bool {
	return SessionV4ReadsEnabledForMode(SessionStorageMode(cfg))
}

// SessionV3Frozen reports whether the legacy transcript is a frozen read-only
// fallback (mode 4). The v3 files stay untouched so a rollback can still read
// them; only new legacy imports stop.
func SessionV3Frozen(cfg *Config) bool {
	return SessionV3FrozenForMode(SessionStorageMode(cfg))
}

// ResolveSafeSessionStorageMode is the boot-time guard for hand-edited
// settings: a mode that would skip the staged dual-write period is clamped to
// the last safe stage instead of starting on a transition the audit trail never
// approved. The boolean reports whether the mode was changed.
func ResolveSafeSessionStorageMode(current string, history []string) (string, bool) {
	normalized, ok := NormalizeSessionStorageMode(current)
	if !ok {
		return SessionStorageV3Only, true
	}
	if normalized != SessionStorageV4Only {
		return normalized, false
	}
	if sessionStorageStagedModes("", history) {
		return normalized, false
	}
	return SessionStorageDualWriteReadV4, true
}

// SessionStorageNeedsRestart reports whether moving from one mode to another
// requires a restart. Only the read side can move under a live runtime: modes 2
// and 3 write the same pair and differ in which copy history reads prefer, and
// every read consults the current mode. Everything else changes the boot
// contract — the mirror is created at boot (mode 1 vs 2/3/4) and mode 4 freezes
// the legacy transcript as a read-only fallback — so it takes a restart.
func SessionStorageNeedsRestart(from, to string) bool {
	normalizedFrom, okFrom := NormalizeSessionStorageMode(from)
	normalizedTo, okTo := NormalizeSessionStorageMode(to)
	if !okFrom || !okTo {
		return true
	}
	if normalizedFrom == normalizedTo {
		return false
	}
	readSideOnly := func(mode string) bool {
		return mode == SessionStorageDualWriteReadV3 || mode == SessionStorageDualWriteReadV4
	}
	return !(readSideOnly(normalizedFrom) && readSideOnly(normalizedTo))
}

// ErrSessionStorageSkippedStage refuses an upgrade that would jump over the
// dual-write stages.
var ErrSessionStorageSkippedStage = errors.New("session storage: a dual-write stage is required before v4_only")

// ValidateSessionStorageTransition decides whether the desktop may apply a
// mode change. Downgrades are always allowed (rollback must stay cheap); an
// upgrade may move at most one stage unless a dual-write stage was already
// used, and v3_only -> v4_only is refused outright: every session must be
// mirrored at least once before the read side trusts v4.
func ValidateSessionStorageTransition(current, target string, history []string) error {
	current, okCurrent := NormalizeSessionStorageMode(current)
	target, okTarget := NormalizeSessionStorageMode(target)
	if !okTarget {
		return fmt.Errorf("session storage: %q is not a mode", target)
	}
	if !okCurrent {
		current = SessionStorageV3Only
	}
	if current == target {
		return nil
	}
	currentIndex := SessionStorageModeIndex(current)
	targetIndex := SessionStorageModeIndex(target)
	if currentIndex < 0 || targetIndex < 0 {
		return fmt.Errorf("session storage: unknown mode transition %q -> %q", current, target)
	}
	if targetIndex < currentIndex {
		return nil
	}
	if staged := sessionStorageStagedModes(current, history); staged {
		return nil
	}
	if targetIndex-currentIndex > 1 {
		return fmt.Errorf("%w: %s -> %s", ErrSessionStorageSkippedStage, current, target)
	}
	return nil
}

// sessionStorageStagedModes reports whether a dual-write mode is already in
// effect or was used before, which is what makes a later jump to v4_only safe.
func sessionStorageStagedModes(current string, history []string) bool {
	for _, mode := range append([]string{current}, history...) {
		normalized, ok := NormalizeSessionStorageMode(mode)
		if !ok {
			continue
		}
		if normalized == SessionStorageDualWriteReadV3 || normalized == SessionStorageDualWriteReadV4 {
			return true
		}
	}
	return false
}

// SessionStorageChange records one mode switch for audit (task 155 telemetry).
// The counts are the inventory of both stores taken just before the switch, so a
// rollback can be checked against what the switch started from. The data itself
// is never moved: the v3 files stay in place and v4 is additive.
type SessionStorageChange struct {
	At      time.Time `json:"at"`
	From    string    `json:"from"`
	To      string    `json:"to"`
	Source  string    `json:"source,omitempty"`
	Restart bool      `json:"restart,omitempty"`
	V3Count int       `json:"v3Count,omitempty"`
	V3Bytes int64     `json:"v3Bytes,omitempty"`
	V4Count int       `json:"v4Count,omitempty"`
	V4Bytes int64     `json:"v4Bytes,omitempty"`
}

// SessionStorageInventory is the inventory of both stores at one moment.
type SessionStorageInventory struct {
	V3Count int
	V3Bytes int64
	V4Count int
	V4Bytes int64
}

// sessionSnapshotFileLimit bounds the walk so a switch on a huge store stays
// fast; the counts are for audit, not accounting.
const sessionSnapshotFileLimit = 20000

// SessionStorageSnapshot counts the files and bytes held by the legacy and the
// v4 store. It is the "dump" a mode switch records before it applies.
func SessionStorageSnapshot() SessionStorageInventory {
	v3Count, v3Bytes := countSessionFiles(SessionDir())
	v4Count, v4Bytes := countSessionFiles(SessionStoreDir())
	return SessionStorageInventory{V3Count: v3Count, V3Bytes: v3Bytes, V4Count: v4Count, V4Bytes: v4Bytes}
}

func countSessionFiles(dir string) (int, int64) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return 0, 0
	}
	count := 0
	var bytes int64
	_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry == nil {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		count++
		if info, statErr := entry.Info(); statErr == nil {
			bytes += info.Size()
		}
		if count >= sessionSnapshotFileLimit {
			return fs.SkipAll
		}
		return nil
	})
	return count, bytes
}

// SessionStorageChangeLogPath is the append-only audit log for mode switches.
func SessionStorageChangeLogPath() string {
	dir := userSupportDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "session-storage-changelog.jsonl")
}

// AppendSessionStorageChange records a mode switch. Telemetry must never fail a
// settings write, so callers log the error instead of surfacing it.
func AppendSessionStorageChange(change SessionStorageChange) error {
	path := SessionStorageChangeLogPath()
	if path == "" {
		return errors.New("session storage: user state directory unavailable")
	}
	if change.At.IsZero() {
		change.At = time.Now().UTC()
	}
	payload, err := json.Marshal(change)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(payload, '\n')); err != nil {
		return err
	}
	return nil
}

// ReadSessionStorageChanges returns the recorded switches, oldest first.
func ReadSessionStorageChanges() ([]SessionStorageChange, error) {
	path := SessionStorageChangeLogPath()
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	changes := make([]SessionStorageChange, 0, 8)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var change SessionStorageChange
		if err := json.Unmarshal([]byte(line), &change); err != nil {
			continue
		}
		changes = append(changes, change)
	}
	return changes, nil
}

// SessionStorageHistoryModes lists every mode the changelog has seen, which
// ValidateSessionStorageTransition uses to honor "a dual-write stage first".
func SessionStorageHistoryModes() []string {
	changes, err := ReadSessionStorageChanges()
	if err != nil {
		return nil
	}
	modes := make([]string, 0, len(changes)*2)
	for _, change := range changes {
		modes = append(modes, change.From, change.To)
	}
	return modes
}
