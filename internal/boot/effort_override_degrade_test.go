package boot

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/event"
)

// Task 1042 acceptance ①: a persisted residual EffortOverride — a desktop
// tab's saved tab.effort restored onto a model whose config declares no
// effort vocabulary — must degrade to the provider default at build time
// (issue #62's "effort is not configurable" session-killer) instead of
// hard-failing the build and blocking every subsequent turn. The same-session
// model switch already silently drops such a value (app_model.go's
// SetModelForTab); this pins the boot-level choke point covering every other
// caller (startup, settings-apply, runtime reload, rebind).
func TestBuildDegradesResidualEffortOverrideOnModelWithoutEffortVocabulary(t *testing.T) {
	dir := robustTempDir(t)
	t.Chdir(dir)

	// A genuinely neutral OpenAI-compatible gateway: no supported_efforts,
	// no reasoning protocol, no vendor host the built-in heuristics recognize —
	// so config.NormalizeEffort rejects "high" with errEffortNotConfigurable.
	writeFile(t, dir, "reasonix.toml", `
default_model = "gateway/model"

[[providers]]
name = "gateway"
kind = "openai"
base_url = "https://api.example-gateway.com/v1"
model = "model"
api_key_env = "REASONIX_TEST_KEY_UNSET"
`)

	var notices []event.Event
	residual := "high" // persisted residue, never re-validated by a live /effort
	ctrl, err := BuildRuntime(context.Background(), Options{
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.Notice {
				notices = append(notices, e)
			}
		}),
		EffortOverride: &residual,
	})
	if err != nil {
		t.Fatalf("Build with residual effort must degrade, not fail: %v", err)
	}
	defer func() {
		if res := ctrl.Controller; res != nil {
			res.Close()
		}
	}()

	for _, notice := range notices {
		if notice.Level == event.LevelWarn && strings.Contains(notice.Text, "reasoning-effort override") &&
			strings.Contains(notice.Detail, "high") {
			return
		}
	}
	t.Fatalf("expected a degrade warning naming the dropped effort; got %v", notices)
}

// The degrade is one-sided: a level the model CAN take must still be honored
// verbatim, so a legitimate persisted override keeps applying (1031 zero
// regression + the non-residual path).
func TestBuildHonorsEffortOverrideOnModelThatDeclaresIt(t *testing.T) {
	dir := robustTempDir(t)
	t.Chdir(dir)

	writeFile(t, dir, "reasonix.toml", `
default_model = "gateway/model"

[[providers]]
name = "gateway"
kind = "openai"
base_url = "https://api.example-gateway.com/v1"
model = "model"
api_key_env = "REASONIX_TEST_KEY_UNSET"
supported_efforts = ["low", "high", "max"]
`)

	residual := "max"
	res, err := BuildRuntime(context.Background(), Options{EffortOverride: &residual})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer func() {
		if res.Runtime != nil {
			_ = res.Runtime.Close()
		}
		if res.Controller != nil {
			res.Controller.Close()
		}
	}()

	if got := res.Controller.ModelRef(); got != "gateway/model" {
		t.Fatalf("controller model ref = %q, want gateway/model", got)
	}
}

// A level outside a declared list (residue "ultra" on a model declaring
// low/high) is also residue, not a live user command — it degrades the same
// way rather than failing the build. This keeps the boot contract uniform:
// nothing that reaches boot as Options.EffortOverride can refuse to boot.
func TestBuildDegradesResidualEffortOutsideDeclaredList(t *testing.T) {
	dir := robustTempDir(t)
	t.Chdir(dir)

	writeFile(t, dir, "reasonix.toml", `
default_model = "gateway/model"

[[providers]]
name = "gateway"
kind = "openai"
base_url = "https://api.example-gateway.com/v1"
model = "model"
api_key_env = "REASONIX_TEST_KEY_UNSET"
supported_efforts = ["low", "high"]
`)

	residual := "ultra" // outside ["low","high"] — a usage-class error today
	if _, err := BuildRuntime(context.Background(), Options{EffortOverride: &residual}); err != nil {
		t.Fatalf("Build with out-of-list residual effort must degrade, not fail: %v", err)
	}
}
