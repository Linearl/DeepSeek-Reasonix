package agent

import "strings"

// AppliedTurnBodyTexts returns the trimmed body of every plain user turn the
// transcript durably holds (任务866: the follow-up twin of
// AppliedSteerReceiptTexts).
//
// P15's steer receipts only carry mid-turn injections (MidTurnSteerPrefix), so
// a follow-up row that reached a turn leaves no receipt the residue matcher
// can compare against — and the degraded collab follow-up (delivery=steer
// downgraded to queued_followup) parked as TranscriptNotDurable by a failed
// turn snapshot replayed onto the shelf after every restart (实测 2026-10-13:
// two degraded follow-ups back as pending after the 24.4 update restart, one
// pinned uncertain across four restarts in one day). The transcript itself is
// the evidence: a follow-up turn persists its body as a plain user message,
// so a pending row whose body equals one of these texts did reach a turn.
//
// Steer messages are skipped — their unwrapped texts are already the
// steer-receipt set's job, and a row consumed as a steer settles through that
// face. Transport framing is stripped (leading transient blocks such as
// current-time, the trailing delivery-runtime marker) so row bodies compare
// against what was actually said, not what the host wrapped around it. A
// missing or unreadable transcript returns nil: no receipt, no automatic
// settlement — the same fail-open contract as AppliedSteerReceiptTexts.
func AppliedTurnBodyTexts(sessionPath string) map[string]struct{} {
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
		if _, ok := SteerText(m.Message.Content); ok {
			continue
		}
		body := strings.TrimSpace(stripTrailingDeliveryRuntime(StripTransientUserBlocks(m.Message.Content)))
		if body != "" {
			set[body] = struct{}{}
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}
