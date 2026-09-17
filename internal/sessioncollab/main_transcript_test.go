package sessioncollab

import "testing"

// IsMainTranscript is the single filter between "a real conversation" and "a
// sidecar that shares the stem". Incident 2026-09-17: without it the directory
// counted 580 rows for ~30 real sessions and produced phantom duplicate
// contact_id warnings.
func TestIsMainTranscript(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"20260907-041615.000-deepseek.jsonl", true},
		{"20260907-041615.000-deepseek.turns.jsonl", false},
		{"20260907-041615.000-deepseek.events.jsonl", false},
		{"20260907-041615.000-deepseek.conflicts.jsonl", false},
		{"20260907-041615.000-deepseek.jsonl.meta", false},
		{"20260907-041615.000-deepseek.ckpt", false},
		{"20260907-041615.000-deepseek.inbox.jsonl", false},
		// Audit F154-4: the first cut of this predicate matched ".guardian" but
		// the real sidecar is "<stem>.guardian.jsonl", so it leaked into the
		// directory as a phantom session. Now delegated to store.
		{"20260907-041615.000-deepseek.guardian.jsonl", false},
		{"x.guardian.jsonl", false},
		{"sc_abc.inbox.jsonl", false},
		{"session.jsonl", true},
		{"notes.txt", false},
		{".gitkeep", false},
	}
	for _, c := range cases {
		if got := IsMainTranscript(c.name); got != c.want {
			t.Errorf("IsMainTranscript(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}
