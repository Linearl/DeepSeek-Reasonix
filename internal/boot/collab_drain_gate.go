package boot

import "reasonix/internal/config"

// collabDrainInboxEnabled decides whether the agent-side drain_inbox tool is
// registered (Block2 M-a, task 235). MailStore must have exactly one consumer
// at a time:
//
//   - experimental_collab_background_delivery ON  → the host pump skips
//     delivery entirely (desktop pump.drain early-return) and drain_inbox is
//     the sole consumer (the M4 user ruling: "consumed exclusively by the
//     agent's drain_inbox tool").
//   - OFF (default) → the host pump delivers through runCollabDelivery
//     (two-phase Claim→Ack) and drain_inbox is NOT registered. Registering it
//     alongside the pump would double-consume the same batch: the pump's
//     two-phase Claim→Ack and drain's same-lock Claim+Ack can interleave on
//     the read-only Claim cursor (audit M-a).
//
// Registration is boot-time, so flipping the switch takes effect on restart.
func collabDrainInboxEnabled(agent *config.AgentConfig) bool {
	return agent != nil && agent.ExperimentalCollabBackgroundDelivery
}
