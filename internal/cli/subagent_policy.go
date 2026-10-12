package cli

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"reasonix/internal/i18n"
)

// runSubagentPolicyCommand switches the sub-agent delegation tier for
// subsequent turns (#9004). The tier is transient (in-memory, per-turn
// injection): it applies from the next turn and works while a turn is
// running. SetSubagentPolicy persists it to the session's BranchMeta when a
// session is bound, but the CLI never reads that back — a session restart
// still falls back to the default (light) here. Task 820 corrects the old
// comment claiming no persistence happens at all.
func (m *chatTUI) runSubagentPolicyCommand(input string) tea.Cmd {
	args := tokenizeArgs(input)
	if len(args) != 2 {
		m.notice("usage: /subagent-policy light|balanced|aggressive")
		return nil
	}
	if m.ctrl == nil {
		m.notice("subagent-policy: controller not ready")
		return nil
	}
	if err := m.ctrl.SetSubagentPolicy(args[1]); err != nil {
		m.notice("subagent-policy: " + err.Error())
		return nil
	}
	m.notice(fmt.Sprintf(i18n.M.SubagentPolicySet, args[1]))
	return nil
}
