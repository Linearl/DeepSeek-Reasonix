package agent

import (
	"strings"
	"testing"
	"time"

	"reasonix/internal/sessioncollab"
)

// Task 319 acceptance: an injected peer death pages the watcher with recovery
// guidance (≤N minutes is the every_s clamp: max 300s), the page fires once
// per episode, an invisible runtime never guesses a death, and the empty
// subscription set stays fully idle (inherited from 284's wake-block design).

// TestTurnAbnormalEndPushesRecoveryGuidance: probe reports a dead turn → one
// steer push whose body carries the "任务不无声丢失" recovery guidance; the
// latch keeps the second tick silent; a healthy observation resets the latch
// so a LATER death pages again (once per episode, not once forever).
func TestTurnAbnormalEndPushesRecoveryGuidance(t *testing.T) {
	env := newSubTestEnv(t)
	status := "failed"
	env.svc.cfg.SessionTurnStatus = func(id string) (string, bool) { return status, true }

	if _, err := env.svc.Subscribe("ct_peer", "peer", []string{SubscribeTurnAbnormalEnd}, time.Hour, 0, 0); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	env.tick()
	if env.pushCount() != 1 {
		t.Fatalf("pushes = %d, want 1 (abnormal end pages once)", env.pushCount())
	}
	push := env.lastPush()
	if push.Delivery != string(sessioncollab.DeliverySteer) {
		t.Fatalf("delivery = %q, want steer (mid-turn page)", push.Delivery)
	}
	for _, want := range []string{subscribeMessagePrefix, "任务不无声丢失", "进展文档", "重派", "status=failed"} {
		if !strings.Contains(push.Body, want) {
			t.Errorf("push body missing %q: %s", want, push.Body)
		}
	}
	// Latch: the same dead state must not page again.
	env.tick()
	if env.pushCount() != 1 {
		t.Fatalf("pushes = %d after re-tick, want 1 (latched per episode)", env.pushCount())
	}
	// Recovery resets the latch; a SECOND death pages again.
	status = "completed"
	env.tick()
	status = "failed"
	env.tick()
	if env.pushCount() != 2 {
		t.Fatalf("pushes = %d after recovery+re-death, want 2 (once per episode)", env.pushCount())
	}
}

// TestTurnAbnormalEndNeverGuesses: no probe or an invisible runtime must not
// page the watcher for a healthy (or simply unseen) peer.
func TestTurnAbnormalEndNeverGuesses(t *testing.T) {
	env := newSubTestEnv(t) // cfg.SessionTurnStatus stays nil
	if _, err := env.svc.Subscribe("ct_peer", "peer", []string{SubscribeTurnAbnormalEnd}, time.Hour, 0, 0); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	env.tick()
	if env.pushCount() != 0 {
		t.Fatalf("pushes = %d with nil probe, want 0 (unknown is never a death)", env.pushCount())
	}
	env.svc.cfg.SessionTurnStatus = func(string) (string, bool) { return "", false }
	env.tick()
	if env.pushCount() != 0 {
		t.Fatalf("pushes = %d with known=false, want 0", env.pushCount())
	}
	// A healthy terminal is not an abnormal end.
	env.svc.cfg.SessionTurnStatus = func(string) (string, bool) { return "completed", true }
	env.tick()
	if env.pushCount() != 0 {
		t.Fatalf("pushes = %d with completed status, want 0", env.pushCount())
	}
}

// TestTurnStatusClassifier: the abnormal set is exactly the non-completed
// terminals of event.TurnStatus, and death_class buckets them for the 304
// logging family.
func TestTurnStatusClassifier(t *testing.T) {
	for _, s := range []string{"failed", "interrupted", "recovery_required", "protocol_failed"} {
		if !turnStatusIsAbnormalEnd(s) {
			t.Errorf("turnStatusIsAbnormalEnd(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"completed", "queued", "in_progress", "waiting_user", "cancelling", ""} {
		if turnStatusIsAbnormalEnd(s) {
			t.Errorf("turnStatusIsAbnormalEnd(%q) = true, want false (not an abnormal terminal)", s)
		}
	}
	if got := classifyTurnDeath("failed"); got != "provider_or_turn_failure" {
		t.Errorf("classifyTurnDeath(failed) = %q", got)
	}
	if got := classifyTurnDeath("interrupted"); got != "interrupted" {
		t.Errorf("classifyTurnDeath(interrupted) = %q", got)
	}
	if got := classifyTurnDeath("recovery_required"); got != "recovery_required" {
		t.Errorf("classifyTurnDeath(recovery_required) = %q", got)
	}
}

// TestSubscriptionPagingCadenceClamped: the "≤N minutes" acceptance knob is
// every_s — clamped to at most 300s, so a worst-case config still pages
// within N minutes of the event, and the default is 30s.
func TestSubscriptionPagingCadenceClamped(t *testing.T) {
	env := newSubTestEnv(t)
	sub, err := env.svc.Subscribe("ct_peer", "peer", []string{SubscribeStuck}, time.Hour, time.Hour, 0) // every=1h asked
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if sub.EveryS > 300 {
		t.Fatalf("every_s = %d, want clamp ≤ 300s (page within N minutes)", sub.EveryS)
	}
	sub2, err := env.svc.Subscribe("ct_peer", "peer", []string{SubscribeStuck}, time.Hour, 0, 0) // default
	if err != nil {
		t.Fatalf("Subscribe default: %v", err)
	}
	if sub2.EveryS != 30 {
		t.Fatalf("default every_s = %d, want 30s", sub2.EveryS)
	}
}

// TestEmptySubscriptionSetNeverPolls: with zero subscriptions the loop's next
// wake is the blocked-horizon (wake channel), not a timer — the inherited
// zero-overhead guarantee (acceptance ④) that 319 must not regress.
func TestEmptySubscriptionSetNeverPolls(t *testing.T) {
	env := newSubTestEnv(t)
	interval := env.svc.nextInterval()
	if interval < 24*time.Hour {
		t.Fatalf("empty-store next interval = %v, want the blocked horizon (≥24h, wake-channel only)", interval)
	}
	if _, err := env.svc.Subscribe("ct_peer", "peer", []string{SubscribeStuck}, time.Hour, 0, 0); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if got := env.svc.nextInterval(); got > 300*time.Second {
		t.Fatalf("subscribed-store next interval = %v, want ≤ 300s (the every_s clamp)", got)
	}
}
