package desktoplauncher

import "testing"

// The relaunch handoff token must reach neither the desktop's flag parsing
// nor the legacy-strip output: extractWaitFor owns it end to end.
func TestExtractWaitFor(t *testing.T) {
	cases := []struct {
		name   string
		in     []string
		wantPK int
		wantR  []string
	}{
		{"absent", []string{"a", "b"}, 0, []string{"a", "b"}},
		{"two-token", []string{"--wait-for", "1234", "extra"}, 1234, []string{"extra"}},
		{"equals", []string{"--wait-for=1234"}, 1234, []string{}},
		{"first-position", []string{"--wait-for", "99"}, 99, []string{}},
		{"malformed-two-token", []string{"--wait-for", "abc", "extra"}, 0, []string{"extra"}},
		{"malformed-equals", []string{"--wait-for=abc", "extra"}, 0, []string{"extra"}},
		{"negative", []string{"--wait-for", "-5", "extra"}, 0, []string{"extra"}},
		{"dangling", []string{"--wait-for"}, 0, []string{"--wait-for"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotPID, gotRest := extractWaitFor(tc.in)
			if gotPID != tc.wantPK {
				t.Fatalf("pid = %d, want %d", gotPID, tc.wantPK)
			}
			if len(gotRest) != len(tc.wantR) {
				t.Fatalf("rest = %v, want %v", gotRest, tc.wantR)
			}
			for i := range gotRest {
				if gotRest[i] != tc.wantR[i] {
					t.Fatalf("rest = %v, want %v", gotRest, tc.wantR)
				}
			}
		})
	}
}
