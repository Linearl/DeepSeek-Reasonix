package main

import (
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// Task 442 — the composer gauge popup reads two fields off ContextPanel: the
// composition segments (the host hides the segmented bar when they are absent)
// and the provider half of the tab's model ref (the opencode-go quota card
// shows only for that provider). Both must survive the panel mapping, the
// segments must sum to their own total, and a tab whose model ref carries no
// provider half must degrade to an empty provider (cards stay hidden) rather
// than to a bogus provider name.
func TestContextPanelCarriesCompositionAndProviderName(t *testing.T) {
	ag := agent.New(
		usageProvider{usage: nil},
		tool.NewRegistry(),
		agent.NewSession("system prompt body long enough to measure as a segment"),
		agent.Options{ContextWindow: 200},
		event.Discard,
	)
	tab := &WorkspaceTab{
		ID:    "tab",
		Ctrl:  control.New(control.Options{Executor: ag, Sink: event.Discard}),
		Scope: "global",
		Ready: true,
		model: "opencode-go/glm-5.3",
	}
	app := &App{tabs: map[string]*WorkspaceTab{"tab": tab}}

	panel := app.ContextPanel("tab")
	if panel.Composition == nil {
		t.Fatalf("composition = nil, want segments while the executor has a live view")
	}
	c := *panel.Composition
	if c.TotalTokens <= 0 {
		t.Fatalf("composition total = %d, want > 0", c.TotalTokens)
	}
	if want := c.SystemPromptTokens + c.BuiltinToolTokens + c.SkillTokens + c.McpToolTokens + c.MessageTokens; c.TotalTokens != want {
		t.Fatalf("composition total = %d, want the segment sum %d: %+v", c.TotalTokens, want, c)
	}
	if c.SystemPromptTokens <= 0 {
		t.Fatalf("system prompt segment = %d, want the session prompt measured: %+v", c.SystemPromptTokens, c)
	}
	if panel.ProviderName != "opencode-go" {
		t.Fatalf("providerName = %q, want the provider half of %q", panel.ProviderName, tab.model)
	}

	// Conversation content lands in the message segment and the total keeps
	// summing — the bar re-measures on every panel read, it is not a snapshot
	// taken once at session start.
	ag.Session().Add(provider.Message{Role: provider.RoleUser, Content: strings.Repeat("u", 400)})
	rebuilt := app.ContextPanel("tab")
	if rebuilt.Composition == nil {
		t.Fatalf("composition = nil after adding a user message")
	}
	c2 := *rebuilt.Composition
	if c2.MessageTokens <= 0 {
		t.Fatalf("message segment = %d, want the user message measured: %+v", c2.MessageTokens, c2)
	}
	if want := c2.SystemPromptTokens + c2.BuiltinToolTokens + c2.SkillTokens + c2.McpToolTokens + c2.MessageTokens; c2.TotalTokens != want {
		t.Fatalf("composition total = %d, want the segment sum %d: %+v", c2.TotalTokens, want, c2)
	}
	if c2.TotalTokens <= c.TotalTokens {
		t.Fatalf("total did not grow with the added message: %d -> %d", c.TotalTokens, c2.TotalTokens)
	}

	// A different provider keeps the cards off (conditional display, no lab
	// switch), and a model ref without a provider half degrades to "".
	tab.model = "zhipu/glm-5"
	if got := app.ContextPanel("tab").ProviderName; got != "zhipu" {
		t.Fatalf("providerName = %q, want %q", got, "zhipu")
	}
	tab.model = "bare-model"
	if got := app.ContextPanel("tab").ProviderName; got != "" {
		t.Fatalf("providerName = %q, want empty for a model ref without a provider half", got)
	}
	tab.model = "  opencode-go-anthropic/qwen3.7-max  "
	if got := app.ContextPanel("tab").ProviderName; got != "opencode-go-anthropic" {
		t.Fatalf("providerName = %q, want the trimmed provider half", got)
	}
}
