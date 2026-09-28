package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// TestSetSessionCollabMailDefaults: the task-309 mailbox defaults persist via
// the settings setter, and the delivery channel rejects unknown values
// instead of storing them.
func TestSetSessionCollabMailDefaults(t *testing.T) {
	c := Default()
	if err := c.SetSessionCollabMailDefaults(boolPtr(false), boolPtr(true), strPtr("followup")); err != nil {
		t.Fatalf("set defaults: %v", err)
	}
	if c.Agent.SessionCollabMailIdempotentDefault {
		t.Fatal("idempotent default should be false after the setter")
	}
	if !c.Agent.SessionCollabMailReceiptDefault {
		t.Fatal("receipt default should be true after the setter")
	}
	if c.Agent.SessionCollabDefaultDelivery != "followup" {
		t.Fatalf("delivery = %q, want followup", c.Agent.SessionCollabDefaultDelivery)
	}
	if err := c.SetSessionCollabMailDefaults(nil, nil, strPtr("bogus")); err == nil {
		t.Fatal("unknown delivery channel must be rejected")
	}
	if err := c.SetSessionCollabMailDefaults(nil, nil, strPtr("")); err != nil {
		t.Fatalf("empty delivery must normalize to steer, got %v", err)
	}
	if c.Agent.SessionCollabDefaultDelivery != "steer" {
		t.Fatalf("empty delivery normalized to %q, want steer", c.Agent.SessionCollabDefaultDelivery)
	}
}

// TestRenderSessionCollabMailDefaults: the three defaults render
// unconditionally (so a settings-view change or a hand-added line is not
// dropped on save) and survive a render/decode round trip.
func TestRenderSessionCollabMailDefaults(t *testing.T) {
	c := Default()
	if err := c.SetSessionCollabMailDefaults(boolPtr(false), boolPtr(true), strPtr("followup")); err != nil {
		t.Fatal(err)
	}
	rendered := RenderTOML(c)
	for _, want := range []string{
		"session_collab_mail_idempotent_default = false",
		"session_collab_mail_receipt_default = true",
		`session_collab_default_delivery = "followup"`,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered config is missing %q:\n%s", want, rendered)
		}
	}
	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v", err)
	}
	if got.Agent.SessionCollabMailIdempotentDefault {
		t.Fatal("idempotent default did not survive the round trip")
	}
	if !got.Agent.SessionCollabMailReceiptDefault {
		t.Fatal("receipt default did not survive the round trip")
	}
	if got.Agent.SessionCollabDefaultDelivery != "followup" {
		t.Fatalf("delivery did not survive the round trip: %q", got.Agent.SessionCollabDefaultDelivery)
	}
}

func strPtr(v string) *string { return &v }
