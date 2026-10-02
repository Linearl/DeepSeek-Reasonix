package config

import (
	"strings"
	"testing"
)

// TestUserCommunicationPolicyCoversPanelSurface pins the clauses that make the
// section work on the session panel: the final message carries the outcome,
// mid-turn text is disposable, and readable prose beats fragments.
func TestUserCommunicationPolicyCoversPanelSurface(t *testing.T) {
	for _, want := range []string{
		"cannot see your thinking",
		"final text message",
		"no tool calls after it",
		"Lead with the outcome",
		"Readable",
		"direct answer in prose",
	} {
		if !strings.Contains(UserCommunicationPolicy, want) {
			t.Fatalf("UserCommunicationPolicy missing %q", want)
		}
	}
	// The section is a deliberate ~1.2K addition (20261002 prompt research,
	// report §4.1 #1). A rewrite that turns it into a workflow manual should
	// fail here instead of silently growing every session's cache-stable prefix.
	if len(UserCommunicationPolicy) > 1600 {
		t.Fatalf("UserCommunicationPolicy grew to %d bytes; keep it a communication rule, not a manual", len(UserCommunicationPolicy))
	}
}
