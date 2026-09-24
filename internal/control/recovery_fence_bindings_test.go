package control

import (
	"os"
	"strings"
	"testing"
)

// Task 299: the recovery-fence binding (approval mode + unattended flag) must
// wrap EVERY turn entry. It used to live only inside runGoalLoopWithRawDisplay,
// so a plain foreground turn under autopilot reached the agent fence unbound —
// the write barrier fired and every turn ended with recovery_required, which
// is exactly the reported "interrupted tool needs review" stall. This is a
// source-level guard: constructing the full orchestrator per entry would only
// re-assert the same two lines with far more fixture.
func TestEveryTurnEntryBindsRecoveryFence(t *testing.T) {
	src, err := os.ReadFile("controller.go")
	if err != nil {
		t.Fatalf("read controller.go: %v", err)
	}
	body := string(src)
	entries := []string{
		"func (c *Controller) runGoalLoopWithRawDisplay(",
		"func (c *Controller) runEditedGoalLoopWithRawDisplay(",
		"func (c *Controller) runTurnWithRawDisplay(",
	}
	for _, signature := range entries {
		idx := strings.Index(body, signature)
		if idx < 0 {
			t.Fatalf("turn entry %q not found — did it get renamed?", signature)
		}
		// The function body runs to the next top-level entry; the helper call
		// must appear inside it.
		rest := body[idx:]
		end := len(rest)
		for _, next := range []string{
			"\nfunc (c *Controller) ",
		} {
			if rel := strings.Index(rest[len(signature):], next); rel >= 0 && len(signature)+rel < end {
				end = len(signature) + rel
			}
		}
		if !strings.Contains(rest[:end], "c.withRecoveryFenceBindings(ctx)") {
			t.Fatalf("%q does not wrap its ctx with withRecoveryFenceBindings — unattended autopilot turns would hit the fence unbound", signature)
		}
	}
	if !strings.Contains(body, "func (c *Controller) withRecoveryFenceBindings(") {
		t.Fatal("withRecoveryFenceBindings helper missing from controller.go")
	}
}
