package agent

import (
	"strings"
)

// AppliedSteerReceiptTexts returns the trimmed texts of every guidance message
// the session transcript durably injected as a mid-turn steer (P15).
//
// Injected steers persist as user-role messages carrying MidTurnSteerPrefix;
// SteerText unwraps the transport framing (turn preferences, delivery-runtime
// marker) back to the user's exact instruction. The returned set is the
// application receipt for durable guidance rows: a pending row whose body
// equals one of these texts was already applied, so recovery must settle it
// instead of replaying it onto the shelf.
//
// Plain user messages are deliberately excluded — a follow-up turn is
// indistinguishable from any later repeat of the same instruction, so its
// pending rows stay user-decided. A missing or unreadable transcript returns
// nil: no receipt, no automatic settlement.
func AppliedSteerReceiptTexts(sessionPath string) map[string]struct{} {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return nil
	}
	msgs, err := LoadSessionUserMessages(sessionPath)
	if err != nil {
		return nil
	}
	set := make(map[string]struct{})
	for _, m := range msgs {
		text, ok := SteerText(m.Message.Content)
		if !ok {
			continue
		}
		if trimmed := strings.TrimSpace(text); trimmed != "" {
			set[trimmed] = struct{}{}
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}
