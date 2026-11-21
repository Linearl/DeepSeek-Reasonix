package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/tool"
)

// tl1728 tests: conditional-offer tools must stay discoverable in
// use_capability action=list with a tri-lingual offer-condition note, while
// every unconditional provider-visible core tool keeps its tl1719-era
// filtering (zero regression). The offer mechanism itself (ContextualTool
// judgment, execute gate) is a non-goal and is never invoked here — the stubs
// below pin that list enumeration is decoupled from the per-turn ctx verdict.

// tl1728Stub is a minimal tool whose contextual-ness is decided by the
// `contextual` flag. ProviderVisible's return value is irrelevant to list
// enumeration by design: the list side only type-asserts the interface.
type tl1728Stub struct {
	name       string
	contextual bool
}

func (s tl1728Stub) Name() string { return s.name }

func (s tl1728Stub) Description() string { return "tl1728 stub " + s.name }

func (s tl1728Stub) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (s tl1728Stub) ReadOnly() bool { return true }

func (s tl1728Stub) Execute(context.Context, json.RawMessage) (string, error) { return "", nil }

func (s tl1728Stub) ProviderVisible(context.Context) bool { return s.contextual }

// tl1728ListPayload decodes the legacy full tier of action=list.
type tl1728ListPayload struct {
	Capabilities []struct {
		ID    string `json:"id"`
		Kind  string `json:"kind"`
		Name  string `json:"name"`
		Offer string `json:"offer"`
	} `json:"capabilities"`
}

func tl1728RunList(t *testing.T, reg *tool.Registry) tl1728ListPayload {
	t.Helper()
	frontend := newTL1719ListFrontend(t, reg)
	out, err := frontend.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload tl1728ListPayload
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode list result: %v\n%s", err, out)
	}
	return payload
}

// tl1728OfferOf returns the offer note and presence of one row id.
func tl1728OfferOf(payload tl1728ListPayload, id string) (string, bool) {
	for _, c := range payload.Capabilities {
		if c.ID == id {
			return c.Offer, true
		}
	}
	return "", false
}

// TestTl1728ConditionalOfferToolsEnumeratedWithOfferNote pins V1/V2/V3: the
// six allowlisted ContextualTool family members enter the enumeration (each
// +1 vs the tl1719 baseline) with a tri-lingual offer note, while the
// unconditional core tools (bash) stay filtered — zero regression.
func TestTl1728ConditionalOfferToolsEnumeratedWithOfferNote(t *testing.T) {
	offerNames := []string{"bash_output", "kill_shell", "wait", "complete_step", "create_goal", "update_goal"}
	reg := tool.NewRegistry()
	for _, name := range offerNames {
		reg.Add(tl1728Stub{name: name, contextual: true})
	}
	// bash/read_file must NOT be contextual: tl1719CompressStub implements no
	// ProviderVisible method (the contextual flag alone would not unqualify
	// tl1728Stub — interface satisfaction is method-set level, and tl1728Stub
	// always carries ProviderVisible).
	reg.Add(tl1719CompressStub{name: "bash"})
	reg.Add(tl1719CompressStub{name: "read_file"})
	allow := append(append([]string{}, offerNames...), "bash", "read_file")
	reg.SetProviderVisibleTools(allow)

	payload := tl1728RunList(t, reg)
	if got := len(payload.Capabilities); got != len(offerNames) {
		t.Fatalf("enumeration count=%d, want %d (six conditional-offer rows, bash/read_file still filtered):\n%s",
			got, len(offerNames), payload.Capabilities)
	}
	for _, name := range offerNames {
		offer, ok := tl1728OfferOf(payload, "tool:"+name)
		if !ok {
			t.Fatalf("conditional-offer tool %q missing from list (discoverability gap):\n%s", name, payload.Capabilities)
		}
		if offer == "" {
			t.Fatalf("row tool:%s carries no offer-condition note", name)
		}
		// Tri-lingual hard pin: en segment marker + 简体 + 繁體 keywords.
		if !strings.Contains(offer, "Offered only") {
			t.Fatalf("offer note for %s missing the en segment: %s", name, offer)
		}
		if !strings.Contains(offer, "仅") || !strings.Contains(offer, "僅") {
			t.Fatalf("offer note for %s must carry both zh and zh-TW segments: %s", name, offer)
		}
	}
	for _, hidden := range []string{"tool:bash", "tool:read_file"} {
		if _, ok := tl1728OfferOf(payload, hidden); ok {
			t.Fatalf("unconditional provider-visible tool %s must stay filtered (tl1719-era behavior):\n%s", hidden, payload.Capabilities)
		}
	}
}

// TestTl1728ExtrasSwitchToolsEnumerated pins V4: the providerSurfaceExtras
// family (multi_edit / restart_update / session_hub) is conditional via its
// experiment switch, not ContextualTool — the notes map is the authority and
// the row must carry the switch-conditional tri-lingual note.
func TestTl1728ExtrasSwitchToolsEnumerated(t *testing.T) {
	for _, name := range []string{"multi_edit", "restart_update", "session_hub"} {
		reg := tool.NewRegistry()
		reg.Add(tl1728Stub{name: name}) // no ContextualTool — switch-conditional only
		reg.SetProviderVisibleTools([]string{name})
		payload := tl1728RunList(t, reg)
		offer, ok := tl1728OfferOf(payload, "tool:"+name)
		if !ok {
			t.Fatalf("switch-conditional tool %q missing from list:\n%s", name, payload.Capabilities)
		}
		if !strings.Contains(offer, "experimental_") {
			t.Fatalf("offer note for %s must name its experiment switch: %s", name, offer)
		}
		if !strings.Contains(offer, "实验开关") || !strings.Contains(offer, "實驗開關") {
			t.Fatalf("offer note for %s must carry both zh and zh-TW segments: %s", name, offer)
		}
	}
}

// TestTl1728FallbackNoteForUnannotatedContextualTool pins V5: a ContextualTool
// without a dedicated note stays listed with the generic tri-lingual fallback
// — a newly added contextual tool can never silently regress to
// invisible-with-no-explanation.
func TestTl1728FallbackNoteForUnannotatedContextualTool(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(tl1728Stub{name: "custom_contextual", contextual: true})
	reg.SetProviderVisibleTools([]string{"custom_contextual"})
	payload := tl1728RunList(t, reg)
	offer, ok := tl1728OfferOf(payload, "tool:custom_contextual")
	if !ok {
		t.Fatalf("unannotated ContextualTool must stay listed via the fallback note:\n%s", payload.Capabilities)
	}
	if offer != conditionalOfferFallbackNote {
		t.Fatalf("expected the generic fallback note, got: %s", offer)
	}
}

// TestTl1728OfferNoteOnNonAllowlistedContextual pins V7: submit_plan is
// contextual but outside the static allowlist (it was already listed before
// tl1728) — its row now also carries the offer note.
func TestTl1728OfferNoteOnNonAllowlistedContextual(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(tl1728Stub{name: "submit_plan", contextual: true})
	// Not in the allowlist: the tool rides the catalog only.
	payload := tl1728RunList(t, reg)
	offer, ok := tl1728OfferOf(payload, "tool:submit_plan")
	if !ok {
		t.Fatalf("non-allowlisted contextual tool must stay listed:\n%s", payload.Capabilities)
	}
	if !strings.Contains(offer, "planning turn") || !strings.Contains(offer, "planning 回合") {
		t.Fatalf("offer note for submit_plan must state the planning-round condition: %s", offer)
	}
}

// TestTl1728TriLingualNoteContract pins V8 at the source: every dedicated
// note is one string with exactly two full-width separators (en｜简体｜繁體) and
// keeps the model-side "offer" vocabulary; the fallback obeys the same shape.
func TestTl1728TriLingualNoteContract(t *testing.T) {
	notes := map[string]string{
		"bash_output":            offerNoteBackgroundJobs,
		"kill_shell":             offerNoteBackgroundJobs,
		"wait":                   offerNoteBackgroundJobs,
		"complete_step":          offerNoteCompleteStep,
		"create_goal":            offerNoteCreateGoal,
		"update_goal":            offerNoteUpdateGoal,
		"submit_plan":            offerNoteSubmitPlan,
		"extend_research_budget": offerNoteResearchBudget,
		"multi_edit":             offerNoteMultiEdit,
		"restart_update":         offerNoteRestartUpdate,
		"session_hub":            offerNoteSessionHub,
	}
	for name, note := range notes {
		if got := strings.Count(note, "｜"); got != 2 {
			t.Fatalf("note for %s must have exactly two tri-lingual separators, got %d: %s", name, got, note)
		}
		if !strings.Contains(note, "offer") {
			t.Fatalf("note for %s must keep the model-side vocabulary \"offer\": %s", name, note)
		}
	}
	if got := strings.Count(conditionalOfferFallbackNote, "｜"); got != 2 {
		t.Fatalf("fallback note must have exactly two separators, got %d: %s", got, conditionalOfferFallbackNote)
	}
	// Distinct conditions must have distinct copy (shared family copy is fine,
	// but a copy-pasted generic string across different conditions is not).
	if offerNoteCompleteStep == offerNoteCreateGoal || offerNoteSubmitPlan == offerNoteResearchBudget {
		t.Fatalf("distinct offer conditions must not share identical copy")
	}
}

// TestTl1728EveryContextualToolHasOfferNote is the standing guardrail (修完补
// 防线): every registered builtin implementing ContextualTool must carry a
// dedicated note in conditionalOfferNotes. A new contextual tool without one
// turns this red instead of silently degrading to the generic fallback.
func TestTl1728EveryContextualToolHasOfferNote(t *testing.T) {
	for _, bt := range tool.Builtins() {
		if _, conditional := bt.(tool.ContextualTool); !conditional {
			continue
		}
		name := bt.Name()
		if _, hit := conditionalOfferNotes[name]; !hit {
			t.Fatalf("builtin ContextualTool %q lacks a conditionalOfferNotes entry; add a precise tri-lingual offer note", name)
		}
	}
	// The agent-package contextual tools are not builtins; name them here so
	// the note contract stays checked even without a registry enumeration.
	for _, name := range []string{"submit_plan"} {
		if _, hit := conditionalOfferNotes[name]; !hit {
			t.Fatalf("agent ContextualTool %q lacks a conditionalOfferNotes entry", name)
		}
	}
}
