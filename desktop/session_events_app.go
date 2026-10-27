package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
)

// TabSessionEventsSize (task 997) is the current-conversation half of the
// overview events card: the byte size of THIS tab session's own event log.
// It is one os.Stat on the resolved log path — no walk, no sampler dependency
// — so the panel can refresh it on every save without a disk scan (the total
// half keeps riding the perf sampler's cached eventsMb walk, task 932).
// Missing tab / booting controller / no session yet / stat failure all report
// 0, which the frontend renders as a dash, never as a fake zero-size claim.
func (a *App) TabSessionEventsSize(tabID string) int64 {
	a.mu.RLock()
	tab, ok := a.tabs[tabID]
	var ctrl control.SessionAPI
	if ok && tab != nil {
		ctrl = tab.Ctrl
	}
	a.mu.RUnlock()
	if ctrl == nil {
		return 0
	}
	sp := strings.TrimSpace(ctrl.SessionPath())
	if sp == "" {
		return 0
	}
	// tl 1172 L2: 归档态按归档字节数计（明文优先，缺失回落 .zst）。
	return agent.SessionEventLogZstSize(sp)
}

// SessionEventsEntry is one session's event-log footprint for the task-333
// storage panel. Both byte counts are plain file stats — the same basis the
// perf monitor's eventsMb uses — and the over-limit mark comes from
// agent.EventsLogAboveThreshold, so the statistic card shares the gate's
// judgment source.
type SessionEventsEntry struct {
	Path        string  `json:"path"`
	Name        string  `json:"name"`
	EventsBytes int64   `json:"eventsBytes"`
	LiveBytes   int64   `json:"liveBytes"` // main transcript jsonl (live content proxy)
	Ratio       float64 `json:"ratio"`     // eventsBytes / LiveBytes, 0 when live is 0
	OverLimit   bool    `json:"overLimit"`
	Open        bool    `json:"open"`
	// Task 345: Busy marks a session the repair must not touch right now -
	// its tab is open (the desktop holds the lease) or any lease record is
	// live (another writer, serve, or a stale in-process holder). The panel
	// disables the per-row repair and shows the state so "which of these is
	// safe to fix" is answered before the click, not after the skip.
	Busy bool `json:"busy"`
	// 任务 373 R4 备料：处置可观测两字段。ReclaimBytes 是 fold 的回收上限
	// （events − live，向下取 0）——「修这条能拿回多少」在点击之前可读，处置
	// 排序不必再靠 events 总大小猜。LiveOverCap 标记 live 内容本身超 auto
	// cap 的会话：fold 后的 log（≈live）仍超 cap，修复不可能把它修到 cap 之
	// 下——面板可据此把「修复无效」类与「修复有收益」类分开（2026-10-10 实测
	// 两类并存：头部 116MB/3.1x 未超 4x 因子，而 95-111MB 档多为此类，正是
	// fold-no-shrink WARN 刷屏的来源）。
	ReclaimBytes int64 `json:"reclaimBytes"`
	LiveOverCap  bool  `json:"liveOverCap"`
}

// SessionEventsInventoryView is the statistic card's data: every session in
// the active directory (sorted over-limit first by size) plus the totals and
// the thresholds the marks were judged against.
type SessionEventsInventoryView struct {
	Entries   []SessionEventsEntry `json:"entries"`
	OverCount int                  `json:"overCount"` // sessions over the effective threshold
	Mode      string               `json:"mode"`
	Factor    float64              `json:"factor"` // judgment factor (auto: configured; off/manual: built-in default)
	CapMB     int64                `json:"capMB"`  // judgment cap in MiB (auto only; 0 = none)
	// IdleSweepOff (task 1073): explicit notice when cap=0 in auto mode — the
	// idle sweep is disabled entirely (eventsIdleSweepFromConfig enabled =
	// capMB>0) and only the factor judgment remains. This used to be silent:
	// the user saw "上限 0" with no hint that the automatic maintenance rewrite
	// was off, while config.toml said 50. The panel now says so instead of
	// leaving the operator guessing.
	IdleSweepOff bool `json:"idleSweepOff"`
}

// SessionEventsCompactResult reports one compact attempt: the byte delta the
// user sees, plus skipped/error state for the batch list.
type SessionEventsCompactResult struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Round     int    `json:"round"`
	Before    int64  `json:"before"`
	After     int64  `json:"after"`
	Freed     int64  `json:"freed"`
	ElapsedMs int64  `json:"elapsedMs"`
	Skipped   bool   `json:"skipped"` // session busy (lease held elsewhere)
	Error     string `json:"error,omitempty"`
}

// eventsRotationJudgmentFrom computes the display judgment from an already
// loaded config: auto mode uses the configured thresholds, off/manual fall
// back to the built-in factor so the card's "over limit" keeps a stable
// meaning when the gate is not auto. Loading once keeps the mode and the
// thresholds from disagreeing between two reads (review finding, 2026-09-28).
func eventsRotationJudgmentFrom(cfg *config.Config) (factor float64, capMB int64) {
	if config.EventsAutoRotationMode(cfg) == config.EventsAutoRotationAuto {
		return config.EventsRotationFactor(cfg), config.EventsRotationCapMB(cfg)
	}
	return config.EventsRotationFactorDefault, 0
}

// eventsRotationJudgment reads the display judgment with its own config load
// (used by tests and single callers).
func eventsRotationJudgment() (factor float64, capMB int64) {
	cfg, err := config.Load()
	if err != nil {
		return config.EventsRotationFactorDefault, 0
	}
	return eventsRotationJudgmentFrom(cfg)
}

// sessionEventsEntryJudgment fills the derived observability fields of one
// inventory row (任务 373 R4 备料): the best-case fold reclaim and the
// repair-futile mark. Pure arithmetic on already-statted sizes plus the view's
// judgment thresholds, factored out so tests can pin the semantics without a
// live session catalog.
func sessionEventsEntryJudgment(entry *SessionEventsEntry, mode string, capMB int64) {
	if entry.EventsBytes > entry.LiveBytes {
		entry.ReclaimBytes = entry.EventsBytes - entry.LiveBytes
	}
	entry.LiveOverCap = mode == config.EventsAutoRotationAuto && capMB > 0 &&
		entry.LiveBytes > capMB<<20
}

// SessionEventsInventory lists every session across every known session
// directory with their event-log sizes (largest first) and the over-limit
// marks (task 333). Task 1073: the enumeration used to be a.ListSessions(),
// which only covers the ACTIVE tab's directory — the user's >50MiB count was
// 9 disk-wide but the panel only showed 3 because the other 6 sat in
// project/session directories the active tab does not own. The idle sweep
// already walks knownSessionDirs(); this now matches so the panel's count can
// reconcile with a disk-wide scan. Busy/open marks come from the same
// runtime+lease facts the sweep uses (cross-directory), not just the active
// tab's "open" flag.
func (a *App) SessionEventsInventory() SessionEventsInventoryView {
	cfg, loadErr := config.Load()
	// 任务 1073 面板值一致性：loadErr 时三个字段全部落回默认/manual，与
	// eventsRotationJudgmentFrom(nil) 同一取值口径——此前 mode 默认 manual
	// 而 factor 落回 DefaultFactor(4.0)、cap 落回 0，与 config 生效值
	// (auto,3.0,50) 出现过用户看到的「4/0 vs 3/50」错位。
	mode := config.EventsAutoRotationManual
	factor := config.EventsRotationFactorDefault
	var capMB int64
	if loadErr == nil {
		mode = config.EventsAutoRotationMode(cfg)
		factor, capMB = eventsRotationJudgmentFrom(cfg)
	}
	view := SessionEventsInventoryView{Entries: []SessionEventsEntry{}, Mode: mode, Factor: factor, CapMB: capMB}
	// Task 1073: cap=0 in auto mode means the idle sweep is off; say so instead
	// of silently disabling the maintenance rewrite.
	view.IdleSweepOff = mode == config.EventsAutoRotationAuto && capMB == 0
	for _, meta := range a.allSessionMetasAcrossDirs() {
		if meta.DeletedAt != 0 {
			continue
		}
		eventsBytes := agent.SessionEventLogZstSize(meta.Path)
		liveBytes := int64(0)
		if info, err := os.Stat(meta.Path); err == nil {
			liveBytes = info.Size()
		}
		name := sessionEventsDisplayTitle(a.localizedTopicTitle(meta.TopicTitle, topicTitleSourceAuto), meta.Title, meta.Preview, meta.Path)
		// Task 345: an open tab keeps this process's lease on the session and
		// any live lease record (foreign writer, serve, or stale holder)
		// blocks CompactSessionFile - surface it before the click.
		busy := meta.Open
		if !busy {
			if _, ctrl := a.controllerForSessionPath(meta.Path); ctrl != nil {
				busy = true
			} else if info, leaseErr := agent.LoadSessionLeaseInfo(meta.Path); leaseErr == nil && info != nil {
				busy = true
			}
		}
		ratio := float64(0)
		if liveBytes > 0 {
			ratio = float64(eventsBytes) / float64(liveBytes)
		}
		entry := SessionEventsEntry{
			Path:        meta.Path,
			Name:        name,
			EventsBytes: eventsBytes,
			LiveBytes:   liveBytes,
			Ratio:       ratio,
			OverLimit:   agent.EventsLogAboveThreshold(eventsBytes, liveBytes, factor, capMB),
			Open:        meta.Open,
			Busy:        busy,
		}
		sessionEventsEntryJudgment(&entry, mode, capMB)
		if entry.OverLimit {
			view.OverCount++
		}
		view.Entries = append(view.Entries, entry)
	}
	// Over-limit first (largest), then the rest by size — the card shows the
	// top three names directly off this order.
	sort.SliceStable(view.Entries, func(i, j int) bool {
		a, b := view.Entries[i], view.Entries[j]
		if a.OverLimit != b.OverLimit {
			return a.OverLimit
		}
		return a.EventsBytes > b.EventsBytes
	})
	return view
}

// allSessionMetasAcrossDirs enumerates every session across every known
// session directory (task 1073 panel enumeration), deduplicating by cleaned
// path so a session reachable from two directories is only counted once.
// Sessions whose listSessionsFromDir probe errors (a damaged sidecar, a
// directory mid-reconcile) are skipped with their error surfaced by the
// caller's own logging; a directory error is skipped the same way the sweep
// skips it.
func (a *App) allSessionMetasAcrossDirs() []SessionMeta {
	seen := make(map[string]bool)
	out := []SessionMeta{}
	for _, dir := range a.knownSessionDirs() {
		for _, meta := range a.listSessionsFromDir(dir, "") {
			key := strings.ToLower(filepath.Clean(meta.Path))
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, meta)
		}
	}
	return out
}

// sessionEventsDisplayTitle is tl 1076's name chain: the topic title (the
// same name the sidebar project tree shows for this conversation) first, then
// a user-chosen session title, then the first-message preview, then a
// human-readable stamp carved from the session file name (20260927-030006.576
// 166300-... -> "0927 03:00"). A bare jsonl path is what the user rejected -
// the last resort is a short recognizable label, never the full path.
//
// 任务书（tl 1076）验收锚点是"topic > Title > Preview"三级顺序：pre-1076 的
// task-345 顺序（title > preview > topic）导致未被单独命名的会话显示成首条
// 用户消息的截断（"已经安装驱动了，稍后我…"），用户无法据此识别会话。
func sessionEventsDisplayTitle(topic, title, preview, path string) string {
	for _, candidate := range []string{topic, title, preview} {
		if s := strings.TrimSpace(candidate); s != "" {
			if runecount := len([]rune(s)); runecount > 40 {
				return string([]rune(s)[:40]) + "…"
			}
			return s
		}
	}
	base := strings.TrimSuffix(filepath.Base(strings.TrimSpace(path)), ".jsonl")
	if len(base) >= 13 {
		if _, err := time.Parse("20060102-1504", base[:13]); err == nil {
			return base[:8] + " " + base[9:11] + ":" + base[11:13]
		}
	}
	if base != "" && base != "." {
		return base
	}
	return strings.TrimSpace(path)
}

// sessionEventsName resolves a display name for a path from the current list.
// tl 1076: the panel and the per-row repair result share this single chain, so
// a compact result row names the same topic the inventory card just showed
// (pre-1076 the two disagreed whenever the session carried no custom title).
func (a *App) sessionEventsName(path string) string {
	for _, meta := range a.ListSessions() {
		if meta.Path != path {
			continue
		}
		return sessionEventsDisplayTitle(a.localizedTopicTitle(meta.TopicTitle, topicTitleSourceAuto),
			meta.Title, meta.Preview, meta.Path)
	}
	return ""
}

// isSessionNotIdle reports whether a compact failure is the lease-busy skip.
// The typed check replaces an error-string match so rewording the agent's
// message cannot silently turn "skipped" rows into "failed" (review finding).
func isSessionNotIdle(err error) bool {
	var leaseErr *agent.SessionLeaseError
	return errors.As(err, &leaseErr)
}

// CompactSessionEvents runs the task-333 "repair this session" action on one
// session. Lease acquisition inside agent.CompactSessionFile is the idleness
// check: a busy session lands in Error+Skipped and — unlike a Go-style error
// return — the result still reaches the UI, because Wails discards a (res,
// err) pair whenever err is non-nil (review finding). before/after are the
// exact file sizes the rewrite saw; the error return stays reserved for
// failures that produce no result at all.
func (a *App) CompactSessionEvents(path string) (SessionEventsCompactResult, error) {
	res := SessionEventsCompactResult{Path: path, Name: a.sessionEventsName(path), Round: 1}
	start := time.Now()
	before, after, err := agent.CompactSessionFile(path)
	res.ElapsedMs = time.Since(start).Milliseconds()
	res.Before, res.After = before, after
	res.Freed = before - after
	if err != nil {
		res.Error = err.Error()
		res.Skipped = isSessionNotIdle(err)
	}
	return res, nil
}

// CompactAllSessionEvents re-runs the scan→compact loop until no session is
// over the judgment threshold or the round budget is spent (task 333's
// multi-round slimmer). Busy sessions are listed as skipped, a failing session
// never stops the rest, and a round that frees nothing ends the loop early —
// the budget exists so a pathological case can never spin.
func (a *App) CompactAllSessionEvents(maxRounds int) []SessionEventsCompactResult {
	return runEventsRepairRounds(maxRounds, a.SessionEventsInventory, agent.CompactSessionFile)
}

// runEventsRepairRounds is the multi-round core behind the all-session repair,
// with the scan and the compact action injected so tests can drive round
// progression without a live session catalog.
func runEventsRepairRounds(maxRounds int, inventory func() SessionEventsInventoryView, compact func(path string) (int64, int64, error)) []SessionEventsCompactResult {
	if maxRounds <= 0 || maxRounds > 3 {
		maxRounds = 3
	}
	results := []SessionEventsCompactResult{}
	for round := 1; round <= maxRounds; round++ {
		inv := inventory()
		if inv.OverCount == 0 {
			break
		}
		freedThisRound := int64(0)
		for _, entry := range inv.Entries {
			if !entry.OverLimit {
				continue
			}
			res := SessionEventsCompactResult{Path: entry.Path, Name: entry.Name, Round: round}
			start := time.Now()
			before, after, err := compact(entry.Path)
			res.ElapsedMs = time.Since(start).Milliseconds()
			res.Before, res.After = before, after
			res.Freed = before - after
			if err != nil {
				res.Error = err.Error()
				res.Skipped = isSessionNotIdle(err)
			} else {
				freedThisRound += res.Freed
			}
			results = append(results, res)
		}
		if freedThisRound <= 0 {
			break // nothing moved (all busy/failed): do not spin the next round
		}
	}
	return results
}

// String renders a compact result for logs/tests.
func (r SessionEventsCompactResult) String() string {
	if r.Error != "" {
		return fmt.Sprintf("%s (round %d): %s", r.Name, r.Round, r.Error)
	}
	return fmt.Sprintf("%s (round %d): %d -> %d", r.Name, r.Round, r.Before, r.After)
}
