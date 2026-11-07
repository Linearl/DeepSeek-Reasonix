package main

// tl1415f package-82 field reversal nails: the panel's Selected main line and
// the open/restore funnel must read one authority, even when the catalog's
// digest-validated preference has been voided by a system rewrite of the
// adopted file.
//
// Field scene (user, 2026-11-07 18:08, package 82): the storage panel marked
// recovery-7fa1f7e5 (11-05, 198 messages) as the conversation's selected main
// line, but the first open from the project tree rendered the 10-18 collab
// main (173 messages). tl1415e wired the funnel to recoveryAuthorityMainPath,
// whose catalog authority is digest-validated at reconcile time
// (agent.RecoveryPreferenceCurrent) — a repair/tolerant-replay/normalizing
// rewrite of the adopted transcript voids the filing while the panel keeps
// rendering the same undigested meta flag as Selected (recovery_chains.go
// preferredRows). With the filing voided, both heuristic branches died too:
// CanonicalSessionPathForTopic needs a preferred/adopted role (diverged in the
// field) and uniqueLinearRecoveryLeaf rejected the rewrite-conflict copy as a
// turns regression (6 < 7, lineage.go) — so the open stayed on the demoted
// main. The restart-restore half stayed stuck for a second reason: the tab
// authority (#1) names the line the restored tab already sits on
// (recoveryRecordBelongsToGroup counts the ordinary root as its own group's
// member), so retargetOpenTabsToContinuations self-answered and never moved.
//
// Fixture shape ("panel=A, funnel-B"): root is the ordinary main line; adopted
// is a rewrite-conflict recovery copy that the user explicitly preferred. The
// preferred digest is drifted against the file content BEFORE the reconcile,
// so the catalog files no preferred member (role diverged) while the meta flag
// persists — the exact split the panel trusts and the funnel must follow. The
// adopted copy keeps fewer turns than its parent, so the linear-leaf heuristic
// stays dead and the meta-flag authority (#3 of
// recoveryAuthorityMainPathForOpen) is the only thing that can answer A.

import (
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/sessioncatalog"
)

func driftedPreferenceFixture(t *testing.T, flag bool) (string, string, *sessioncatalog.Catalog) {
	t.Helper()
	dir := schemaOneTempDir(t)
	catalog, err := sessioncatalog.Open(t.Context(), sessioncatalog.Options{InMemory: true, DisableRepair: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(t.Context()) })
	main := filepath.Join(dir, "root.jsonl")
	adopted := filepath.Join(dir, "root-recovery-7fa1f7e5deadbeef.jsonl")
	// Root: 5 user turns. Adopted: a rewrite-conflict fork that replays the
	// first four messages and continues with its own — 3 user turns before the
	// drift, 4 after. Both shapes stay below the parent's 5, so
	// uniqueLinearRecoveryLeaf rejects the chain as a turns regression exactly
	// like the field.
	activateTestSave(t, main, "r1", "r2", "r3", "r4", "r5", "r6", "r7", "r8", "r9")
	activateTestSave(t, adopted, "r1", "r2", "r3", "r4", "a5", "a6")
	created := time.UnixMilli(100)
	rootMeta := agent.BranchMeta{ID: "root", Scope: "global", TopicID: "topic", TopicTitle: "Topic", CreatedAt: created, UpdatedAt: created}
	adoptedMeta := agent.BranchMeta{ID: agent.BranchID(adopted), Scope: "global", TopicID: "topic", TopicTitle: "Topic", Recovered: true, ParentID: "root", RecoveryDepth: 1, CreatedAt: created.Add(time.Second), UpdatedAt: created.Add(time.Second)}
	if err := agent.SaveBranchMetaPreserveUpdated(main, rootMeta); err != nil {
		t.Fatal(err)
	}
	if err := agent.SaveBranchMetaPreserveUpdated(adopted, adoptedMeta); err != nil {
		t.Fatal(err)
	}
	if flag {
		// The adoption write: flags the adopted copy with the digest of its
		// current (six-message) content and clears every other member.
		if err := agent.SetRecoveryPreferred([]string{main, adopted}, adopted); err != nil {
			t.Fatal(err)
		}
		// Keep the on-disk meta exactly as the adoption write left it.
		stored, ok, err := agent.LoadBranchMeta(adopted)
		if err != nil || !ok {
			t.Fatalf("reload adopted meta after SetRecoveryPreferred: ok=%v err=%v", ok, err)
		}
		adoptedMeta = stored
	}
	if flag {
		// The system rewrite that drifts the digest: a repair/normalize/re-save
		// appends to the adopted transcript (6 → 8 messages) without the user
		// choosing again. The preferred digest now names content the file no
		// longer has — agent.RecoveryPreferenceCurrent goes false at the
		// reconcile below, voiding the filing while the meta flag persists.
		activateTestSave(t, adopted, "r1", "r2", "r3", "r4", "a5", "a6", "a7", "a8")
		if err := agent.SaveBranchMetaPreserveUpdated(adopted, adoptedMeta); err != nil {
			t.Fatal(err)
		}
	}
	if err := catalog.ReconcileDirectory(t.Context(), sessioncatalog.DirectoryTarget{Path: dir, Scope: "global"}); err != nil {
		t.Fatal(err)
	}
	return main, adopted, catalog
}

// ① First open (project-tree/session-row request names the ordinary main
// line): the funnel must land on the panel's Selected member even with the
// catalog preference voided and the linear-leaf heuristic dead.
func TestOpenAuthorityTl1415fFirstOpenFollowsPanelSelectedUnderDigestDrift(t *testing.T) {
	isolateDesktopUserDirs(t)
	main, adopted, catalog := driftedPreferenceFixture(t, true)
	app := openAuthorityApp(t, catalog)

	if got := app.continuePathForOpen(main); !sameRecoveryLineagePath(got, adopted) {
		t.Fatalf("continuePathForOpen(demoted main) = %q, want the panel-selected adopted copy %q (catalog preference voided, meta flag persists)", got, adopted)
	}
	if _, sessionPath := app.resolveOpenTopicSessionPath("global", "", main); !sameRecoveryLineagePath(sessionPath, adopted) {
		t.Fatalf("first open of %q resolved to %q, want the panel-selected adopted copy %q", main, sessionPath, adopted)
	}
}

// ② Restart restore: a restored tab sitting on the demoted main must rebind to
// the panel-selected member. The tab authority must not self-answer here —
// recoveryRecordBelongsToGroup counts the ordinary root as its own group's
// member, so the pre-tl1415f funnel resolved the restored tab's own line as
// the authority and the retarget never fired (field: restart kept the 10-18
// collab copy).
func TestOpenAuthorityTl1415fRestoreRetargetFollowsPanelSelectedUnderDigestDrift(t *testing.T) {
	isolateDesktopUserDirs(t)
	main, adopted, catalog := driftedPreferenceFixture(t, true)
	app := openAuthorityApp(t, catalog)

	tab := &WorkspaceTab{ID: "restore-tab", Scope: "global", WorkspaceRoot: globalWorkspaceRoot(), TopicID: "topic", SessionPath: main}
	app.mu.Lock()
	app.tabs[tab.ID] = tab
	app.mu.Unlock()

	app.retargetOpenTabsToContinuations()
	if !sameRecoveryLineagePath(tab.SessionPath, adopted) {
		t.Fatalf("restored tab stayed on %q, want the panel-selected adopted copy %q", tab.SessionPath, adopted)
	}
}

// ③ Explicitly opening the recovery member stays inspection even when it holds
// the meta flag: the guard only reconciles ordinary (non-recovered) requests,
// so the flag authority must not hijack a History open-a-copy shape.
func TestOpenAuthorityTl1415fExplicitRecoveryMemberOpenStaysPutUnderDigestDrift(t *testing.T) {
	isolateDesktopUserDirs(t)
	main, adopted, catalog := driftedPreferenceFixture(t, true)
	app := openAuthorityApp(t, catalog)

	if got := app.continuePathForOpen(adopted); got != "" {
		t.Fatalf("continuePathForOpen(flagged recovery member) = %q, want keep-put (inspection semantics)", got)
	}
	if _, sessionPath := app.resolveOpenTopicSessionPath("global", "", adopted); !sameRecoveryLineagePath(sessionPath, adopted) {
		t.Fatalf("explicit open of recovery member %q redirected to %q", adopted, sessionPath)
	}
	_ = main
}

// ④ No meta flag anywhere (drift without an adoption): the funnel must not
// invent a verdict — the dead heuristics keep their verdict and the request
// stays put, exactly the no-preference semantics tl1415e ④ pinned.
func TestOpenAuthorityTl1415fNoMetaFlagKeepsRequestPut(t *testing.T) {
	isolateDesktopUserDirs(t)
	main, adopted, catalog := driftedPreferenceFixture(t, false)
	app := openAuthorityApp(t, catalog)

	if got := app.continuePathForOpen(main); got != "" {
		t.Fatalf("continuePathForOpen(main) without any preference = %q, want keep-put (no authority may be invented)", got)
	}
	if got := app.continuePathForOpen(adopted); got != "" {
		t.Fatalf("continuePathForOpen(recovery member) without any preference = %q, want keep-put", got)
	}
}

// ⑤ Same-source acceptance: the funnel's answer equals the row the versions
// panel renders as Selected — the exact property the package-82 reversal
// reported ("panel marks recovery-7fa1f7e5, tab renders the 10-18 collb copy").
func TestOpenAuthorityTl1415fOpenTargetMatchesPanelSelectedRow(t *testing.T) {
	isolateDesktopUserDirs(t)
	main, adopted, catalog := driftedPreferenceFixture(t, true)
	app := openAuthorityApp(t, catalog)

	set, err := app.ListRecoveryChains(main)
	if err != nil {
		t.Fatal(err)
	}
	openTarget := app.continuePathForOpen(main)
	selected := ""
	for _, chain := range set.Chains {
		if chain.Selected {
			selected = chain.Path
			break
		}
	}
	if !sameRecoveryLineagePath(selected, adopted) {
		t.Fatalf("panel Selected row = %q, want the adopted copy %q (meta flag fallback)", selected, adopted)
	}
	if !sameRecoveryLineagePath(openTarget, selected) {
		t.Fatalf("open target %q differs from panel Selected %q — the split the field reported", openTarget, selected)
	}
}
