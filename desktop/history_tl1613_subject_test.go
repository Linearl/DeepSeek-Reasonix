package main

import "testing"

// tl1613 pins the archived-history subject chain: tools that carry the new
// optional description argument show it as the collapsed card's human title,
// falling back to the previous subject (command/pattern/path) when the
// description is absent — old sessions and description-less calls must render
// exactly as before. The command raw text itself is never lost: the frontend
// command block reads it from args directly.
func TestTl1613HistoryToolSubjectPrefersDescription(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args string
		want string
	}{
		{
			name: "bash with description",
			tool: "bash",
			args: `{"command":"go test ./internal/tool/... -count=1","description":"Run the tool package tests"}`,
			want: "Run the tool package tests",
		},
		{
			name: "bash without description falls back to command",
			tool: "bash",
			args: `{"command":"go test ./internal/tool/... -count=1"}`,
			want: "go test ./internal/tool/... -count=1",
		},
		{
			name: "blank description falls back to command",
			tool: "bash",
			args: `{"command":"git status","description":"  "}`,
			want: "git status",
		},
		{
			name: "grep with description",
			tool: "grep",
			args: `{"pattern":"retry.*deadline","path":"internal/agent","description":"Find retry deadline handling"}`,
			want: "Find retry deadline handling",
		},
		{
			name: "grep without description falls back to pattern",
			tool: "grep",
			args: `{"pattern":"retry.*deadline","path":"internal/agent"}`,
			want: "retry.*deadline",
		},
		{
			name: "glob with description",
			tool: "glob",
			args: `{"pattern":"**/*.test.ts","description":"Locate frontend test files"}`,
			want: "Locate frontend test files",
		},
		{
			name: "read_file with description",
			tool: "read_file",
			args: `{"path":"internal/tool/builtin/bash.go","description":"Check how bash validates arguments"}`,
			want: "Check how bash validates arguments",
		},
		{
			name: "edit_file without description falls back to path",
			tool: "edit_file",
			args: `{"path":"internal/tool/builtin/bash.go","old_string":"a","new_string":"b"}`,
			want: "internal/tool/builtin/bash.go",
		},
		{
			name: "write_file with description",
			tool: "write_file",
			args: `{"path":"docs/notes.md","content":"hello","description":"Draft the tl1613 handoff note"}`,
			want: "Draft the tl1613 handoff note",
		},
		{
			name: "multi_edit with description",
			tool: "multi_edit",
			args: `{"path":"card.tsx","edits":[{"old_string":"a","new_string":"b"}],"description":"Rename shellCommandOf across the card"}`,
			want: "Rename shellCommandOf across the card",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := historyToolSubject(tc.tool, tc.args); got != tc.want {
				t.Fatalf("historyToolSubject(%q, %s) = %q, want %q", tc.tool, tc.args, got, tc.want)
			}
		})
	}
}

// tl1613: the subject is a one-line title — a multiline description must be
// clipped to a single line (clipSingleLine collapses whitespace), never leak
// newlines into the collapsed row.
func TestTl1613HistoryToolSubjectClipsMultilineDescription(t *testing.T) {
	got := historyToolSubject("bash", `{"command":"make all","description":"Build and test\nthe whole tree"}`)
	if got != "Build and test the whole tree" {
		t.Fatalf("multiline description not flattened: %q", got)
	}
}
