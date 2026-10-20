package openai

import (
	"context"
	"errors"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

// Task 1031: MiniMax's M3.1 family (MiniMax-M3.1, MiniMax-M3.1-Flash) replaced
// M3's binary thinking knob with a reasoning_effort depth scale
// (low|medium|high|xhigh|max, omission defaulting to max) and rejects
// thinking.type=disabled outright (error 2013). The 2026-09-01 M3 survey (no
// depth scale) is stale for this generation; these tests pin the split.

func TestIsMiniMaxM31(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"MiniMax-M3.1", true},
		{"MiniMax-M3.1-Flash", true},
		{"minimax-m3.1-flash", true},               // case-insensitive
		{"minimax/MiniMax-M3.1", true},             // provider-ref prefix stripped
		{"MiniMax-M3", false},                      // M3 is NOT M3.1
		{"MiniMax-M2.7", false},                    // earlier generation
		{"MiniMax-M3.10", false},                   // longer number must not prefix-match
		{"some-miniMax-m3.1-gateway-model", false}, /* bare substring is not a SKU match */
	}
	for _, tc := range cases {
		if got := IsMiniMaxM31(tc.model); got != tc.want {
			t.Errorf("IsMiniMaxM31(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}

// TestReasoningCapabilityMiniMaxM31 pins the declared capability split: M3.1
// gets the five-level depth scale with max as the documented default, M3 keeps
// the binary adaptive|disabled vocabulary untouched.
func TestReasoningCapabilityMiniMaxM31(t *testing.T) {
	for _, model := range []string{"MiniMax-M3.1", "MiniMax-M3.1-Flash"} {
		cap := ReasoningForConfig(provider.Config{
			BaseURL: "https://api.minimaxi.com/v1",
			Model:   model,
			Extra:   map[string]any{},
		})
		if got, want := joinIDs(cap.IDs()), "low,medium,high,xhigh,max"; got != want {
			t.Errorf("%s: options = [%s], want [%s]", model, got, want)
		}
		if cap.Default != "max" {
			t.Errorf("%s: default = %q, want max (documented omission default)", model, cap.Default)
		}
		// The levels that would 400 on the wire must fail Validate here, before I/O.
		for _, bad := range []string{"disabled", "adaptive", "none", "off"} {
			if err := cap.Validate(model, bad); err == nil {
				t.Errorf("%s: Validate(%q) should be rejected", model, bad)
			}
		}
		for _, ok := range []string{"low", "medium", "high", "xhigh", "max", ""} {
			if err := cap.Validate(model, ok); err != nil {
				t.Errorf("%s: Validate(%q) = %v, want nil", model, ok, err)
			}
		}
	}
	// M3 zero regression: the binary vocabulary stays as before.
	m3 := ReasoningForConfig(provider.Config{
		BaseURL: "https://api.minimaxi.com/v1",
		Model:   "MiniMax-M3",
		Extra:   map[string]any{},
	})
	if got, want := joinIDs(m3.IDs()), "adaptive,disabled"; got != want {
		t.Errorf("MiniMax-M3: options = [%s], want [%s] (M3 unchanged)", got, want)
	}
	if m3.Default != "adaptive" {
		t.Errorf("MiniMax-M3: default = %q, want adaptive", m3.Default)
	}
}

// TestReasoningCapabilityMiniMaxM31SkipsThinkingCollapse: an explicit
// `thinking = "disabled"` config would collapse the capability to the binary
// "disabled" on M3 (and every other adapter), but M3.1 cannot honour it —
// the depth scale must stay selectable instead of declaring a level the wire
// can never send.
func TestReasoningCapabilityMiniMaxM31SkipsThinkingCollapse(t *testing.T) {
	cap := ReasoningForConfig(provider.Config{
		BaseURL: "https://api.minimaxi.com/v1",
		Model:   "MiniMax-M3.1-Flash",
		Extra:   map[string]any{"thinking": "disabled"},
	})
	if got, want := joinIDs(cap.IDs()), "low,medium,high,xhigh,max"; got != want {
		t.Errorf("options = [%s], want the depth scale [%s]", got, want)
	}
}

// TestBuildRequestMiniMaxM31Effort covers the M3.1 wire shape: every declared
// depth reaches reasoning_effort verbatim, and thinking.type is never emitted —
// the field M3.1 400s on.
func TestBuildRequestMiniMaxM31Effort(t *testing.T) {
	for _, tc := range []struct {
		name   string
		model  string
		effort string
		want   string // expected reasoning_effort on the wire
	}{
		{name: "auto-omits-field", model: "MiniMax-M3.1-Flash", effort: "", want: ""},
		{name: "low", model: "MiniMax-M3.1-Flash", effort: "low", want: "low"},
		{name: "medium", model: "MiniMax-M3.1-Flash", effort: "medium", want: "medium"},
		{name: "high", model: "MiniMax-M3.1", effort: "high", want: "high"},
		{name: "xhigh", model: "MiniMax-M3.1-Flash", effort: "xhigh", want: "xhigh"},
		{name: "max", model: "MiniMax-M3.1", effort: "max", want: "max"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := (&client{model: tc.model, minimax: true, effort: tc.effort}).buildRequest(provider.Request{})
			if req.ReasoningEffort != tc.want {
				t.Errorf("ReasoningEffort = %q, want %q", req.ReasoningEffort, tc.want)
			}
			if req.Thinking != nil {
				t.Errorf("M3.1 must not send thinking.type, got %+v", req.Thinking)
			}
		})
	}
}

// TestBuildRequestMiniMaxM31PerRequestOverride: a session-scoped override rides
// the same wire field (Stream validates it against the M3.1 capability first).
func TestBuildRequestMiniMaxM31PerRequestOverride(t *testing.T) {
	c := &client{model: "MiniMax-M3.1-Flash", minimax: true, effort: ""}
	req := c.buildRequest(provider.Request{EffortOverride: "xhigh"})
	if req.ReasoningEffort != "xhigh" || req.Thinking != nil {
		t.Fatalf("override wire: reasoning_effort=%q thinking=%+v, want xhigh with no thinking field", req.ReasoningEffort, req.Thinking)
	}
}

// TestBuildRequestMiniMaxM31IgnoresThinkingDisabled: a legacy
// `thinking = "disabled"` config must not reach the wire as
// thinking.type=disabled — that combination is a guaranteed 400 on M3.1.
func TestBuildRequestMiniMaxM31IgnoresThinkingDisabled(t *testing.T) {
	req := (&client{model: "MiniMax-M3.1-Flash", minimax: true, effort: "high", thinkingType: "disabled"}).buildRequest(provider.Request{})
	if req.Thinking != nil {
		t.Fatalf("thinking = %+v, want nil (M3.1 rejects thinking.type)", req.Thinking)
	}
	if req.ReasoningEffort != "high" {
		t.Fatalf("ReasoningEffort = %q, want high", req.ReasoningEffort)
	}
}

// TestNewMiniMaxM31EffortValidation pins the boot gate: the five depth levels
// and auto are accepted; the M3 vocabulary and retired levels are rejected with
// an M3.1-specific, actionable message instead of failing per-request.
func TestNewMiniMaxM31EffortValidation(t *testing.T) {
	base := provider.Config{Name: "m31", BaseURL: "https://api.minimaxi.com/v1", Model: "MiniMax-M3.1-Flash", APIKey: "k"}
	for _, ok := range []string{"", "low", "medium", "high", "xhigh", "max"} {
		if _, err := New(withEffort(base, ok)); err != nil {
			t.Errorf("effort=%q should be accepted: %v", ok, err)
		}
	}
	for _, bad := range []string{"adaptive", "disabled", "none", "off", "turbo"} {
		_, err := New(withEffort(base, bad))
		if err == nil {
			t.Errorf("effort=%q should be rejected", bad)
			continue
		}
		// Rejection can surface from either gate — the capability Validate in
		// configuredEffort or the M3.1 branch of the factory switch — but the
		// message must name the supported depth levels either way.
		if !strings.Contains(err.Error(), "xhigh") {
			t.Errorf("effort=%q error = %q, want it to name the M3.1 depth levels", bad, err.Error())
		}
	}
}

// TestNewMiniMaxM31PerRequestVocabulary: the probe the frontend fast path
// reads must equal the capability face (task 606fix lesson — a probe missing a
// declared level declines every switch to a full rebuild).
func TestNewMiniMaxM31PerRequestVocabulary(t *testing.T) {
	p, err := New(provider.Config{Name: "m31", BaseURL: "https://api.minimaxi.com/v1", Model: "MiniMax-M3.1-Flash", APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(p.(*client).PerRequestEfforts(), ",")
	if got != "low,medium,high,xhigh,max" {
		t.Errorf("PerRequestEfforts = [%s], want the five-level M3.1 scale", got)
	}
	// M3 zero regression: no per-request vocabulary without an explicit list.
	m3p, err := New(provider.Config{Name: "m3", BaseURL: "https://api.minimaxi.com/v1", Model: "MiniMax-M3", APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if got := m3p.(*client).PerRequestEfforts(); len(got) != 0 {
		t.Errorf("MiniMax-M3 PerRequestEfforts = %v, want empty (rebuild path, unchanged)", got)
	}
	// An explicit supported_efforts declaration stays authoritative.
	declared, err := New(provider.Config{
		Name: "m31d", BaseURL: "https://api.minimaxi.com/v1", Model: "MiniMax-M3.1-Flash", APIKey: "k",
		Extra: map[string]any{"supported_efforts": []string{"low", "high"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(declared.(*client).PerRequestEfforts(), ","); got != "low,high" {
		t.Errorf("declared PerRequestEfforts = [%s], want [low,high]", got)
	}
}

// TestStreamRejectsM31ThinkingOffOverrides: the request gate must reject the
// levels M3.1 400s on before any I/O, while admitting the depth scale.
func TestStreamRejectsM31ThinkingOffOverrides(t *testing.T) {
	p, err := New(provider.Config{Name: "m31", BaseURL: "https://api.minimaxi.com/v1", Model: "MiniMax-M3.1-Flash", APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	c := p.(*client)
	for _, bad := range []string{"disabled", "adaptive", "none"} {
		_, err := c.Stream(context.Background(), provider.Request{EffortOverride: bad})
		var unsupported *provider.UnsupportedReasoningEffort
		if !errors.As(err, &unsupported) {
			t.Fatalf("override %q: err = %v, want UnsupportedReasoningEffort before I/O", bad, err)
		}
	}
	// The depth levels pass Validate; the attempt then fails at I/O (no server
	// in tests) — assert the capability accepts them instead of the transport error.
	for _, ok := range []string{"low", "medium", "high", "xhigh", "max"} {
		if err := c.reasoning.Validate(c.model, ok); err != nil {
			t.Errorf("override %q: capability gate = %v, want nil", ok, err)
		}
	}
}
