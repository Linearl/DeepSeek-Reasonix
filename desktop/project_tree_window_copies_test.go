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
// Task 757 narrowed the collapse (user decision, reversing the 04b8d02ef
// "test is stale" adjudication, per audit report 94 方案 A): only the
// controller-less copy stays collapsed — the active record still owns the
// single row here because it is the only controller-holding record. Active
// sessions with their own controllers each get their own row (see
// tabs_runtime_status_test.go).
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

// Task 757 (user decision, audit report 94 方案 A): the collapse only covers
// controller-less copies. When a topic holds TWO controller-holding active
// sessions plus a stale copy, the actives each get their own row and the stale
// copy must neither become a third row nor leak its leftover activity status
// onto the neutral parent — the 352 noise scenario must not regress in its
// multi-active form either.
func TestRuntimeTopicProjectionKeepsControllerlessCopiesCollapsedBesideActiveRows(t *testing.T) {
	app := NewApp()
	freshPath := "/sessions/20260928-042815.686047-fresh.jsonl"
	secondPath := "/sessions/20260928-050000.000000-second.jsonl"
	stalePath := "/sessions/20260928-031402.112233-origin.jsonl"
	app.tabs["live"] = &WorkspaceTab{
		ID: "live", Scope: "global", TopicID: "topic-window",
		TopicTitle: "专家团复查", SessionPath: freshPath,
		Ctrl: &activationStubController{sessionPath: freshPath},
	}
	app.tabs["second"] = &WorkspaceTab{
		ID: "second", Scope: "global", TopicID: "topic-window",
		TopicTitle: "专家团复查", SessionPath: secondPath,
		Ctrl: &activationStubController{sessionPath: secondPath},
	}
	app.detachedSessions["stale"] = &WorkspaceTab{
		Scope: "global", TopicID: "topic-window",
		TopicTitle: "专家团复查", SessionPath: stalePath,
		ActivityStatus: topicStatusWaitingConfirmation,
	}

	snapshot := app.GetProjectTreeRuntimeSnapshot()
	if len(snapshot.Topics) != 1 {
		t.Fatalf("runtime snapshot topics = %d, want 1 logical row", len(snapshot.Topics))
	}
	node := snapshot.Topics[0].Node
	if len(node.Children) != 2 {
		t.Fatalf("active sessions emitted %d child rows (%#v), want exactly the two controller-holding ones", len(node.Children), node.Children)
	}
	for _, child := range node.Children {
		if sessionRuntimeKey(child.SessionPath) == sessionRuntimeKey(stalePath) {
			t.Fatalf("controller-less copy %s expanded as a session row: %#v", stalePath, node.Children)
		}
		if !child.Running || child.Status != topicStatusThinking {
			t.Fatalf("active session row %s = running:%v status:%q, want running/thinking", child.SessionPath, child.Running, child.Status)
		}
	}
	// The stale copy carries a leftover waiting_confirmation: the parent stays
	// neutral in the multi-active form (statuses live on the child rows).
	if node.Status != "" || node.Running {
		t.Fatalf("parent merged child/stale runtime statuses: %+v", node)
	}
	if !node.Open {
		t.Fatalf("parent open = false, want the open tabs aggregated up")
	}

	// One contract, every entry: the catalog-merge path splits the actives and
	// keeps the copy collapsed identically.
	nodes := app.runtimeOnlyProjectTopics("global", "")
	if len(nodes) != 1 {
		t.Fatalf("catalog-merge entry = %d rows, want 1 logical row", len(nodes))
	}
	if len(nodes[0].Children) != 2 {
		t.Fatalf("catalog-merge entry children = %d (%#v), want the 2 active session rows", len(nodes[0].Children), nodes[0].Children)
	}
}

// TestRuntimeTopicProjectionCollapsesOnlyControllerlessCopies is the
// source-level guard for the narrowed 352/757 contract (following the task 299
// dynamic-scan paradigm): the runtime projection collapses ONLY the
// controller-less window copies, while controller-holding active sessions each
// emit their own child row (the split contract in tabs_runtime_status_test.go).
// The scan dynamically locates the runtimeProjectTopicNodes builder body and
// asserts the narrowing structure — the grouping by controller ownership, the
// multi-active split threshold, child emission from the active-only loop, and
// exactly one emission site — so the contract can neither regrow into "never
// split" (352 over-generalization) nor loosen into "expand everything" (the
// stem-row noise), with floors so a broken scan fails loud instead of green,
// plus a call-site floor keeping both production entries wired.
func TestRuntimeTopicProjectionCollapsesOnlyControllerlessCopies(t *testing.T) {
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
		// 757 narrowing: the grouping by controller ownership must be present,
		// the multi-active split threshold intact, and child rows emitted from
		// the active-only loop.
		for _, anchor := range []string{"session.ctrl != nil", "len(active) >= 2", "for _, session := range active"} {
			if !strings.Contains(rest, anchor) {
				t.Fatalf("task 757 narrowing failed: %s missing %q — the controller-less collapse regrew into never-split or the split loosened; fix the builder", file, anchor)
			}
		}
		// Exactly one child-emission site, and it sits inside the active-only
		// loop: any additional site can expand controller-less copies as stem
		// rows again.
		if appends := strings.Count(rest, "node.Children = append("); appends != 1 {
			t.Fatalf("task 757 regression: %d child-emission sites in runtimeProjectTopicNodes, want exactly 1 (the active-only loop)", appends)
		}
		appendIdx := strings.Index(rest, "node.Children = append(")
		if idx := strings.Index(rest, "for _, session := range active"); idx < 0 || idx > appendIdx {
			t.Fatalf("task 757 regression: child rows are no longer emitted from the active-only loop — controller-less copies would expand as stem rows again")
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
