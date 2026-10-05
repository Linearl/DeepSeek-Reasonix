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

// TestSetDagGraphCacheByteCapsWritesAndPushes pins the task-499 host half:
// both byte-ceiling setters persist their config keys AND the agent cache
// reflects the pushed ceilings with no restart (keys: dag_graph_cache_max_mb /
// dag_graph_cache_entry_max_mb, type int, MiB, 0 in the file = built-in
// 2048/1024; range 16..65536 refused outside by the config layer).
func TestSetDagGraphCacheByteCapsWritesAndPushes(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	t.Cleanup(func() {
		agent.SetSessionGraphCacheMaxBytes(0)
		agent.SetSessionGraphCacheEntryMaxBytes(0)
	})

	app := &App{ctx: context.Background()}
	if err := app.SetDagGraphCacheMaxMB(4096); err != nil {
		t.Fatalf("SetDagGraphCacheMaxMB(4096): %v", err)
	}
	if err := app.SetDagGraphCacheEntryMaxMB(512); err != nil {
		t.Fatalf("SetDagGraphCacheEntryMaxMB(512): %v", err)
	}
	_, totalCap, entryCap, _, _ := agent.SessionGraphCacheByteStats()
	if totalCap != 4096<<20 {
		t.Fatalf("agent total cap = %d, want 4096MiB (live push, no restart)", totalCap)
	}
	if entryCap != 512<<20 {
		t.Fatalf("agent entry cap = %d, want 512MiB (live push, no restart)", entryCap)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if got := config.DagGraphCacheMaxMB(cfg); got != 4096 {
		t.Fatalf("config total = %d, want 4096", got)
	}
	if got := config.DagGraphCacheEntryMaxMB(cfg); got != 512 {
		t.Fatalf("config entry = %d, want 512", got)
	}
	body, err := os.ReadFile(config.UserConfigPath())
	if err != nil {
		t.Fatalf("read user config: %v", err)
	}
	if !strings.Contains(string(body), "dag_graph_cache_max_mb = 4096") ||
		!strings.Contains(string(body), "dag_graph_cache_entry_max_mb = 512") {
		t.Fatalf("render table missing the byte-cap key lines:\n%s", body)
	}

	// Out-of-range is refused by the config layer and never reaches the agent.
	if err := app.SetDagGraphCacheMaxMB(1); err == nil {
		t.Fatal("total 1 MiB must be refused (below the 16 MiB floor)")
	}
	if err := app.SetDagGraphCacheEntryMaxMB(70000); err == nil {
		t.Fatal("entry 70000 MiB must be refused (above the 65536 ceiling)")
	}
	if _, totalCap, entryCap, _, _ := agent.SessionGraphCacheByteStats(); totalCap != 4096<<20 || entryCap != 512<<20 {
		t.Fatalf("caps after refused writes = %d/%d, want unchanged 4096/512 MiB", totalCap, entryCap)
	}
}
