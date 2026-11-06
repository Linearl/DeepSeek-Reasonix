package main

// tl1415c acceptance ② nails: the panel's main-line verdict and the switch
// gate's verdict must agree on the SAME fixture, and the fixture must be the
// exact shape that split them in the field — the open tab's transcript
// (authority #1) differing from the catalog's canonical/representative marker
// (the folding artifact the switch gate must ignore).
//
// Field incident (user复测, package 80): the tab sat on a recovery copy while
// the catalog still named the scan origin canonical. The gate (tab path +
// explicit preference) answered "the copy is the main line"; the panel's chain
// inventory (meta preferred → scan-origin fallback) answered "the scan origin
// is the main line". The user saw the real main line BELOW the marked one and
// asked "下面那个是主线为何还能切换到此版本".
//
// Fix under test: ListRecoveryChains now resolves Selected through
// recoveryAuthorityMainPath — the same resolver order the already-main gate
// reads (tab path → explicit preference) — so one authority feeds both sides.
//
// Pins (same fixture drives both sides):
//  ① panel verdict: Selected lands on the tab's line (the copy), not on the
//    catalog-canonical scan origin;
//  ② gate verdict: switching TO the tab's line is refused already-main (the
//    catalog canonical marker must NOT trigger a false refusal);
//  ③ gate verdict: switching to the scan-origin row — the row the OLD panel
//    used to mark as main — must SUCCEED (it is not the authority main line);
//  ④ after the switch both sides re-read the same new line (Selected follows).

import (
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
)

func TestRecoveryChainSelectedMatchesSwitchGateOnTabVsCanonical(t *testing.T) {
	isolateDesktopUserDirs(t)
	root, fork, catalog := activateTestSetup(t,
		[]string{"shared question", "shared answer", "root unique", "root answer"},
		[]string{"shared question", "shared answer", "fork unique", "fork answer"})
	app := activateTestApp(catalog)

	// The split shape: a tab holds this topic with its CURRENT transcript on
	// the recovery copy. No explicit preference exists anywhere, so the OLD
	// panel derivation fell back to the scan origin while the gate read the
	// tab — the two-verdict split.
	dir := filepath.Dir(root)
	exec := agent.New(nil, nil, agent.NewSession(""), agent.Options{}, event.Discard)
	ctrl := control.New(control.Options{Executor: exec, SessionDir: dir, SessionPath: fork, Label: "fork-line"})
	defer ctrl.Close()
	tab := &WorkspaceTab{
		ID: "topic-tab", Scope: "global", WorkspaceRoot: globalTabWorkspaceRoot(),
		TopicID: "topic", SessionPath: fork, Ctrl: ctrl, Ready: true,
		sink: &tabEventSink{tabID: "topic-tab", app: app}, disabledMCP: map[string]ServerView{},
	}
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	// ① Panel verdict: the chain inventory must mark the tab's line as the
	// selected/main row. Before tl1415c this marked the scan origin.
	set, err := app.ListRecoveryChains(root)
	if err != nil {
		t.Fatal(err)
	}
	selectedByPath := map[string]bool{}
	for _, chain := range set.Chains {
		selectedByPath[chain.Path] = chain.Selected
	}
	if !selectedByPath[fork] {
		t.Fatalf("chain Selected = %v, want the tab's line (%s) selected", selectedByPath, fork)
	}
	if selectedByPath[root] {
		t.Fatalf("the catalog-canonical scan origin (%s) must not be Selected while the tab sits on %s", root, fork)
	}

	// ② Gate verdict on the SAME fixture: the tab's line is already the main
	// line — refused, explicitly, even though the catalog canonical names the
	// other file.
	err = app.SetActiveSessionVersion(RecoveryPreferenceRequest{Path: fork})
	if err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("switch to the tab's line = %v, want an already-main refusal", err)
	}

	// ③ Gate verdict: the row the OLD panel marked as main (the scan origin)
	// is NOT the authority main line — the switch must succeed.
	if err := app.SetActiveSessionVersion(RecoveryPreferenceRequest{Path: root}); err != nil {
		t.Fatalf("switch to the scan-origin row: %v, want success (it is not the authority main line)", err)
	}
	if meta, ok, _ := agent.LoadBranchMeta(root); !ok || !meta.RecoveryPreferred {
		t.Fatalf("root meta after switch ok=%v, want the persisted preference", ok)
	}

	// ④ Both sides re-read the same new line: the tab rehydrated onto it and
	// the chain inventory marks it selected.
	if got := tab.Ctrl.SessionPath(); !sameRecoveryLineagePath(got, root) {
		t.Fatalf("tab session path after switch = %q, want %q", got, root)
	}
	setAfter, err := app.ListRecoveryChains(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, chain := range setAfter.Chains {
		want := sameRecoveryLineagePath(chain.Path, root)
		if chain.Selected != want {
			t.Fatalf("chain %q Selected=%v, want %v after the switch", chain.Path, chain.Selected, want)
		}
	}
}

// tl1415c: with no tab and no explicit preference (the plain schema-1 shape)
// the catalog-filed fallback keeps the old verdict — the scan origin stays the
// selected row. The authority upgrade must not move the neutral case.
func TestRecoveryChainSelectedNeutralShapeUnchanged(t *testing.T) {
	isolateDesktopUserDirs(t)
	root, fork, catalog := activateTestSetup(t,
		[]string{"shared question", "shared answer"},
		[]string{"shared question", "shared answer", "fork unique"})
	app := activateTestApp(catalog)

	set, err := app.ListRecoveryChains(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, chain := range set.Chains {
		want := sameRecoveryLineagePath(chain.Path, root)
		if chain.Selected != want {
			t.Fatalf("neutral shape: chain %q Selected=%v, want %v (scan origin)", chain.Path, chain.Selected, want)
		}
	}
	_ = fork
}

// tl1415c: the authority resolver returns "" for a path the catalog does not
// file, and ListRecoveryChains keeps the tl1430b meta-preferred verdict there —
// the resolver must not invent a verdict for unfiled shapes.
func TestRecoveryAuthorityUnfiledPathFallsBackToMetaPreferred(t *testing.T) {
	isolateDesktopUserDirs(t)
	root, fork, catalog := activateTestSetup(t,
		[]string{"shared question", "shared answer"},
		[]string{"shared question", "shared answer", "fork unique"})
	app := activateTestApp(catalog)

	if got := app.recoveryAuthorityMainPath(catalog, filepath.Join(filepath.Dir(root), "unfiled.jsonl")); got != "" {
		t.Fatalf("authority for an unfiled path = %q, want empty", got)
	}
	// Explicit preference (tl1430b verdict path): after preferring the fork,
	// an authority-less lookup (no tabs) still reports the preferred member.
	if err := app.ChooseRecoveryBranch(RecoveryPreferenceRequest{Scope: "global", TopicID: "topic", Path: fork}); err != nil {
		t.Fatal(err)
	}
	if got := app.recoveryAuthorityMainPath(catalog, root); !sameRecoveryLineagePath(got, fork) {
		t.Fatalf("authority after explicit preference = %q, want %q", got, fork)
	}
}
