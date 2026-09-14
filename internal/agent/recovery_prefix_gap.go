package agent

import (
	"encoding/json"

	"reasonix/internal/provider"
)

// PrefixGap is the leading run of a losing chain that the winning chain never
// contains — the segment worth grafting onto the winner's head. Task 90: a
// promote that swaps in a winner which does not cover the current main would
// otherwise leave main-only work behind in the trash archive, and this is the
// part of it that can be recovered without inventing history.
type PrefixGap struct {
	// Messages is the segment to graft. It is always aligned to turn boundaries:
	// a tool result never arrives without the call that produced it, because an
	// orphaned result is rejected by the provider on the next request.
	Messages []provider.Message
	// ForkIndex is the copy offset where the chain stops being unique — the
	// first turn that the winner already knows about. len(copy) means the whole
	// chain is gap, which is a different situation (see HasForkPoint).
	ForkIndex int
}

// HasForkPoint reports whether the copy ever rejoins the winner. When it does
// not, the two chains are unrelated rather than diverged, and grafting a
// whole unrelated conversation onto another would be destructive.
func (g PrefixGap) HasForkPoint() bool {
	return g.ForkIndex >= 0
}

// messageFingerprint keys a message for set membership using the same
// normalisation and serialisation that messagesEqualForStorage compares with.
// Sharing the transformation is the point: a second notion of "same message"
// would eventually disagree with the first, and the disagreement would show up
// as a message that looks present but is treated as missing (or the reverse).
func messageFingerprint(m provider.Message) string {
	encoded, err := json.Marshal(messageForSessionIdentity(m))
	if err != nil {
		return ""
	}
	return string(encoded)
}

// isUserTurnStart reports whether m opens a conversational turn as far as the
// transcript is concerned. Host-generated user-role messages are protocol
// scaffolding, not turns; an empty Origin predates the field and counts as user
// input.
func isUserTurnStart(m provider.Message) bool {
	return m.Role == provider.RoleUser && m.Origin != provider.MessageOriginHost
}

// turnsOf splits messages into turns, each starting at a user-role message.
// Anything before the first user message is its own leading group, which keeps
// the split total: every message belongs to exactly one group.
func turnsOf(messages []provider.Message) [][]provider.Message {
	var turns [][]provider.Message
	for _, m := range messages {
		if len(turns) == 0 || isUserTurnStart(m) {
			turns = append(turns, nil)
		}
		turns[len(turns)-1] = append(turns[len(turns)-1], m)
	}
	return turns
}

// prefixGapForMessages is the testable core of SessionContentPrefixGap: given
// the winner's messages and the losing copy's, it finds the leading turns of the
// copy that the winner does not contain at all.
//
// A turn counts as present when ANY of its messages appears in the winner. That
// is deliberately generous: a turn the winner partly knows is a turn that
// diverged, not one that is missing, and grafting half a turn is exactly what
// the turn alignment exists to prevent.
func prefixGapForMessages(winner, copy []provider.Message) PrefixGap {
	known := make(map[string]struct{}, len(winner))
	for _, m := range winner {
		known[messageFingerprint(m)] = struct{}{}
	}

	turns := turnsOf(copy)
	// Two counters on purpose: the slice is indexed by turn, ForkIndex is a
	// message offset. Conflating them silently returns the whole copy.
	messageOffset := 0
	for turnIndex, turn := range turns {
		present := false
		for _, m := range turn {
			if _, ok := known[messageFingerprint(m)]; ok {
				present = true
				break
			}
		}
		if present {
			return PrefixGap{Messages: flattenTurns(turns[:turnIndex]), ForkIndex: messageOffset}
		}
		messageOffset += len(turn)
	}
	// The copy never rejoins the winner.
	return PrefixGap{Messages: flattenTurns(turns), ForkIndex: -1}
}

func flattenTurns(turns [][]provider.Message) []provider.Message {
	var out []provider.Message
	for _, turn := range turns {
		out = append(out, turn...)
	}
	return out
}

// SessionContentPrefixGap reports the losing copy's leading turns that the
// canonical transcript does not contain. ok is false when either side cannot be
// loaded safely — the same fail-closed rule SessionContentOverlap follows, since
// a damaged log must not produce a merge decision.
func SessionContentPrefixGap(canonicalPath, copyPath string) (PrefixGap, bool) {
	canonical, ok := LoadSessionContentSnapshot(canonicalPath)
	if !ok {
		return PrefixGap{}, false
	}
	copied, ok := LoadSessionContentSnapshot(copyPath)
	if !ok {
		return PrefixGap{}, false
	}
	return prefixGapForMessages(canonical.messages, copied.messages), true
}
