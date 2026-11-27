package builtin

// tl1838: the 2026-11-27 field reports showed the model rebuilding the list
// from memory after the host cleared it at a compaction/turn boundary and
// being rejected as "invented completed" — the description documented the
// rules but never the recovery posture. The rewrite must (a) document the
// lost-list recovery path, (b) stay front-loaded with the tl1716 quick rules,
// and (c) keep the density from growing back: the pre-tl1838 description was
// a 2653-byte wall of prose the model demonstrably skipped past, so the new
// text carries an explicit byte ceiling — new prose added without trimming
// turns the ceiling red.

import (
	"strings"
	"testing"
)

// The description must document the loss-recovery posture and quote the exact
// todo_read response ("no todo list is active") the model will see, so the
// empty-baseline branch (a fresh list is legal) is distinguishable from the
// still-tracked branch (rebuild from the echoed list, never from memory).
func TestTodoWriteDescriptionDocumentsLossRecovery(t *testing.T) {
	desc := todoWriteDescription()
	for _, want := range []string{
		"LIST LOST",
		"todo_read FIRST",
		"no todo list is active",
		"fresh list with fresh step_ids",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("description must document the loss-recovery posture, missing %q", want)
		}
	}
	// Recovery is a high-frequency scenario (4 same-day incidents): it must sit
	// with the quick rules, before the example and the details tail.
	lost := strings.Index(desc, "LIST LOST")
	example := strings.Index(desc, "Minimal ops example:")
	if lost < 0 || example < 0 || lost > example {
		t.Fatalf("LIST LOST guidance must appear before the example (front-loaded)")
	}
}

// Density guard ("上限+新增即红"): the ceiling stays below the pre-tl1838
// baseline of 2653 bytes even though the rewrite ADDS the recovery section.
// Raise it only alongside a real trim, never by accretion.
func TestTodoWriteDescriptionLengthCeiling(t *testing.T) {
	desc := todoWriteDescription()
	const ceiling = 2400 // bytes; the pre-tl1838 description was 2674 bytes
	if len(desc) > ceiling {
		t.Fatalf("description grew to %d bytes (ceiling %d); trim existing prose instead of adding", len(desc), ceiling)
	}
	if len(desc) >= 2674 {
		t.Fatalf("description is %d bytes — no longer slimmer than the pre-tl1838 baseline 2674", len(desc))
	}
}
