package main

import "testing"

// TestIsConversationDualOpen pins task 203's dual-open probe, including the
// audit M1 regression: topic counting must be scoped to the queried
// conversation, so an unrelated conversation's dual open never gates this one.
func TestIsConversationDualOpen(t *testing.T) {
	mk := func(path, topic string, removed bool) *WorkspaceTab {
		return &WorkspaceTab{SessionPath: path, TopicID: topic, removed: removed}
	}
	app := func(tabs ...*WorkspaceTab) *App {
		m := map[string]*WorkspaceTab{}
		for i, tab := range tabs {
			m[string(rune('a'+i))] = tab
		}
		return &App{tabs: m}
	}
	cases := []struct {
		name  string
		app   *App
		query string
		want  bool
	}{
		{
			name:  "same path in two tabs is dual open",
			app:   app(mk("/s/one.jsonl", "", false), mk("/s/one.jsonl", "", false)),
			query: "/s/one.jsonl",
			want:  true,
		},
		{
			name:  "prompt fanout: one topic, two session paths",
			app:   app(mk("/s/fan-a.jsonl", "topic-1", false), mk("/s/fan-b.jsonl", "topic-1", false)),
			query: "/s/fan-a.jsonl",
			want:  true,
		},
		{
			name: "cross topic negative (audit M1): another topic's dual open must not gate this conversation",
			app: app(
				mk("/s/alone.jsonl", "topic-a", false),
				mk("/s/b1.jsonl", "topic-b", false),
				mk("/s/b2.jsonl", "topic-b", false),
			),
			query: "/s/alone.jsonl",
			want:  false,
		},
		{
			name:  "cross topic negative: merge gate stays open for the single-open group",
			app:   app(mk("/s/alone.jsonl", "topic-a", false), mk("/s/b1.jsonl", "topic-b", false), mk("/s/b2.jsonl", "topic-b", false)),
			query: "/s/alone.jsonl",
			want:  false,
		},
		{
			name:  "removed tab does not count",
			app:   app(mk("/s/one.jsonl", "topic-1", false), mk("/s/one.jsonl", "topic-1", true)),
			query: "/s/one.jsonl",
			want:  false,
		},
		{
			name:  "single tab is not dual open",
			app:   app(mk("/s/one.jsonl", "topic-1", false), mk("/s/other.jsonl", "topic-2", false)),
			query: "/s/one.jsonl",
			want:  false,
		},
		{
			name:  "empty query never reports dual open",
			app:   app(mk("/s/one.jsonl", "", false), mk("/s/one.jsonl", "", false)),
			query: "",
			want:  false,
		},
	}
	for _, tc := range cases {
		if got := tc.app.isConversationDualOpen(tc.query); got != tc.want {
			t.Errorf("%s: isConversationDualOpen(%q) = %v, want %v", tc.name, tc.query, got, tc.want)
		}
	}
	if (&App{}).isConversationDualOpen("/s/one.jsonl") {
		t.Error("nil tabs map must report false")
	}
}
