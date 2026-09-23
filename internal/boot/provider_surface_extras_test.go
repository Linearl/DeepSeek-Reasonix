package boot

import (
	"context"
	"encoding/json"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/tool"
)

// surfaceProbeTool is a minimal tool.Tool for registry visibility tests.
type surfaceProbeTool struct{ name string }

func (p surfaceProbeTool) Name() string        { return p.name }
func (p surfaceProbeTool) Description() string { return p.name + " desc" }
func (p surfaceProbeTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (p surfaceProbeTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "", nil
}
func (p surfaceProbeTool) ReadOnly() bool { return true }

// TestProviderSurfaceExtrasFollowTheFlag pins the gate: restart_update joins
// the provider-visible surface only while experimental_autonomous_update is
// on (task 254 second fault — the model tools list must carry it, or the
// first auto-update run cannot bootstrap).
func TestProviderSurfaceExtrasFollowTheFlag(t *testing.T) {
	on := &config.Config{}
	on.Desktop.ExperimentalAutonomousUpdate = true
	if got := providerSurfaceExtras(on); len(got) != 1 || got[0] != "restart_update" {
		t.Fatalf("extras with the switch on = %v", got)
	}
	off := &config.Config{}
	if got := providerSurfaceExtras(off); len(got) != 0 {
		t.Fatalf("extras with the switch off = %v, want none", got)
	}
	if got := providerSurfaceExtras(nil); len(got) != 0 {
		t.Fatalf("extras with nil config = %v, want none", got)
	}
}

// TestProviderVisibleSurfaceCarriesRestartUpdate pins the third call surface:
// with the tool registered and the flag-gated extra passed, the model-visible
// schema surface includes restart_update; with extras empty it stays hidden
// while remaining executable via Get (capability dispatch).
func TestProviderVisibleSurfaceCarriesRestartUpdate(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(surfaceProbeTool{name: "restart_update"})
	reg.Add(surfaceProbeTool{name: "use_capability"})

	// Flag off: registered and dispatchable, but not in the model tools list.
	applyUnifiedProviderToolSurface(reg, providerSurfaceExtras(&config.Config{})...)
	if reg.ProviderVisible("restart_update") {
		t.Fatal("restart_update must stay hidden while the switch is off")
	}
	if !reg.ProviderVisible("use_capability") {
		t.Fatal("sanity: use_capability should be visible in the unified surface")
	}

	// Flag on: the model tools list carries restart_update.
	on := &config.Config{}
	on.Desktop.ExperimentalAutonomousUpdate = true
	applyUnifiedProviderToolSurface(reg, providerSurfaceExtras(on)...)
	if !reg.ProviderVisible("restart_update") {
		t.Fatal("restart_update must be provider-visible with the switch on")
	}
	if !reg.ProviderVisible("use_capability") {
		t.Fatal("the unified surface must keep its core tools")
	}
	// Still executable via Get (the capability-dispatch surface).
	if _, ok := reg.Get("restart_update"); !ok {
		t.Fatal("restart_update must remain executable via Get")
	}
}
