package control

import "testing"

// tl1724: ParseRewindScope is the single wire-format scope parser shared by
// the desktop preview/commit entry points and the serve HTTP handler, so the
// same invalid input must yield the same rejection everywhere.
func TestParseRewindScope(t *testing.T) {
	cases := []struct {
		in   string
		want RewindScope
		ok   bool
	}{
		{"code", RewindCode, true},
		{"conversation", RewindConversation, true},
		{"both", RewindBoth, true},
		{"bogus", RewindBoth, false},
		{"", RewindBoth, false},
		{"CODE", RewindBoth, false},
		{"code ", RewindBoth, false},
	}
	for _, tc := range cases {
		got, err := ParseRewindScope(tc.in)
		if tc.ok {
			if err != nil {
				t.Fatalf("ParseRewindScope(%q): unexpected error %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ParseRewindScope(%q) = %v, want %v", tc.in, got, tc.want)
			}
			continue
		}
		if err == nil {
			t.Fatalf("ParseRewindScope(%q): nil error, want ErrRewindInvalidScope", tc.in)
		}
		if err.Error() != ErrRewindInvalidScope.Error() {
			t.Fatalf("ParseRewindScope(%q): error = %q, want stable contract text %q", tc.in, err.Error(), ErrRewindInvalidScope.Error())
		}
	}
}
