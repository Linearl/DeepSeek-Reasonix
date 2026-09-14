package agent

import (
	"testing"

	"reasonix/internal/provider"
)

// Task 90: the prefix gap is what a promotion grafts onto the winner so the
// losing chain's unique head is not left behind in the archive. These tests pin
// the cases that decide whether that graft is safe.

func userTurn(content string) provider.Message {
	return provider.Message{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: content}
}

func assistantTurn(content string) provider.Message {
	return provider.Message{Role: provider.RoleAssistant, Content: content}
}

func prefixToolResult(callID string) provider.Message {
	return provider.Message{Role: provider.RoleTool, ToolCallID: callID, Content: "ok"}
}

func names(messages []provider.Message) []string {
	out := make([]string, 0, len(messages))
	for _, m := range messages {
		out = append(out, m.Content)
	}
	return out
}

func TestPrefixGapIsEmptyWhenTheWinnerContainsTheCopy(t *testing.T) {
	winner := []provider.Message{userTurn("C"), assistantTurn("D"), userTurn("E")}
	copied := []provider.Message{userTurn("C"), assistantTurn("D")}

	gap := prefixGapForMessages(winner, copied)
	if len(gap.Messages) != 0 {
		t.Fatalf("gap = %v, want empty: the winner covers this chain", names(gap.Messages))
	}
	if !gap.HasForkPoint() {
		t.Fatal("a covered copy still rejoins the winner, so a fork point exists")
	}
}

func TestPrefixGapRecoversTheSegmentTheWinnerLacks(t *testing.T) {
	// The model this task was written from: main CDEFG, losing chain ABCH, and
	// picking the G chain should yield ABCDEFG with H still archived.
	winner := []provider.Message{userTurn("C"), assistantTurn("D"), userTurn("E"), assistantTurn("F"), userTurn("G")}
	copied := []provider.Message{userTurn("A"), assistantTurn("B"), userTurn("C"), assistantTurn("H")}

	gap := prefixGapForMessages(winner, copied)
	if got := names(gap.Messages); len(got) != 2 || got[0] != "A" || got[1] != "B" {
		t.Fatalf("gap = %v, want [A B]", got)
	}
	if gap.ForkIndex != 2 {
		t.Fatalf("fork index = %d, want 2 (C is where the copy rejoins)", gap.ForkIndex)
	}

	// Grafting the gap ahead of the winner must produce exactly ABCDEFG.
	grafted := append(append([]provider.Message{}, gap.Messages...), winner...)
	if got := names(grafted); len(got) != 7 || got[0] != "A" || got[6] != "G" {
		t.Fatalf("grafted = %v, want ABCDEFG", got)
	}
}

func TestPrefixGapReportsAnUnrelatedChain(t *testing.T) {
	winner := []provider.Message{userTurn("X"), assistantTurn("Y")}
	copied := []provider.Message{userTurn("A"), assistantTurn("B")}

	gap := prefixGapForMessages(winner, copied)
	if gap.HasForkPoint() {
		t.Fatal("a chain that never rejoins the winner has no fork point")
	}
	if got := names(gap.Messages); len(got) != 2 {
		t.Fatalf("gap = %v, want the whole copy", got)
	}
}

func TestPrefixGapTreatsAWholeCopyPrefixAsGap(t *testing.T) {
	winner := []provider.Message{userTurn("A"), assistantTurn("B"), userTurn("C")}
	copied := []provider.Message{userTurn("A"), assistantTurn("B")}

	gap := prefixGapForMessages(winner, copied)
	// A and B are present in the winner, so nothing is missing.
	if len(gap.Messages) != 0 {
		t.Fatalf("gap = %v, want empty", names(gap.Messages))
	}
}

func TestPrefixGapWithAnEmptyWinnerKeepsEverything(t *testing.T) {
	copied := []provider.Message{userTurn("A"), assistantTurn("B")}
	gap := prefixGapForMessages(nil, copied)
	if got := names(gap.Messages); len(got) != 2 {
		t.Fatalf("gap = %v, want the whole copy", got)
	}
	if gap.HasForkPoint() {
		t.Fatal("nothing to rejoin, so no fork point")
	}
}

// A turn the winner partly knows is a turn that diverged, not one that is
// missing. Grafting half of it would produce an orphaned tool result, which the
// provider rejects, so the whole turn must stay behind.
func TestPrefixGapKeepsTurnAlignment(t *testing.T) {
	winner := []provider.Message{userTurn("X"), prefixToolResult("call-2")}
	copied := []provider.Message{
		userTurn("A"), assistantTurn("B"),
		// This turn's result exists in the winner, so the turn is not missing —
		// even though its opening user message is not in the winner either.
		userTurn("C"), prefixToolResult("call-2"),
	}

	gap := prefixGapForMessages(winner, copied)
	if got := names(gap.Messages); len(got) != 2 || got[0] != "A" {
		t.Fatalf("gap = %v, want just the first turn [A B]", got)
	}
	for _, m := range gap.Messages {
		if m.Role == provider.RoleTool {
			t.Fatalf("gap carries a tool result without its call: %v", names(gap.Messages))
		}
	}
}

// Host-generated user-role messages are protocol scaffolding, so they must not
// be mistaken for the start of a conversational turn.
func TestPrefixGapIgnoresHostMessagesAsTurnStarts(t *testing.T) {
	host := provider.Message{Role: provider.RoleUser, Origin: provider.MessageOriginHost, Content: "host-protocol"}
	copied := []provider.Message{userTurn("A"), host}
	winner := []provider.Message{userTurn("Z")}

	gap := prefixGapForMessages(winner, copied)
	if len(gap.Messages) != 2 {
		t.Fatalf("gap = %v, want both messages: the host message does not open a turn", names(gap.Messages))
	}
}

// The fingerprint must agree with the comparison the rest of the storage layer
// uses, or a message could look present to one and missing to the other.
func TestPrefixGapFingerprintMatchesStorageEquality(t *testing.T) {
	base := userTurn("same")
	withLocalMetadata := base
	withLocalMetadata.ID = "local-id"
	withLocalMetadata.CreatedAt = 1234567

	if messageFingerprint(base) != messageFingerprint(withLocalMetadata) {
		t.Fatal("local-only metadata must not change a message's identity")
	}
	if !messagesEqualForStorage(base, withLocalMetadata) {
		t.Fatal("precondition: storage equality ignores local-only metadata")
	}

	different := userTurn("different")
	if messageFingerprint(base) == messageFingerprint(different) {
		t.Fatal("different content must not share a fingerprint")
	}
	if messagesEqualForStorage(base, different) {
		t.Fatal("precondition: storage equality distinguishes content")
	}
}
