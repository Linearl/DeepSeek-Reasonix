package boot

import "testing"

// minor-1 (audit-2): the boot decision is published once and every reader —
// the desktop pump gate included — sees exactly that snapshot, never a fresh
// config read. This pins the publish/read contract that the pump-side
// same-source test (desktop/session_collab_gate_test.go) builds on.
func TestCollabDrainInboxGateSnapshotRoundTrip(t *testing.T) {
	PublishCollabDrainInboxGate(true)
	if !CollabDrainInboxGate() {
		t.Fatal("published true must read back true")
	}
	PublishCollabDrainInboxGate(false)
	if CollabDrainInboxGate() {
		t.Fatal("published false must read back false")
	}
}
