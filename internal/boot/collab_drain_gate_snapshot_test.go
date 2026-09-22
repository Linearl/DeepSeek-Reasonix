package boot

import "testing"

// minor-1 + audit-2 ⑥①: the boot decision is captured ONCE per process and
// every reader — the desktop pump gate included — sees exactly that snapshot,
// never a fresh config read. The FIRST publish decides; a later publish (a
// per-tab rebuild re-reading a changed config file) must be a no-op, or the
// "switch flip applies on restart" contract breaks. Runs on the package's
// untouched sync.Once — this is the test process's first publish.
func TestCollabDrainInboxGateFirstPublishWins(t *testing.T) {
	if CollabDrainInboxGate() {
		t.Fatal("precondition: gate starts false in a fresh test process")
	}
	PublishCollabDrainInboxGate(true)
	if !CollabDrainInboxGate() {
		t.Fatal("first publish must decide the gate")
	}
	PublishCollabDrainInboxGate(false) // second call: must be a no-op
	if !CollabDrainInboxGate() {
		t.Fatal("a later publish must NOT flip the gate (once-per-process)")
	}
}
