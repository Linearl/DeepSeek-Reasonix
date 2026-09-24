package control

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Task 299: the recovery-fence binding (approval mode + unattended flag) must
// wrap EVERY turn. The first version of this guard hardcoded three entry
// signatures and immediately went stale: the orchestrator's production call
// surface turned out to be eight sites across controller.go, turn_images.go
// and friends, two of them reached unbound from autopilot. This version is
// dynamic — it scans the package for every `newTurnOrchestrator(` call site
// and asserts the enclosing top-level function binds the fence, so a future
// entry cannot be added without the guard noticing.
//
// The binding is idempotent (two ctx.WithValue wraps of the same keys), so
// requiring it at every call site rather than reasoning about which caller
// already bound keeps the rule mechanical: no call-chain inference, no false
// green when a wrapper's ctx turns out to come from somewhere new.
//
// Known reading of the rule: a bind placed in the outer function while the
// orchestrator call sits in a closure over a DIFFERENT ctx (e.g. a guarded
// callback's parameter) would satisfy the string check without covering the
// real path — that shape must be bound inside the closure, as
// runSubagentSkillSlash does. The guard covers the macro omission; the
// closure shape is one line of review.
func TestEveryOrchestratorCallSiteBindsRecoveryFence(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob control package: %v", err)
	}
	sites := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		lines := strings.Split(string(src), "\n")
		for i, line := range lines {
			if !strings.Contains(line, "newTurnOrchestrator(") {
				continue
			}
			if strings.HasPrefix(line, "func newTurnOrchestrator(") {
				continue // the constructor definition itself
			}
			sites++
			start := i
			for start > 0 && !strings.HasPrefix(lines[start], "func ") {
				start--
			}
			end := i + 1
			for end < len(lines) && !strings.HasPrefix(lines[end], "func ") {
				end++
			}
			body := strings.Join(lines[start:end], "\n")
			if !strings.Contains(body, "withRecoveryFenceBindings(") {
				t.Errorf("%s:%d: turn via %q does not bind the recovery fence — unattended autopilot would hit the barrier unbound",
					name, i+1, strings.TrimSpace(line))
			}
		}
	}
	if sites < 8 {
		t.Fatalf("guard scanned only %d orchestrator call sites, expected the full production surface (8 at task-299 time) — did the scan pattern break?", sites)
	}
	controller, err := os.ReadFile("controller.go")
	if err != nil {
		t.Fatalf("read controller.go: %v", err)
	}
	if !strings.Contains(string(controller), "func (c *Controller) withRecoveryFenceBindings(") {
		t.Fatal("withRecoveryFenceBindings helper missing from controller.go")
	}
}
