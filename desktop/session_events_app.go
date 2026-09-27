package main

import (
	"fmt"
	"os"
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

// eventsRotationJudgment reads the display judgment: auto mode uses the
// configured thresholds, off/manual fall back to the built-in factor so the
// card's "over limit" keeps a stable meaning when the gate is not auto.
func eventsRotationJudgment() (factor float64, capMB int64) {
	cfg, err := config.Load()
	if err != nil {
		return config.EventsRotationFactorDefault, 0
	}
	if config.EventsAutoRotationMode(cfg) == config.EventsAutoRotationAuto {
		return config.EventsRotationFactor(cfg), config.EventsRotationCapMB(cfg)
	}
	return config.EventsRotationFactorDefault, 0
}

// SessionEventsInventory lists the active directory's sessions with their
// event-log sizes (largest first) and the over-limit marks (task 333).
func (a *App) SessionEventsInventory() SessionEventsInventoryView {
	factor, capMB := eventsRotationJudgment()
	mode := config.EventsAutoRotationManual
	if cfg, err := config.Load(); err == nil {
		mode = config.EventsAutoRotationMode(cfg)
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
		name := strings.TrimSpace(meta.Title)
		if name == "" {
			name = strings.TrimSpace(meta.Preview)
		}
		if runecount := len([]rune(name)); runecount > 40 {
			name = string([]rune(name)[:40]) + "…"
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

// CompactSessionEvents runs the task-333 "repair this session" action on one
// session. Lease acquisition inside agent.CompactSessionFile is the idleness
// check: a busy session returns the lease error to the UI instead of failing
// silently, and before/after are the exact file sizes the rewrite saw.
func (a *App) CompactSessionEvents(path string) (SessionEventsCompactResult, error) {
	res := SessionEventsCompactResult{Path: path, Name: a.sessionEventsName(path), Round: 1}
	start := time.Now()
	before, after, err := agent.CompactSessionFile(path)
	res.ElapsedMs = time.Since(start).Milliseconds()
	res.Before, res.After = before, after
	res.Freed = before - after
	if err != nil {
		res.Error = err.Error()
		res.Skipped = strings.Contains(err.Error(), "not idle")
		return res, err
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
				res.Skipped = strings.Contains(err.Error(), "not idle")
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
