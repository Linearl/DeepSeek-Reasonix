package agent

import (
	"encoding/json"
	"testing"
)

// Task 418 (security batch S4): the directory rows hint must stay provably
// bounded — a caller-controlled limit (tool JSON args) may never size the
// allocation directly. The clamp itself is contract (schema says "max 1000");
// these pins hold the cap and the defaults observable in the echoed limit.
func TestDirectoryPageCapsLimitAtMaxRows(t *testing.T) {
	cfg, _ := controlFixture(t)
	for _, tc := range []struct {
		name  string
		limit int
		want  int
	}{
		{"absurd limit collapses to the documented max", 1_000_000_000, collabDirectoryMaxRows},
		{"negative limit falls back to the directory default", -7, 200},
		{"zero limit falls back to the directory default", 0, 200},
		{"in-range limit passes through", 10, 10},
		{"exactly the max stays the max", collabDirectoryMaxRows, collabDirectoryMaxRows},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := directoryPage(cfg, tc.limit, nil, "")
			if err != nil {
				t.Fatalf("directoryPage(%d): %v", tc.limit, err)
			}
			var page struct {
				Limit int `json:"limit"`
			}
			if err := json.Unmarshal([]byte(out), &page); err != nil {
				t.Fatalf("page is not JSON: %v\n%s", err, out)
			}
			if page.Limit != tc.want {
				t.Fatalf("echoed limit = %d, want %d (requested %d)", page.Limit, tc.want, tc.limit)
			}
		})
	}
}
