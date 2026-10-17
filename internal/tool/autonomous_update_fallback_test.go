package tool

import (
	"context"
	"errors"
	"testing"
)

type stubController struct{ err error }

func (s stubController) ListVersions(context.Context) ([]VersionHealth, string, string, error) {
	return nil, "", "", s.err
}
func (s stubController) SetTarget(context.Context, string) (string, error) {
	return "", s.err
}
func (s stubController) ExecuteTarget(context.Context, string) (string, error) {
	return "", s.err
}
func (s stubController) RestartOnly(context.Context, string) (string, error) {
	return "", s.err
}

// clearFallbacks restores the process-wide slot after a test. The fallback is
// process-wide BY DESIGN (that is the fix), so tests must not leak
// registrations into each other or into other package tests.
func clearFallbacks(t *testing.T) {
	t.Cleanup(func() {
		var nilController *AutonomousUpdateController
		fallbackAutonomousUpdate.Store(nilController)
	})
}

// TestFallbackControllerServesCapabilityRoutedCalls pins the task-254 field
// fix: a call path that never crosses the agent's context-binding point
// (capability-routed dispatch) must still reach the desktop controller, and a
// context binding must keep winning over the process-wide fallback.
func TestFallbackControllerServesCapabilityRoutedCalls(t *testing.T) {
	clearFallbacks(t)

	// No binding, no fallback: unavailable (CLI/serve hosts stay correct).
	if _, ok := AutonomousUpdateControllerFromContext(context.Background()); ok {
		t.Fatal("without a binding or fallback the controller must be unavailable")
	}

	// Fallback registered, still no binding: capability-routed calls reach it.
	fallback := stubController{err: errors.New("fallback")}
	SetFallbackAutonomousUpdateController(fallback)
	got, ok := AutonomousUpdateControllerFromContext(context.Background())
	if !ok || got != AutonomousUpdateController(fallback) {
		t.Fatalf("fallback controller not served: ok=%v got=%v", ok, got)
	}

	// A context binding wins over the fallback (agent path unchanged).
	bound := stubController{err: errors.New("bound")}
	ctx := WithAutonomousUpdateController(context.Background(), bound)
	got, ok = AutonomousUpdateControllerFromContext(ctx)
	if !ok || got != AutonomousUpdateController(bound) {
		t.Fatalf("context binding must win: ok=%v got=%v", ok, got)
	}

	// A nil registration is ignored instead of clobbering a live fallback.
	SetFallbackAutonomousUpdateController(nil)
	if _, ok := AutonomousUpdateControllerFromContext(context.Background()); !ok {
		t.Fatal("nil registration must not clear a live fallback")
	}
}
