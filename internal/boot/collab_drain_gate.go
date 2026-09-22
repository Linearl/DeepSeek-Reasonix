package boot

import (
	"sync/atomic"

	"reasonix/internal/config"
)

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

// collabDrainInboxSnapshot is the process-wide gate decision, captured once at
// boot. minor-1 (audit-2): the desktop pump's skip gate reads this same
// snapshot instead of a live config load, so the registration right and the
// pump skip can never disagree inside one process lifetime. The ON→OFF flip
// without a restart used to let the pump resume under a still-registered
// drain_inbox and reopen the M-a double-consume race; a flip now takes effect
// on restart, on both sides, always together.
var collabDrainInboxSnapshot atomic.Bool

// PublishCollabDrainInboxGate records the boot-time gate decision. Called once
// from boot, next to the registration it mirrors.
func PublishCollabDrainInboxGate(enabled bool) { collabDrainInboxSnapshot.Store(enabled) }

// CollabDrainInboxGate reports the boot-time drain_inbox registration decision
// so the desktop pump's skip check resolves at the same instant as the
// registration (minor-1, audit-2): two sides that read different config states
// are not an exclusion.
func CollabDrainInboxGate() bool { return collabDrainInboxSnapshot.Load() }
