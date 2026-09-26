package agent

// Task 243 A2 (sub-report 01-④1): the dispatch ledger — a turn-scoped echo of
// who this conversation already talked to in the current turn. MiMo's
// dispatch ledger (tool/session.ts:685-749) fixed "the dispatcher cannot see
// the batch it just sent → duplicate dispatch" (tasks 175/218); this is the
// same echo on Reasonix's collab tools, deliberately display-only: it reports,
// it never refuses (semantic de-duplication is explicitly out of scope).

// maxDispatchLedger bounds one turn's echo. Batches are far smaller; the cap
// keeps a pathological turn from growing the transcript answer unbounded.
const maxDispatchLedger = 16

// RecordDispatch appends a resolved dispatch target to the current turn's
// ledger. Safe with a nil agent (direct unit construction) and idempotent
// enough for echo purposes — repeats are what a double dispatch looks like,
// so the ledger shows them rather than hiding them.
func (a *Agent) RecordDispatch(target string) {
	if a == nil || target == "" {
		return
	}
	ledger := a.turn.dispatchLedger
	if n := len(ledger); n > 0 && ledger[n-1] == target {
		return
	}
	ledger = append(ledger, target)
	if len(ledger) > maxDispatchLedger {
		ledger = ledger[len(ledger)-maxDispatchLedger:]
	}
	a.turn.dispatchLedger = ledger
}

// RecentDispatches returns this turn's dispatch echo (nil when empty so JSON
// omitempty callers can skip the field).
func (a *Agent) RecentDispatches() []string {
	if a == nil || len(a.turn.dispatchLedger) == 0 {
		return nil
	}
	out := make([]string, len(a.turn.dispatchLedger))
	copy(out, a.turn.dispatchLedger)
	return out
}
