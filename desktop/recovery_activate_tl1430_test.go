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
	// tl1430b: the fork uses the real recovery naming the versions panel
	// scans for (recoveryCopiesByMain keys on -recovery-<hex> filenames); a
	// plain "fork.jsonl" never enters the candidate list and the table-side
	// nails would test an empty shape.
	fork := filepath.Join(dir, "root-recovery-7fa1f7e5deadbeef.jsonl")
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
	// tl1415c B3: the active-work refusal carries the stable machine code so
	// the frontend maps it to the localized human copy (cause stays verbatim
	// after the code).
	if !strings.Contains(err.Error(), recoveryActiveWorkCode) {
		t.Fatalf("active-work refusal %q lacks the machine code prefix %q", err, recoveryActiveWorkCode)
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

// tl1430b return-fix end-to-end nails: after a switch reports success (the
// receipt the user saw), BOTH downstream reads must follow the new main line —
// the reopen target (catalog representative after the next reconcile) and the
// panel's own selected-row data source (ListRecoveryChains). The package-78
// field report: receipt flashed, main row unchanged, reopen unchanged — the
// adoption landed in meta but never reached the projection layer.
func TestSetActiveSessionVersionRepresentativeAndChainSelectedFollowSwitch(t *testing.T) {
	isolateDesktopUserDirs(t)
	root, fork, catalog := activateTestSetup(t,
		[]string{"shared question", "shared answer", "root unique", "root answer"},
		[]string{"shared question", "shared answer", "fork unique", "fork answer", "fork tail", "fork answer 2"})
	app := activateTestApp(catalog)

	if err := app.SetActiveSessionVersion(RecoveryPreferenceRequest{Path: fork}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	// The next background scan reconciles the directory again; the reopen
	// target must survive it.
	if err := catalog.ReconcileDirectory(context.Background(), sessioncatalog.DirectoryTarget{Path: filepath.Dir(root), Scope: "global"}); err != nil {
		t.Fatal(err)
	}
	topic, ok, err := catalog.GetTopic(context.Background(), sessioncatalog.TopicKey{Scope: "global", TopicID: "topic"})
	if err != nil || !ok {
		t.Fatalf("topic after switch: ok=%v err=%v", ok, err)
	}
	if rep := strings.TrimSpace(topic.RepresentativePath); !sameRecoveryLineagePath(rep, fork) {
		t.Fatalf("reopen target after switch+rescan = %q, want the adopted fork %q", rep, fork)
	}
	if path := app.catalogSessionPathForTopic("global", "", "topic"); !sameRecoveryLineagePath(path, fork) {
		t.Fatalf("catalogSessionPathForTopic after switch = %q, want the adopted fork", path)
	}

	// Ring 2: the panel's own table data source. Selected must move off the
	// scan origin (root) onto the adopted line.
	set, err := app.ListRecoveryChains(root)
	if err != nil {
		t.Fatal(err)
	}
	selected := ""
	for _, chain := range set.Chains {
		if chain.Selected {
			selected = chain.Path
		}
	}
	if !sameRecoveryLineagePath(selected, fork) {
		t.Fatalf("chain selected row after switch = %q, want the adopted fork (main row must flip in place)", selected)
	}
}

// The field-reported conversation ("人类专属调研员1") carries damage history.
// A damaged tail must not silently void the user's explicit adoption at ANY
// downstream read. Two rounds of field reports pin the two halves:
//   - tl1430b: preferred without a digest is refused by RecoveryPreferenceCurrent
//     at promote time (receipt fired, nothing moved);
//   - tl1430-2 (package 79): the digest was written, but the catalog projection
//     re-proved the choice through the strict snapshot
//     (LoadSessionContentSnapshot fail-closes on damaged/dirty logs), dropped
//     the flag, and representative/reopen kept the old line.
// The write side, the promote check, AND the projection must accept the same
// tolerant-replay proof. Hard assertions: any half slipping fails here.
func TestSetActiveSessionVersionAdoptsForkWithDamagedTail(t *testing.T) {
	isolateDesktopUserDirs(t)
	// Root stays the LONGER line on purpose: with a longer fork the linear-
	// chain rule would land the representative on the fork regardless of the
	// preference, masking the promote path under test. The field shape is a
	// shorter valuable fork the user deliberately promotes over a longer main.
	root, fork, catalog := activateTestSetup(t,
		[]string{"shared question", "shared answer", "root unique", "root answer", "root tail", "root answer 2"},
		[]string{"shared question", "shared answer", "fork unique", "fork answer"})
	// A torn trailing record after the healthy prefix, the way an interrupted
	// writer leaves the log.
	f, err := os.OpenFile(store.SessionEventLog(fork), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{\"schema_version\":1,\"type\":\"append\",\"trunc"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if _, err := agent.LoadSession(fork); err == nil {
		// Load may succeed with a damaged marker; it must at least flag it.
		t.Log("damaged fork loaded without error (tolerant loader)")
	}

	app := activateTestApp(catalog)
	if err := app.SetActiveSessionVersion(RecoveryPreferenceRequest{Path: fork}); err != nil {
		t.Fatalf("activate damaged fork: %v", err)
	}
	meta, ok, err := agent.LoadBranchMeta(fork)
	if err != nil || !ok {
		t.Fatalf("fork meta after switch: ok=%v err=%v", ok, err)
	}
	if !meta.RecoveryPreferred || strings.TrimSpace(meta.RecoveryPreferredDigest) == "" {
		t.Fatalf("damaged fork preference after switch: preferred=%v digestEmpty=%v, want flag+digest (write side)", meta.RecoveryPreferred, meta.RecoveryPreferredDigest == "")
	}

	// Ring 4 (tl1430-2): the projection must keep the adoption. The next
	// background scan reconciles the directory again; every read below runs on
	// that reconciled catalog.
	if err := catalog.ReconcileDirectory(context.Background(), sessioncatalog.DirectoryTarget{Path: filepath.Dir(root), Scope: "global"}); err != nil {
		t.Fatal(err)
	}
	record, ok, err := catalog.GetSession(context.Background(), fork)
	if err != nil || !ok {
		t.Fatalf("fork catalog row after reconcile: ok=%v err=%v", ok, err)
	}
	// The projection persists the preference as filing role (the bare scan-time
	// flag is consumed in-memory by promoteCanonicalLeaves), so preferred+
	// canonical IS the persisted form of the explicit choice.
	if record.RecoveryRole != sessioncatalog.RecoveryRolePreferred || !record.RecoveryCanonical {
		t.Fatalf("catalog projection role after damaged-fork switch = %s canonical=%v, want preferred+canonical (projection dropped the tolerant-proof preference): %+v", record.RecoveryRole, record.RecoveryCanonical, record)
	}
	topic, ok, err := catalog.GetTopic(context.Background(), sessioncatalog.TopicKey{Scope: "global", TopicID: "topic"})
	if err != nil || !ok {
		t.Fatalf("topic: ok=%v err=%v", ok, err)
	}
	if rep := strings.TrimSpace(topic.RepresentativePath); !sameRecoveryLineagePath(rep, fork) {
		t.Fatalf("representative after damaged-fork switch = %q (fork=%q) — adoption voided downstream", rep, fork)
	}

	// Ring 1 surface: the versions table the user watches. State and mainline
	// row must move in place, immediately, from the reconciled catalog.
	view := app.GetRecoveryLineage(ProjectTopicKey{Scope: "global", TopicID: "topic", Path: fork})
	if view.State != "preferred" {
		t.Fatalf("lineage state after damaged-fork switch = %+v, want preferred", view)
	}
	for _, member := range view.Members {
		if member.Canonical != sameRecoveryLineagePath(member.Path, fork) {
			t.Fatalf("canonical member after damaged-fork switch = %+v, want only the adopted fork", member)
		}
	}
}

// tl1430-2 reopen nail: the same switch, observed the way the user checks it -
// a brand-new app instance (new tab or full restart) must land on the adopted
// line, both at the open-target resolution (the first decision of every topic
// open) and at the actual transcript a fresh controller loads. The stale-path
// shape (sidebar still carrying the old row's path) is the one that shipped in
// package 79.
func TestSetActiveSessionVersionDamagedForkReopenFollowsAdoption(t *testing.T) {
	isolateDesktopUserDirs(t)
	root, fork, catalog := activateTestSetup(t,
		[]string{"shared question", "shared answer", "root unique", "root answer", "root tail", "root answer 2"},
		[]string{"shared question", "shared answer", "fork unique", "fork answer"})
	f, err := os.OpenFile(store.SessionEventLog(fork), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{\"schema_version\":1,\"type\":\"append\",\"trunc"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	app := activateTestApp(catalog)
	if err := app.SetActiveSessionVersion(RecoveryPreferenceRequest{Path: fork}); err != nil {
		t.Fatalf("activate damaged fork: %v", err)
	}
	if err := catalog.ReconcileDirectory(context.Background(), sessioncatalog.DirectoryTarget{Path: filepath.Dir(root), Scope: "global"}); err != nil {
		t.Fatal(err)
	}

	// A fresh instance: empty tabs, same catalog - the restart shape.
	reopened := activateTestApp(catalog)
	if _, sessionPath := reopened.resolveOpenTopicSessionPath("global", "", root); !sameRecoveryLineagePath(sessionPath, fork) {
		t.Fatalf("reopen (stale sidebar path %q) resolved to %q, want the adopted fork", root, sessionPath)
	}
	// The startup-restore retarget (retargetOpenTabsToContinuations) asks the
	// same predicate; the restored old-mainline tab must fold onto the fork.
	if continued := reopened.continuePathForOpen(root); !sameRecoveryLineagePath(continued, fork) {
		t.Fatalf("continuation for restored tab %q = %q, want the adopted fork", root, continued)
	}
	// What a fresh controller instance actually loads on that resolution.
	// The replay counts the seeded system message: 1 system + 4 fork contents.
	// The torn tail must not leak extra messages into the readable prefix.
	loaded, err := agent.LoadSession(fork)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := loaded.Snapshot()
	if len(snapshot) != 5 || snapshot[len(snapshot)-1].Content != "fork answer" {
		t.Fatalf("reopen replay = %d messages (last %q), want the adopted fork's readable prefix (system+4) ending at %q", len(snapshot), snapshot[len(snapshot)-1].Content, "fork answer")
	}
}

// Clean-fork control for the damaged variant: a healthy shorter fork adopted
// over a longer main moves the representative through the preference-promote
// path alone. If this fails while the damaged variant fails identically, the
// break is in the promote chain, not the damage gate.
func TestSetActiveSessionVersionAdoptsCleanShortFork(t *testing.T) {
	isolateDesktopUserDirs(t)
	root, fork, catalog := activateTestSetup(t,
		[]string{"shared question", "shared answer", "root unique", "root answer", "root tail", "root answer 2"},
		[]string{"shared question", "shared answer", "fork unique", "fork answer"})
	app := activateTestApp(catalog)
	if err := app.SetActiveSessionVersion(RecoveryPreferenceRequest{Path: fork}); err != nil {
		t.Fatalf("activate clean short fork: %v", err)
	}
	if err := catalog.ReconcileDirectory(context.Background(), sessioncatalog.DirectoryTarget{Path: filepath.Dir(root), Scope: "global"}); err != nil {
		t.Fatal(err)
	}
	topic, ok, err := catalog.GetTopic(context.Background(), sessioncatalog.TopicKey{Scope: "global", TopicID: "topic"})
	if err != nil || !ok {
		t.Fatalf("topic: ok=%v err=%v", ok, err)
	}
	if rep := strings.TrimSpace(topic.RepresentativePath); !sameRecoveryLineagePath(rep, fork) {
		t.Fatalf("clean short fork: representative = %q, want the adopted fork (promote chain broken independent of damage)", rep)
	}
}
