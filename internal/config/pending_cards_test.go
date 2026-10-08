package config

import (
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

// TestPendingCardsDefaultsOff: the task-408 pending-decision card queue ships
// off (铁律 2) — Default() must not turn it on, and the TTL must fall back to
// the built-in 30 minutes.
func TestPendingCardsDefaultsOff(t *testing.T) {
	if Default().Agent.ExperimentalPendingCards {
		t.Fatal("experimental_pending_cards must default to off")
	}
	if got := Default().Agent.PendingCardTTLMinutes; got != 0 {
		t.Fatalf("pending_card_ttl_minutes default = %d, want 0 (built-in default)", got)
	}
}

// TestRenderPendingCards: the switch renders unconditionally (a hand-added
// line must survive the next config rewrite — task 321's silent-drop lesson)
// and round-trips through render/decode; the TTL clamps out-of-band values.
func TestRenderPendingCards(t *testing.T) {
	c := Default()
	rendered := RenderTOML(c)
	if !strings.Contains(rendered, "experimental_pending_cards = false") {
		t.Fatalf("rendered config must state the switch explicitly even when off:\n%s", rendered)
	}
	if !strings.Contains(rendered, "pending_card_ttl_minutes = 0") {
		t.Fatalf("rendered config must state the TTL explicitly:\n%s", rendered)
	}

	c.Agent.ExperimentalPendingCards = true
	c.Agent.PendingCardTTLMinutes = 45
	rendered = RenderTOML(c)
	if !strings.Contains(rendered, "experimental_pending_cards = true") || !strings.Contains(rendered, "pending_card_ttl_minutes = 45") {
		t.Fatalf("rendered config omitted the enabled switch/TTL:\n%s", rendered)
	}
	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v", err)
	}
	if !got.Agent.ExperimentalPendingCards || got.Agent.PendingCardTTLMinutes != 45 {
		t.Fatalf("switch/TTL did not round-trip: %+v", got.Agent)
	}

	var explicit Config
	if _, err := toml.Decode("[agent]\nexperimental_pending_cards = true\npending_card_ttl_minutes = 10\n", &explicit); err != nil {
		t.Fatalf("decode explicit switch: %v", err)
	}
	if !explicit.Agent.ExperimentalPendingCards || explicit.Agent.PendingCardTTLMinutes != 10 {
		t.Fatal("explicit TOML switch/TTL was ignored")
	}

	// TTL 钳制：0=内置默认；越界值拒绝（设置面显示存值，静默钳制会让 UI 说谎）。
	c2 := Default()
	if err := c2.SetPendingCardTTLMinutes(0); err != nil {
		t.Fatalf("ttl 0 must be the built-in default: %v", err)
	}
	if err := c2.SetPendingCardTTLMinutes(-5); err == nil {
		t.Fatal("negative ttl must be refused")
	}
	if err := c2.SetPendingCardTTLMinutes(8 * 24 * 60); err == nil {
		t.Fatal("ttl beyond 7 days must be refused")
	}
}

// TestPendingCardsLiveResolution: the live reader's TTL semantics — explicit
// value wins, zero falls back to the built-in default.
func TestPendingCardsLiveResolution(t *testing.T) {
	cfg := Default()
	if err := cfg.SetExperimentalPendingCards(true); err != nil {
		t.Fatalf("arm: %v", err)
	}
	if !cfg.Agent.ExperimentalPendingCards {
		t.Fatal("setter did not arm the switch")
	}
	ttl := DefaultPendingCardTTL
	if cfg.Agent.PendingCardTTLMinutes > 0 {
		ttl = time.Duration(cfg.Agent.PendingCardTTLMinutes) * time.Minute
	}
	if ttl != DefaultPendingCardTTL {
		t.Fatalf("default ttl resolution = %s, want %s", ttl, DefaultPendingCardTTL)
	}
	if err := cfg.SetPendingCardTTLMinutes(10); err != nil {
		t.Fatalf("set ttl: %v", err)
	}
	if got := time.Duration(cfg.Agent.PendingCardTTLMinutes) * time.Minute; got != 10*time.Minute {
		t.Fatalf("explicit ttl = %s, want 10m", got)
	}
}
