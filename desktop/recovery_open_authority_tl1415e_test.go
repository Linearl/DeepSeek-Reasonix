package main

// tl1415e acceptance nails: the ordinary open/restore paths land on the
// panel's main-line authority. Retest reversal (coordinator note, 2026-11-07
// 10:15): tl1415c unified the panel's Selected verdict and the switch gate on
// recoveryAuthorityMainPath, but ordinary opens still followed the linear-leaf
// heuristic in continuePathForOpen — the tab ended up rendering the 10-18
// collab copy while the panel marked recovery-7fa1f7e5 as the selected main
// line (user's field scene; the merge note "普通打开/恢复不变" left this gap).
//
// Fixture shape ("authority=A, linear leaf=B"): the preferred member A is
// flagged RecoveryPreferred but is NOT the unique linear leaf; member B sits
// at the end of the single monotonic parent chain, so
// sessioncatalog.OrdinaryContinuePath's heuristic answers B while the panel's
// authority (explicit preference) reads A. Both open (project-tree click →
// resolveOpenTopicSessionPath) and restore (restart restore →
// retargetOpenTabsToContinuations) must land on A; explicit opens of a
// recovery member keep the inspection semantics; no-preference shapes keep
// the heuristic untouched.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/sessioncatalog"
)

func openAuthorityFixture(t *testing.T, preferA bool) (string, string, string, *sessioncatalog.Catalog) {
	t.Helper()
	dir := schemaOneTempDir(t)
	catalog, err := sessioncatalog.Open(context.Background(), sessioncatalog.Options{InMemory: true, DisableRepair: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })
	root := filepath.Join(dir, "root.jsonl")
	// Real recovery naming: recoveryCopiesByMain keys on -recovery-<hex>
	// filenames, and BranchID is the filename stem — the parent chain below
	// rides those ids.
	main := filepath.Join(dir, "root-recovery-7fa1f7e5deadbeef.jsonl")
	leaf := filepath.Join(dir, "root-recovery-b0acfede00000001.jsonl")
	activateTestSave(t, root, "root question", "root answer", "root extra")
	activateTestSave(t, main, "root question", "root answer", "root extra", "main unique", "main reply", "main tail")
	activateTestSave(t, leaf, "root question", "root answer", "root extra", "main unique", "main reply", "main tail", "leaf unique", "leaf reply")
	created := time.UnixMilli(100)
	for path, meta := range map[string]agent.BranchMeta{
		root: {ID: "root", Scope: "global", TopicID: "topic", TopicTitle: "Topic", CreatedAt: created, UpdatedAt: created},
		main: {ID: agent.BranchID(main), Scope: "global", TopicID: "topic", TopicTitle: "Topic", Recovered: true, ParentID: "root", RecoveryDepth: 1, CreatedAt: created.Add(time.Second), UpdatedAt: created.Add(time.Second)},
		leaf: {ID: agent.BranchID(leaf), Scope: "global", TopicID: "topic", TopicTitle: "Topic", Recovered: true, ParentID: agent.BranchID(main), RecoveryDepth: 2, CreatedAt: created.Add(2 * time.Second), UpdatedAt: created.Add(2 * time.Second)},
	} {
		if err := agent.SaveBranchMetaPreserveUpdated(path, meta); err != nil {
			t.Fatal(err)
		}
	}
	if preferA {
		// ChooseRecoveryBranch's own write: clears every member's flag, then
		// flags A with its content digest — the panel's authority #2 reads
		// this through RecoveryPreferenceCurrent at reconcile time.
		if err := agent.SetRecoveryPreferred([]string{root, main, leaf}, main); err != nil {
			t.Fatal(err)
		}
	}
	if err := catalog.ReconcileDirectory(context.Background(), sessioncatalog.DirectoryTarget{Path: dir, Scope: "global"}); err != nil {
		t.Fatal(err)
	}
	return root, main, leaf, catalog
}

func openAuthorityApp(t *testing.T, catalog *sessioncatalog.Catalog) *App {
	t.Helper()
	app := NewApp()
	app.sessionCatalog.Store(catalog)
	t.Cleanup(func() { _ = app.stopSessionCatalog(time.Second) })
	return app
}

// ① The ordinary open (project-tree click funnels through
// resolveOpenTopicSessionPath) lands on the authority member A, not on the
// linear leaf B the heuristic picks.
func TestOpenAuthorityReconcilesOrdinaryOpenToPreferredMember(t *testing.T) {
	isolateDesktopUserDirs(t)
	root, main, leaf, catalog := openAuthorityFixture(t, true)
	app := openAuthorityApp(t, catalog)

	if got := app.continuePathForOpen(root); !sameRecoveryLineagePath(got, main) {
		t.Fatalf("continuePathForOpen(parent) = %q, want the authority member %q (linear leaf %q must not win)", got, main, leaf)
	}
	if _, sessionPath := app.resolveOpenTopicSessionPath("global", "", root); !sameRecoveryLineagePath(sessionPath, main) {
		t.Fatalf("ordinary open of %q resolved to %q, want the authority member %q", root, sessionPath, main)
	}
}

// ② The restore path: retargetOpenTabsToContinuations rebinds restored tabs
// through continuePathForOpen — its controller-less branch writes
// tab.SessionPath directly, so a skeleton restored on the parent lands on the
// authority member, not the linear leaf.
func TestOpenAuthorityReconcilesRestoreRetargetToPreferredMember(t *testing.T) {
	isolateDesktopUserDirs(t)
	root, main, leaf, catalog := openAuthorityFixture(t, true)
	app := openAuthorityApp(t, catalog)

	tab := &WorkspaceTab{ID: "restore-tab", Scope: "global", WorkspaceRoot: globalWorkspaceRoot(), TopicID: "topic", SessionPath: root}
	app.mu.Lock()
	app.tabs[tab.ID] = tab
	app.mu.Unlock()

	app.retargetOpenTabsToContinuations()
	if !sameRecoveryLineagePath(tab.SessionPath, main) {
		t.Fatalf("restored tab rebound to %q, want the authority member %q (linear leaf %q must not win)", tab.SessionPath, main, leaf)
	}
}

// ③ Explicitly opening a recovery member stays inspection: the guard only
// reconciles ordinary (non-recovered) requests, so the History open-a-copy
// shape is not hijacked onto the preferred line.
func TestOpenAuthorityKeepsExplicitRecoveryMemberOpensPut(t *testing.T) {
	isolateDesktopUserDirs(t)
	_, main, leaf, catalog := openAuthorityFixture(t, true)
	app := openAuthorityApp(t, catalog)

	if got := app.continuePathForOpen(leaf); got != "" {
		t.Fatalf("continuePathForOpen(recovery member) = %q, want keep-put (inspection semantics; authority %q must not hijack)", got, main)
	}
	if _, sessionPath := app.resolveOpenTopicSessionPath("global", "", leaf); !sameRecoveryLineagePath(sessionPath, leaf) {
		t.Fatalf("explicit open of recovery member %q redirected to %q", leaf, sessionPath)
	}
}

// ④ No explicit preference, no tab: the heuristic keeps its verdict —
// conversations without a recovery preference keep their exact open
// semantics (the authority must not invent a verdict for this shape).
func TestOpenAuthorityWithoutPreferenceKeepsLinearLeaf(t *testing.T) {
	isolateDesktopUserDirs(t)
	root, main, leaf, catalog := openAuthorityFixture(t, false)
	app := openAuthorityApp(t, catalog)

	if got := app.continuePathForOpen(root); !sameRecoveryLineagePath(got, leaf) {
		t.Fatalf("continuePathForOpen(parent) without preference = %q, want the linear leaf %q (unchanged heuristic; %q holds no preference)", got, leaf, main)
	}
}
