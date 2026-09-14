package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

func encodeLine(t *testing.T, m provider.Message) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return string(b)
}

func systemLine(t *testing.T, content string) string {
	t.Helper()
	return encodeLine(t, provider.Message{Role: provider.RoleSystem, Content: content})
}

func TestGraftInsertsAfterLeadingSystemMessages(t *testing.T) {
	lines := []string{
		systemLine(t, "prompt"),
		systemLine(t, "memory"),
		encodeLine(t, userTurn("C")),
		encodeLine(t, assistantTurn("D")),
	}
	gap := []provider.Message{userTurn("A"), assistantTurn("B")}

	merged, err := graftPrefixOntoLines(lines, gap)
	if err != nil {
		t.Fatalf("graft: %v", err)
	}
	if len(merged) != 6 {
		t.Fatalf("lines = %d, want 6", len(merged))
	}
	// The two preamble lines stay in front.
	if merged[0] != lines[0] || merged[1] != lines[1] {
		t.Fatalf("leading system lines moved: %q %q", merged[0], merged[1])
	}
	// The gap lands next, and the original turns follow untouched.
	if merged[2] == "" || merged[3] == "" {
		t.Fatal("gap lines missing")
	}
	if merged[4] != lines[2] || merged[5] != lines[3] {
		t.Fatal("existing turns were rewritten instead of preserved")
	}
}

// The point of working on lines: existing bytes must survive the graft exactly.
func TestGraftPreservesEveryExistingLineByteForByte(t *testing.T) {
	odd := `{"role":"assistant","content":"tab\there","raw_content":"a\nb"}` // unusual escapes on purpose
	lines := []string{systemLine(t, "preamble"), odd}
	merged, err := graftPrefixOntoLines(lines, []provider.Message{userTurn("A")})
	if err != nil {
		t.Fatalf("graft: %v", err)
	}
	if merged[2] != odd {
		t.Fatalf("existing line changed:\n got %s\nwant %s", merged[1], odd)
	}
}

func TestGraftWithoutLeadingSystemInsertsAtTheTop(t *testing.T) {
	lines := []string{encodeLine(t, userTurn("C"))}
	merged, err := graftPrefixOntoLines(lines, []provider.Message{userTurn("A")})
	if err != nil {
		t.Fatalf("graft: %v", err)
	}
	if len(merged) != 2 || merged[1] != lines[0] {
		t.Fatalf("merged = %v", merged)
	}
}

func TestGraftWithNoGapChangesNothing(t *testing.T) {
	lines := []string{systemLine(t, "p"), encodeLine(t, userTurn("C"))}
	merged, err := graftPrefixOntoLines(lines, nil)
	if err != nil {
		t.Fatalf("graft: %v", err)
	}
	if len(merged) != len(lines) {
		t.Fatalf("merged = %v", merged)
	}
	for i := range lines {
		if merged[i] != lines[i] {
			t.Fatal("a no-op graft changed the transcript")
		}
	}
}

// An unreadable head means the file cannot be trusted to be what it claims, and
// neither placement is right: ahead of unknown content puts turns before the
// preamble, behind it puts them inside it. A graft that cannot be placed
// correctly must not happen at all.
func TestGraftRefusesAnUnreadableHead(t *testing.T) {
	lines := []string{"{not json", encodeLine(t, userTurn("C"))}
	if _, err := graftPrefixOntoLines(lines, []provider.Message{userTurn("A")}); err == nil {
		t.Fatal("a transcript with an unreadable head must not be grafted onto")
	}
}
func TestGraftStopsAtTheFirstNonSystemLine(t *testing.T) {
	lines := []string{
		systemLine(t, "preamble"),
		encodeLine(t, userTurn("C")),
		systemLine(t, "a later system line"),
	}
	merged, err := graftPrefixOntoLines(lines, []provider.Message{userTurn("A")})
	if err != nil {
		t.Fatalf("graft: %v", err)
	}
	// Only the first line is leading system, so the gap goes at index 1.
	if merged[0] != lines[0] {
		t.Fatal("the first system line must stay first")
	}
	if merged[3] != lines[2] {
		t.Fatal("a later system line must not be treated as leading")
	}
}

func TestTranscriptLinesRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int
	}{
		{"lf with trailing newline", "a\nb\n", 2},
		{"crlf", "a\r\nb\r\n", 2},
		{"no trailing newline", "a\nb", 2},
		{"single line", "a", 1},
		{"empty", "", 0},
		{"only newlines", "\n\n", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := splitTranscriptLines([]byte(tc.raw))
			if len(lines) != tc.want {
				t.Fatalf("lines = %d, want %d (%q)", len(lines), tc.want, lines)
			}
			if tc.want == 0 {
				return
			}
			// Joining must produce a transcript the next append can extend.
			out := string(joinTranscriptLines(lines))
			if !strings.HasSuffix(out, "\n") || strings.HasSuffix(out, "\n\n") {
				t.Fatalf("joined transcript must end with exactly one newline: %q", out)
			}
			if again := splitTranscriptLines([]byte(out)); len(again) != tc.want {
				t.Fatalf("round trip changed the line count: %d -> %d", tc.want, len(again))
			}
		})
	}
}
