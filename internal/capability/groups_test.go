package capability

import (
	"strings"
	"testing"
)

// appendixA53 is the task-951 report appendix A list (53 L1 tool entries,
// 78 Name() registrations − the 24 L0 tools), extended by task 934 with the
// five workflow_* management tools. The group tagging must cover it exactly —
// every tool in exactly one group, no phantom members.
var appendixA53 = []string{
	"adopt_session_version", "channel_cancel", "channel_list", "channel_read", "channel_send",
	"claim_check", "code_index", "create_task_card", "create_worktree", "decision_pattern_solidify",
	"delete_range", "delete_session", "delete_symbol", "dingtalk", "distill", "drain_inbox", "dream",
	"event_wait", "feishu", "get_session_info", "get_task_card", "heartbeat_task_enable",
	"heartbeat_task_list", "heartbeat_task_upsert", "inspect_worktree_merge", "list_session_versions",
	"list_task_cards", "ls", "mailbox", "manage_sessions", "merge_worktree_back", "move_file",
	"move_topic_to_group", "notebook_edit", "open_isolated_worktree_project", "peek_own_inbox",
	"prepare_worktree_merge", "qq", "query_collab_mail", "read_collab_status", "rename_session",
	"rename_topic", "screenshot", "search_sessions", "session_control", "submit_feedback",
	"subscribe_session", "talk_to_session_sync", "todo_read", "ui_interact", "update_task_card",
	"web_fetch", "weixin", "workflow_define", "workflow_get", "workflow_list",
	"workflow_run", "workflow_status",
}

// TestGroupCoversAppendixA53Exactly pins the 58-tool / 9-group tagging: no
// missing tool, no duplicate membership, no member outside appendix A.
func TestGroupCoversAppendixA53Exactly(t *testing.T) {
	owner := map[string]string{}
	for _, g := range groupCatalog {
		for _, member := range g.Members {
			if prev, dup := owner[member]; dup {
				t.Errorf("tool %q is in both group %q and %q — one tool, one group", member, prev, g.Name)
			}
			owner[member] = g.Name
		}
	}
	want := map[string]bool{}
	for _, name := range appendixA53 {
		want[name] = true
		if _, ok := owner[name]; !ok {
			t.Errorf("appendix-A tool %q is not assigned to any group", name)
		}
	}
	for member := range owner {
		if !want[member] {
			t.Errorf("group member %q is not in appendix A (phantom member)", member)
		}
	}
	if len(groupCatalog) != 9 {
		t.Fatalf("want 9 groups, got %d", len(groupCatalog))
	}
}

// TestGroupDirectoryRowsRender checks the list-row format contract that task
// 953's list v2 consumes: scenario, member count, entry pointer, example,
// and the inspect path — with the entry pointer suppressed when session_hub
// is not registered (anti-ghost, 951 #4).
func TestGroupDirectoryRowsRender(t *testing.T) {
	rows := BuildGroupEntries(map[string]bool{"rename_session": true})
	if len(rows) != 9 {
		t.Fatalf("want 9 group rows, got %d", len(rows))
	}
	for _, row := range rows {
		if row.Kind != KindGroup {
			t.Errorf("group row %s has kind %s", row.ID, row.Kind)
		}
		if !strings.HasPrefix(row.ID, "tool:group/") {
			t.Errorf("group row id %q missing tool:group/ prefix", row.ID)
		}
		if !strings.Contains(row.Description, "组卡：inspect "+row.ID) {
			t.Errorf("group row %q description missing inspect pointer: %s", row.ID, row.Description)
		}
		if !strings.Contains(row.Description, "入口=") || !strings.Contains(row.Description, "例=") {
			t.Errorf("group row %q description missing entry/example parts: %s", row.ID, row.Description)
		}
	}
	// session_hub unregistered → entry suppressed to 无, never a ghost pointer.
	for _, row := range rows {
		if row.Name == "session" && !strings.Contains(row.Description, "入口=无") {
			t.Errorf("session row must render 入口=无 without session_hub registered: %s", row.Description)
		}
	}
	// session_hub registered → entry pointer shows.
	rows = BuildGroupEntries(map[string]bool{"rename_session": true, "session_hub": true})
	for _, row := range rows {
		if row.Name == "session" && !strings.Contains(row.Description, "入口=session_hub") {
			t.Errorf("session row must render 入口=session_hub when registered: %s", row.Description)
		}
	}
}

// TestGroupCardsOnlyFirstBatch: the first batch carries cards for session and
// im only; the other six groups are directory rows (card=nil).
func TestGroupCardsOnlyFirstBatch(t *testing.T) {
	for _, g := range groupCatalog {
		switch g.Name {
		case "session", "im":
			if g.Card == nil {
				t.Errorf("first-batch group %q must carry a card", g.Name)
			} else {
				if len(g.Card.Scenario) < 3 {
					t.Errorf("group card %q needs >=3 scenario lines, got %d", g.Name, len(g.Card.Scenario))
				}
				if len(g.Card.Members) != len(g.Members) {
					t.Errorf("group card %q member table (%d) must list every member (%d)", g.Name, len(g.Card.Members), len(g.Members))
				}
				if len(g.Card.Examples) < 2 {
					t.Errorf("group card %q needs >=2 examples, got %d", g.Name, len(g.Card.Examples))
				}
			}
		default:
			if g.Card != nil {
				t.Errorf("group %q is not first batch — card must be nil until call data justifies one", g.Name)
			}
		}
	}
	// Card member IDs must point at real catalog ids of the same group.
	for _, g := range groupCatalog {
		if g.Card == nil {
			continue
		}
		members := map[string]bool{}
		for _, m := range g.Members {
			members["tool:"+m] = true
		}
		for _, row := range g.Card.Members {
			if !members[row.ID] {
				t.Errorf("group card %q lists %q which is not a member of the group", g.Name, row.ID)
			}
		}
	}
}

// TestRouteSuggestsGroupByTrigger: route推组 — a trigger hit yields a
// suggest-only candidate pointing at the group card; a non-matching input
// yields nothing. Group entries are only in the catalog when the switch is
// on, so the registration guard is structural.
func TestRouteSuggestsGroupByTrigger(t *testing.T) {
	entries := BuildGroupEntries(nil)
	decision := Route("帮我把改动 rename_session 的名字改一下，还有发qq消息给群里的同学", entries)
	found := false
	for _, c := range decision.Candidates {
		if c.Entry.Kind != KindGroup {
			continue
		}
		if c.Policy != AutoUseSuggest {
			t.Errorf("group %s routed with policy %s — groups must be suggest-only", c.Entry.ID, c.Policy)
		}
		if !strings.Contains(c.Reason, "inspect "+c.Entry.ID) {
			t.Errorf("group %s reason must point at the group card: %s", c.Entry.ID, c.Reason)
		}
		found = true
	}
	if !found {
		t.Fatal("no group candidate routed for a trigger-matching input")
	}
	if got := Route("随便聊聊天气", entries); len(got.Candidates) != 0 {
		t.Errorf("non-matching input must route no group candidates, got %d", len(got.Candidates))
	}
}

// TestToolGroupLookup: the pure-metadata tag lookup used by search
// attribution and other renderers.
func TestToolGroupLookup(t *testing.T) {
	cases := map[string]string{
		"rename_session":      "session",
		"qq":                  "im",
		"mailbox":             "inbox",
		"create_worktree":     "worktree",
		"screenshot":          "system",
		"heartbeat_task_list": "heartbeat",
		"move_file":           "files",
		"dream":               "memory",
		"grep":                "", // L0 tool, untagged
		"not_a_real_tool":     "",
	}
	for tool, want := range cases {
		if got := ToolGroup(tool); got != want {
			t.Errorf("ToolGroup(%q) = %q, want %q", tool, got, want)
		}
	}
}

// TestParseGroupIDRoundTrip.
func TestParseGroupIDRoundTrip(t *testing.T) {
	for _, g := range groupCatalog {
		id := GroupID(g.Name)
		name, ok := ParseGroupID(id)
		if !ok || name != g.Name {
			t.Errorf("ParseGroupID(%q) = %q,%v want %q,true", id, name, ok, g.Name)
		}
	}
	if _, ok := ParseGroupID("tool:group/"); ok {
		t.Error("empty group name must not parse")
	}
	if _, ok := ParseGroupID("tool:rename_session"); ok {
		t.Error("non-group id must not parse as group")
	}
}
