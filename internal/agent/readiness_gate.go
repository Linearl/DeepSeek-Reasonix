package agent

import "reasonix/internal/taskcontract"

// readinessPauseActive reports whether an unmet final-readiness requirement may
// pause the turn and hand the user a recovery card.
//
// Delivery and closed-loop Goal/Plan turns pause on their readiness contract.
// Standard reports quality gaps in its completion summary and ends normally.
func (a *Agent) readinessPauseActive(check finalReadinessCheck) bool {
	if a == nil {
		return false
	}
	// Task 56: an unattended run has nobody to answer the recovery card, so pausing
	// would strand it - staying alive is the whole point of autopilot (goal plus
	// self-approval). The gap is still audited and the Goal FSM sees the missing
	// evidence on the next turn instead of waiting for a human.
	if a.autopilot {
		return false
	}
	return a.turn.constraints.PolicyFloor == taskcontract.PolicyFloorDelivery ||
		a.closedLoopActive() || a.planContractSnapshot() != nil
}
