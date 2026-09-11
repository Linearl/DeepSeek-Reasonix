package agent

import (
	"fmt"
	"strings"

	"reasonix/internal/taskcontract"
)

// readinessPauseActive reports whether an unmet final-readiness requirement may
// pause the turn and hand the user a recovery card.
//
// Delivery and closed-loop Goal/Plan turns pause on their readiness contract.
// Standard reports quality gaps in its completion summary and ends normally.
func (a *Agent) readinessPauseActive(check finalReadinessCheck) bool {
	if a == nil {
		return false
	}
	// An unattended run has nobody to answer the recovery card, so it advises and
	// continues instead - see unattendedReadinessAdvisory.
	if a.autopilot {
		return false
	}
	return a.readinessContractApplies(check)
}

// readinessContractApplies reports whether the turn carries a readiness contract
// at all. Delivery and closed-loop Goal/Plan turns do; Standard does not - its
// gaps are quality notes in the completion summary, not requirements.
func (a *Agent) readinessContractApplies(check finalReadinessCheck) bool {
	if a == nil {
		return false
	}
	return a.turn.constraints.PolicyFloor == taskcontract.PolicyFloorDelivery ||
		a.closedLoopActive() || a.planContractSnapshot() != nil
}

// unattendedReadinessAdvisory reports whether an unmet requirement should be
// announced and the run continued rather than paused for a human.
//
// Autopilot is unattended: nobody can answer a recovery card, so pausing would
// strand the run. But dropping the gap silently - the first task-56 attempt,
// which returned false for every autopilot turn - also waived the evidence bar
// the contract exists to enforce, so a gap and a satisfied contract looked the
// same afterwards. The gap is therefore kept, not waived: it is audited,
// persisted for the next turn, and announced in the transcript, which matches
// how upstream's evidence flow reports "Recorded as unverified: ..." instead of
// rejecting the sign-off outright.
func (a *Agent) unattendedReadinessAdvisory(check finalReadinessCheck) bool {
	return a != nil && a.autopilot && a.readinessContractApplies(check)
}

// readinessAdvisoryNotice is the transcript line for an advised gap.
func readinessAdvisoryNotice() string {
	return "Autopilot recorded this turn as unfinished and kept going. Missing evidence was carried into the next turn instead of stopping the run."
}

// autopilotGraceContinuationNotice explains why a budget pause did not stop an
// unattended run.
func autopilotGraceContinuationNotice() string {
	return "Autopilot continued past a budget boundary. Nobody is here to answer the continue prompt, so the run kept working; a repeated landing will still stop it."
}

// readinessAdvisoryDetail names the concrete gaps so the record is actionable
// rather than a bare category.
func readinessAdvisoryDetail(missing []string) string {
	if len(missing) == 0 {
		return "unfinished: readiness gap reported without named evidence ids"
	}
	return fmt.Sprintf("unfinished: %s", strings.Join(missing, ", "))
}
