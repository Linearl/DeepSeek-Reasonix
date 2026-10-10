package main

// Task 539 route A ("take over without interrupting"): when the desktop user
// accepts a remote takeover the tab enters the YIELDING state — it keeps its
// session lease and its running turn finishes normally; new local turns and
// rewinds are refused for the window; once the runtime drains, the lease is
// released with a handoff reservation for the serve writer (ReleaseForHandoff)
// and the marker is flipped to the yielded ack. This closes the W3/W5 hole of
// the old interrupt-style yield: releasing mid-turn made every later save of
// that turn Stale-reject (lost writes with the lease already gone).
//
// The machine never cancels the turn. Cancelling happens only through the
// explicit remote forced takeover (T3, GC's double-gated force button) — the
// caller cancels and registers the yield with force=true.

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"reasonix/internal/agent"
)

// sessionYieldDesktopGrace extends the desktop-side wait beyond serve's own
// T2 poll window: the serve poll gives up first (removing the marker), so the
// desktop observes the withdrawal and rolls back instead of both sides
// tearing down simultaneously.
const sessionYieldDesktopGrace = 5 * time.Second

// sessionYieldMarkerDiscoveryGrace bounds how long the machine waits for the
// request marker to appear on the gateway path (the gate accepts BEFORE the
// forwarded serve request writes the marker). Past this grace with no marker
// the forwarded request is assumed dead and the yield rolls back.
const sessionYieldMarkerDiscoveryGrace = 10 * time.Second

// sessionYieldPollTick is the machine's work/marker poll cadence.
const sessionYieldPollTick = 100 * time.Millisecond

// errTabYieldingToTakeover is the typed admission refusal while a tab is
// yielding its session to a remote takeover.
var errTabYieldingToTakeover = errors.New("会话正在让渡给远程接管：本轮结束后自动完成移交，请稍后再试")

type sessionYield struct {
	tab    *WorkspaceTab
	path   string // transcript path this yield is for
	key    string // sessionRuntimeKey(path)
	marker string // marker file path ("" while undiscovered on the gateway path)
	force  bool   // remote forced takeover (turn cancelled by the caller)
	// startedAt anchors the marker-discovery grace on the gateway path.
	startedAt time.Time
	// markerSeen records that the request marker existed at least once; a
	// later absence is then a withdrawal (serve gave up), not a missing
	// request.
	markerSeen bool
	done       chan struct{}
}

var (
	sessionYieldMu      sync.Mutex
	activeSessionYields = map[string]*sessionYield{} // sessionRuntimeKey → yield
)

// takeoverYieldNotifier is the App-owned runtime event emitter for yield
// lifecycle notices (registered at startup next to the prompt sink).
var (
	takeoverYieldNotifyMu sync.Mutex
	takeoverYieldNotifier func(kind, path, detail string)
)

// RegisterTakeoverYieldNotifier installs the App-owned emitter for yield
// lifecycle events ("yielding" / "yielded" / "rollback" / "forced").
func RegisterTakeoverYieldNotifier(fn func(kind, path, detail string)) {
	takeoverYieldNotifyMu.Lock()
	takeoverYieldNotifier = fn
	takeoverYieldNotifyMu.Unlock()
}

func notifyTakeoverYield(kind, path, detail string) {
	takeoverYieldNotifyMu.Lock()
	fn := takeoverYieldNotifier
	takeoverYieldNotifyMu.Unlock()
	if fn != nil {
		fn(kind, path, detail)
	}
}

// beginSessionYieldToTakeover registers the tab's session as yielding and
// starts the wait machine. Non-blocking and idempotent per session key: a
// second call for an already-yielding session is a no-op. force=true marks a
// remote forced takeover whose turn the caller has already cancelled.
func beginSessionYieldToTakeover(tab *WorkspaceTab, path, marker string, force bool) {
	if tab == nil {
		return
	}
	key := sessionRuntimeKey(path)
	if key == "" {
		slog.Warn("desktop: yield requested for unmappable session path", "path", path)
		return
	}
	sessionYieldMu.Lock()
	if _, busy := activeSessionYields[key]; busy {
		sessionYieldMu.Unlock()
		return
	}
	y := &sessionYield{
		tab:    tab,
		path:   path,
		key:    key,
		marker: marker,
		force:  force,
		done:   make(chan struct{}),
	}
	if y.marker == "" {
		// Gateway path: the forwarded serve request writes the marker after
		// the gate allows; derive the path and let the machine discover it.
		y.marker = agent.TakeoverRequestMarkerPath(path)
	}
	activeSessionYields[key] = y
	sessionYieldMu.Unlock()
	kind := "yielding"
	if force {
		kind = "forced"
	}
	notifyTakeoverYield(kind, path, "")
	slog.Info("desktop: session yield started (turn keeps running)", "path", path, "marker", y.marker, "force", force)
	go y.run()
}

// sessionYieldActiveForTab reports whether the tab currently has a session
// yielding to a remote takeover. The turn admission barrier refuses new turns
// while true, so a yield can always reach its end.
func sessionYieldActiveForTab(tab *WorkspaceTab) bool {
	if tab == nil {
		return false
	}
	sessionYieldMu.Lock()
	defer sessionYieldMu.Unlock()
	for _, y := range activeSessionYields {
		if y.tab == tab {
			return true
		}
	}
	return false
}

// sessionYieldActiveForKey reports whether a session key is yielding.
func sessionYieldActiveForKey(key string) bool {
	sessionYieldMu.Lock()
	defer sessionYieldMu.Unlock()
	_, busy := activeSessionYields[key]
	return busy
}

// unregisterSessionYield removes the registry entry and reports whether the
// caller won the removal (only the machine tears a yield down).
func unregisterSessionYield(y *sessionYield) bool {
	sessionYieldMu.Lock()
	defer sessionYieldMu.Unlock()
	if cur, ok := activeSessionYields[y.key]; ok && cur == y {
		delete(activeSessionYields, y.key)
		return true
	}
	return false
}

// run is the yield machine: wait for the runtime to drain (turn finished,
// no pending prompt, no background jobs), then release the lease with a
// handoff reservation and publish the yield-ack. Terminal outcomes:
//   - complete: runtime drained → ReleaseForHandoff + yielded marker;
//   - rollback: marker withdrawn by serve (T2 timeout) / discovery grace
//     exhausted / desktop-side deadline hit — the lease is KEPT, the tab
//     returns to normal local operation, the user is told why.
func (y *sessionYield) run() {
	defer close(y.done)
	y.startedAt = time.Now()
	deadline := y.startedAt.Add(agent.SessionTakeoverYieldWindow + sessionYieldDesktopGrace)
	target := ""
	for {
		time.Sleep(sessionYieldPollTick)
		if y.tab == nil || sessionRuntimeKey(y.tab.currentSessionPath()) != y.key {
			// The tab moved to another session mid-yield: the yield lost its
			// subject (and its lease went with the swap). Roll back quietly.
			slog.Info("desktop: yield tab switched sessions, rolling back", "path", y.path)
			y.rollback("桌面端已切换会话，让渡取消")
			return
		}
		// Marker lifecycle: discover the target writer id, publish the
		// accepted state, detect withdrawals.
		target = y.refreshMarker(target)
		if y.markerGone() {
			y.rollback("接管请求已撤回或超时")
			return
		}
		if !y.markerSeen && y.marker != "" && time.Since(y.startedAt) > sessionYieldMarkerDiscoveryGrace {
			// Gateway path: the forwarded serve request never materialized
			// its marker — the forward died (serve crash, session gone).
			// Releasing the lease now would strand it with no acquirer.
			y.rollback("接管请求未到达（转发失败或已取消）")
			return
		}
		if y.tab.hasActiveRuntimeWork() {
			if time.Now().After(deadline) {
				y.rollback("桌面回合在等待窗口内未结束")
				return
			}
			continue
		}
		y.complete(target)
		return
	}
}

// refreshMarker reads the request marker, publishes the accepted state for a
// plain request, and returns the discovered target writer id ("" when none).
func (y *sessionYield) refreshMarker(currentTarget string) string {
	raw, err := os.ReadFile(y.marker)
	if err != nil {
		if os.IsNotExist(err) && !y.markerSeen && time.Since(y.startedAt) < sessionYieldMarkerDiscoveryGrace {
			// Gateway path: marker not written yet — keep waiting.
			return currentTarget
		}
		return currentTarget
	}
	y.markerSeen = true
	state := agent.ParseTakeoverMarker(string(raw))
	switch state.Kind {
	case agent.TakeoverMarkerKindRequest:
		// Accepted: publish the pending state so a concurrent watcher (and
		// any human looking at the file) sees the takeover is being honored.
		target := state.TargetWriterID
		_ = os.WriteFile(y.marker, []byte(agent.FormatTakeoverMarkerPending(target)), 0o600)
		return target
	case agent.TakeoverMarkerKindForced:
		// A forced request that arrived through the marker-watcher topology
		// (direct-serve mode): nobody cancelled for us — cancel here, once.
		if !y.force {
			y.force = true
			if y.tab != nil && y.tab.Ctrl != nil {
				slog.Info("desktop: forced takeover via marker, cancelling active turn", "path", y.path)
				y.tab.Ctrl.Cancel()
			}
			notifyTakeoverYield("forced", y.path, "")
		}
		return state.TargetWriterID
	case agent.TakeoverMarkerKindPending:
		if state.TargetWriterID != "" {
			return state.TargetWriterID
		}
		return currentTarget
	default:
		return currentTarget
	}
}

// markerGone reports a withdrawal: the marker existed (seen) and is now
// missing without our own completion — the serve poll gave up.
func (y *sessionYield) markerGone() bool {
	if !y.markerSeen {
		return false
	}
	if _, err := os.Stat(y.marker); err == nil {
		return false
	}
	return true
}

// complete releases the lease (with a handoff reservation for the serve
// writer when known), publishes the yield-ack, and retires the tab's watcher.
func (y *sessionYield) complete(target string) {
	if !unregisterSessionYield(y) {
		return
	}
	tab := y.tab
	tab.sessionLeaseMu.Lock()
	old := tab.sessionLease
	tab.sessionLease = nil
	tab.sessionLeaseMu.Unlock()
	handoffID := fmt.Sprintf("%s-takeover-%d", agent.SessionWriterID(), time.Now().UnixNano())
	if old != nil {
		if target != "" {
			if err := old.ReleaseForHandoff(target, handoffID); err != nil {
				// Reservation refused (already released / raced): a plain
				// release still lets the serve acquire; the serve falls back
				// to its plain acquire when the consume fails.
				slog.Warn("desktop: yield handoff reservation failed, releasing plainly", "path", y.path, "err", err)
				old.Release()
			}
		} else {
			slog.Info("desktop: yield completing without a target writer, releasing plainly", "path", y.path)
			old.Release()
		}
	}
	// Retire the lease bookkeeping exactly like the other release paths
	// (task 611): clear the runtime key mirror and stop the marker watcher —
	// the yielded marker would only confuse it.
	tab.storeSessionLeaseRuntimeKey("")
	tab.stopTakeoverRequestWatcher()
	// Yield-ack for the serve poll (it consumes the reservation from here).
	if y.marker != "" {
		_ = os.WriteFile(y.marker, []byte(agent.FormatTakeoverMarkerYielded(agent.SessionWriterID(), handoffID)), 0o600)
		// Orphan cleanup: if the serve poll already gave up, the ack would
		// linger; drop it once the reservation window is over.
		go func(marker string) {
			time.Sleep(agent.SessionTakeoverYieldWindow + 10*time.Second)
			if raw, err := os.ReadFile(marker); err == nil &&
				agent.ParseTakeoverMarker(string(raw)).Kind == agent.TakeoverMarkerKindYielded {
				_ = os.Remove(marker)
			}
		}(y.marker)
	}
	notifyTakeoverYield("yielded", y.path, "")
	slog.Info("desktop: session yielded to remote takeover", "path", y.path, "target", target, "handoff", handoffID)
}

// rollback cancels the yield while KEEPING the lease: the desktop tab returns
// to normal local operation and the user learns why the takeover did not
// complete. The marker (if any) is left for the serve poll to clean up.
func (y *sessionYield) rollback(reason string) {
	if !unregisterSessionYield(y) {
		return
	}
	notifyTakeoverYield("rollback", y.path, reason)
	slog.Info("desktop: session yield rolled back (lease kept)", "path", y.path, "reason", reason)
}

// waitForSessionYieldIdle blocks until the tab's yield machine (if any) has
// fully terminated — not merely unregistered: complete() keeps writing the
// ack and releasing the lease after the registry entry is gone. Test seam
// for the async machine.
func waitForSessionYieldIdle(tab *WorkspaceTab, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	var done chan struct{}
	for done == nil {
		sessionYieldMu.Lock()
		for _, y := range activeSessionYields {
			if y.tab == tab {
				done = y.done
			}
		}
		sessionYieldMu.Unlock()
		if done != nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if done == nil {
		return !sessionYieldActiveForTab(tab)
	}
	select {
	case <-done:
		return true
	case <-time.After(time.Until(deadline)):
		return false
	}
}
