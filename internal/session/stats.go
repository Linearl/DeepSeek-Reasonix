package session

// OperationResidency is the in-memory footprint of one session's idempotency
// bookkeeping: how many operation records it keeps resident, plus a rough byte
// estimate (task 184).
//
// Why it exists: the 14.9 GB working-set report left this map as the remaining
// suspect inside the session layer. Rather than argue about it structurally, the
// host performance monitor samples this number so the growth curve can be read
// against it.
type OperationResidency struct {
	SessionID   string `json:"sessionId"`
	Operations  int    `json:"operations"`
	ApproxBytes int64  `json:"approxBytes"`
}

// operationRecordFixedBytes is the per-record cost beyond its two string keys:
// compactOperationRecord already drops the event payload, so what remains is a
// Commit header (ids, sequence numbers, timestamps). 256 bytes is deliberately
// an over-estimate, so a large reported number cannot be dismissed as rounding.
const operationRecordFixedBytes = 256

// OperationResidency reports this session's resident operation records. It is a
// read-only snapshot: taking it never changes the records, and a concurrent
// commit either lands before or after it.
func (s *Session) OperationResidency() OperationResidency {
	if s == nil {
		return OperationResidency{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var bytes int64
	for id, record := range s.operations {
		bytes += int64(len(id)+len(record.hash)) + operationRecordFixedBytes
	}
	return OperationResidency{SessionID: s.id, Operations: len(s.operations), ApproxBytes: bytes}
}

// OperationResidency aggregates the resident operation records of every runtime
// this service currently holds open. Closed sessions contribute nothing, which is
// the point: a leak would show up as a session that stays counted.
func (s *Service) OperationResidency() []OperationResidency {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	runtimes := make([]*Runtime, 0, len(s.active))
	for _, runtime := range s.active {
		runtimes = append(runtimes, runtime)
	}
	s.mu.Unlock()

	out := make([]OperationResidency, 0, len(runtimes))
	for _, runtime := range runtimes {
		if runtime == nil || runtime.session == nil {
			continue
		}
		out = append(out, runtime.session.OperationResidency())
	}
	return out
}
