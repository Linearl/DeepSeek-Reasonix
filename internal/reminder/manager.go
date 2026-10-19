package reminder

import (
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Delivery receives a due reminder. Implementations enqueue the message into
// the owning session's durable inbox (controller follow-up path, 任务579
// idle-turn bridge); they must be safe to call from the manager's timer
// goroutine. `overdue` marks restart catch-up deliveries: the entry's due
// time passed while the process was down, so the injected notice must say so.
type Delivery func(sessionPath string, r Reminder)

// Manager owns the in-process timers for one controller. It re-arms timers
// from the durable store whenever a session binds (rebind/constructor scan)
// and fires each due entry exactly once. Entries cancelled between arming and
// firing are dropped at delivery time by the controller's status re-check.
type Manager struct {
	deliver Delivery
	now     func() time.Time

	mu     sync.Mutex
	timers map[string]*time.Timer // key: reminder id
	closed bool
	wg     sync.WaitGroup
}

// NewManager builds a manager. Nil deliver disables actual delivery (tests
// that only exercise arming semantics).
func NewManager(deliver Delivery) *Manager {
	if deliver == nil {
		deliver = func(string, Reminder) {}
	}
	return &Manager{
		deliver: deliver,
		now:     time.Now,
		timers:  make(map[string]*time.Timer),
	}
}

// SetNowFunc overrides the clock (tests only). Call before Sync/Track.
func (m *Manager) SetNowFunc(now func() time.Time) {
	m.now = now
}

// Sync scans the session's durable queue and re-arms every pending entry:
// future entries get a timer at their due time, already-overdue entries fire
// immediately with the catch-up annotation. Call on session bind / rebind.
// Sync replaces the whole prior timer set first — entries may have changed on
// disk while the session was away, and only what the file holds now may fire.
func (m *Manager) Sync(sessionPath string) {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return
	}
	entries, err := List(sessionPath)
	if err != nil {
		// The queue file is per-session auxiliary state; a broken file must
		// not break session bind. Loud log, no timers — the next successful
		// Set re-syncs. Prior timers are left alone here: dropping them on a
		// read failure would silently cancel alarms the file may still hold.
		slog.Warn("reminder: sync queue", "session", sessionPath, "err", err)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.dropAllLocked()
	now := m.now().UnixMilli()
	for _, e := range entries {
		m.armLocked(sessionPath, e, now)
	}
}

// Track arms one freshly-set reminder without rescanning the file.
func (m *Manager) Track(sessionPath string, r Reminder) {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.armLocked(sessionPath, r, m.now().UnixMilli())
}

// Forget disarms one timer (cancel path). No-op when already fired.
func (m *Manager) Forget(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.timers[id]; ok {
		t.Stop()
		delete(m.timers, id)
	}
}

// DropAll disarms every timer. A controller serves one session at a time, so
// a session identity change (rebind) drops the whole set and re-syncs from
// the (new) session's durable queue.
func (m *Manager) DropAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dropAllLocked()
}

func (m *Manager) dropAllLocked() {
	for id, t := range m.timers {
		t.Stop()
		delete(m.timers, id)
	}
}

// Close stops all timers and seals the manager. In-flight deliveries finish
// on their own goroutines; Close does not wait for them (the controller's
// close path owns any stronger joining).
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.closed = true
	for id, t := range m.timers {
		t.Stop()
		delete(m.timers, id)
	}
}

// armLocked arms (or re-arms) one entry. Caller holds m.mu.
// Overdue entries deliver on their own goroutine with the catch-up flag.
func (m *Manager) armLocked(sessionPath string, r Reminder, nowMillis int64) {
	// Re-arm discipline: replace any existing timer for this id.
	if t, ok := m.timers[r.ID]; ok {
		t.Stop()
		delete(m.timers, r.ID)
	}
	delay := time.Duration(r.DueAt-nowMillis) * time.Millisecond
	if delay <= 0 {
		// Already overdue (restart catch-up, or the process slept past the
		// due time): fire immediately, marked late.
		r.FiredLate = true
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			m.deliver(sessionPath, r)
		}()
		return
	}
	id := r.ID
	m.timers[id] = time.AfterFunc(delay, func() {
		m.mu.Lock()
		delete(m.timers, id)
		m.mu.Unlock()
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			m.deliver(sessionPath, r)
		}()
	})
}
