package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Task 352: during the create/recover window one topic can hold two runtime
// records for the same conversation (the pre-consolidation copy). The old
// runtime projection expanded every record as a per-session child row, so the
// sidebar briefly showed raw file stems ("20260928-042815-…") with a
// "previously" meta line until recovery consolidated the copy and the row
// self-healed away. The contract is one logical topic row: both runtime
// projection entries must collapse the window copies instead of expanding
// them, and the open record owns the representative session path.
func TestRuntimeTopicProjectionCollapsesWindowCopies(t *testing.T) {
	app := NewApp()
	freshPath := "/sessions/20260928-042815.686047-fresh.jsonl"
	originPath := "/sessions/20260928-031402.112233-origin.jsonl"
	app.tabs["live"] = &WorkspaceTab{
		ID: "live", Scope: "global", TopicID: "topic-window",
		TopicTitle: "专家团复查", SessionPath: freshPath,
		Ctrl: &activationStubController{sessionPath: freshPath},
	}
	app.detachedSessions["stale"] = &WorkspaceTab{
		Scope: "global", TopicID: "topic-window",
		TopicTitle: "专家团复查", SessionPath: originPath,
	}

	snapshot := app.GetProjectTreeRuntimeSnapshot()
	if len(snapshot.Topics) != 1 {
		t.Fatalf("runtime snapshot topics = %d, want 1 logical row", len(snapshot.Topics))
	}
	node := snapshot.Topics[0].Node
	if len(node.Children) != 0 {
		t.Fatalf("window copies expanded as %d child rows (first label %q), want no session-kind children", len(node.Children), node.Children[0].Label)
	}
	if node.Label != "专家团复查" {
		t.Fatalf("logical row label = %q, want the topic title instead of a file stem", node.Label)
	}
	if !node.Open {
		t.Fatalf("logical row open = false, want the open snapshot aggregated up")
	}
	if node.SessionPath != freshPath {
		t.Fatalf("logical row session path = %q, want the open record %q", node.SessionPath, freshPath)
	}

	// The catalog-merge entry (withLiveTopics) reads the same builder and must
	// stay collapsed too — one contract, every entry.
	nodes := app.runtimeOnlyProjectTopics("global", "")
	if len(nodes) != 1 {
		t.Fatalf("runtime-only topics = %d, want 1 logical row", len(nodes))
	}
	if len(nodes[0].Children) != 0 {
		t.Fatalf("catalog-merge entry expanded %d child rows, want none", len(nodes[0].Children))
	}
}

// TestRuntimeTopicProjectionEmitsNoSessionChildRows is the source-level guard
// (task 352, following the task 299 dynamic-scan paradigm): the collapse above
// is one deleted code block away from regrowing, so the guard scans the
// package instead of trusting the fixture. It dynamically locates the
// runtimeProjectTopicNodes builder body and asserts it never appends
// session-kind children, with floors so a broken scan fails loud instead of
// green — plus a call-site floor keeping both production entries wired.
func TestRuntimeTopicProjectionEmitsNoSessionChildRows(t *testing.T) {
	pattern := filepath.Join("*.go")
	files, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob %s: %v", pattern, err)
	}
	if len(files) < 10 {
		t.Fatalf("scan floor: glob found %d go files, the scan pattern is broken", len(files))
	}

	builderMarker := "func (a *App) runtimeProjectTopicNodes"
	callSites := 0
	builderFound := false
	for _, file := range files {
		// Production surface only: this guard's own source text contains the
		// markers below and would self-match.
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		text := string(src)
		callSites += strings.Count(text, "runtimeProjectTopicNodes(")
		start := strings.Index(text, builderMarker)
		if start < 0 {
			continue
		}
		builderFound = true
		rest := text[start:]
		if end := strings.Index(rest, "\nfunc "); end > 0 {
			rest = rest[:end]
		}
		// Floor: the extracted body must be the real builder, not a stub
		// renamer — it still groups snapshots per topic and aggregates the
		// runtime status.
		for _, anchor := range []string{"byTopic", "catalogStateStatus", "node.Running = true", "sessionsByTopic"} {
			if !strings.Contains(rest, anchor) {
				t.Fatalf("builder body floor failed: %s missing %q — the scan pattern went stale, fix the guard", file, anchor)
			}
		}
		if strings.Contains(rest, "node.Children = append(") {
			t.Fatalf("task 352 regression: runtimeProjectTopicNodes emits per-session child rows again (file stems + \"previously\" meta return); merge copies into the parent row instead")
		}
	}
	if !builderFound {
		t.Fatalf("builder %q not found in any go file — the scan pattern is broken", builderMarker)
	}
	// Definition site + the two production entries (runtimeOnlyProjectTopics
	// chain and projectTreeRuntimeTopics). A renamed/removed entry must be a
	// conscious contract change, not a silent one.
	if callSites < 3 {
		t.Fatalf("runtimeProjectTopicNodes referenced %d times, want >=3 (definition + 2 production entries)", callSites)
	}
}
