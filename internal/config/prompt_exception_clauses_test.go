package config

import (
	"strings"
	"testing"
)

// TestAutonomyPolicyAssessmentException pins the assessment-before-fix
// exception (20261002 prompt research, gap report §2 G1): without it the
// surrounding "persist until fully handled" wording reads a plain question as
// an unfinished task and pushes the model to patch code the user only asked
// about. The exception makes the deliverable of a question an assessment.
func TestAutonomyPolicyAssessmentException(t *testing.T) {
	for _, want := range []string{
		"Exception: when the user is describing a problem",
		"rather than requesting a change",
		"the deliverable is your assessment",
		"Report your findings and stop",
		"Don't apply a fix until they ask for one",
	} {
		if !strings.Contains(AutonomyPolicy, want) {
			t.Fatalf("AutonomyPolicy missing %q", want)
		}
	}
	// The exception must come after the persistence clauses so it reads as
	// their carve-out, not as a competing instruction.
	if strings.Index(AutonomyPolicy, "Persist until") > strings.Index(AutonomyPolicy, "Exception:") {
		t.Fatal("AutonomyPolicy exception must follow the persistence clauses")
	}
	if len(AutonomyPolicy) > 1200 {
		t.Fatalf("AutonomyPolicy grew to %d bytes (cap 1200)", len(AutonomyPolicy))
	}
}

// TestUserDecisionPolicyOutwardFacingActions pins the outward-facing action
// clauses (20261002 prompt research, gap report §2 G5): confirm before hard to
// reverse or outward-facing actions, approval does not carry across contexts,
// and sending to an external service is publishing (cacheable/indexable even
// after deletion).
func TestUserDecisionPolicyOutwardFacingActions(t *testing.T) {
	for _, want := range []string{
		"hard to reverse or outward-facing",
		"confirm first unless durably authorized or explicitly told to proceed",
		"approval in one context does not extend to the next",
		"Sending content to an external service publishes it",
		"cached or indexed even after deletion",
	} {
		if !strings.Contains(UserDecisionPolicy, want) {
			t.Fatalf("UserDecisionPolicy missing %q", want)
		}
	}
	// The ask-tool contract (the reason this constant exists) must survive the
	// addition.
	if !strings.Contains(UserDecisionPolicy, "call the ask tool so the user can choose") {
		t.Fatal("UserDecisionPolicy lost the ask tool contract")
	}
}
