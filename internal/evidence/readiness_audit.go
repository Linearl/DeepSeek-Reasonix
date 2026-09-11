package evidence

// ReadinessAuditResult classifies one host final-answer readiness audit receipt.
type ReadinessAuditResult string

const (
	ReadinessAllowed ReadinessAuditResult = "allowed"
	ReadinessBlocked ReadinessAuditResult = "blocked"
	ReadinessErrored ReadinessAuditResult = "errored"
	// ReadinessAdvised records a gap that was announced and carried into the next
	// turn instead of pausing the run. An unattended (autopilot) turn has nobody to
	// answer a recovery card, so the contract is surfaced rather than enforced by
	// stopping - but it is not waived: the gap is still audited and persisted.
	ReadinessAdvised ReadinessAuditResult = "advised"
)

// ReadinessAudit is a structured, non-rendered receipt for the final-answer
// readiness gate. It lets metrics sinks count why the host blocked a final
// answer without scraping Notice text or adding model-visible state.
type ReadinessAudit struct {
	Result                    ReadinessAuditResult
	Recovered                 bool
	MissingProjectChecks      int
	IncompleteTodos           int
	CommandMismatchMissing    int
	MissingAcceptanceCriteria int
	MissingVerification       int
	MissingReview             int
	MissingSignoff            int
	MissingActionEvidence     int
	MissingMutation           int
	MissingCapabilities       int
}
