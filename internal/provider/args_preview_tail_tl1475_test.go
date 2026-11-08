package provider

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// tl1475: the streaming argument excerpt must stay a bounded, rune-safe
// head+tail combination — providers call it per delta chunk, so it must never
// scan or copy the whole accumulated payload, and it must never produce
// invalid UTF-8 for CJK arguments. The head keeps subject keys ("path",
// "command") readable while the tail shows the newest streamed characters.
func TestArgsPreviewExcerptShortPassesThrough(t *testing.T) {
	in := `{"path":"a.txt","content":"x"}`
	if got := ArgsPreviewExcerpt(in); got != in {
		t.Fatalf("short payload must pass through unchanged: got %q", got)
	}
	if got := ArgsPreviewExcerpt(""); got != "" {
		t.Fatalf("empty payload must stay empty, got %q", got)
	}
}

func TestArgsPreviewExcerptHeadAndTail(t *testing.T) {
	head := `{"path":"logs/app.log","content":"` + strings.Repeat("a", 2000)
	got := ArgsPreviewExcerpt(head)
	if !strings.HasPrefix(got, `{"path":"logs/app.log","content":"`) {
		t.Fatalf("excerpt must keep the head (subject keys readable), got %q", got[:40])
	}
	if !strings.Contains(got, argsPreviewSeparator) {
		t.Fatalf("truncated excerpt must carry the head/tail separator")
	}
	if i := strings.Index(got, argsPreviewSeparator); i > ArgsPreviewHeadBytes+len(`{"path":"logs/app.log","content":"`) {
		t.Fatalf("head segment exceeded the budget: %d bytes", i)
	}
	if !strings.HasSuffix(got, strings.Repeat("a", 100)) {
		t.Fatalf("tail must keep the NEWEST characters, got %q", got[len(got)-20:])
	}
	if len(got) > ArgsPreviewHeadBytes+ArgsPreviewTailBytes+len(argsPreviewSeparator)+len(`{"path":"logs/app.log","content":"`) {
		t.Fatalf("excerpt exceeded the total budget: len=%d", len(got))
	}
}

func TestArgsPreviewExcerptRuneSafeForCJK(t *testing.T) {
	// CJK runes are 3 bytes: byte budgets would otherwise split one on either cut.
	in := `{"path":"流.log","content":"` + strings.Repeat("流", 600) // > head+tail budget
	got := ArgsPreviewExcerpt(in)
	if !utf8.ValidString(got) {
		t.Fatalf("excerpt must be valid UTF-8: %q", got)
	}
	headPart := got
	if i := strings.Index(got, argsPreviewSeparator); i >= 0 {
		headPart = got[:i]
	}
	if !strings.HasPrefix(headPart, `{"path":"流.log"`) {
		t.Fatalf("head cut must land on a rune boundary (path readable), got %q", headPart)
	}
	if !strings.HasSuffix(got, "流") {
		t.Fatalf("tail cut must land on a rune boundary, got %q", got[len(got)-6:])
	}
}

func TestArgsPreviewExcerptHelpers(t *testing.T) {
	if got := tailRuneSafe("", 100); got != "" {
		t.Fatalf("empty input must stay empty, got %q", got)
	}
	if got := tailRuneSafe("abc", 0); got != "" {
		t.Fatalf("non-positive limit must disable the tail, got %q", got)
	}
	if got := tailRuneSafe("abc", -1); got != "" {
		t.Fatalf("negative limit must disable the tail, got %q", got)
	}
	if got := cutRuneSafe("abc流def", 0, 4); got != "abc" {
		t.Fatalf("cut must back up to a rune boundary, got %q", got)
	}
}

func TestArgsPreviewBudgetPins(t *testing.T) {
	// The bounds are display budgets, not protocol constants — pin them so an
	// accidental bump cannot silently inflate per-tick wire cost (tl1475).
	if ArgsPreviewHeadBytes != 384 || ArgsPreviewTailBytes != 800 {
		t.Fatalf("preview budgets changed: head=%d tail=%d (update the wire-cost rationale if intentional)", ArgsPreviewHeadBytes, ArgsPreviewTailBytes)
	}
}
