package main

import (
	"testing"

	"reasonix/internal/boot"
	"reasonix/internal/config"
)

// minor-1/minor-2 (audit-2) — the unreachability proof for the M-a overlap.
//
// The pump's two-phase Claim→Ack (runCollabDelivery) and drain_inbox's
// same-lock Claim+Ack (MailStore.Drain) interleave on the read-only Claim
// cursor, so they must never be active at the same time. That exclusion is
// enforced by construction: ONE boot snapshot decides BOTH the drain_inbox
// registration (internal/boot, publish site) and the pump skip
// (collabBackgroundDelivery, read site).
//
//   gate=true  → pump skips entirely, drain_inbox registered (sole consumer)
//   gate=false → pump delivers, drain_inbox withheld
//
// A live config flip without a restart must change nothing: the pump-side
// gate no longer reads config.Load(), so the pump cannot resume under a
// still-registered drain_inbox (the ON→OFF window that reopened the M-a
// double-consume race).
func TestCollabBackgroundDeliveryResolvesBootSnapshot(t *testing.T) {
	boot.PublishCollabDrainInboxGate(true)
	if !collabBackgroundDelivery() {
		t.Fatal("gate true must make the pump skip (drain_inbox owns MailStore)")
	}
	// Simulate the ON→OFF flip without a restart: the live branch would now
	// say off, but the process gate stays on.
	if collabBackgroundDeliveryFromConfig(&config.Config{}) {
		t.Fatal("live branch says off; only the boot snapshot may keep the gate on")
	}
	if !collabBackgroundDelivery() {
		t.Fatal("ON→OFF without restart resumed the pump under a registered drain_inbox (M-a race reopened)")
	}
	boot.PublishCollabDrainInboxGate(false)
	if collabBackgroundDelivery() {
		t.Fatal("gate false must let the pump deliver (drain_inbox withheld)")
	}
}
