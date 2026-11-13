package sessioninbox

import "sync/atomic"

// Lightweight process-local counters (no message bodies). Telemetry sinks may
// scrape these; zero values mean the feature was unused.
var (
	metricEnqueue       atomic.Int64
	metricEnqueueBytes  atomic.Int64
	metricRecovered     atomic.Int64
	metricPaused        atomic.Int64
	metricSteerAccepted atomic.Int64
	metricSteerRejected atomic.Int64
	metricCapacityRej   atomic.Int64
	metricUncertain     atomic.Int64
	metricTxFail        atomic.Int64
	// tl1339 刀①/刀②（gate-held 可见化）：三类「未转 exhausted 也无处查」的
	// 静默停各占一个平面计数。零值 = 该形态未发生。
	metricGateHeldSkips  atomic.Int64
	metricRetryExhausted atomic.Int64
	metricDormantClaims  atomic.Int64
	// tl1577 刀①（paused 补口）：泵亲手清掉一个自动暂停的次数。tl1339min
	// 时代这类停滞只留痕不处置；本刀起泵在空闲扫描命中「自动门 + 门后滞留
	// queued 活」时按 709 语义清门重驱，每次清门计一次。
	metricGateLifts atomic.Int64
)

// MetricsSnapshot is a body-free counters view for diagnostics.
type MetricsSnapshot struct {
	Enqueue        int64 `json:"enqueue"`
	EnqueueBytes   int64 `json:"enqueueBytes"`
	Recovered      int64 `json:"recovered"`
	Paused         int64 `json:"paused"`
	SteerAccepted  int64 `json:"steerAccepted"`
	SteerRejected  int64 `json:"steerRejected"`
	CapacityReject int64 `json:"capacityReject"`
	Uncertain      int64 `json:"uncertain"`
	TxFail         int64 `json:"txFail"`
	// tl1339: gate-held visibility counters (body-free, see the Note* docs).
	GateHeldSkips       int64 `json:"gateHeldSkips"`
	RetryExhausted      int64 `json:"retryExhausted"`
	DormantOrphanClaims int64 `json:"dormantOrphanClaims"`
	// tl1577: automatic-pause lifts by the dispatch pump (see NoteGateLifted).
	GateLifts int64 `json:"gateLifts"`
}

// Metrics returns current process-local inbox counters.
func Metrics() MetricsSnapshot {
	return MetricsSnapshot{
		Enqueue:             metricEnqueue.Load(),
		EnqueueBytes:        metricEnqueueBytes.Load(),
		Recovered:           metricRecovered.Load(),
		Paused:              metricPaused.Load(),
		SteerAccepted:       metricSteerAccepted.Load(),
		SteerRejected:       metricSteerRejected.Load(),
		CapacityReject:      metricCapacityRej.Load(),
		Uncertain:           metricUncertain.Load(),
		TxFail:              metricTxFail.Load(),
		GateHeldSkips:       metricGateHeldSkips.Load(),
		RetryExhausted:      metricRetryExhausted.Load(),
		DormantOrphanClaims: metricDormantClaims.Load(),
		GateLifts:           metricGateLifts.Load(),
	}
}

// NoteEnqueue increments durable-enqueue counters (body length only, no text).
func NoteEnqueue(bytes int64) {
	metricEnqueue.Add(1)
	if bytes > 0 {
		metricEnqueueBytes.Add(bytes)
	}
}

// NoteRecovered records crash-recovery item counts.
func NoteRecovered(n int) {
	if n > 0 {
		metricRecovered.Add(int64(n))
	}
}

func NotePaused()         { metricPaused.Add(1) }
func NoteSteerAccepted()  { metricSteerAccepted.Add(1) }
func NoteSteerRejected()  { metricSteerRejected.Add(1) }
func NoteCapacityReject() { metricCapacityRej.Add(1) }
func NoteUncertain()      { metricUncertain.Add(1) }
func NoteTxFail()         { metricTxFail.Add(1) }

// NoteGateHeldSkip tallies one dispatch cycle that found queued follow-ups
// parked behind the manifest gate (Paused / schema readonly hides them from
// NextQueued). tl1339 刀①: this stop burns no retry budget and produced no
// trail at all — the counter makes the "gate-held" state queryable. The gate
// owner still owns the next kick; nothing about dispatch behavior changes.
// tl1577 刀① narrowed the population: automatic pauses with parked work are
// lifted by the pump (counted by NoteGateLifted, never reaching here), so
// what remains is the user-held gate and the readonly gate — exactly the two
// holds a pump must not cross.
func NoteGateHeldSkip() { metricGateHeldSkips.Add(1) }

// NoteRetryExhausted tallies one dispatch cycle that found the generic
// admission-failure retry budget already spent with the failing condition
// persisting. tl1339 刀①: after the 3 bounded retries the pump used to stop
// dead silent (unlike the 579④ runtime path, which reports exhaustion); this
// is the missing trail. Zero behavior change: the budget still stands down
// and the item stays queued.
func NoteRetryExhausted() { metricRetryExhausted.Add(1) }

// NoteDormantOrphanClaim tallies one dormant-probe sighting of a claimed-but-
// ownerless head: a row in StateRunning / StateSteerAccepted whose runtime is
// gone from this process (claim-then-vanish), which every wake path therefore
// cannot see. tl1339 刀②: pure visibility — the probe still generates no
// wake target for such sessions.
func NoteDormantOrphanClaim() { metricDormantClaims.Add(1) }

// NoteGateLifted tallies one automatic-pause lift performed by the dispatch
// pump (tl1577 刀①, the 1007-family wake path): the pump's idle scan hit
// "Paused && !UserPaused with parked queued followups" and cleared it through
// the 任务709 store primitive (the same seam the desktop idle-turn bridge
// uses for background sessions). User-held pauses are never crossed and never
// counted here — they keep flowing to NoteGateHeldSkip.
func NoteGateLifted() { metricGateLifts.Add(1) }
