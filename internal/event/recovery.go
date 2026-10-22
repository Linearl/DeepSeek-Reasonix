package event

// RetryScope distinguishes dial/connect-phase attempts, connection+header
// retries, body-phase stream retries, and host-classified protocol recovery.
// Older clients ignore an unknown value and still render the generic retry
// state.
type RetryScope string

const (
	// RetryScopeConnect marks the dial/connect phase (task 1055-A3): the
	// request is initiated but the TCP/TLS handshake has not completed. It is
	// a distinct silence stage — never reused for the header wait
	// (header_wait_slow) or the first-token wait (prefill_slow) — so each
	// stretch of silence has exactly one name.
	RetryScopeConnect RetryScope = "connect"
	RetryScopeHeaders RetryScope = "headers"
	RetryScopeStream  RetryScope = "stream"
	// RetryScopeProtocol marks host-classified protocol recovery.
	RetryScopeProtocol RetryScope = "protocol"
)

// RecoveryStatus is a local UI projection, never provider-visible metadata.
type RecoveryStatus struct {
	// State is the durable tool-recovery state (for example recovery_required).
	// It is local UI metadata and never provider-visible.
	State                string `json:"state,omitempty"`
	CallID               string `json:"call_id,omitempty"`
	AttemptID            string `json:"attempt_id,omitempty"`
	RequiresUserDecision bool   `json:"requires_user_decision,omitempty"`
	ReadOnly             bool   `json:"read_only,omitempty"`
	Phase                string `json:"phase,omitempty"`
	Reason               string `json:"reason,omitempty"`
	NextAttemptAt        int64  `json:"next_attempt_at,omitempty"`
	WaitedMs             int64  `json:"waited_ms,omitempty"`
	WaitBudgetMs         int64  `json:"wait_budget_ms,omitempty"`
	Waiting              bool   `json:"waiting,omitempty"`
	// Task 845 (user-ratified retry semantics): Queue marks the rate-limit
	// QUEUE face (429 in the waiting lane — "service busy, queued"), and
	// QuotaWait marks the quota long-wait face ("quota exhausted, resuming
	// in ~N"). Both waits are cancellable; the strip keeps Stop available.
	Queue     bool `json:"queue,omitempty"`
	QuotaWait bool `json:"quota_wait,omitempty"`
	// Task 372 (visibility only — the task-243 A4 admission semantics are
	// untouched): BudgetUsed/BudgetLimit mirror the sliding window behind
	// Allow so the UI can say "auto-retried N (N/limit) times, recovering".
	// BudgetExhausted marks the terminal frame once the window or the
	// attempt cap refuses another round — the UI then shows the give-up
	// end state instead of an ambiguous running/idle flip.
	BudgetUsed      int  `json:"budget_used,omitempty"`
	BudgetLimit     int  `json:"budget_limit,omitempty"`
	BudgetExhausted bool `json:"budget_exhausted,omitempty"`
}
