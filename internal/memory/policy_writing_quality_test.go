package memory

import (
	"strings"
	"testing"
)

// TestPolicyBlockTeachesWritingQuality pins the memory writing-quality guide
// (20261002 prompt research, report §4.1 #2): the four-type typology, dedup
// against the loaded index, and the "not what the repo records" exclusion.
// Recall had policy guidance; writing quality had only the remember tool
// description until this block gained its second half.
func TestPolicyBlockTeachesWritingQuality(t *testing.T) {
	set := &Set{Store: Store{Dir: "/memory/project"}}
	block := set.PolicyBlock()
	for _, want := range []string{
		"`user` (who the user is",
		"`feedback` (how they want you to work, with the why)",
		"`project` (ongoing goals or constraints not derivable from the code or git history)",
		"`reference` (a pointer to an external resource)",
		"instead of creating a near-duplicate",
		"save the non-obvious point behind it",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("PolicyBlock missing writing-quality guidance %q:\n%s", want, block)
		}
	}
	// The reading-side rule predates the writing guide and has its own
	// consumers; the extension must not crowd it out.
	if !strings.Contains(block, "background rather than standing instructions") {
		t.Fatalf("PolicyBlock lost the stale-background reading rule:\n%s", block)
	}
}

// TestPolicyBlockStaysBounded keeps the guide a compact policy paragraph, not
// a manual: it sits in the cache-stable prefix of every memory-enabled session.
func TestPolicyBlockStaysBounded(t *testing.T) {
	set := &Set{Store: Store{Dir: "/memory/project"}}
	if got := len(set.PolicyBlock()); got > 1600 {
		t.Fatalf("PolicyBlock grew to %d bytes; keep the writing guide compact", got)
	}
}
