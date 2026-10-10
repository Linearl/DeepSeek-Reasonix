package sessioncatalog

import (
	"context"
	"path/filepath"
	"testing"
)

// Test718RecomputeKeepsRegistryTitle pins the task 718 fix: the registry
// (SyncMetadata) is the single title authority for topics it claims
// (metadata_present=1). Before the fix, recomputeTopic unconditionally reset
// the topic title to the newest session row's topic_title (or its preview),
// so every persist/reconcile flipped the sidebar name back to the stale
// branch-meta value until the next metadata sync healed it — the
// "wrong name, self-heals, wrong again" cycle (#718).
func Test718RecomputeKeepsRegistryTitle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	catalog, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "catalog.sqlite"), DisableRepair: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })

	// Registry claims the topic with a fresh title (a rename landed there).
	if err := catalog.SyncMetadata(ctx, nil, []TopicMetadata{{
		Scope: "global", TopicID: "t1", Title: "注册表新名", TitleSource: "manual",
	}}); err != nil {
		t.Fatal(err)
	}

	// A session persist indexed afterwards still carries the pre-rename
	// branch-meta title — exactly the rename window race.
	stale := SessionRecord{
		Path: "/sessions/t1.jsonl", Directory: "/sessions", Scope: "global",
		TopicID: "t1", TopicTitle: "旧meta名", LastActivityAt: 10, Turns: 2, TurnsState: TurnsValid, Health: HealthOK,
	}
	if err := catalog.UpsertSession(ctx, stale); err != nil {
		t.Fatal(err)
	}
	topic, ok, err := catalog.GetTopic(ctx, TopicKey{Scope: "global", TopicID: "t1"})
	if err != nil || !ok {
		t.Fatalf("get topic t1: ok=%v err=%v", ok, err)
	}
	if topic.Title != "注册表新名" {
		t.Fatalf("session upsert clobbered registry title: got %q, want %q", topic.Title, "注册表新名")
	}

	// Preview fallback must not leak either: an empty topic_title used to make
	// recomputeTopic promote the session preview into the sidebar name.
	stale.TopicTitle = ""
	stale.Preview = "首条用户消息片段"
	stale.LastActivityAt = 11
	if err := catalog.UpsertSession(ctx, stale); err != nil {
		t.Fatal(err)
	}
	topic, ok, err = catalog.GetTopic(ctx, TopicKey{Scope: "global", TopicID: "t1"})
	if err != nil || !ok {
		t.Fatalf("re-get topic t1: ok=%v err=%v", ok, err)
	}
	if topic.Title != "注册表新名" {
		t.Fatalf("preview leaked into registry topic title: got %q, want %q", topic.Title, "注册表新名")
	}

	// A later metadata sync re-asserts the registry value (idempotent here).
	if err := catalog.SyncMetadata(ctx, nil, []TopicMetadata{{
		Scope: "global", TopicID: "t1", Title: "注册表新名", TitleSource: "manual",
	}}); err != nil {
		t.Fatal(err)
	}
	topic, _, _ = catalog.GetTopic(ctx, TopicKey{Scope: "global", TopicID: "t1"})
	if topic.Title != "注册表新名" {
		t.Fatalf("post-sync title = %q, want %q", topic.Title, "注册表新名")
	}
}

// Test718CatalogOnlyTopicStillDerivesTitle pins the other side of the 718
// contract: topics the registry does not know about (metadata_present=0, e.g.
// written by an older CLI or a concurrent process) keep deriving their title
// from session rows — that derivation is their only source.
func Test718CatalogOnlyTopicStillDerivesTitle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	catalog, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "catalog.sqlite"), DisableRepair: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })

	record := SessionRecord{
		Path: "/sessions/orphan.jsonl", Directory: "/sessions", Scope: "global",
		TopicID: "orphan", TopicTitle: "会话派生名", LastActivityAt: 5, Turns: 1, TurnsState: TurnsValid, Health: HealthOK,
	}
	if err := catalog.UpsertSession(ctx, record); err != nil {
		t.Fatal(err)
	}
	topic, ok, err := catalog.GetTopic(ctx, TopicKey{Scope: "global", TopicID: "orphan"})
	if err != nil || !ok {
		t.Fatalf("get orphan topic: ok=%v err=%v", ok, err)
	}
	if topic.Title != "会话派生名" {
		t.Fatalf("catalog-only topic title = %q, want session-derived %q", topic.Title, "会话派生名")
	}

	// Activity on the session updates the derived title for catalog-only rows.
	record.TopicTitle = "派生新名"
	record.LastActivityAt = 6
	if err := catalog.UpsertSession(ctx, record); err != nil {
		t.Fatal(err)
	}
	topic, ok, err = catalog.GetTopic(ctx, TopicKey{Scope: "global", TopicID: "orphan"})
	if err != nil || !ok {
		t.Fatalf("re-get orphan topic: ok=%v err=%v", ok, err)
	}
	if topic.Title != "派生新名" {
		t.Fatalf("catalog-only topic title after re-upsert = %q, want %q", topic.Title, "派生新名")
	}

	// Once the registry claims it, derivation stops and the registry wins.
	if err := catalog.SyncMetadata(ctx, nil, []TopicMetadata{{
		Scope: "global", TopicID: "orphan", Title: "注册表收编名", TitleSource: "manual",
	}}); err != nil {
		t.Fatal(err)
	}
	record.TopicTitle = "再晚的meta名"
	record.LastActivityAt = 7
	if err := catalog.UpsertSession(ctx, record); err != nil {
		t.Fatal(err)
	}
	topic, ok, err = catalog.GetTopic(ctx, TopicKey{Scope: "global", TopicID: "orphan"})
	if err != nil || !ok {
		t.Fatalf("final get orphan topic: ok=%v err=%v", ok, err)
	}
	if topic.Title != "注册表收编名" {
		t.Fatalf("claimed topic title = %q, want registry %q", topic.Title, "注册表收编名")
	}
}
