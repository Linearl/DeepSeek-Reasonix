package agent

import (
	"testing"

	"reasonix/internal/provider"
)

// Task 172: host messages split into two visibility classes. Guidance the user can act on
// must reach the transcript; machine contracts (compaction briefings) must not. The split
// has to survive the focus section that compactionInstructionWithFocus appends.
func TestIsHostProtocolMessageSeparatesBriefingsFromGuidance(t *testing.T) {
	briefings := []provider.Message{
		HostGeneratedUserMessage(compactionInstruction),
		HostGeneratedUserMessage(compactionInstructionWithFocus("keep the migration plan and the failing test names")),
	}
	for _, m := range briefings {
		if !IsHostProtocolMessage(m) {
			t.Fatalf("compaction briefing must stay internal protocol: %q", firstLine(m.Content))
		}
	}

	guidance := []provider.Message{
		HostGeneratedUserMessage("Host guidance: the plan still needs a research pass before it can be approved."),
		HostGeneratedUserMessage("Readiness catch-up: the previous turn claimed completion without evidence — re-check the failing test before answering."),
		HostGeneratedUserMessage("Host progress redirect: the current todo still has no new completion after 6 tool-call rounds."),
		HostGeneratedUserMessage("Continue the file you were reading before answering."),
	}
	for _, m := range guidance {
		if IsHostProtocolMessage(m) {
			t.Fatalf("host guidance must remain user-visible: %q", firstLine(m.Content))
		}
		if !IsHostGeneratedUserMessage(m) {
			t.Fatalf("host guidance must still be recognised as host-generated: %q", firstLine(m.Content))
		}
	}

	user := provider.Message{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "please continue"}
	if IsHostProtocolMessage(user) {
		t.Fatal("user-authored messages are never host protocol")
	}
	assistant := provider.Message{Role: provider.RoleAssistant, Content: "done"}
	if IsHostProtocolMessage(assistant) {
		t.Fatal("assistant messages are never host protocol")
	}
}

func TestIsCompactionBriefingInstructionMatchesTheInstructionConstant(t *testing.T) {
	if !IsCompactionBriefingInstruction(compactionInstruction) {
		t.Fatal("the compaction instruction itself must be recognised")
	}
	if !IsCompactionBriefingInstruction("\n  " + compactionInstructionWithFocus("focus")) {
		t.Fatal("leading whitespace and an appended focus section must not defeat recognition")
	}
	if IsCompactionBriefingInstruction("Readiness catch-up: re-check the failing test.") {
		t.Fatal("guidance must not be mistaken for a compaction briefing")
	}
	if IsCompactionBriefingInstruction("") {
		t.Fatal("empty text is not a briefing")
	}
}
