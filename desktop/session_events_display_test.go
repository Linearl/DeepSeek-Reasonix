package main

import "testing"

// Task 345: the display-name chain must never fall back to the bare path a
// user rejected - title, preview, topic, then a stamped label carved from the
// file name; the 40-rune clamp applies to the prose candidates only.
func TestSessionEventsDisplayTitle(t *testing.T) {
	const long = "这是一个非常长的会话标题需要被截断到四十个字符以内再继续写一些字"
	got := sessionEventsDisplayTitle("Project plan", "first user msg", "topic title", "/x/y/a.jsonl")
	if got != "Project plan" {
		t.Fatalf("title priority = %q, want Project plan", got)
	}
	got = sessionEventsDisplayTitle("", "  first user msg  ", "topic title", "/x/y/a.jsonl")
	if got != "first user msg" {
		t.Fatalf("preview fallback = %q", got)
	}
	got = sessionEventsDisplayTitle("", "", "topic title", "/x/y/a.jsonl")
	if got != "topic title" {
		t.Fatalf("topic fallback = %q", got)
	}
	got = sessionEventsDisplayTitle("", "", "", "/sessions/20260927-030006.576166300-collab.jsonl")
	if got != "20260927 03:00" {
		t.Fatalf("stamp fallback = %q, want 20260927 03:00", got)
	}
	got = sessionEventsDisplayTitle("", "", "", "/sessions/weird-name.jsonl")
	if got != "weird-name" {
		t.Fatalf("basename fallback = %q", got)
	}
	got = sessionEventsDisplayTitle(long, "", "", "/x/a.jsonl")
	if r := len([]rune(got)); r > 41 {
		t.Fatalf("long title not clamped: %d runes", r)
	}
	// The rejected shape: a full path must never surface as the display name
	// when any other candidate exists.
	if got := sessionEventsDisplayTitle("", "", "", "/deep/dir/session.jsonl"); got == "/deep/dir/session.jsonl" {
		t.Fatal("bare path leaked into the display name")
	}
}
