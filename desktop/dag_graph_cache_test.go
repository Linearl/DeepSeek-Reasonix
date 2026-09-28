package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
)

// TestSetDagGraphCacheCapacityWritesAndPushes pins the task-196fix2 host half
// end to end: the setter persists the config key AND the agent save path
// reflects the new capacity with no restart — this is the contract task 347's
// settings item will call (key: dag_graph_cache_capacity, type int, range
// 1..16, 0 in the file = built-in 3).
func TestSetDagGraphCacheCapacityWritesAndPushes(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	t.Cleanup(func() { agent.SetSessionGraphCacheCapacity(config.DagGraphCacheCapacityDefault) })

	app := &App{ctx: context.Background()}
	if err := app.SetDagGraphCacheCapacity(5); err != nil {
		t.Fatalf("SetDagGraphCacheCapacity(5): %v", err)
	}
	if got := agent.SessionGraphCacheCapacityNow(); got != 5 {
		t.Fatalf("agent capacity after push = %d, want 5 (live push, no restart)", got)
	}
	// The written config carries the key so the next boot restores it.
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if got := config.DagGraphCacheCapacity(cfg); got != 5 {
		t.Fatalf("config capacity = %d, want 5", got)
	}
	body, err := os.ReadFile(config.UserConfigPath())
	if err != nil {
		t.Fatalf("read user config: %v", err)
	}
	if !strings.Contains(string(body), "dag_graph_cache_capacity = 5") {
		t.Fatalf("render table missing the key line:\n%s", body)
	}

	// Out-of-range is refused by the config layer and never reaches the agent.
	if err := app.SetDagGraphCacheCapacity(99); err == nil {
		t.Fatal("capacity 99 must be refused")
	}
	if got := agent.SessionGraphCacheCapacityNow(); got != 5 {
		t.Fatalf("agent capacity after refused write = %d, want unchanged 5", got)
	}
}
