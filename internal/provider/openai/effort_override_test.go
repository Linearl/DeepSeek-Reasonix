package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"reasonix/internal/provider"
)

func newTestClient(t *testing.T, model string, extra map[string]any) *client {
	t.Helper()
	cfg := provider.Config{Name: "p", BaseURL: "https://api.deepseek.com", Model: model, APIKey: "k", Extra: extra}
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p.(*client)
}

func TestEffortOverrideDeepSeekFlash(t *testing.T) {
	c := newTestClient(t, "deepseek-v4-flash", map[string]any{"reasoning_protocol": "deepseek"})
	if got := c.buildRequest(provider.Request{}).ReasoningEffort; got != "high" {
		t.Fatalf("default reasoning_effort = %q, want high", got)
	}
	if got := c.buildRequest(provider.Request{EffortOverride: "low"}).ReasoningEffort; got != "low" {
		t.Fatalf("override low: reasoning_effort = %q, want low", got)
	}
	assertRejectedEffort(t, c, "medium")
	request := c.buildRequest(provider.Request{EffortOverride: "disabled"})
	if request.ReasoningEffort != "" || request.Thinking == nil || request.Thinking.Type != "disabled" {
		t.Fatalf("disabled override = thinking:%#v effort:%q, want thinking disabled and no effort", request.Thinking, request.ReasoningEffort)
	}
}

func TestEffortOverrideDeepSeekNonFlashRejectsLow(t *testing.T) {
	c := newTestClient(t, "deepseek-v4", map[string]any{"reasoning_protocol": "deepseek"})
	assertRejectedEffort(t, c, "low")
	if got := c.buildRequest(provider.Request{EffortOverride: "max"}).ReasoningEffort; got != "max" {
		t.Fatalf("max is in the official DeepSeek vocabulary, got %q", got)
	}
}

func TestEffortOverrideDeepSeekProSupportsLow(t *testing.T) {
	c := newTestClient(t, "deepseek-v4-pro", map[string]any{"reasoning_protocol": "deepseek"})
	if got := c.buildRequest(provider.Request{EffortOverride: "low"}).ReasoningEffort; got != "low" {
		t.Fatalf("Pro low reasoning_effort = %q, want low", got)
	}
}

func TestEffortOverrideHonorsSupportedEfforts(t *testing.T) {
	c := newTestClient(t, "deepseek-v4", map[string]any{
		"reasoning_protocol": "deepseek",
		"effort":             "high",
		"supported_efforts":  []string{"low", "high", "disabled"},
	})
	if got := c.buildRequest(provider.Request{EffortOverride: "low"}).ReasoningEffort; got != "low" {
		t.Fatalf("declared vocabulary must admit low, got %q", got)
	}
	assertRejectedEffort(t, c, "max")
	request := c.buildRequest(provider.Request{EffortOverride: "disabled"})
	if request.ReasoningEffort != "" || request.Thinking == nil || request.Thinking.Type != "disabled" {
		t.Fatalf("disabled override = thinking:%#v effort:%q, want thinking disabled and no effort", request.Thinking, request.ReasoningEffort)
	}
}

func assertRejectedEffort(t *testing.T, c *client, id string) {
	t.Helper()
	_, err := c.Stream(context.Background(), provider.Request{EffortOverride: id})
	var unsupported *provider.UnsupportedReasoningEffort
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected rejection before I/O for %q, got %v", id, err)
	}
}
func TestEffortOverrideRejectedByBinaryThinkingKnobs(t *testing.T) {
	// Zhipu GLM left this loop in task 601: its wire mapping carries
	// low..max through reasoning_effort on top of thinking.type, so strengths
	// are admitted (TestEffortOverrideGLMStrengthsReachWire) — only MiniMax
	// and LongCat keep the hard strength rejection.
	for _, url := range []string{"https://api.minimaxi.com/v1", "https://api.longcat.chat/v1"} {
		p, err := New(provider.Config{Name: "test", BaseURL: url, Model: "model"})
		if err != nil {
			t.Fatal(err)
		}
		c := p.(*client)
		assertRejectedEffort(t, c, "low")
		out := c.buildRequest(provider.Request{EffortOverride: "disabled"})
		if out.Thinking == nil || out.Thinking.Type != "disabled" || out.ReasoningEffort != "" {
			t.Fatalf("disabled wire: %+v", out)
		}
	}
}
func TestEffortOverrideRejectedWithoutDepthVocabulary(t *testing.T) {
	c := &client{model: "unknown"}
	assertRejectedEffort(t, c, "low")
	disabled := newTestClient(t, "deepseek-v4", map[string]any{"reasoning_protocol": "deepseek", "thinking": "disabled"})
	assertRejectedEffort(t, disabled, "high")
}

// Task 354: MiMo's per-request probe must expose exactly the canonical
// four-level set that NormalizeEffort emits for a MiMo entry
// (normalizeMimoEffort folds minimal→low, xhigh/max/ultra→high,
// none/disabled/off→none). Before this, the probe read configured
// supported_efforts — empty for a stock MiMo entry — so every medium/high
// switch was declined as level-not-in-vocabulary and paid a full rebuild
// (1855 installed log: 25-26s per switch).
func TestEffortOverrideMiMoVocabulary(t *testing.T) {
	p, err := New(provider.Config{
		Name:    "mimo",
		BaseURL: "https://api.xiaomimimo.com/v1",
		Model:   "mimo-v2.6-flash",
		APIKey:  "k",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c := p.(*client)
	got := c.PerRequestEfforts()
	want := []string{"none", "low", "medium", "high"}
	if !slices.Equal(got, want) {
		t.Fatalf("PerRequestEfforts = %v, want %v", got, want)
	}
}

// Task 601: a stock Zhipu GLM entry declares no supported_efforts, so the
// per-request probe used to come up empty and every /effort switch declined
// as level-not-in-vocabulary and paid a full runtime rebuild (observed 5.3s
// per switch on the GLM coding plan, desktop.log 23:02:07). The probe must
// expose exactly the set internal/config's normalizeGLMEffort can emit, and
// an explicit supported_efforts list stays the probe face — NormalizeEffort
// emits from it verbatim.
func TestEffortOverrideGLMVocabulary(t *testing.T) {
	p, err := New(provider.Config{
		Name:    "glm",
		BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4",
		Model:   "glm-5.3-flash",
		APIKey:  "k",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := p.(*client).PerRequestEfforts()
	want := []string{"disabled", "low", "medium", "high", "max"}
	if !slices.Equal(got, want) {
		t.Fatalf("PerRequestEfforts = %v, want %v", got, want)
	}

	declared, err := New(provider.Config{
		Name:    "glm",
		BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4",
		Model:   "glm-5.3-flash",
		APIKey:  "k",
		Extra:   map[string]any{"supported_efforts": []string{"low", "high"}},
	})
	if err != nil {
		t.Fatalf("New declared: %v", err)
	}
	if got := declared.(*client).PerRequestEfforts(); !slices.Equal(got, []string{"low", "high"}) {
		t.Fatalf("declared PerRequestEfforts = %v, want [low high]", got)
	}
}

// Task 601: the resolved GLM capability is clipped to enabled|disabled, but
// the wire mapping carries low..max through reasoning_effort on top of
// thinking.type. Stream must admit the strengths — the same fork exception
// configuredEffort applies at boot — so a per-request depth switch reaches
// the wire instead of failing the turn after the session override was armed.
func TestEffortOverrideGLMStrengthsReachWire(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	p, err := New(provider.Config{
		Name:    "glm",
		BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4",
		Model:   "glm-5.3-flash",
		APIKey:  "k",
		Extra:   map[string]any{"request_url": srv.URL},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c := p.(*client)

	cases := []struct {
		override   string
		wantType   string
		wantEffort string // "" = the key must be absent
	}{
		{override: "max", wantType: "enabled", wantEffort: "max"},
		{override: "high", wantType: "enabled", wantEffort: "high"},
		{override: "medium", wantType: "enabled", wantEffort: "medium"},
		{override: "low", wantType: "enabled", wantEffort: "low"},
		{override: "disabled", wantType: "disabled", wantEffort: ""},
	}
	for _, tc := range cases {
		ch, err := c.Stream(context.Background(), provider.Request{
			Messages:       []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
			EffortOverride: tc.override,
		})
		if err != nil {
			var unsupported *provider.UnsupportedReasoningEffort
			if errors.As(err, &unsupported) {
				t.Fatalf("override %q rejected before I/O: %v", tc.override, err)
			}
			t.Fatalf("Stream override %q: %v", tc.override, err)
		}
		for range ch {
		}
		shape := c.buildRequest(provider.Request{EffortOverride: tc.override})
		if shape.Thinking == nil || shape.Thinking.Type != tc.wantType {
			t.Fatalf("override %q thinking = %#v, want type %q", tc.override, shape.Thinking, tc.wantType)
		}
		if shape.ReasoningEffort != tc.wantEffort {
			t.Fatalf("override %q reasoning_effort = %q, want %q", tc.override, shape.ReasoningEffort, tc.wantEffort)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != len(cases) {
		t.Fatalf("server saw %d bodies, want %d", len(bodies), len(cases))
	}
	wireEffort, _ := bodies[0]["reasoning_effort"].(string)
	if wireEffort != "max" {
		t.Fatalf("wire body reasoning_effort = %v, want max", bodies[0]["reasoning_effort"])
	}
	wireThinking, _ := bodies[0]["thinking"].(map[string]any)
	if wireThinking == nil || wireThinking["type"] != "enabled" {
		t.Fatalf("wire body thinking = %v, want enabled", bodies[0]["thinking"])
	}
}

// A declared supported_efforts list keeps the probe face, and its strengths
// still reach the wire: the resolved capability is clipped to the binary knob,
// so without the fork bypass a declared low/high would fail the turn.
func TestEffortOverrideGLMDeclaredStrengthsReachWire(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	p, err := New(provider.Config{
		Name:    "glm",
		BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4",
		Model:   "glm-5.3-flash",
		APIKey:  "k",
		Extra: map[string]any{
			"request_url":       srv.URL,
			"supported_efforts": []string{"low", "high"},
		},
	})
	if err != nil {
		t.Fatalf("New declared low: %v", err)
	}
	ch, err := p.(*client).Stream(context.Background(), provider.Request{
		Messages:       []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
		EffortOverride: "low",
	})
	if err != nil {
		t.Fatalf("Stream declared low: %v", err)
	}
	for range ch {
	}
}

// Task 606: "none" is a legal MiMo wire value — normalizeMimoEffort folds
// none/disabled/off onto none and reasoning_effort passes through verbatim —
// but the resolved capability omitted it, so the probe armed a none override
// and the very next request died on UNSUPPORTED_REASONING_EFFORT; a persisted
// effort=none also failed New outright at configuredEffort. The capability now
// lists none, so both gates admit it and the override reaches the wire
// verbatim; low/medium/high keep flowing unchanged.
func TestEffortOverrideMiMoNoneReachesWire(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	base := provider.Config{
		Name:    "mimo",
		BaseURL: "https://api.xiaomimimo.com/v1",
		Model:   "mimo-v2.5-pro",
		APIKey:  "k",
		Extra:   map[string]any{"request_url": srv.URL},
	}
	p, err := New(base)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c := p.(*client)

	// The two gates that used to reject none (Stream's reasoning.Validate on
	// the resolved capability, and configuredEffort at New construction) must
	// both admit it now.
	if err := c.reasoning.Validate(c.model, "none"); err != nil {
		t.Fatalf("capability rejects none: %v", err)
	}
	bootCfg := base
	bootCfg.Extra = map[string]any{"effort": "none"}
	if _, err := New(bootCfg); err != nil {
		t.Fatalf("New with persisted effort=none: %v", err)
	}

	for _, override := range []string{"none", "low", "medium", "high"} {
		ch, err := c.Stream(context.Background(), provider.Request{
			Messages:       []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
			EffortOverride: override,
		})
		if err != nil {
			var unsupported *provider.UnsupportedReasoningEffort
			if errors.As(err, &unsupported) {
				t.Fatalf("override %q rejected before I/O: %v", override, err)
			}
			t.Fatalf("Stream override %q: %v", override, err)
		}
		for range ch {
		}
		if got := c.buildRequest(provider.Request{EffortOverride: override}).ReasoningEffort; got != override {
			t.Fatalf("override %q reasoning_effort = %q, want verbatim", override, got)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 4 {
		t.Fatalf("server saw %d bodies, want 4", len(bodies))
	}
	if got := bodies[0]["reasoning_effort"]; got != "none" {
		t.Fatalf("wire body reasoning_effort = %v, want none", got)
	}
}

// Task 606 alignment: an explicit supported_efforts list keeps the probe face
// for MiMo too (mirroring the GLM branch), and DeclaredReasoning replaces the
// capability with that same list — a declaration that excludes none stays
// authoritative at both gates, instead of the probe admitting a level the
// next request rejects.
func TestEffortOverrideMiMoDeclaredEffortsRuleBothGates(t *testing.T) {
	p, err := New(provider.Config{
		Name:    "mimo",
		BaseURL: "https://api.xiaomimimo.com/v1",
		Model:   "mimo-v2.5-pro",
		APIKey:  "k",
		Extra:   map[string]any{"supported_efforts": []string{"low", "medium", "high"}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c := p.(*client)
	if got := c.PerRequestEfforts(); !slices.Equal(got, []string{"low", "medium", "high"}) {
		t.Fatalf("declared PerRequestEfforts = %v, want [low medium high]", got)
	}
	assertRejectedEffort(t, c, "none")
	if err := c.reasoning.Validate(c.model, "none"); err == nil {
		t.Fatal("capability must still reject none under an explicit declaration excluding it")
	}
}
