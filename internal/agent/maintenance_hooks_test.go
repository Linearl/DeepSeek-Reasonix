package agent

import (
	"slices"
	"testing"

	"reasonix/internal/event"
)

// The tl-1194 ordering contract (触发点候选 B): the host-state rebuild fires
// from the same deferred single-flight release as the 920 settle hook, but
// strictly BEFORE it — parked turns are only released after the refresh has
// landed. If this ordering ever inverts or the rebuild hook is dropped from
// endMaintenance, the stale-prompt valve is gone again.
func TestEndMaintenanceFiresRebuildBeforeSettled(t *testing.T) {
	a := New(nil, nil, NewSession("sys"), Options{}, event.Discard)
	var order []string
	a.SetHostStateRebuildHook(func() { order = append(order, "rebuild") })
	a.SetCompactionSettledHook(func() { order = append(order, "settled") })

	a.maintaining.Add(1)
	a.endMaintenance()

	if !slices.Equal(order, []string{"rebuild", "settled"}) {
		t.Fatalf("hook order = %v, want [rebuild settled]", order)
	}

	// The counter guard: a second release must not re-fire either hook.
	a.endMaintenance()
	if !slices.Equal(order, []string{"rebuild", "settled"}) {
		t.Fatalf("second release re-fired hooks: %v", order)
	}
}

func TestEndMaintenanceNilRebuildHook(t *testing.T) {
	a := New(nil, nil, NewSession("sys"), Options{}, event.Discard)
	var settled int
	a.SetCompactionSettledHook(func() { settled++ })
	// Rebuild hook never registered (bench/compaction-only builds): the settle
	// hook must still fire — a parked turn can never outlive its gate.
	a.maintaining.Add(1)
	a.endMaintenance()
	if settled != 1 {
		t.Fatalf("settled fired %d times, want 1", settled)
	}
}

func TestSetHostStateRebuildHookNilReceiver(t *testing.T) {
	var a *Agent
	a.SetHostStateRebuildHook(func() {}) // must not panic (nil discipline)
}
