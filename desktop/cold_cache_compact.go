package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/billing"
	"reasonix/internal/boot"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/plugin"
	"reasonix/internal/store"
)

// Task 297: before a long conversation goes cold, compact it once while its
// prefix cache is still warm — one compaction fee now beats a full-price
// input on every later wake, and a switched model invalidates the whole
// prefix anyway (promptCacheKey carries modelRef). The pass is a desktop
// process tick, not a heartbeat prompt: the heartbeat engine only submits
// scheduled prompts into topics (heartbeat.go header contract) and the
// target conversations are idle — there is no turn to hang the work on.
//
// The loop always runs; every tick does a live config read, so the lab
// switch gates the behaviour with zero restart and the OFF path touches
// nothing but one config load (iron rule 2).

const coldCacheCompactTickInterval = 10 * time.Minute

// coldCacheCompactConfig is the effective knob set for one tick: file values
// fall back to the built-in defaults (600 KiB / 5h), mirroring the
// dag_graph_cache_capacity 0-means-default pattern (task 196fix2).
type coldCacheCompactConfig struct {
	Enabled     bool
	MinBytes    int64
	IdleMinutes int
}

func loadColdCacheCompactConfig() coldCacheCompactConfig {
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return coldCacheCompactConfig{}
	}
	return normalizeColdCacheCompactKnobs(
		cfg.Agent.ExperimentalColdCacheCompact,
		cfg.Agent.ColdCacheCompactMinBytes,
		cfg.Agent.ColdCacheCompactIdleMinutes,
	)
}

// normalizeColdCacheCompactKnobs applies the file-value rules: 0/negative
// falls back to the built-in default (600 KiB / 5h) and never disables —
// only the switch does that (task 196fix2's 0-means-default pattern).
func normalizeColdCacheCompactKnobs(enabled bool, minBytes int64, idleMinutes int) coldCacheCompactConfig {
	out := coldCacheCompactConfig{Enabled: enabled, MinBytes: minBytes, IdleMinutes: idleMinutes}
	if out.MinBytes <= 0 {
		out.MinBytes = config.ColdCacheCompactMinBytesDefault
	}
	if out.IdleMinutes <= 0 {
		out.IdleMinutes = config.ColdCacheCompactIdleMinutesDefault
	}
	return out
}

// coldCacheCompactDecision is the whole policy in one testable function: it
// reports whether the pass should compact a session right now, and why not
// otherwise (the reason is the skip taxonomy the tick and the tests share).
//
// The once-per-cooling-window guard keys on LastActivityAt itself: an
// attempt is remembered together with the activity it was made for, so the
// same cooling window never compacts twice, while a conversation the user
// touches again and lets cool a second time stays eligible — that is the
// "compact before it goes cold, not in a loop" contract.
//
// Task 424 adds the park guard: a session whose last attempt ended in the
// terminal "no foldable region remains" class is parked for its current
// activity stamp — retrying cannot succeed until the session itself changes
// (a new write bumps LastActivityAt, which re-arms eligibility).
func coldCacheCompactDecision(enabled bool, now time.Time, lastActivityAt, lastOpenedAt, attemptedActivityAt, parkedActivityAt, sizeBytes int64, minBytes int64, idleMinutes int) (bool, string) {
	if !enabled {
		return false, "switch off"
	}
	if idleMinutes <= 0 || minBytes <= 0 {
		return false, "invalid config"
	}
	if lastActivityAt <= 0 {
		return false, "no activity stamp"
	}
	// Task 380-A: opening a conversation is a wake-up. The idle clock runs
	// from the later of (last activity, last open) so a cold session the user
	// just opened is not compacted synchronously on their wait path — that is
	// the 297 symptom this task fixes; the headless path covers it before.
	idleAnchor := lastActivityAt
	if lastOpenedAt > idleAnchor {
		idleAnchor = lastOpenedAt
	}
	idle := now.Sub(time.UnixMilli(idleAnchor))
	if idle < time.Duration(idleMinutes)*time.Minute {
		return false, "not idle long enough"
	}
	if sizeBytes < minBytes {
		return false, "context under size floor"
	}
	if parkedActivityAt == lastActivityAt {
		return false, "parked: no foldable region remains (re-arms on new activity)"
	}
	if attemptedActivityAt == lastActivityAt {
		return false, "already compacted this cooling window"
	}
	return true, "eligible"
}

// sessionContextBytes is the stored proxy for "how big is the visible
// context": live transcript + events, the same two files the task-333
// statistic card measures. It never errors — a missing file reads as zero
// (a session with no transcript is under any floor).
func sessionContextBytes(path string) int64 {
	var total int64
	if info, err := os.Stat(path); err == nil {
		total += info.Size()
	}
	if info, err := os.Stat(store.SessionEventLog(path)); err == nil {
		total += info.Size()
	}
	return total
}

type coldCacheCompactLoop struct {
	a    *App
	mu   sync.Mutex
	done map[string]int64 // session path -> LastActivityAt the attempt was made for
	// Task 424: parked records the terminal "no foldable region remains"
	// outcome the same way done records success — keyed by the LastActivityAt
	// the failed attempt was made for. A parked session is skipped until its
	// activity stamp changes (a new write re-arms it); the other failure
	// classes keep the per-tick retry.
	parked map[string]int64
	// Task 380-A/B: firstSeen records when this process first noticed a live
	// controller for a session (≈ open time, bounded by the tick interval);
	// inFlight marks a compact currently running so later ticks don't stack
	// duplicate passes on the same session (an in-flight pass runs to
	// completion, it is never interrupted).
	firstSeen map[string]int64
	inFlight  map[string]bool
}

// startColdCacheCompactLoop boots the unconditioned ticker: the lab switch is
// read live on every tick, so OFF costs one config read and never touches a
// session, while ON takes effect without a restart.
func (a *App) startColdCacheCompactLoop() {
	l := &coldCacheCompactLoop{
		a:         a,
		done:      make(map[string]int64),
		parked:    make(map[string]int64),
		firstSeen: make(map[string]int64),
		inFlight:  make(map[string]bool),
	}
	a.goSafe("coldCacheCompactLoop", l.run)
}

const (
	coldCacheCompactModeLive     = "live"
	coldCacheCompactModeHeadless = "headless"
)

// coldCacheUsageCapture collects the billable usage of one headless compact
// pass (task 380 sixth-acceptance accounting log). boot.Build wraps opts.Sink
// with the cost-quote sink, so Usage events arrive with CostQuote already
// priced from the billing catalog — the capture only sums. Non-usage events
// are dropped exactly like the event.Discard this replaces.
type coldCacheUsageCapture struct {
	mu               sync.Mutex
	promptTokens     int
	completionTokens int
	cacheHitTokens   int
	requests         int
	cost             billing.Money
	costKnown        bool
	costMixed        bool
}

func (c *coldCacheUsageCapture) Emit(e event.Event) {
	if c == nil || e.Kind != event.Usage || e.Usage == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.promptTokens += e.Usage.PromptTokens
	c.completionTokens += e.Usage.CompletionTokens
	c.cacheHitTokens += e.Usage.CacheHitTokens
	c.requests += e.Usage.RequestCount
	if e.CostQuote == nil {
		return
	}
	if !c.costKnown {
		c.cost, c.costKnown = e.CostQuote.Original, true
		return
	}
	if sum, err := billing.AddMoney(c.cost, e.CostQuote.Original); err == nil {
		c.cost = sum
	} else {
		c.costMixed = true
	}
}

// logFields renders the capture as slog fields for the accounting log. A nil
// capture (the live path) renders nothing — that pass's usage already lands in
// the session's own ledger, so the cost is not double-counted here.
func (c *coldCacheUsageCapture) logFields() []any {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	fields := []any{
		"usagePromptTokens", c.promptTokens,
		"usageCompletionTokens", c.completionTokens,
		"usageCacheHitTokens", c.cacheHitTokens,
		"usageRequests", c.requests,
	}
	switch {
	case c.costMixed:
		fields = append(fields, "costMixedCurrency", true)
	case c.costKnown:
		fields = append(fields, "costAmount", c.cost.Float64(), "costCurrency", c.cost.Currency)
	}
	return fields
}

func (l *coldCacheCompactLoop) run() {
	ticker := time.NewTicker(coldCacheCompactTickInterval)
	defer ticker.Stop()
	for now := range ticker.C {
		l.tick(now)
	}
}

func (l *coldCacheCompactLoop) tick(now time.Time) {
	knobs := loadColdCacheCompactConfig()
	if !knobs.Enabled {
		return
	}
	active := l.a.activeTab()
	for _, meta := range l.a.ListSessions() {
		if meta.DeletedAt != 0 {
			continue
		}
		// Task 380-B: a tab the user is actively looking at is never a
		// compaction candidate — only background/cold tabs qualify.
		tab, ctrl := l.a.controllerForSessionPath(meta.Path)
		if tab != nil && tab == active {
			continue
		}
		l.mu.Lock()
		attempted := l.done[meta.Path]
		parked := l.parked[meta.Path]
		if ctrl != nil {
			if _, seen := l.firstSeen[meta.Path]; !seen {
				l.firstSeen[meta.Path] = now.UnixMilli()
			}
		}
		openedAt := l.firstSeen[meta.Path]
		busy := l.inFlight[meta.Path]
		l.mu.Unlock()
		if busy {
			// An in-flight pass runs to completion; never stack a second one.
			continue
		}
		size := sessionContextBytes(meta.Path)
		ok, _ := coldCacheCompactDecision(knobs.Enabled, now, meta.LastActivityAt, openedAt, attempted, parked, size, knobs.MinBytes, knobs.IdleMinutes)
		if !ok {
			continue
		}
		l.compactOne(meta.Path, meta.WorkspaceRoot, size, now.Sub(time.UnixMilli(meta.LastActivityAt)), meta.LastActivityAt)
	}
}

// compactOne runs the same pass as the "compact now" button — through the
// live controller when the session has one (task 380-B: background tabs
// only), or through the task-380 headless path when it does not. Either way
// the pass is marked in-flight so later ticks never stack a duplicate, and
// the attempt stamp is only written on success.
func (l *coldCacheCompactLoop) compactOne(path, workspaceRoot string, size int64, idle time.Duration, lastActivityAt int64) {
	l.mu.Lock()
	if l.inFlight[path] {
		l.mu.Unlock()
		return
	}
	l.inFlight[path] = true
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		delete(l.inFlight, path)
		l.mu.Unlock()
	}()

	_, ctrl := l.a.controllerForSessionPath(path)
	// Task 380 sixth-acceptance accounting: every pass logs the trigger moment
	// (log timestamp; durationMs derives it), the target session, the
	// before/after context bytes and the catalog-priced cost of the summary
	// call. The usage capture only exists on the headless path — a live tab's
	// usage already lands in its own session ledger.
	started := time.Now()
	var usage *coldCacheUsageCapture
	mode := coldCacheCompactModeLive
	var err error
	if ctrl != nil {
		err = ctrl.Compact(l.a.ctx, "")
	} else {
		// Task 380 main fix: cold sessions with no live controller used to be
		// skipped forever, so the whole compaction landed on the user's wait
		// path when they opened the conversation. Build a throwaway headless
		// controller on the same compact chain instead — the user is not
		// present (idle >5h, background), so a background build here is the
		// cheap side of the trade.
		mode = coldCacheCompactModeHeadless
		usage = &coldCacheUsageCapture{}
		err = l.headlessCompactOne(path, workspaceRoot, usage)
	}
	duration := time.Since(started)
	if err != nil {
		// Task 424: the terminal "no foldable region remains" class parks the
		// session for its current activity stamp instead of retrying every
		// tick — the session state itself cannot yield a compaction, so the
		// old per-tick WARN turned into an endless 10-minute log burst (one
		// ~5s headless build per dead session). New activity bumps
		// LastActivityAt, which re-arms the pass.
		if coldCacheCompactTerminal(err) {
			l.parkNoFoldable(path, lastActivityAt)
			slog.Info("desktop: cold cache compact parked (nothing foldable left; re-arms on next session activity)",
				"path", path, "bytes", size, "mode", mode,
				"idleMinutes", int(idle.Minutes()))
			return
		}
		// Failed passes stay invisible to the user (no dialog) and retry on
		// the next tick: the attempt stamp is only written on success, so a
		// transient provider error does not park the conversation until the
		// user touches it again.
		slog.Warn("desktop: cold cache compact failed (will retry next tick)",
			"path", path, "mode", mode, "bytes", size,
			"durationMs", duration.Milliseconds(), "err", err)
		return
	}
	l.mu.Lock()
	l.done[path] = lastActivityAt
	l.mu.Unlock()
	fields := append([]any{
		"path", path, "mode", mode,
		"bytesBefore", size, "bytesAfter", sessionContextBytes(path),
		"idleMinutes", int(idle.Minutes()), "durationMs", duration.Milliseconds(),
	}, usage.logFields()...)
	slog.Info("desktop: cold cache compact completed", fields...)
}

// parkNoFoldable records the task-424 park stamp: the compact attempt for
// this LastActivityAt ended in the terminal no-foldable-region class, so the
// tick skips the session until its activity stamp changes.
func (l *coldCacheCompactLoop) parkNoFoldable(path string, lastActivityAt int64) {
	l.mu.Lock()
	l.parked[path] = lastActivityAt
	l.mu.Unlock()
}

// coldCacheCompactTerminal reports whether a compact failure is the task-424
// terminal class: the context is over the maintenance threshold with no
// foldable region left, so retrying over the unchanged session state can never
// succeed (task 297's missing termination condition). Every other failure
// class stays retryable on the next tick.
func coldCacheCompactTerminal(err error) bool {
	return errors.Is(err, agent.ErrNoFoldableRegion)
}

// headlessCompactOne compacts a controller-less session: a throwaway
// boot.Build carries the session's saved state (SessionDir = the session's
// directory, same fold the restart checks use), runs the one compact pass on
// the standard chain, and closes. No sink output, no tab, no hydrate of any
// UI surface — the cost is one cold build plus the compaction itself, paid
// while the user is away instead of in front of them. sink collects the pass's
// billable usage for the task-380 accounting log; boot.Build wraps it with the
// cost-quote sink so usage arrives already priced from the billing catalog.
func (l *coldCacheCompactLoop) headlessCompactOne(path, workspaceRoot string, sink event.Sink) error {
	sessionDir := filepath.Dir(path)
	ctrl, err := boot.Build(l.a.bootContext(), boot.Options{
		Sink:                     sink,
		RequireKey:               false,
		StatsSource:              "cold-cache-compact",
		SessionDir:               sessionDir,
		WorkspaceRoot:            workspaceRoot,
		MCPHostProfile:           plugin.HostProfileDesktopApps,
		CleanupPendingReconciler: reconcileDesktopCleanupPending,
	})
	if err != nil {
		return fmt.Errorf("headless build: %w", err)
	}
	defer ctrl.Close()
	return ctrl.Compact(l.a.ctx, "")
}

// controllerForSessionPath finds the live tab/controller bound to a session
// path (identity comparison through sessionRuntimeKey, the same fold the
// restart and lease checks use). Callers must not hold a.mu.
func (a *App) controllerForSessionPath(path string) (*WorkspaceTab, control.SessionAPI) {
	want := sessionRuntimeKey(path)
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, tab := range a.tabs {
		if tab == nil || tab.Ctrl == nil {
			continue
		}
		if sp := tab.Ctrl.SessionPath(); sp != "" && sessionRuntimeKey(sp) == want {
			return tab, tab.Ctrl
		}
	}
	return nil, nil
}
