package boot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/provider"
)

// Task 880: each official OpenCode Go route must carry the configured effort
// on the wire. These tests resolve the real preset entry, build the provider
// through NewProvider, and assert the serialized request body — the chat route
// uses reasoning_effort, the Anthropic route thinking.type + output_config
// .effort, and the Responses route reasoning.effort.

func newOpenCodeGoPresetEntry(t *testing.T, presetID, ref string) *config.ProviderEntry {
	t.Helper()
	preset, ok := config.CuratedProviderPreset(presetID)
	if !ok {
		t.Fatalf("preset %q missing", presetID)
	}
	var cfg config.Config
	for _, entry := range preset.Entries {
		if err := cfg.UpsertProvider(entry); err != nil {
			t.Fatalf("upsert preset %q entry %q: %v", presetID, entry.Name, err)
		}
	}
	entry, ok := cfg.ResolveModel(ref)
	if !ok {
		t.Fatalf("ResolveModel(%q) failed", ref)
	}
	return entry
}

func TestOpenCodeGoChatRouteCarriesEffortOnWire(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		effort    string // "" keeps the preset default
		wantWire  string
		wantThink bool   // deepseek protocol also pins thinking.type
		wantOff   string // thinking.type value when pinned
	}{
		{name: "default effort", model: "glm-5.3", wantWire: "low"},
		{name: "per-request override", model: "glm-5.3", effort: "high", wantWire: "high"},
		{name: "mimo catalog model", model: "mimo-v2.5", effort: "medium", wantWire: "medium"},
		{name: "deepseek protocol", model: "deepseek-v4.1-flash", effort: "low", wantWire: "low", wantThink: true, wantOff: "enabled"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotReq map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
					t.Errorf("decode request: %v", err)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
			}))
			defer srv.Close()

			entry := newOpenCodeGoPresetEntry(t, "opencode-go-recommended", "opencode-go/"+tc.model)
			if tc.effort != "" {
				normalized, err := config.NormalizeEffort(entry, tc.effort)
				if err != nil {
					t.Fatalf("NormalizeEffort(%q) = %v", tc.effort, err)
				}
				entry.Effort = normalized
			}
			entry.BaseURL = srv.URL

			p, err := NewProvider(entry)
			if err != nil {
				t.Fatalf("NewProvider(%s): %v", tc.model, err)
			}
			ch, err := p.Stream(context.Background(), provider.Request{
				Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
			})
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			for chunk := range ch {
				if chunk.Type == provider.ChunkError {
					t.Fatalf("stream error: %v", chunk.Err)
				}
			}
			if got := gotReq["reasoning_effort"]; got != tc.wantWire {
				t.Errorf("reasoning_effort = %#v, want %q", got, tc.wantWire)
			}
			if tc.wantThink {
				thinking, _ := gotReq["thinking"].(map[string]any)
				if got, _ := thinking["type"].(string); got != tc.wantOff {
					t.Errorf("thinking.type = %#v, want %q", thinking["type"], tc.wantOff)
				}
			}
		})
	}
}

func TestOpenCodeGoAnthropicRouteCarriesEffortOnWire(t *testing.T) {
	var gotReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"event: message_start\n" +
				"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n" +
				"event: content_block_delta\n" +
				"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n" +
				"event: message_delta\n" +
				"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
				"event: message_stop\n" +
				"data: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer srv.Close()

	entry := newOpenCodeGoPresetEntry(t, "opencode-go-recommended", "opencode-go-anthropic/qwen3.7-plus")
	normalized, err := config.NormalizeEffort(entry, "high")
	if err != nil {
		t.Fatalf("NormalizeEffort(high) on anthropic route = %v", err)
	}
	entry.Effort = normalized
	entry.BaseURL = srv.URL

	p, err := NewProvider(entry)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	ch, err := p.Stream(context.Background(), provider.Request{
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for chunk := range ch {
		if chunk.Type == provider.ChunkError {
			t.Fatalf("stream error: %v", chunk.Err)
		}
	}
	thinking, _ := gotReq["thinking"].(map[string]any)
	if got, _ := thinking["type"].(string); got != "adaptive" {
		t.Errorf("thinking.type = %#v, want adaptive", thinking["type"])
	}
	outputConfig, _ := gotReq["output_config"].(map[string]any)
	if got, _ := outputConfig["effort"].(string); got != "high" {
		t.Errorf("output_config.effort = %#v, want high", outputConfig["effort"])
	}
}

func TestOpenCodeGoResponsesRouteCarriesEffortOnWire(t *testing.T) {
	var gotReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"))
	}))
	defer srv.Close()

	// The recommended bundle defaults this route to "low"; an explicit
	// /effort selection must ride through unchanged.
	for _, tc := range []struct {
		name   string
		effort string
		want   string
	}{
		{name: "preset default", want: "low"},
		{name: "explicit effort", effort: "medium", want: "medium"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotReq = nil
			entry := newOpenCodeGoPresetEntry(t, "opencode-go-recommended", "opencode-go-responses/grok-4.5")
			if tc.effort != "" {
				normalized, err := config.NormalizeEffort(entry, tc.effort)
				if err != nil {
					t.Fatalf("NormalizeEffort(%q) = %v", tc.effort, err)
				}
				entry.Effort = normalized
			}
			entry.BaseURL = srv.URL

			p, err := NewProvider(entry)
			if err != nil {
				t.Fatalf("NewProvider: %v", err)
			}
			ch, err := p.Stream(context.Background(), provider.Request{
				Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
			})
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			for chunk := range ch {
				if chunk.Type == provider.ChunkError {
					t.Fatalf("stream error: %v", chunk.Err)
				}
			}
			reasoning, _ := gotReq["reasoning"].(map[string]any)
			if got, _ := reasoning["effort"].(string); got != tc.want {
				t.Errorf("reasoning.effort = %#v, want %q", reasoning["effort"], tc.want)
			}
		})
	}
}
