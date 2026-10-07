package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// TestSessionCollabDeleteUnreadGateDefaultsOff: the task-509 pre-archive gate
// ships off (软开关) — Default() must not turn it on.
func TestSessionCollabDeleteUnreadGateDefaultsOff(t *testing.T) {
	if Default().Agent.SessionCollabDeleteUnreadGate {
		t.Fatal("session_collab_delete_unread_gate must default to off")
	}
}

// TestRenderSessionCollabDeleteUnreadGate: the gate renders unconditionally
// (a hand-added line must survive the next config rewrite — task 321's
// silent-drop lesson) and round-trips through render/decode both ways.
func TestRenderSessionCollabDeleteUnreadGate(t *testing.T) {
	c := Default()
	rendered := RenderTOML(c)
	if !strings.Contains(rendered, "session_collab_delete_unread_gate = false") {
		t.Fatalf("rendered config must state the gate explicitly even when off:\n%s", rendered)
	}

	c.Agent.SessionCollabDeleteUnreadGate = true
	rendered = RenderTOML(c)
	if !strings.Contains(rendered, "session_collab_delete_unread_gate = true") {
		t.Fatalf("rendered config omitted the enabled gate:\n%s", rendered)
	}
	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v", err)
	}
	if !got.Agent.SessionCollabDeleteUnreadGate {
		t.Fatal("gate did not round-trip through render/decode")
	}

	var explicit Config
	if _, err := toml.Decode("[agent]\nsession_collab_delete_unread_gate = true\n", &explicit); err != nil {
		t.Fatalf("decode explicit switch: %v", err)
	}
	if !explicit.Agent.SessionCollabDeleteUnreadGate {
		t.Fatal("explicit TOML switch was ignored")
	}
}
