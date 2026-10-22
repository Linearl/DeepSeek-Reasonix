package main

import "testing"

// tl 1076: the display-name chain is topic title (the sidebar project-tree
// name), then a user-chosen session title, then the first-message preview,
// then a stamped label carved from the file name; the 40-rune clamp applies to
// the prose candidates only. The pre-1076 task-345 order (title > preview >
// topic) showed unnamed sessions as a truncated first message ("已经安装驱动了，
// 稍后我…"), which the user cannot identify — this test pins the reorder.
func TestSessionEventsDisplayTitle(t *testing.T) {
	const long = "这是一个非常长的会话标题需要被截断到四十个字符以内再继续写一些字"
	// All three prose candidates present: topic wins (acceptance ① 三级顺序断言).
	got := sessionEventsDisplayTitle("topic title", "Project plan", "first user msg", "/x/y/a.jsonl")
	if got != "topic title" {
		t.Fatalf("topic priority = %q, want topic title", got)
	}
	// Topic absent: the user title wins over the preview.
	got = sessionEventsDisplayTitle("", "Project plan", "first user msg", "/x/y/a.jsonl")
	if got != "Project plan" {
		t.Fatalf("title fallback = %q, want Project plan", got)
	}
	// Both title and topic absent: preview (the rejected pre-1076 display for
	// unnamed sessions must stay a fallback, never the first choice).
	got = sessionEventsDisplayTitle("", "", "  first user msg  ", "/x/y/a.jsonl")
	if got != "first user msg" {
		t.Fatalf("preview fallback = %q", got)
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
