package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/sessioncatalog"
	"reasonix/internal/store"
)

// tl1430 acceptance nails: every activation path must either really switch or
// fail loudly - a silent no-op is forbidden. The three new pins ride the
// adoption test: events stay append-only (byte prefixes unchanged), the
// head_switch marker lands on the adopted transcript, and the projection
// (replayed transcript) matches the head the switch installed.

func activateTestSave(t *testing.T, path string, messages ...string) {
	t.Helper()
	session := agent.NewSession("system")
	for index, message := range messages {
		role := provider.RoleUser
		if index%2 == 1 {
			role = provider.RoleAssistant
		}
		session.Add(provider.Message{Role: role, Content: message})
	}
	if err := session.Save(path); err != nil {
		t.Fatal(err)
	}
}

func activateTestEventLog(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(store.SessionEventLog(path))
	if err != nil {
		t.Fatalf("read event log of %s: %v", path, err)
	}
	return data
}

func activateTestSetup(t *testing.T, rootMessages, forkMessages []string) (string, string, *sessioncatalog.Catalog) {
	t.Helper()
	dir := schemaOneTempDir(t)
	catalog, err := sessioncatalog.Open(context.Background(), sessioncatalog.Options{InMemory: true, DisableRepair: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })
	root := filepath.Join(dir, "root.jsonl")
	fork := filepath.Join(dir, "fork.jsonl")
	activateTestSave(t, root, rootMessages...)
	activateTestSave(t, fork, forkMessages...)
	created := time.UnixMilli(100)
	for path, meta := range map[string]agent.BranchMeta{
		root: {ID: "root", Scope: "global", TopicID: "topic", TopicTitle: "Topic", CreatedAt: created, UpdatedAt: created},
		fork: {ID: "fork", Scope: "global", TopicID: "topic", TopicTitle: "Topic", Recovered: true, ParentID: "root", RecoveryDepth: 1, CreatedAt: created.Add(time.Second), UpdatedAt: created.Add(time.Second)},
	} {
		if err := agent.SaveBranchMetaPreserveUpdated(path, meta); err != nil {
			t.Fatal(err)
		}
	}
	if err := catalog.ReconcileDirectory(context.Background(), sessioncatalog.DirectoryTarget{Path: dir, Scope: "global"}); err != nil {
		t.Fatal(err)
	}
	return root, fork, catalog
}

func activateTestApp(catalog *sessioncatalog.Catalog) *App {
	app := &App{tabs: map[string]*WorkspaceTab{}, detachedSessions: map[string]*WorkspaceTab{}}
	app.sessionCatalog.Store(catalog)
	return app
}

// tl1430 A1: the tl1280 leftover branch used to drop the head id and report
// success; a stale caller must now get an explicit refusal and no state change.
func TestSetActiveSessionVersionRefusesHeadIDExplicitly(t *testing.T) {
	isolateDesktopUserDirs(t)
	root, fork, catalog := activateTestSetup(t,
		[]string{"shared question", "shared answer", "root unique", "root answer"},
		[]string{"shared question", "shared answer", "fork unique", "fork answer"})
	app := activateTestApp(catalog)

	rootLog := activateTestEventLog(t, root)
	forkLog := activateTestEventLog(t, fork)

	err := app.SetActiveSessionVersion(RecoveryPreferenceRequest{Scope: "global", TopicID: "topic", Path: fork, HeadID: "leftover-head"})
	if err == nil || !strings.Contains(err.Error(), "retired") {
		t.Fatalf("head-id activation error = %v, want an explicit retired refusal", err)
	}
	if !bytesEqual(activateTestEventLog(t, root), rootLog) || !bytesEqual(activateTestEventLog(t, fork), forkLog) {
		t.Fatal("a refused head-id activation must not touch either event log")
	}
	if meta, ok, _ := agent.LoadBranchMeta(fork); !ok || meta.RecoveryPreferred {
		t.Fatalf("refused activation still recorded a preference: %+v ok=%v", meta, ok)
	}
}

// tl1430 A2/A5/A6/A7/A8: activating a recovery copy with no scope/topicId
// resolves the owning topic from the catalog filing (direction D), flips the
// canonical main line to the copy, appends one head_switch marker on the
// adopted transcript, and leaves every prior event byte-identical on both
// sides. Reopen reads follow the persisted preference.
func TestSetActiveSessionVersionAdoptsCopyResolvingTopicFromFiling(t *testing.T) {
	isolateDesktopUserDirs(t)
	root, fork, catalog := activateTestSetup(t,
		[]string{"shared question", "shared answer", "root unique", "root answer"},
		[]string{"shared question", "shared answer", "fork unique", "fork answer", "fork tail", "fork answer 2"})
	app := activateTestApp(catalog)

	rootLog := activateTestEventLog(t, root)
	forkLog := activateTestEventLog(t, fork)
	rootBefore, err := agent.LoadSession(root)
	if err != nil {
		t.Fatal(err)
	}
	rootMessagesBefore := len(rootBefore.Snapshot())

	// The versions panel is path-driven: no scope, no topicId.
	if err := app.SetActiveSessionVersion(RecoveryPreferenceRequest{Path: fork}); err != nil {
		t.Fatalf("activate copy: %v", err)
	}

	// Pin 1 - append-only: both event logs keep their byte prefixes.
	forkAfter := activateTestEventLog(t, fork)
	if !bytesEqual(activateTestEventLog(t, root), rootLog) {
		t.Fatal("the old main line's event log changed during a switch")
	}
	if len(forkAfter) <= len(forkLog) || !bytesHasPrefix(forkAfter, forkLog) {
		t.Fatal("the adopted transcript's event log lost or rewrote its prior bytes")
	}

	// Pin 2 - head_switch marker on the adopted log: from/to/authoring side.
	switchLine := findHeadSwitchRecord(t, forkAfter)
	if switchLine.headFrom == "" || !sameRecoveryLineagePath(switchLine.headFrom, root) || !sameRecoveryLineagePath(switchLine.headTo, fork) {
		t.Fatalf("head_switch record = %+v, want from=%s to=%s", switchLine, root, fork)
	}

	// Pin 3 - projection follows the head: replaying the adopted file yields
	// exactly the messages it had before the switch (the marker adds none),
	// and the old main line is untouched.
	forkReplay, err := agent.LoadSession(fork)
	if err != nil {
		t.Fatal(err)
	}
	if got := forkReplay.Snapshot(); len(got) != 7 || got[6].Content != "fork answer 2" {
		t.Fatalf("adopted transcript after switch = %d messages, want the 6 pre-switch messages plus system untouched", len(got))
	}
	rootAfter, err := agent.LoadSession(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rootAfter.Snapshot()) != rootMessagesBefore {
		t.Fatal("the old main line's replayed transcript changed during a switch")
	}

	// Persisted reopen semantics: the preference moved and the lineage now
	// presents the fork as canonical.
	if meta, ok, _ := agent.LoadBranchMeta(fork); !ok || !meta.RecoveryPreferred {
		t.Fatalf("fork meta after switch = %+v ok=%v, want a persisted preference", meta, ok)
	}
	view := app.GetRecoveryLineage(ProjectTopicKey{Scope: "global", TopicID: "topic", Path: fork})
	if view.State != "preferred" {
		t.Fatalf("lineage state after switch = %+v, want preferred", view)
	}
	for _, member := range view.Members {
		if member.Canonical != sameRecoveryLineagePath(member.Path, fork) {
			t.Fatalf("canonical member after switch = %+v, want only the adopted fork", member)
		}
	}

	// Repeating the switch on the incumbent main line must say so instead of
	// pretending to switch again.
	err = app.SetActiveSessionVersion(RecoveryPreferenceRequest{Path: fork})
	if err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("duplicate switch error = %v, want an explicit already-main-line refusal", err)
	}
}

// tl1430 A3 hot-switch: an idle tab rehydrates onto the adopted line in place,
// keeping tab identity, without any close/reopen.
func TestSetActiveSessionVersionHotSwitchesIdleTab(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := filepath.Dir(mustActivateSessionDir(t))
	root := filepath.Join(dir, "root.jsonl")
	fork := filepath.Join(dir, "fork.jsonl")
	activateTestSave(t, root, "shared question", "shared answer", "root unique", "root answer")
	activateTestSave(t, fork, "shared question", "shared answer", "fork unique", "fork answer")
	created := time.UnixMilli(100)
	for path, meta := range map[string]agent.BranchMeta{
		root: {ID: "root", Scope: "global", TopicID: "topic", TopicTitle: "Topic", CreatedAt: created, UpdatedAt: created},
		fork: {ID: "fork", Scope: "global", TopicID: "topic", TopicTitle: "Topic", Recovered: true, ParentID: "root", RecoveryDepth: 1, CreatedAt: created.Add(time.Second), UpdatedAt: created.Add(time.Second)},
	} {
		if err := agent.SaveBranchMetaPreserveUpdated(path, meta); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := sessioncatalog.Open(context.Background(), sessioncatalog.Options{InMemory: true, DisableRepair: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })
	if err := catalog.ReconcileDirectory(context.Background(), sessioncatalog.DirectoryTarget{Path: dir, Scope: "global"}); err != nil {
		t.Fatal(err)
	}

	exec := agent.New(nil, nil, agent.NewSession(""), agent.Options{}, event.Discard)
	ctrl := control.New(control.Options{Executor: exec, SessionDir: dir, SessionPath: root, Label: "main"})
	defer ctrl.Close()
	app := NewApp()
	tab := &WorkspaceTab{
		ID: "topic-tab", Scope: "global", WorkspaceRoot: globalTabWorkspaceRoot(),
		TopicID: "topic", SessionPath: root, Ctrl: ctrl, Ready: true,
		sink: &tabEventSink{tabID: "topic-tab", app: app}, disabledMCP: map[string]ServerView{},
	}
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	app.sessionCatalog.Store(catalog)

	if err := app.SetActiveSessionVersion(RecoveryPreferenceRequest{Path: fork}); err != nil {
		t.Fatalf("hot switch: %v", err)
	}
	if tab.Ctrl == ctrl {
		t.Fatal("hot switch kept the old controller binding instead of rebinding the tab")
	}
	if got := tab.Ctrl.SessionPath(); !sameRecoveryLineagePath(got, fork) {
		t.Fatalf("idle tab session path after switch = %q, want %q", got, fork)
	}
	switchLine := findHeadSwitchRecord(t, activateTestEventLog(t, fork))
	if switchLine.headTo == "" || !sameRecoveryLineagePath(switchLine.headFrom, root) {
		t.Fatalf("head_switch after hot switch = %+v, want from=%s", switchLine, root)
	}
}

// tl1430 A4: a tab with work in flight is refused with an explicit message -
// no silent swap, no state change on either side.
func TestSetActiveSessionVersionRefusesRunningTabExplicitly(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := desktopSessionDir(globalTabWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "root.jsonl")
	fork := filepath.Join(dir, "fork.jsonl")
	activateTestSave(t, root, "shared question", "shared answer", "root unique", "root answer")
	activateTestSave(t, fork, "shared question", "shared answer", "fork unique", "fork answer")
	created := time.UnixMilli(100)
	for path, meta := range map[string]agent.BranchMeta{
		root: {ID: "root", Scope: "global", TopicID: "topic", TopicTitle: "Topic", CreatedAt: created, UpdatedAt: created},
		fork: {ID: "fork", Scope: "global", TopicID: "topic", TopicTitle: "Topic", Recovered: true, ParentID: "root", RecoveryDepth: 1, CreatedAt: created.Add(time.Second), UpdatedAt: created.Add(time.Second)},
	} {
		if err := agent.SaveBranchMetaPreserveUpdated(path, meta); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := sessioncatalog.Open(context.Background(), sessioncatalog.Options{InMemory: true, DisableRepair: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })
	if err := catalog.ReconcileDirectory(context.Background(), sessioncatalog.DirectoryTarget{Path: dir, Scope: "global"}); err != nil {
		t.Fatal(err)
	}

	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	ctrl := control.New(control.Options{Runner: runner, SessionDir: dir, SessionPath: root, Label: "main", Sink: event.Discard})
	defer ctrl.Close()
	app := NewApp()
	tab := &WorkspaceTab{
		ID: "topic-tab", Scope: "global", WorkspaceRoot: globalTabWorkspaceRoot(),
		TopicID: "topic", SessionPath: root, Ctrl: ctrl, Ready: true,
		sink: &tabEventSink{tabID: "topic-tab", app: app}, disabledMCP: map[string]ServerView{},
	}
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	app.sessionCatalog.Store(catalog)

	ctrl.Submit("keep running")
	<-runner.started
	defer func() { runner.release <- struct{}{} }()

	forkLog := activateTestEventLog(t, fork)
	err = app.SetActiveSessionVersion(RecoveryPreferenceRequest{Path: fork})
	if err == nil || !strings.Contains(err.Error(), "working") {
		t.Fatalf("running-tab switch error = %v, want an explicit busy refusal", err)
	}
	if !sameRecoveryLineagePath(tab.Ctrl.SessionPath(), root) {
		t.Fatalf("running tab was switched anyway: %q", tab.Ctrl.SessionPath())
	}
	if !bytesEqual(activateTestEventLog(t, fork), forkLog) {
		t.Fatal("a refused switch still appended to the adopted log")
	}
	if meta, ok, _ := agent.LoadBranchMeta(fork); !ok || meta.RecoveryPreferred {
		t.Fatalf("refused switch still recorded a preference: %+v", meta)
	}
}

type headSwitchRecord struct {
	headFrom string
	headTo   string
}

// findHeadSwitchRecord scans the raw event log lines for the tl1430 marker.
func findHeadSwitchRecord(t *testing.T, log []byte) headSwitchRecord {
	t.Helper()
	for _, line := range strings.Split(string(log), "\n") {
		if !strings.Contains(line, `"type":"head_switch"`) {
			continue
		}
		var rec struct {
			HeadFrom string `json:"head_from"`
			HeadTo   string `json:"head_to"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode head_switch line %q: %v", line, err)
		}
		return headSwitchRecord{headFrom: rec.HeadFrom, headTo: rec.HeadTo}
	}
	t.Fatal("no head_switch record found in the event log")
	return headSwitchRecord{}
}

func bytesEqual(left, right []byte) bool {
	return string(left) == string(right)
}

func bytesHasPrefix(data, prefix []byte) bool {
	return len(data) >= len(prefix) && string(data[:len(prefix)]) == string(prefix)
}

// mustActivateSessionDir returns the session dir the hot-switch test anchors
// its transcripts in, creating it first.
func mustActivateSessionDir(t *testing.T) string {
	t.Helper()
	dir := desktopSessionDir(globalTabWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}
