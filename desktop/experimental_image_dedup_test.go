package main

import "testing"

// TestStrongerImageDedupMode: the desktop/agent mirror pair for the
// task-373-R1.1 three-position switch merges by strongest mode
// (all > first > off); unknown strings count as "off" so a stray value can
// never widen the effective write path in the settings view.
func TestStrongerImageDedupMode(t *testing.T) {
	for _, tc := range []struct{ a, b, want string }{
		{"off", "off", "off"},
		{"off", "first", "first"},
		{"first", "off", "first"},
		{"first", "first", "first"},
		{"first", "all", "all"},
		{"all", "off", "all"},
		{"", "first", "first"},
		{"garbage", "first", "first"},
		{"garbage", "", "off"},
		{"all", "garbage", "all"},
	} {
		if got := strongerImageDedupMode(tc.a, tc.b); got != tc.want {
			t.Fatalf("strongerImageDedupMode(%q, %q) = %q, want %q", tc.a, tc.b, got, tc.want)
		}
	}
}
