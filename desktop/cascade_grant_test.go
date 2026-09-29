package main

import (
	"os"
	"path/filepath"
	"strings"
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

// Task 365 C5: the first message of create_collab_session rides the steer
// path, which never runs the mail pump's onDelivered hook — the grant that
// binds the child to its task source has to be registered at the delivery
// site itself. This is a wiring assertion: the register call must sit inside
// the successful Deliver branch of queueCollabFirstMessage, with the same
// target/source orientation as onDelivered (recipient, sender).
func TestFirstMessageDeliveryBindsCascadeGrant(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("session_collab.go"))
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)
	queueIdx := strings.Index(code, "func (a *App) queueCollabFirstMessage")
	if queueIdx < 0 {
		t.Fatal("queueCollabFirstMessage not found")
	}
	body := code[queueIdx:]
	if !strings.Contains(body, "registerCascadeGrant(item.ContactID, from)") {
		t.Fatal("queueCollabFirstMessage must bind the cascade grant on successful delivery (task 365 C5)")
	}
	if !strings.Contains(body, "Task 365 C5") {
		t.Fatal("the grant binding must carry the task-365 rationale comment")
	}
}
