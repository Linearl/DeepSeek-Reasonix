package agent

import (
	"context"
	"testing"
)

// Task 367 C1: the hop bound exists to break grant cycles, not to intercept
// the normal single parent<->child hop. The table is pinned in code
// (CascadeHopExhausted) and here: depth 0/1 never blocked, the bound itself
// and beyond degrade to local.
func TestCascadeHopSingleHopNeverBlocked(t *testing.T) {
	depth0 := context.Background()
	if CascadeHopExhausted(depth0) {
		t.Fatal("depth 0 (the requesting session itself) must never be hop-blocked")
	}
	depth1 := WithCascadeHop(depth0)
	if CascadeHopExhausted(depth1) {
		t.Fatal("depth 1 (the normal single parent<->child delegation) must never be hop-blocked (task 367 C1)")
	}
	depth2 := WithCascadeHop(depth1)
	if CascadeHopExhausted(depth2) {
		t.Fatal("depth 2 is still under the bound and must stay allowed")
	}
}

func TestCascadeHopDeepChainDegradesToLocal(t *testing.T) {
	// Walk to the bound itself and one past it (constructed, not a real chain).
	ctx := context.Background()
	for i := 0; i < MaxCascadeHops(); i++ {
		ctx = WithCascadeHop(ctx)
	}
	if !CascadeHopExhausted(ctx) {
		t.Fatalf("depth %d (== maxCascadeHops) must degrade to local", MaxCascadeHops())
	}
	onePast := WithCascadeHop(ctx)
	if !CascadeHopExhausted(onePast) {
		t.Fatal("a depth beyond maxCascadeHops must stay degraded (anti-loop guard intact)")
	}
	if depth, _ := CascadeHop(onePast); depth != MaxCascadeHops()+1 {
		t.Fatalf("constructed depth must be maxCascadeHops+1, got %d", depth)
	}
}
