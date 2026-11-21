// tl1733 Block1 打点:召回注入与按需查看的 (memory, session) 计数。
//
// Purpose: Block2 的分层公式需要两类观测——Recall(索引/自动召回把一条记忆
// 注入给某个会话)与 Hit(该记忆随后被 memory read 实际查看)。第一版以
// (memory, session) 对计数落盘,零召回裁条(注入瘦身)消费其全局窗口口径。
//
// 落盘位置裁定(任务书硬要求):独立 usage-counts.json,放在全局记忆目录
// (跨项目共享,天然满足零召回裁条的"全局口径";GlobalDir 不可用时退回项目
// 目录)。它不进 MEMORY.md,也不进 system 前缀——post-maintenance 刷新链
// (SetLeadingSystemPromptWithReason / ReloadFromDisk)与 #Memory 区重建链都
// 不会触碰它,因此压缩/重启后计数连续。
package memory

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"reasonix/internal/fileutil"
)

// usageCountsFile is the on-disk ledger, kept out of MEMORY.md and out of the
// system prefix by design (see package comment).
const usageCountsFile = "usage-counts.json"

// usageSessionsPerMemory bounds the per-memory session ledger so a long-lived
// store cannot grow the file without limit. The newest sessions by last-event
// time survive; evicted sessions only lose per-session granularity (the
// memory-level totals and last-event stamps are unaffected), which is what the
// Block1 cutoff reads and all Block2 needs beyond its >=20-sample sessions.
const usageSessionsPerMemory = 32

// usageSessionEntry is one (memory, session) pair's counters.
type usageSessionEntry struct {
	Recall       int       `json:"recall,omitempty"`
	View         int       `json:"view,omitempty"`
	LastRecallAt time.Time `json:"last_recall_at,omitempty"`
	LastViewAt   time.Time `json:"last_view_at,omitempty"`
}

// lastEvent returns the entry's most recent event time (zero when it has none).
func (e usageSessionEntry) lastEvent() time.Time {
	if e.LastRecallAt.After(e.LastViewAt) {
		return e.LastRecallAt
	}
	return e.LastViewAt
}

// usageEntry is one memory's counters: memory-level totals plus the bounded
// per-session ledger Block2 scores over.
type usageEntry struct {
	RecallTotal  int                           `json:"recall_total,omitempty"`
	ViewTotal    int                           `json:"view_total,omitempty"`
	LastRecallAt time.Time                     `json:"last_recall_at,omitempty"`
	LastViewAt   time.Time                     `json:"last_view_at,omitempty"`
	Sessions     map[string]usageSessionEntry  `json:"sessions,omitempty"`
}

// usageFile is the serialized shape of usage-counts.json. StartedAt is the
// data-maturity anchor: the zero-recall cutoff (tl1733 Block1 item 3) stays
// inert until the ledger has been accumulating for usageMaturityDays.
type usageFile struct {
	Version   int                     `json:"version"`
	StartedAt time.Time               `json:"started_at,omitempty"`
	Memories  map[string]*usageEntry  `json:"memories,omitempty"`
}

// usageStore is the process-resident handle over one ledger file. Stores are
// value types passed around by copy, so instances share state through the
// package registry keyed by file path (same shape as the recall-index cache).
type usageStore struct {
	mu     sync.Mutex
	path   string
	data   usageFile
	loaded bool
}

// usageRegistry maps ledger path -> process-resident store.
var usageRegistry = struct {
	mu sync.Mutex
	m  map[string]*usageStore
}{m: map[string]*usageStore{}}

// UsageLedgerPath resolves the ledger file location: the global memory dir
// when available (one ledger across projects = the cutoff's global scope),
// else the project dir. Empty when the store is disabled. Exported for
// diagnostics and tests; the ledger itself stays package-private.
func UsageLedgerPath(s Store) string {
	dir := s.GlobalDir
	if dir == "" {
		dir = s.Dir
	}
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, usageCountsFile)
}

// usageFor returns the shared ledger handle for the store, or nil when memory
// is disabled. Callers treat nil as a no-op.
func usageFor(s Store) *usageStore {
	path := UsageLedgerPath(s)
	if path == "" {
		return nil
	}
	usageRegistry.mu.Lock()
	defer usageRegistry.mu.Unlock()
	u, ok := usageRegistry.m[path]
	if !ok {
		u = &usageStore{path: path}
		usageRegistry.m[path] = u
	}
	return u
}

// RecordUsageRecall counts one recall injection: the given facts entered the
// prompt for the given session (control-layer turn-tail injection). No-op when
// memory is disabled or no fact was injected.
func RecordUsageRecall(s Store, session string, ids []string) {
	usageFor(s).recordRecall(session, ids, time.Now().UTC())
}

// RecordUsageView counts one on-demand read: a provider-facing memory read
// returned the fact's full body (the `memory` tool's read operation).
func RecordUsageView(s Store, session, id string) {
	usageFor(s).recordView(session, id, time.Now().UTC())
}

// UsageMature reports whether the ledger has accumulated for at least days —
// the zero-recall cutoff's hard maturity gate. No ledger data (never recorded,
// or StartedAt lost) means not mature: without evidence, the injection renders
// the full index (no subtraction from zero data).
func (s Store) UsageMature(now time.Time, days int) bool {
	return usageFor(s).mature(now, days)
}

// UsageRecalledWithin reports whether the fact recorded any recall within the
// window (global scope: one ledger across projects). No entry = no recall.
func (s Store) UsageRecalledWithin(id string, now time.Time, window time.Duration) bool {
	return usageFor(s).recalledWithin(id, now, window)
}

func (u *usageStore) recordRecall(session string, ids []string, now time.Time) {
	if u == nil || len(ids) == 0 {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.ensureLoadedLocked()
	if u.data.StartedAt.IsZero() {
		u.data.StartedAt = now
	}
	for _, id := range ids {
		e := u.entryLocked(id)
		e.RecallTotal++
		e.LastRecallAt = now
		if session != "" {
			se, flush := u.sessionEntryLocked(e, session)
			se.Recall++
			se.LastRecallAt = now
			flush()
			u.trimSessionsLocked(e, now)
		}
	}
	u.saveLocked()
}

func (u *usageStore) recordView(session, id string, now time.Time) {
	if u == nil || id == "" {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.ensureLoadedLocked()
	if u.data.StartedAt.IsZero() {
		u.data.StartedAt = now
	}
	e := u.entryLocked(id)
	e.ViewTotal++
	e.LastViewAt = now
	if session != "" {
		se, flush := u.sessionEntryLocked(e, session)
		se.View++
		se.LastViewAt = now
		flush()
		u.trimSessionsLocked(e, now)
	}
	u.saveLocked()
}

func (u *usageStore) mature(now time.Time, days int) bool {
	if u == nil || days <= 0 {
		return false
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.ensureLoadedLocked()
	if u.data.StartedAt.IsZero() {
		return false
	}
	return now.Sub(u.data.StartedAt) >= time.Duration(days)*24*time.Hour
}

func (u *usageStore) recalledWithin(id string, now time.Time, window time.Duration) bool {
	if u == nil || id == "" {
		return false
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.ensureLoadedLocked()
	e, ok := u.data.Memories[id]
	if !ok || e == nil || e.LastRecallAt.IsZero() {
		return false
	}
	return !e.LastRecallAt.After(now) && now.Sub(e.LastRecallAt) <= window
}

func (u *usageStore) entryLocked(id string) *usageEntry {
	if u.data.Memories == nil {
		u.data.Memories = map[string]*usageEntry{}
	}
	e, ok := u.data.Memories[id]
	if !ok || e == nil {
		e = &usageEntry{}
		u.data.Memories[id] = e
	}
	return e
}

// sessionEntryLocked returns a mutable copy of the (memory, session) row plus
// a write-back closure: Go cannot take the address of a map value, so callers
// mutate the copy and flush it with the returned func.
func (u *usageStore) sessionEntryLocked(e *usageEntry, session string) (*usageSessionEntry, func()) {
	if e.Sessions == nil {
		e.Sessions = map[string]usageSessionEntry{}
	}
	se, ok := e.Sessions[session]
	if !ok {
		se = usageSessionEntry{}
	}
	return &se, func() { e.Sessions[session] = se }
}

// trimSessionsLocked evicts the stalest session rows once the per-memory
// ledger crosses usageSessionsPerMemory, so long-lived stores stay bounded.
func (u *usageStore) trimSessionsLocked(e *usageEntry, now time.Time) {
	if len(e.Sessions) <= usageSessionsPerMemory {
		return
	}
	for len(e.Sessions) > usageSessionsPerMemory {
		oldest, oldestAt := "", now
		for s, se := range e.Sessions {
			at := se.lastEvent()
			if at.IsZero() || at.Before(oldestAt) {
				oldest, oldestAt = s, at
			}
		}
		if oldest == "" {
			return
		}
		delete(e.Sessions, oldest)
	}
}

func (u *usageStore) ensureLoadedLocked() {
	if u.loaded {
		return
	}
	u.loaded = true
	raw, err := os.ReadFile(u.path)
	if err != nil {
		return // first run: no ledger yet
	}
	var data usageFile
	if err := json.Unmarshal(raw, &data); err != nil {
		slog.Warn("memory: usage ledger unreadable, starting fresh", "path", u.path, "err", err)
		return
	}
	u.data = data
}

func (u *usageStore) saveLocked() {
	u.data.Version = 1
	raw, err := json.Marshal(u.data)
	if err != nil {
		return // a flat struct of ints/strings cannot fail to marshal
	}
	// Best-effort: a failed ledger write degrades Block2's data and the future
	// cutoff's accuracy, never the session itself.
	if err := fileutil.AtomicWriteFile(u.path, raw, 0o644); err != nil {
		slog.Warn("memory: usage ledger write failed", "path", u.path, "err", err)
	}
}
