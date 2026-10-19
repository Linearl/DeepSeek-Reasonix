package boot

import "testing"

// minor-1 + audit-2 ⑥①: the boot decision is captured ONCE per process and
// every reader — the desktop pump gate included — sees exactly that snapshot,
// never a fresh config read. The FIRST publish decides; a later publish (a
// per-tab rebuild re-reading a changed config file) must be a no-op, or the
// "switch flip applies on restart" contract breaks. Runs on a RESET sync.Once
// (resetCollabDrainGateForTest) so it holds in the full-package run too — the
// package has other tests that publish first, and before the hook this test was
// red in every full-package run (green only when run alone).
func TestCollabDrainInboxGateFirstPublishWins(t *testing.T) {
	// 同包其它测试可能已先发布（整包顺序依赖）；本测试验的是「首次发布决定」，
	// 故在干净 once 上跑，结束后恢复现场（不污染后续测试的 gate 读数）。
	prev := CollabDrainInboxGate()
	resetCollabDrainGateForTest()
	t.Cleanup(func() {
		resetCollabDrainGateForTest()
		PublishCollabDrainInboxGate(prev)
	})
	if CollabDrainInboxGate() {
		t.Fatal("precondition: gate starts false after a reset")
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
