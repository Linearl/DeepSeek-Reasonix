package agent

import (
	"testing"

	"reasonix/internal/provider"
)

func overlapMessages(contents ...string) []provider.Message {
	out := make([]provider.Message, 0, len(contents))
	for i, content := range contents {
		role := provider.RoleUser
		if i%2 == 1 {
			role = provider.RoleAssistant
		}
		out = append(out, provider.Message{Role: role, Content: content})
	}
	return out
}

func snapshotOf(contents ...string) SessionContentSnapshot {
	return SessionContentSnapshot{messages: overlapMessages(contents...)}
}

// TestCopyOverlapCountsTheSplitBothWays pins the two directions that matter: a
// copy fully contained in the canonical reads as pure duplication, and a longer
// copy reads as work of its own.
func TestCopyOverlapCountsTheSplitBothWays(t *testing.T) {
	base := snapshotOf("one", "two", "three")
	longer := snapshotOf("one", "two", "three", "four", "five")

	if got := longer.Overlap(base); got.Shared != 3 || got.Unique != 0 {
		t.Fatalf("contained copy = %+v, want shared 3 unique 0", got)
	}
	if got := base.Overlap(longer); got.Shared != 3 || got.Unique != 2 {
		t.Fatalf("longer copy = %+v, want shared 3 unique 2", got)
	}
}

// TestCopyOverlapStopsAtTheFirstDivergence is the case that makes the number worth
// showing: a branch that agrees for a while and then diverges holds real work past
// the split, and counting it as duplication would hide exactly the loss the report
// exists to surface.
func TestCopyOverlapStopsAtTheFirstDivergence(t *testing.T) {
	base := snapshotOf("one", "two", "three")
	diverged := snapshotOf("one", "two", "different")

	got := base.Overlap(diverged)
	if got.Shared != 2 || got.Unique != 1 {
		t.Fatalf("diverged copy = %+v, want shared 2 unique 1", got)
	}
}

// TestCopyOverlapAgreesWithCovers keeps the report and the merge decision on one
// definition: whenever Covers accepts a copy, Overlap must call it pure
// duplication, or the UI would warn about content the merge already has.
func TestCopyOverlapAgreesWithCovers(t *testing.T) {
	cases := []struct {
		name      string
		canonical []string
		copy      []string
	}{
		{name: "identical", canonical: []string{"a", "b"}, copy: []string{"a", "b"}},
		{name: "prefix", canonical: []string{"a", "b", "c"}, copy: []string{"a", "b"}},
		{name: "diverged", canonical: []string{"a", "b", "c"}, copy: []string{"a", "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			canonical := snapshotOf(tc.canonical...)
			copied := snapshotOf(tc.copy...)
			covers := canonical.Covers(copied)
			overlap := canonical.Overlap(copied)
			if covers && overlap.Unique != 0 {
				t.Fatalf("Covers accepted the copy but Overlap reports %d unique", overlap.Unique)
			}
			if !covers && overlap.Unique == 0 {
				t.Fatalf("Covers rejected the copy but Overlap reports nothing unique")
			}
		})
	}
}
