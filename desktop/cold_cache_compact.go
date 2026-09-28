package main

import (
	"log/slog"
	"os"
	"sync"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/control"
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
func coldCacheCompactDecision(enabled bool, now time.Time, lastActivityAt, attemptedActivityAt, sizeBytes int64, minBytes int64, idleMinutes int) (bool, string) {
	if !enabled {
		return false, "switch off"
	}
	if idleMinutes <= 0 || minBytes <= 0 {
		return false, "invalid config"
	}
	if lastActivityAt <= 0 {
		return false, "no activity stamp"
	}
	idle := now.Sub(time.UnixMilli(lastActivityAt))
	if idle < time.Duration(idleMinutes)*time.Minute {
		return false, "not idle long enough"
	}
	if sizeBytes < minBytes {
		return false, "context under size floor"
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
}

// startColdCacheCompactLoop boots the unconditioned ticker: the lab switch is
// read live on every tick, so OFF costs one config read and never touches a
// session, while ON takes effect without a restart.
func (a *App) startColdCacheCompactLoop() {
	l := &coldCacheCompactLoop{a: a, done: make(map[string]int64)}
	a.goSafe("coldCacheCompactLoop", l.run)
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
	for _, meta := range l.a.ListSessions() {
		if meta.DeletedAt != 0 {
			continue
		}
		l.mu.Lock()
		attempted := l.done[meta.Path]
		l.mu.Unlock()
		size := sessionContextBytes(meta.Path)
		ok, reason := coldCacheCompactDecision(knobs.Enabled, now, meta.LastActivityAt, attempted, size, knobs.MinBytes, knobs.IdleMinutes)
		if !ok {
			if reason == "eligible" {
				continue
			}
			continue
		}
		l.compactOne(meta.Path, size, now.Sub(time.UnixMilli(meta.LastActivityAt)), meta.LastActivityAt)
	}
}

// compactOne resolves the session's live controller and runs the same pass as
// the "compact now" button. A session without a controller (tab closed or
// evicted) is skipped without an attempt stamp: the next tick may find it
// resident again, and opening a conversation just to compact it would cost
// more than the pass saves.
func (l *coldCacheCompactLoop) compactOne(path string, size int64, idle time.Duration, lastActivityAt int64) {
	_, ctrl := l.a.controllerForSessionPath(path)
	if ctrl == nil {
		slog.Debug("desktop: cold cache compact skipped (no live controller)",
			"path", path, "bytes", size)
		return
	}
	if err := ctrl.Compact(l.a.ctx, ""); err != nil {
		// Failed passes stay invisible to the user (no dialog) and retry on
		// the next tick: the attempt stamp is only written on success, so a
		// transient provider error does not park the conversation until the
		// user touches it again.
		slog.Warn("desktop: cold cache compact failed (will retry next tick)",
			"path", path, "err", err)
		return
	}
	l.mu.Lock()
	l.done[path] = lastActivityAt
	l.mu.Unlock()
	slog.Info("desktop: cold cache compact completed",
		"path", path, "bytes", size,
		"idleMinutes", int(idle.Minutes()))
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
