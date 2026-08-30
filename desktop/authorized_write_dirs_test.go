package main

import (
	"strings"
	"testing"

	"reasonix/internal/control"
)

// The session picker (#9623) enumerates in-process tabs with a live
// controller, marks the active tab, and routes grants to the picked tab.

func TestListSessionWriteDirsListsLiveTabsAndMarksActive(t *testing.T) {
	app := &App{
		tabs: map[string]*WorkspaceTab{
			"live":    {ID: "live", TopicTitle: "Live session", Ctrl: &control.Controller{}},
			"booting": {ID: "booting", TopicTitle: "Still booting"},
			"gone":    {ID: "gone", TopicTitle: "Removed", Ctrl: &control.Controller{}, removed: true},
			"other":   {ID: "other", TopicTitle: "Other session", Ctrl: &control.Controller{}},
		},
		activeTabID: "other",
	}

	listed := app.ListSessionWriteDirs()
	if len(listed) != 2 {
		t.Fatalf("ListSessionWriteDirs = %+v, want only the two tabs with controllers", listed)
	}
	if listed[0].TabID != "other" || !listed[0].Active {
		t.Fatalf("first entry = %+v, want the active tab first and marked active", listed[0])
	}
	if listed[0].Session == nil {
		t.Fatalf("active entry session = nil, want an empty non-nil slice")
	}
	if listed[1].TabID != "live" || listed[1].Active {
		t.Fatalf("second entry = %+v, want the inactive live tab", listed[1])
	}
	for _, entry := range listed {
		if strings.TrimSpace(entry.Title) == "" {
			t.Fatalf("entry %+v has an empty title", entry)
		}
	}
}

func TestSessionWriteDirsControllerRoutesByTab(t *testing.T) {
	picked := &control.Controller{}
	other := &control.Controller{}
	app := &App{
		tabs: map[string]*WorkspaceTab{
			"picked": {ID: "picked", Ctrl: picked},
			"other":  {ID: "other", Ctrl: other},
		},
		activeTabID: "other",
	}

	got, err := app.sessionWriteDirsController("picked")
	if err != nil || got != picked {
		t.Fatalf("sessionWriteDirsController(picked) = (%v, %v), want the picked controller", got, err)
	}
	got, err = app.sessionWriteDirsController("")
	if err != nil || got != other {
		t.Fatalf("sessionWriteDirsController(\"\") = (%v, %v), want the active controller", got, err)
	}
	if _, err := app.sessionWriteDirsController("missing"); err == nil {
		t.Fatal("sessionWriteDirsController(missing) = nil error, want unknown-tab failure")
	}
	// A tab without a live controller cannot take session-scope grants.
	app.tabs["booting"] = &WorkspaceTab{ID: "booting"}
	if _, err := app.sessionWriteDirsController("booting"); err == nil {
		t.Fatal("sessionWriteDirsController(booting) = nil error, want unavailable-session failure")
	}
}

func TestRemoveAuthorizedWriteDirForTabUnknownSessionFailsClosed(t *testing.T) {
	app := &App{
		tabs: map[string]*WorkspaceTab{
			"booting": {ID: "booting"},
		},
		activeTabID: "booting",
	}
	if err := app.RemoveAuthorizedWriteDirForTab("booting", 1, "C:/tmp"); err == nil {
		t.Fatal("RemoveAuthorizedWriteDirForTab on a controller-less tab = nil error, want failure")
	}
	if err := app.AddAuthorizedWriteDirForTab("booting", 1, "C:/tmp"); err == nil {
		t.Fatal("AddAuthorizedWriteDirForTab on a controller-less tab = nil error, want failure")
	}
}
