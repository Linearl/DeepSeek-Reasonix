package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
)

// tl1799: one conversation, one name across the two surfaces. The contact
// directory reads the branch-meta sidecar fresh on every scan
// (CustomTitle > TopicTitle > stem, agent.DisplayTitleFromMeta); the sidebar
// reads the topic-level titles store (registry authority, task 718). The two
// only stay consistent when every rename entry converges ALL sidecar name
// fields and projects the directory-visible value into the store.

// A topic rename must converge a lingering CustomTitle on the topic's
// sessions — the directory reads CustomTitle first, so leaving the old value
// behind is exactly the "通讯录是过时旧名" report. Sessions without a custom
// title keep having none.
func TestUpdateTopicSessionTitlesConvergesCustomTitle(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	topicID := "topic_tl1799_converge"
	now := time.Now().UTC()
	withCustom := writeTopicSessionWithPrompt(t, dir, "with-custom.jsonl", topicID, "旧话题名", "", "hello", now)
	if err := agent.SaveBranchMetaPreserveUpdated(withCustom, agent.BranchMeta{
		CreatedAt:   now.Add(-time.Minute),
		UpdatedAt:   now,
		Scope:       "global",
		TopicID:     topicID,
		TopicTitle:  "旧话题名",
		CustomTitle: "旧手动名",
	}); err != nil {
		t.Fatal(err)
	}
	withoutCustom := writeTopicSessionWithPrompt(t, dir, "without-custom.jsonl", topicID, "旧话题名", "", "world", now.Add(time.Minute))

	NewApp().updateTopicSessionTitles(topicID, "新话题名")

	meta, ok, err := agent.LoadBranchMeta(withCustom)
	if err != nil || !ok {
		t.Fatalf("load meta: ok=%v err=%v", ok, err)
	}
	if meta.TopicTitle != "新话题名" || meta.CustomTitle != "新话题名" {
		t.Fatalf("session with custom title: TopicTitle=%q CustomTitle=%q, want both converged to the new topic name", meta.TopicTitle, meta.CustomTitle)
	}
	meta, ok, err = agent.LoadBranchMeta(withoutCustom)
	if err != nil || !ok {
		t.Fatalf("load meta: ok=%v err=%v", ok, err)
	}
	if meta.TopicTitle != "新话题名" || meta.CustomTitle != "" {
		t.Fatalf("session without custom title: TopicTitle=%q CustomTitle=%q, want topic title renamed and no custom title minted", meta.TopicTitle, meta.CustomTitle)
	}
}

// Clearing a custom title (RenameSession with an empty title) must project the
// directory-visible fallback — the shared derivation over the sidecar — into
// the topic titles store. The sidebar otherwise kept the stale manual name
// while the directory had already moved on.
func TestClearingCustomTitleProjectsDirectoryFallback(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	topicID := "topic_tl1799_clear"
	now := time.Now().UTC()
	path := writeTopicSessionWithPrompt(t, dir, "clear-title.jsonl", topicID, "话题名", "", "hello", now)
	if err := agent.SaveBranchMetaPreserveUpdated(path, agent.BranchMeta{
		CreatedAt:   now.Add(-time.Minute),
		UpdatedAt:   now,
		Scope:       "global",
		TopicID:     topicID,
		TopicTitle:  "话题名",
		CustomTitle: "手动名",
	}); err != nil {
		t.Fatal(err)
	}
	app := NewApp()

	if err := agent.RenameSession(path, ""); err != nil {
		t.Fatal(err)
	}
	if err := app.onSessionTitleChanged(dir, path, ""); err != nil {
		t.Fatal(err)
	}

	meta, ok, err := agent.LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("load meta: ok=%v err=%v", ok, err)
	}
	if meta.CustomTitle != "" {
		t.Fatalf("custom title = %q, want cleared", meta.CustomTitle)
	}
	if got := loadTopicTitle("", topicID); got != "话题名" {
		t.Fatalf("topic store title = %q, want the directory-visible fallback %q (stale manual name must not survive)", got, "话题名")
	}
	if source := loadTopicTitleSource("", topicID); source != topicTitleSourceAuto {
		t.Fatalf("topic store title source = %q, want auto (derived names stay in the auto-title lifecycle)", source)
	}
}

// With no TopicTitle on the sidecar either, the fallback is the file stem —
// the exact name the directory shows, not the stale manual name.
func TestClearingCustomTitleFallsBackToStem(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	topicID := "topic_tl1799_stem"
	now := time.Now().UTC()
	path := writeTopicSessionWithPrompt(t, dir, "2026-11-25-stem-case.jsonl", topicID, "", "", "hello", now)
	if err := agent.SaveBranchMetaPreserveUpdated(path, agent.BranchMeta{
		CreatedAt:   now.Add(-time.Minute),
		UpdatedAt:   now,
		Scope:       "global",
		TopicID:     topicID,
		CustomTitle: "手动名",
	}); err != nil {
		t.Fatal(err)
	}
	app := NewApp()

	if err := agent.RenameSession(path, ""); err != nil {
		t.Fatal(err)
	}
	if err := app.onSessionTitleChanged(dir, path, ""); err != nil {
		t.Fatal(err)
	}

	want := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if got := loadTopicTitle("", topicID); got != want {
		t.Fatalf("topic store title = %q, want the stem %q the directory shows", got, want)
	}
}
