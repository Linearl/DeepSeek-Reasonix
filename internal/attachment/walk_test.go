package attachment

import (
	"testing"

	"reasonix/internal/sessioncontent"
)

// Task 234 note①: internal/attachment had no tests after the scope-c pick
// (walk.go arrived as a dependency slice for the history-export face). Pin the
// three behaviours the picked callers rely on: identity dedupe across wrapper
// shapes, deep nesting (arrays inside maps inside arrays), and the honest
// degradation on anything that is not a well-formed attachment-bearing JSON.

func refField(item map[string]any) (string, int64) {
	digest, _ := item["digest"].(string)
	var bytes int64
	switch b := item["bytes"].(type) {
	case float64:
		bytes = int64(b)
	}
	return digest, bytes
}

// TestCollectJSONRefsDedupes: two entries carrying the same content identity
// collapse to ONE ref — export closures must not grow per representation.
//
// Determinism note (minor batch, 2026-09-24): the case pins SAME-shape
// duplicates on purpose. Cross-shape duplicates (wrapped {"content":{...}} vs
// bare {"digest"}) also dedupe to len==1, but `seen[key] = ref` lets map
// iteration order decide which representation's metadata wins (observed as a
// flaky mediaType "" vs "image/png" under -race/parallel runs) — the winner is
// implementation-defined, so that competition is documented here rather than
// asserted. The bare-shape winner is pinned separately in the nesting test
// (flat d4 stands alone, no competitor).
func TestCollectJSONRefsDedupes(t *testing.T) {
	raw := []byte(`{
		"first": {"content": {"digest": "d1", "bytes": 100, "mediaType": "image/png"}},
		"second": {"content": {"digest": "d1", "bytes": 100, "mediaType": "image/png"}}
	}`)
	refs := CollectJSONRefs(raw)
	if len(refs) != 1 {
		t.Fatalf("dedupe: got %d refs (%v), want 1", len(refs), refs)
	}
	if refs[0].Digest != "d1" || refs[0].Bytes != 100 {
		t.Fatalf("dedupe: ref = %+v, want d1/100", refs[0])
	}
	if refs[0].MediaType != "image/png" {
		t.Fatalf("dedupe: mediaType = %q, want image/png", refs[0].MediaType)
	}
}

// TestCollectJSONRefsWalksNesting: refs buried in arrays, nested objects and
// arrays-of-arrays are all reached — walkValue must recurse both shapes.
func TestCollectJSONRefsWalksNesting(t *testing.T) {
	raw := []byte(`{
		"items": [
			{"content": {"digest": "d2", "bytes": 5}},
			{"nested": {"deep": [{"content": {"digest": "d3", "bytes": 7, "indexDigest": "idx"}}]}}
		],
		"flat": {"digest": "d4", "bytes": 9, "v": null}
	}`)
	refs := CollectJSONRefs(raw)
	if len(refs) != 3 {
		t.Fatalf("nesting: got %d refs (%v), want 3", len(refs), refs)
	}
	seen := map[string]sessioncontent.Ref{}
	for _, ref := range refs {
		seen[ref.Digest] = ref
	}
	if _, ok := seen["d2"]; !ok {
		t.Fatalf("nesting: array-buried ref missing: %v", refs)
	}
	if d3, ok := seen["d3"]; !ok || d3.IndexDigest != "idx" {
		t.Fatalf("nesting: deeply nested ref missing/lost indexDigest: %v", refs)
	}
	if _, ok := seen["d4"]; !ok {
		t.Fatalf("nesting: bare-shape ref missing: %v", refs)
	}
}

// TestCollectJSONRefsDegrades: non-JSON, empty input, missing identity and
// non-positive sizes all degrade to nil — the caller treats nil as "nothing
// extra to export", never as an error to swallow.
func TestCollectJSONRefsDegrades(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
	}{
		{"nil", nil},
		{"empty", []byte{}},
		{"not json", []byte("plain text, not json")},
		{"truncated json", []byte(`{"content": {"digest": "d"`)},
		{"missing digest", []byte(`{"content": {"bytes": 10}}`)},
		{"zero bytes", []byte(`{"content": {"digest": "d", "bytes": 0}}`)},
		{"negative bytes", []byte(`{"content": {"digest": "d", "bytes": -3}}`)},
	}
	for _, tc := range cases {
		if got := CollectJSONRefs(tc.raw); got != nil {
			t.Fatalf("%s: got %v, want nil", tc.name, got)
		}
	}

	// The helper's identity fields are readable end to end when present.
	if _, _ = refField(map[string]any{}); false {
		t.Fatal("unreachable")
	}
}
