package textutil

import "strings"

// First returns the first value whose TrimSpace is non-empty, unmodified.
// It returns "" when every value is blank (or no values are given).
//
// Single home for the "first non-blank string" helper that was previously
// duplicated per package (tl1628). Callers that need the trimmed value should
// use FirstTrimmed instead of re-trimming at each call site.
func First(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// FirstTrimmed returns strings.TrimSpace of the first value whose TrimSpace is
// non-empty; it is exactly strings.TrimSpace(First(values...)). The composite
// implementation keeps the selection logic in one place so the two flavors
// cannot drift apart (tl1628).
func FirstTrimmed(values ...string) string {
	return strings.TrimSpace(First(values...))
}
