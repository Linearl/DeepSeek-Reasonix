package event

import "testing"

// TestNoticeSuggestionWireFieldStaysPassThrough pins tl1079's Suggestion field
// contract: the Go side never fabricates suggestion text (the copy lives in
// the frontend locale dictionaries keyed by code — see the 1079-b note in
// notice_codes.go). EmitEvent therefore forwards the event exactly as the
// emitter set it — with a nil-sink guard, and nothing else.
func TestNoticeSuggestionWireFieldStaysPassThrough(t *testing.T) {
	var got []Event
	sink := FuncSink(func(e Event) { got = append(got, e) })

	// A hand-set Suggestion survives the unified Emit path untouched.
	EmitEvent(sink, Event{Kind: Notice, Code: NoticeCodeFinalReadiness, Suggestion: "handwritten"})
	if len(got) != 1 || got[0].Suggestion != "handwritten" {
		t.Fatalf("EmitEvent must forward a hand-set Suggestion verbatim, got %+v", got)
	}

	// An empty Suggestion stays empty — no Go-side back-fill (1079-b: the
	// frontend resolves notice.suggestion.<code> from the locale dictionary).
	EmitEvent(sink, Event{Kind: Notice, Code: NoticeCodeFinalReadiness})
	if len(got) != 2 || got[1].Suggestion != "" {
		t.Fatalf("EmitEvent must not fabricate Suggestion text, got %+v", got)
	}

	// Nil sink is a no-op, not a panic (the guard this wrapper exists for).
	EmitEvent(nil, Event{Kind: Notice, Code: NoticeCodeFinalReadiness})
}

// TestNoticeSuggestionCodesExist pins tl1079's new stable codes to their wire
// values: frontend locale lookups (notice.suggestion.*) and the 1080 light-tier
// notice both key off these exact strings.
func TestNoticeSuggestionCodesExist(t *testing.T) {
	if NoticeCodeSurfaceTimeout != "surface_timeout" {
		t.Errorf("NoticeCodeSurfaceTimeout = %q, want surface_timeout", NoticeCodeSurfaceTimeout)
	}
	if NoticeCodeHeaderWaitLight != "header_wait_light" {
		t.Errorf("NoticeCodeHeaderWaitLight = %q, want header_wait_light", NoticeCodeHeaderWaitLight)
	}
}
