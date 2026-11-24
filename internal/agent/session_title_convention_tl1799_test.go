package agent

import (
	"path/filepath"
	"testing"
)

// tl1799: one conversation, one name. Every naming site must state the same
// priority — DisplayTitleFromMeta is that convention — and the surfaces that
// derive from it must stay byte-identical, or the contact directory and the
// sidebar (and get_session_info) show two names for the same conversation.
// This test pins the order itself plus the two in-package derivations.

func TestDisplayTitleFromMetaOrderPinned(t *testing.T) {
	cases := []struct {
		name      string
		meta      BranchMeta
		metaFound bool
		stem      string
		want      string
	}{
		{"no sidecar falls to stem", BranchMeta{}, false, "2026-11-25-1", "2026-11-25-1"},
		{"read error falls to stem", BranchMeta{CustomTitle: "x"}, false, "stem", "stem"},
		{"empty titles fall to stem", BranchMeta{}, true, "stem", "stem"},
		{"topic title beats stem", BranchMeta{TopicTitle: "话题名"}, true, "stem", "话题名"},
		{"custom title beats topic title", BranchMeta{CustomTitle: "手动名", TopicTitle: "话题名"}, true, "stem", "手动名"},
		{"custom title beats stem", BranchMeta{CustomTitle: "手动名"}, true, "stem", "手动名"},
	}
	for _, tc := range cases {
		if got := DisplayTitleFromMeta(tc.meta, tc.metaFound, tc.stem); got != tc.want {
			t.Fatalf("%s: DisplayTitleFromMeta = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The contact directory scan (scanAddressable — what list_addressable_sessions
// and the desktop 通讯录 render) and SessionDirectoryTitle (the desktop roster,
// delete dry-run, get_session_info fallback) must name one session identically
// in every title state.
func TestDirectoryScanAndSessionDirectoryTitleAgree(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "2026-11-25-session.jsonl")
	writeEmpty(t, path)

	states := []struct {
		name   string
		custom string
		topic  string
		want   string
	}{
		{"bare session", "", "", "2026-11-25-session"},
		{"topic only", "", "话题名", "话题名"},
		{"custom and topic", "手动名", "话题名", "手动名"},
		{"custom only", "手动名", "", "手动名"},
	}
	for _, state := range states {
		if err := UpdateBranchMeta(path, false, func(m *BranchMeta) error {
			m.CustomTitle = state.custom
			m.TopicTitle = state.topic
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if got := SessionDirectoryTitle(path); got != state.want {
			t.Fatalf("%s: SessionDirectoryTitle = %q, want %q", state.name, got, state.want)
		}
		var scanned string
		for _, id := range scanAddressable(dir, dir) {
			if id.SessionPath == path {
				scanned = id.Title
			}
		}
		if scanned == "" {
			t.Fatalf("%s: session missing from the directory scan", state.name)
		}
		if scanned != state.want {
			t.Fatalf("%s: scanAddressable title = %q, want %q (must equal SessionDirectoryTitle byte-for-byte)", state.name, scanned, state.want)
		}
	}
}

// get_session_info's self identity (no target) names the calling session by
// the same convention — custom title first. The historical TopicTitle-first
// order made the tool report a name the directory did not show (tl1799).
func TestResolveInfoTargetSelfTitleFollowsDirectoryConvention(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "self.jsonl")
	writeEmpty(t, self)
	if err := UpdateBranchMeta(self, false, func(m *BranchMeta) error {
		m.CustomTitle = "手动名"
		m.TopicTitle = "话题名"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cfg := SessionCollabConfig{Enabled: true, SessionDir: dir, WorkspaceRoot: dir, CurrentSessionPath: self}
	id, err := resolveInfoTarget(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if id.Title != "手动名" {
		t.Fatalf("self title = %q, want the custom title %q (directory order)", id.Title, "手动名")
	}
}
