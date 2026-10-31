package main

// tl1290: classic layout allows closing every tab. Closing the last visible
// tab no longer refuses; it removes the tab and then appends a fresh blank
// session tab in its place (browser convention), so the desktop never shows
// an empty tab strip. These tests pin the replenish semantics, the
// consecutive-close single-tab invariant and the untouched non-last close.

import (
	"testing"
)

func TestCloseLastTabReplenishesFreshSessionTab(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	first, err := app.EnsureBlankTab("global", "")
	if err != nil {
		t.Fatalf("EnsureBlankTab: %v", err)
	}

	if err := app.CloseTab(first.ID); err != nil {
		t.Fatalf("CloseTab(last): %v", err)
	}

	tabs := app.ListTabs()
	if len(tabs) != 1 {
		t.Fatalf("after closing the last tab ListTabs length = %d, want exactly 1 replenished tab: %+v", len(tabs), tabs)
	}
	replenished := tabs[0]
	if replenished.ID == first.ID {
		t.Fatal("replenished tab should be a fresh tab, not the closed one")
	}
	if !replenished.Active {
		t.Fatal("replenished tab should be the active tab")
	}
	if replenished.SessionPath == "" {
		t.Fatal("replenished tab should carry a pre-created blank session path so the composer can prompt")
	}

	// Acceptance ②: consecutive closes of the sole tab must not pile up empty
	// tabs — each close replenishes exactly one fresh tab.
	for range 3 {
		prevID := replenished.ID
		if err := app.CloseTab(replenished.ID); err != nil {
			t.Fatalf("consecutive CloseTab(last): %v", err)
		}
		tabs = app.ListTabs()
		if len(tabs) != 1 {
			t.Fatalf("consecutive close left %d tabs, want exactly 1: %+v", len(tabs), tabs)
		}
		if tabs[0].ID == prevID {
			t.Fatal("consecutive close kept the closed tab instead of replenishing")
		}
		replenished = tabs[0]
	}

	// Acceptance ③: the replenished surface is a normal persisted tab, so the
	// restart restore path sees exactly one active replenished tab.
	saved := loadTabsFile()
	if len(saved.Tabs) != 1 || saved.Tabs[0].ID != replenished.ID {
		t.Fatalf("persisted tabs = %+v, want only replenished tab %q", saved.Tabs, replenished.ID)
	}
	if saved.ActiveTab != replenished.ID {
		t.Fatalf("persisted active tab = %q, want replenished tab %q", saved.ActiveTab, replenished.ID)
	}
}

func TestCloseNonLastTabDoesNotReplenish(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	first, err := app.EnsureBlankTab("global", "")
	if err != nil {
		t.Fatalf("EnsureBlankTab: %v", err)
	}
	app.mu.Lock()
	app.tabs["survivor"] = &WorkspaceTab{ID: "survivor", Scope: "global", Ready: true, disabledMCP: map[string]ServerView{}}
	app.tabOrder = []string{first.ID, "survivor"}
	app.activeTabID = first.ID
	app.mu.Unlock()

	if err := app.CloseTab(first.ID); err != nil {
		t.Fatalf("CloseTab(non-last): %v", err)
	}

	tabs := app.ListTabs()
	if len(tabs) != 1 || tabs[0].ID != "survivor" {
		t.Fatalf("after a non-last close tabs = %+v, want only survivor with no replenish tab", tabs)
	}
}
