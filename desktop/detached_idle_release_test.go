package main

import "testing"

// TestParseDetachedIdleMinutes pins the O4 gate: off unless a positive minute
// value is set explicitly (fork rule 2 — default process keeps today's
// never-release behaviour).
func TestParseDetachedIdleMinutes(t *testing.T) {
	cases := []struct {
		raw  string
		want int
	}{
		{"", 0},
		{"0", 0},
		{"-5", 0},
		{"garbage", 0},
		{"30", 30},
		{"1", 1},
	}
	for _, c := range cases {
		if got := parseDetachedIdleMinutes(c.raw); got != c.want {
			t.Fatalf("parse(%q)=%d, want %d", c.raw, got, c.want)
		}
	}
}
