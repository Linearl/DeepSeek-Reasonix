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
	"reasonix/internal/store"
)

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

// SessionEventsInventory lists the active directory's sessions with their
// event-log sizes (largest first) and the over-limit marks (task 333).
func (a *App) SessionEventsInventory() SessionEventsInventoryView {
	cfg, loadErr := config.Load()
	mode := config.EventsAutoRotationManual
	factor := config.EventsRotationFactorDefault
	var capMB int64
	if loadErr == nil {
		mode = config.EventsAutoRotationMode(cfg)
		factor, capMB = eventsRotationJudgmentFrom(cfg)
	}
	view := SessionEventsInventoryView{Entries: []SessionEventsEntry{}, Mode: mode, Factor: factor, CapMB: capMB}
	for _, meta := range a.ListSessions() {
		if meta.DeletedAt != 0 {
			continue
		}
		eventsBytes := int64(0)
		if info, err := os.Stat(store.SessionEventLog(meta.Path)); err == nil {
			eventsBytes = info.Size()
		}
		liveBytes := int64(0)
		if info, err := os.Stat(meta.Path); err == nil {
			liveBytes = info.Size()
		}
		name := sessionEventsDisplayTitle(meta.Title, meta.Preview, meta.TopicTitle, meta.Path)
		// Task 345: an open tab keeps this process's lease on the session and
		// any live lease record (foreign writer, serve, or stale holder)
		// blocks CompactSessionFile - surface it before the click.
		busy := meta.Open
		if !busy {
			if info, leaseErr := agent.LoadSessionLeaseInfo(meta.Path); leaseErr == nil && info != nil {
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

// sessionEventsDisplayTitle is task 345's name chain: user title, then the
// first-message preview, then the topic title, then a human-readable stamp
// carved from the session file name (20260927-030006.576166300-... -> "0927
// 03:00"). A bare jsonl path is what the user rejected - the last resort is
// a short recognizable label, never the full path.
func sessionEventsDisplayTitle(title, preview, topic, path string) string {
	for _, candidate := range []string{title, preview, topic} {
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
func (a *App) sessionEventsName(path string) string {
	for _, meta := range a.ListSessions() {
		if meta.Path != path {
			continue
		}
		if title := strings.TrimSpace(meta.Title); title != "" {
			return title
		}
		return strings.TrimSpace(meta.Preview)
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
