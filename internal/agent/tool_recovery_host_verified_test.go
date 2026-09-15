package agent

import (
	"context"
	"testing"

	"reasonix/internal/tool"
)

// Task 107 P1-③: an effect the host can disprove and fence is released without
// asking anyone, while an effect it cannot check keeps the barrier.
func TestHostVerifiableAbsentEffectReleasesTheFence(t *testing.T) {
	a, probe, _ := recoveryActionFixture(t)
	probe.inspection = tool.EffectInspection{State: "absent", Fenced: true}
	if err := a.beginToolRecovery(context.Background(), writePlan(probe, "after-host-verify")); err != nil {
		t.Fatalf("host-verifiable absent effect still blocked the write: %v", err)
	}
	if pending := a.PendingToolRecovery(); len(pending) != 0 {
		t.Fatalf("pending = %+v, want the disproved effect cleared", pending)
	}
}

func TestUnverifiableEffectKeepsTheFence(t *testing.T) {
	for _, inspection := range []tool.EffectInspection{
		{State: "unknown"},
		{State: "absent"}, // absent but not fenced: a retry is not proven safe
		{State: "present", Fenced: true},
	} {
		a, probe, _ := recoveryActionFixture(t)
		probe.inspection = inspection
		if err := a.beginToolRecovery(context.Background(), writePlan(probe, "still-blocked")); err == nil {
			t.Fatalf("inspection %+v must keep the barrier", inspection)
		}
	}
}
