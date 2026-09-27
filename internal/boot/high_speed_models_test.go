package boot

import (
	"testing"

	"reasonix/internal/config"
)

// Task 318.1: the high-speed lane arms only when the experiment is on; off —
// the shipped default — ignores the provider entry's list entirely.
func TestHighSpeedModelsFromConfigGate(t *testing.T) {
	models := []string{"provider/fast-model"}
	if got := highSpeedModelsFromConfig(nil, models); got != nil {
		t.Fatalf("nil config must yield nil (lane dormant), got %v", got)
	}
	off := config.Default()
	if off.Agent.ExperimentalHighSpeedModel {
		t.Fatal("default config must ship the lane off")
	}
	if got := highSpeedModelsFromConfig(off, models); got != nil {
		t.Fatalf("switch off must ignore the configured list, got %v", got)
	}
	on := config.Default()
	if err := on.SetExperimentalHighSpeedModel(true); err != nil {
		t.Fatal(err)
	}
	got := highSpeedModelsFromConfig(on, models)
	if len(got) != 1 || got[0] != models[0] {
		t.Fatalf("switch on must pass the list through, got %v", got)
	}
	// On with an empty list stays empty — the gate never invents models.
	if got := highSpeedModelsFromConfig(on, nil); got != nil {
		t.Fatalf("on + no configured list must stay nil, got %v", got)
	}
}
