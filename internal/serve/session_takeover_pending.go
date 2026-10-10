package serve

import (
	"log/slog"
	"os"
	"time"

	"reasonix/internal/agent"
)

// Task 539 route A: the desktop no longer releases its session lease when the
// user accepts a remote takeover — its running turn finishes first, and only
// then is the lease released (with a handoff reservation). The takeover
// endpoint therefore cannot answer synchronously within 9s any more: it
// writes the request marker, replies 202 immediately, and keeps polling in
// the background until the lease frees up or SessionTakeoverYieldWindow (T2)
// expires. The remote client (GrandCouncil) follows the terminal state via
// GET /status?session=<name>, which reports takeoverPending plus, on failure,
// takeoverStatus/takeoverMessage.

const (
	// takeoverStatePending / takeoverStateDone / takeoverStateFailed are the
	// recorded lifecycle states of one takeover attempt.
	takeoverStatePending = "pending"
	takeoverStateDone    = "done"
	takeoverStateFailed  = "failed"

	// takeoverTimeoutMessage is the 539-approved wording for the T2 timeout.
	// The desktop DID accept — it just never finished its turn within the
	// window — so the old "holder did not yield" wording was misleading.
	takeoverTimeoutMessage = "desktop 本轮未结束，可稍后重试"
	// takeoverWithdrawnMessage covers the desktop removing the marker without
	// a yield (user rejection or prompt timeout) while the poll was running.
	takeoverWithdrawnMessage = "desktop 拒绝或撤回了接管请求"

	// takeoverAttemptTTL bounds how long a terminal outcome stays queryable
	// via /status. GC heartbeats every 15s, so minutes are plenty; the bound
	// keeps the map from growing without limit.
	takeoverAttemptTTL = 10 * time.Minute
)

// Poll cadence and wait window are package vars so tests can shrink them;
// production uses the 700ms tick the old inline loop used and the approved
// 30s T2 window.
var (
	takeoverPollInterval = 700 * time.Millisecond
	takeoverPollWindow   = agent.SessionTakeoverYieldWindow
)

type takeoverAttempt struct {
	state     string
	message   string
	updatedAt time.Time
}

// recordTakeoverState stores the lifecycle state for a takeover attempt on
// path (canonical form). Lazy map init keeps every Server constructor
// untouched (tests build Servers directly).
func (s *Server) recordTakeoverState(path, state, message string) {
	key := agent.CanonicalSessionPath(path)
	s.takeoverMu.Lock()
	if s.takeoverAttempts == nil {
		s.takeoverAttempts = map[string]*takeoverAttempt{}
	}
	now := time.Now()
	for k, v := range s.takeoverAttempts {
		if now.Sub(v.updatedAt) > takeoverAttemptTTL {
			delete(s.takeoverAttempts, k)
		}
	}
	s.takeoverAttempts[key] = &takeoverAttempt{state: state, message: message, updatedAt: now}
	s.takeoverMu.Unlock()
}

// takeoverStatusFor reports the recorded attempt state for path:
// pending=true while the yield is in flight; failed=true (with message) for
// a terminal failure; a done attempt (or no attempt) reports both false —
// the status view's takenOver/running fields carry the success signal.
func (s *Server) takeoverStatusFor(path string) (pending, failed bool, message string) {
	key := agent.CanonicalSessionPath(path)
	s.takeoverMu.Lock()
	defer s.takeoverMu.Unlock()
	att, ok := s.takeoverAttempts[key]
	if !ok || time.Since(att.updatedAt) > takeoverAttemptTTL {
		return false, false, ""
	}
	switch att.state {
	case takeoverStatePending:
		return true, false, ""
	case takeoverStateFailed:
		return false, true, att.message
	default:
		return false, false, ""
	}
}

// decorateTakeoverStatus adds the takeover polling fields to a ?session=
// status view. Safe on any view map; only a recorded attempt adds keys.
func (s *Server) decorateTakeoverStatus(path string, view map[string]any) map[string]any {
	pending, failed, message := s.takeoverStatusFor(path)
	view["takeoverPending"] = pending
	if failed {
		view["takeoverStatus"] = takeoverStateFailed
		view["takeoverMessage"] = message
	}
	return view
}

// pollTakeoverYield waits for the desktop to finish its turn and release the
// lease, then completes the takeover exactly like the old synchronous path:
// acquire → remove marker → rebind → notifyRemoteWriteAuthority. Terminal
// outcomes are recorded for the /status pollers; the marker file is always
// cleaned up so the desktop watcher never sees a stale request.
func (s *Server) pollTakeoverYield(abs, marker, from string) {
	deadline := time.Now().Add(takeoverPollWindow)
	for {
		time.Sleep(takeoverPollInterval)
		if lease := tryTakeoverAcquire(abs, marker); lease != nil {
			_ = os.Remove(marker)
			s.recordTakeoverState(abs, takeoverStateDone, "")
			// Same sequence as the synchronous success path: drop the probe
			// lease, let Rebind acquire its own, then flip the desktop into
			// remote-write mode.
			lease.Release()
			if s.leases == nil {
				slog.Warn("serve: takeover acquired but lease keeper unavailable", "path", abs)
				return
			}
			if err := s.leases.Rebind(abs); err != nil {
				// The takeover itself succeeded (lease acquired + released);
				// the next write path acquires cleanly — same reasoning as
				// the synchronous handler's `yielded` fallback.
				slog.Warn("serve: takeover rebind failed after yield", "path", abs, "err", err)
				return
			}
			notifyRemoteWriteAuthority(from, true)
			return
		}
		if _, err := os.ReadFile(marker); err != nil {
			// Marker gone without our own success: the desktop rejected,
			// timed out its prompt, or withdrew the request.
			s.recordTakeoverState(abs, takeoverStateFailed, takeoverWithdrawnMessage)
			slog.Info("serve: takeover marker withdrawn by desktop", "path", abs)
			return
		}
		// A yielded marker means the ack just landed; the reservation consume
		// in tryTakeoverAcquire succeeds on a following tick.
		if time.Now().After(deadline) {
			_ = os.Remove(marker)
			s.recordTakeoverState(abs, takeoverStateFailed, takeoverTimeoutMessage)
			slog.Info("serve: takeover poll timed out waiting for the desktop turn", "path", abs, "window", agent.SessionTakeoverYieldWindow.String())
			return
		}
	}
}

// tryTakeoverAcquire attempts one acquire cycle: a plain acquire first, then
// — after the desktop published its yield-ack — an explicit consume of the
// handoff reservation using the writer/handoff ids from the marker. A plain
// acquire refuses while an active reservation exists, so the consume is what
// makes the post-yield tick succeed immediately instead of after the
// reservation expiry.
func tryTakeoverAcquire(abs, marker string) *agent.SessionLease {
	if lease, err := agent.TryAcquireSessionLease(abs); err == nil {
		return lease
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		return nil
	}
	state := agent.ParseTakeoverMarker(string(raw))
	if state.Kind != agent.TakeoverMarkerKindYielded || state.WriterID == "" || state.HandoffID == "" {
		return nil
	}
	lease, err := agent.TryAcquireSessionLeaseWithHandoff(abs, state.WriterID, state.HandoffID)
	if err != nil {
		return nil
	}
	return lease
}
