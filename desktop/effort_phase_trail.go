package main

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

// Task 889: in-flight record of one EffortForTab read — the 639 breakdown
// line only lands when a read COMPLETES (field samples: 133s/261s after the
// 2s cap, or never), so the timeout warn line carries this trail instead:
// which step the read entered, when, and its age right now. Observation
// only; a mark with no registered trail is a no-op, so non-EffortForTab
// callers (SetEffort, /effort, session flows) never write.
// effortPhaseTrailCap bounds one trail's events (an attempt walks ~8 phases;
// the cap only guarantees bounded memory if a future edit adds chatty marks).
const effortPhaseTrailCap = 12

// effortPhaseEvent is one phase entry: name + when the read entered it.
// Duration is derived at render time (until the next entry; the last entry
// reports its current age), so a stuck read's last number keeps growing.
type effortPhaseEvent struct {
	phase string
	at    time.Time
}

// effortPhaseTrail is one attempt's record. gen is the attempt token:
// beginEffortReadPhase bumps a counter, so a stale attempt's render/flag/drop
// become no-ops after a newer attempt for the same tab replaced the entry.
type effortPhaseTrail struct {
	gen      uint64
	timedOut bool
	events   []effortPhaseEvent
}

// effortPhaseState groups the trail's own lifetime state so App carries one
// field instead of three loose scalars (struct-state rule); all methods
// below go through it.
type effortPhaseState struct {
	mu     sync.Mutex
	trails map[string]*effortPhaseTrail
	gen    uint64
}

// beginEffortReadPhase registers one attempt's trail (starting with "start",
// so a timeout before any phase mark still renders) and returns its token.
func (a *App) beginEffortReadPhase(tabID string) uint64 {
	a.effortPhase.mu.Lock()
	defer a.effortPhase.mu.Unlock()
	if a.effortPhase.trails == nil {
		a.effortPhase.trails = map[string]*effortPhaseTrail{}
	}
	a.effortPhase.gen++
	tr := &effortPhaseTrail{
		gen:    a.effortPhase.gen,
		events: []effortPhaseEvent{{phase: "start", at: time.Now()}},
	}
	a.effortPhase.trails[tabID] = tr
	return tr.gen
}

// finishEffortReadPhase drops the attempt's trail when gen still owns it —
// after completion the breakdown line already has the full segment times.
func (a *App) finishEffortReadPhase(tabID string, gen uint64) {
	a.effortPhase.mu.Lock()
	defer a.effortPhase.mu.Unlock()
	if tr := a.effortPhase.trails[tabID]; tr != nil && tr.gen == gen {
		delete(a.effortPhase.trails, tabID)
	}
}

// effortPhaseMark appends one phase transition; no registered trail means
// no-op (a read not launched through EffortForTab, or one already finished).
func (a *App) effortPhaseMark(tabID, phase string) {
	a.effortPhase.mu.Lock()
	defer a.effortPhase.mu.Unlock()
	tr := a.effortPhase.trails[tabID]
	if tr == nil {
		return
	}
	tr.events = append(tr.events, effortPhaseEvent{phase: phase, at: time.Now()})
	if len(tr.events) > effortPhaseTrailCap {
		tr.events = append([]effortPhaseEvent(nil), tr.events[len(tr.events)-effortPhaseTrailCap:]...)
	}
}

// markEffortReadTimedOut flags the attempt (generation-checked) so its
// eventual breakdown line emits after_timeout=true and pairs with the warn.
func (a *App) markEffortReadTimedOut(tabID string, gen uint64) {
	a.effortPhase.mu.Lock()
	defer a.effortPhase.mu.Unlock()
	if tr := a.effortPhase.trails[tabID]; tr != nil && tr.gen == gen {
		tr.timedOut = true
	}
}

// effortReadAfterTimeout reports whether the in-flight read for tabID has
// blown its cap so far (read right before the breakdown line is emitted).
func (a *App) effortReadAfterTimeout(tabID string) bool {
	a.effortPhase.mu.Lock()
	defer a.effortPhase.mu.Unlock()
	tr := a.effortPhase.trails[tabID]
	return tr != nil && tr.timedOut
}

// effortPhaseTrailString renders the trail for the timeout warn line:
// "start=2ms reconcile=1838ms snapshot.load=14ms" — each entry owns the time
// until the next, the last reports its current age. "" when gen no longer
// owns the tab's trail (a newer attempt replaced it): no attribution beats
// the wrong attempt's.
func (a *App) effortPhaseTrailString(tabID string, gen uint64) string {
	a.effortPhase.mu.Lock()
	tr := a.effortPhase.trails[tabID]
	if tr == nil || tr.gen != gen || len(tr.events) == 0 {
		a.effortPhase.mu.Unlock()
		return ""
	}
	events := append([]effortPhaseEvent(nil), tr.events...)
	a.effortPhase.mu.Unlock()
	now := time.Now()
	var b strings.Builder
	for i, ev := range events {
		if i > 0 {
			b.WriteByte(' ')
		}
		end := now
		if i+1 < len(events) {
			end = events[i+1].at
		}
		owned := end.Sub(ev.at)
		if owned < 0 {
			owned = 0
		}
		b.WriteString(ev.phase)
		b.WriteByte('=')
		b.WriteString(strconv.FormatInt(owned.Milliseconds(), 10))
		b.WriteString("ms")
	}
	return b.String()
}
