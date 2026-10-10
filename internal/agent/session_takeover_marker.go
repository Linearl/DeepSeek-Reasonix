package agent

import (
	"strings"
	"time"
)

// Task 539: takeover pending protocol. The .takeover-request marker file is
// the existing cross-runtime channel between the serve takeover endpoint and
// the desktop's marker watcher (task 36/249). Route A ("take over without
// interrupting") extends its CONTENT into an explicit three-state protocol so
// both sides can tell "request delivered" from "desktop accepted, desktop
// turn still running" from "lease released, reservation published":
//
//	<serveWriterID>                        request live (serve wrote it; desktop deciding)
//	forced:<serveWriterID>                 remote forced takeover (GC double-gated):
//	                                       desktop skips its prompt, cancels the
//	                                       running turn and yields (T3)
//	pending:<serveWriterID>                desktop accepted; yield waits for the turn to end
//	yielded:<desktopWriterID>:<handoffID>  lease released with a handoff reservation
//	                                       for the serve writer; consume via
//	                                       TryAcquireSessionLeaseWithHandoff
//	absent                                 acquired (serve removes it) | rejected | timed out
//
// The plain writer id stays the first state so an OLD desktop watching an OLD
// marker still sees a recognizable request, and an old serve polling a plain
// acquire still succeeds once the new desktop plain-Releases (fallback when
// no target writer id could be discovered).
const (
	// TakeoverMarkerPendingPrefix marks an accepted-but-still-running yield.
	TakeoverMarkerPendingPrefix = "pending:"
	// TakeoverMarkerForcedPrefix marks a remote forced takeover (GC's
	// double-gated force button): the desktop yields without its own prompt
	// and cancels the running turn first (T3).
	TakeoverMarkerForcedPrefix = "forced:"
	// TakeoverMarkerYieldedPrefix marks a completed yield; the rest of the
	// content is "<desktopWriterID>:<handoffID>" for the WithHandoff consume.
	TakeoverMarkerYieldedPrefix = "yielded:"

	// TakeoverMarkerKindRequest / Forced / Pending / Yielded / Unknown are
	// the parse outcomes of ParseTakeoverMarker.
	TakeoverMarkerKindRequest = "request"
	TakeoverMarkerKindForced  = "forced"
	TakeoverMarkerKindPending = "pending"
	TakeoverMarkerKindYielded = "yielded"
	TakeoverMarkerKindUnknown = "unknown"

	// SessionTakeoverYieldWindow bounds how long the serve-side takeover poll
	// waits for the desktop turn to finish after acceptance (T2 in the 539
	// design). It matches SessionLeaseHandoffWindow so the whole chain — turn
	// end → ReleaseForHandoff → serve consumes the reservation — fits one
	// uniform window. Desktop-side yield waits use the same value plus a
	// short grace so the desktop observes the serve's marker cleanup (and
	// rolls back) instead of tearing down simultaneously.
	SessionTakeoverYieldWindow = 30 * time.Second
)

// TakeoverRequestMarkerPath maps a session transcript path to its takeover
// request marker (the same derivation the serve endpoint and the desktop
// watcher already used inline; single source of truth now).
func TakeoverRequestMarkerPath(sessionPath string) string {
	return strings.TrimSuffix(sessionPath, ".jsonl") + ".takeover-request"
}

// FormatTakeoverMarkerPending renders the accepted state; targetWriterID is
// the serve writer the lease is being yielded to.
func FormatTakeoverMarkerPending(targetWriterID string) string {
	return TakeoverMarkerPendingPrefix + strings.TrimSpace(targetWriterID)
}

// FormatTakeoverMarkerForced renders the remote forced-takeover request.
func FormatTakeoverMarkerForced(targetWriterID string) string {
	return TakeoverMarkerForcedPrefix + strings.TrimSpace(targetWriterID)
}

// FormatTakeoverMarkerYielded renders the yield-ack state. writerID is the
// desktop writer that released the lease (the WithHandoff sourceWriterID);
// handoffID is the reservation id it published (the WithHandoff handoffID).
func FormatTakeoverMarkerYielded(writerID, handoffID string) string {
	return TakeoverMarkerYieldedPrefix + strings.TrimSpace(writerID) + ":" + strings.TrimSpace(handoffID)
}

// TakeoverMarkerState is the decoded marker content.
type TakeoverMarkerState struct {
	// Kind is one of the TakeoverMarkerKind* constants.
	Kind string
	// TargetWriterID is set for request/pending: the serve writer that
	// requested the takeover.
	TargetWriterID string
	// WriterID and HandoffID are set for yielded: the releasing desktop
	// writer and the reservation id to consume.
	WriterID  string
	HandoffID string
}

// ParseTakeoverMarker decodes marker file content. Whitespace is tolerated.
func ParseTakeoverMarker(raw string) TakeoverMarkerState {
	s := strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(s, TakeoverMarkerPendingPrefix):
		target := strings.TrimSpace(strings.TrimPrefix(s, TakeoverMarkerPendingPrefix))
		return TakeoverMarkerState{Kind: TakeoverMarkerKindPending, TargetWriterID: target}
	case strings.HasPrefix(s, TakeoverMarkerForcedPrefix):
		target := strings.TrimSpace(strings.TrimPrefix(s, TakeoverMarkerForcedPrefix))
		return TakeoverMarkerState{Kind: TakeoverMarkerKindForced, TargetWriterID: target}
	case strings.HasPrefix(s, TakeoverMarkerYieldedPrefix):
		rest := strings.SplitN(strings.TrimPrefix(s, TakeoverMarkerYieldedPrefix), ":", 2)
		state := TakeoverMarkerState{Kind: TakeoverMarkerKindYielded}
		if len(rest) > 0 {
			state.WriterID = strings.TrimSpace(rest[0])
		}
		if len(rest) > 1 {
			state.HandoffID = strings.TrimSpace(rest[1])
		}
		return state
	case s != "":
		return TakeoverMarkerState{Kind: TakeoverMarkerKindRequest, TargetWriterID: s}
	default:
		return TakeoverMarkerState{Kind: TakeoverMarkerKindUnknown}
	}
}
