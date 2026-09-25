package main

import (
	"testing"

	"reasonix/internal/agent"
)

// Task 244 B5 (experimental_orphan_lease_reclaim): a lease error may be
// reclaimed when the recorded owner is this process — or, with the switch on,
// when the recorded foreign owner process is dead (a crash leftover, not a
// holder). A live foreign owner keeps being respected in both states.

func leaseInfo(pid int, writer string) *agent.SessionLeaseInfo {
	return &agent.SessionLeaseInfo{SessionPath: "s.jsonl", WriterID: writer, PID: pid}
}

func TestLeaseReclaimDecisionOffKeepsForeignRespect(t *testing.T) {
	alive := func(int) bool { return true }
	dead := func(int) bool { return false }

	// Off (default): any foreign runtime — alive or dead — is refused.
	if leaseReclaimDecision(leaseInfo(111, "foreign"), 222, "own", false, alive) {
		t.Fatal("off state must refuse a live foreign owner")
	}
	if leaseReclaimDecision(leaseInfo(111, "foreign"), 222, "own", false, dead) {
		t.Fatal("off state must refuse even a dead foreign owner (historical behaviour)")
	}
	// Our own lease and the nil-Info path are unchanged.
	if !leaseReclaimDecision(leaseInfo(222, "own"), 222, "own", false, alive) {
		t.Fatal("own lease must stay reclaimable")
	}
	if !leaseReclaimDecision(nil, 222, "own", false, alive) {
		t.Fatal("nil Info must keep attempting the reclaim (OS lock is the arbiter)")
	}
}

func TestLeaseReclaimDecisionOnTakesOrphansOnly(t *testing.T) {
	alive := func(int) bool { return true }
	dead := func(int) bool { return false }

	// On: a dead foreign owner is an orphan — reclaim it (restart recovery).
	if !leaseReclaimDecision(leaseInfo(111, "foreign"), 222, "own", true, dead) {
		t.Fatal("on state must reclaim a dead foreign owner (orphan lease)")
	}
	// On: a live foreign owner is still a holder — never steal from the living.
	if leaseReclaimDecision(leaseInfo(111, "foreign"), 222, "own", true, alive) {
		t.Fatal("on state must still respect a live foreign owner")
	}
	// Own lease and nil Info unchanged.
	if !leaseReclaimDecision(leaseInfo(222, "own"), 222, "own", true, alive) {
		t.Fatal("own lease must stay reclaimable")
	}
	if !leaseReclaimDecision(nil, 222, "own", true, dead) {
		t.Fatal("nil Info path must stay reclaimable")
	}
	// Same PID but a different writer id is not "ours" — only the dead-orphan
	// branch may take it (writer identity stays part of the self check).
	if leaseReclaimDecision(leaseInfo(222, "other-writer"), 222, "own", false, alive) {
		t.Fatal("same pid with a foreign writer must not be ours while the owner lives")
	}
}
