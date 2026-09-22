package main

import (
	"testing"
	"time"
)

// Task 225: the grant registry binds a dispatched session (target) to its
// task source for the 24h window; the delegate resolver honors it only while
// fresh and only when the source runtime is visible.
func TestCascadeGrantRegistryAndExpiry(t *testing.T) {
	t.Run("empty selfPath is never delegated", func(t *testing.T) {
		registerCascadeGrant("", "sc_a")
		if _, _, ok := cascadeDelegateFor(""); ok {
			t.Fatal("an empty session path must keep the prompt local")
		}
	})
	t.Run("expired grant keeps the prompt local", func(t *testing.T) {
		registerCascadeGrant("sc_t", "sc_parent")
		cascadeGrantsMu.Lock()
		g := cascadeGrants["sc_t"]
		cascadeGrants["sc_t"] = cascadeGrant{source: g.source, grantedAt: time.Now().Add(-25 * time.Hour)}
		cascadeGrantsMu.Unlock()
		if _, _, ok := cascadeDelegateFor("C:/x/sc_t.jsonl"); ok {
			t.Fatal("an expired grant must not delegate")
		}
	})
	t.Run("self-dispatch is ignored", func(t *testing.T) {
		registerCascadeGrant("sc_self", "sc_self")
		cascadeGrantsMu.Lock()
		_, has := cascadeGrants["sc_self"]
		cascadeGrantsMu.Unlock()
		if has {
			t.Fatal("a session must not grant cascade to itself")
		}
	})
	t.Run("fresh grant is registered with the source", func(t *testing.T) {
		registerCascadeGrant("sc_fresh", "sc_parent")
		cascadeGrantsMu.Lock()
		g, has := cascadeGrants["sc_fresh"]
		cascadeGrantsMu.Unlock()
		if !has || g.source != "sc_parent" {
			t.Fatalf("the grant must record the source: %+v", g)
		}
	})
}
