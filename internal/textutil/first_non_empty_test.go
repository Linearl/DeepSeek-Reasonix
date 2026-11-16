package textutil

import (
	"strings"
	"testing"
)

// referenceFirst reproduces the pre-consolidation per-package implementation
// (TrimSpace guard, original value returned) so the table below pins behavioral
// equivalence with the code that was replaced by tl1628.
func referenceFirst(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// referenceFirstTrimmed reproduces the trimmed flavor (billing/control-inbox
// variant): same guard, but the trimmed value is returned.
func referenceFirstTrimmed(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func TestFirst(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"no args", nil, ""},
		{"single", []string{"a"}, "a"},
		{"first wins", []string{"a", "b"}, "a"},
		{"skip empty head", []string{"", "b"}, "b"},
		{"skip whitespace head keep original", []string{"  ", "  b  "}, "  b  "},
		{"all blank", []string{"", "   ", "\t", "\n"}, ""},
		{"blank only value with padding", []string{"  x  "}, "  x  "},
		{"zero value strings", []string{"\u00a0x"}, "\u00a0x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := First(tc.in...); got != tc.want {
				t.Fatalf("First(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if got := referenceFirst(tc.in...); got != tc.want {
				t.Fatalf("reference implementation drift: First(%q)=%q but legacy=%q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFirstTrimmed(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"no args", nil, ""},
		{"trims winner", []string{"  b  "}, "b"},
		{"skip blank head", []string{" ", "  b  "}, "b"},
		{"all blank", []string{"", "   ", "\t\n"}, ""},
		{"inner spaces preserved", []string{"a b"}, "a b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FirstTrimmed(tc.in...); got != tc.want {
				t.Fatalf("FirstTrimmed(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if got := referenceFirstTrimmed(tc.in...); got != tc.want {
				t.Fatalf("reference implementation drift: FirstTrimmed(%q)=%q but legacy=%q", tc.in, got, tc.want)
			}
		})
	}
}

// TestFirstTrimmedEquivalentToTrimOfFirst pins the composite identity
// FirstTrimmed(vals...) == strings.TrimSpace(First(vals...)) against the
// loop-based legacy implementations across a mixed corpus, so the billing /
// control-inbox consolidation (tl1628) is provably behavior-preserving.
func TestFirstTrimmedEquivalentToTrimOfFirst(t *testing.T) {
	corpus := []string{"", " ", "\t", "\n", "plain", "  padded  ", "inner space", "\u3000ideographic space", "x", "  y  "}
	// Enumerate ordered prefixes of the corpus as argument lists.
	var argLists [][]string
	for n := 0; n <= len(corpus); n++ {
		argLists = append(argLists, corpus[:n])
	}
	for _, vals := range argLists {
		want := strings.TrimSpace(referenceFirst(vals...))
		if got := FirstTrimmed(vals...); got != want {
			t.Fatalf("FirstTrimmed(%q) = %q, want %q (TrimSpace(First(...)))", vals, got, want)
		}
		if got := referenceFirstTrimmed(vals...); got != want {
			t.Fatalf("legacy loop flavor drift for %q: got %q, want %q", vals, got, want)
		}
	}
}
