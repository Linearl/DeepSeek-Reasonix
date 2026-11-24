package sessioncatalog

import (
	"context"
	"path/filepath"
	"testing"
)

// tl1799: catalog-only topics (metadata_present=0) derive their sidebar title
// from session rows. That derivation must follow the contact directory's
// naming convention (agent.DisplayTitleFromMeta): custom_title first, then
// topic_title. The old topic_title-first order made the sidebar show the
// topic name while the directory showed the custom title for the same
// conversation. Registry-claimed topics (metadata_present=1) stay untouched —
// that authority is the task 718 contract.
func TestCatalogOnlyTopicDerivesCustomTitleFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	catalog, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "catalog.sqlite"), DisableRepair: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })

	record := SessionRecord{
		Path: "/sessions/manual.jsonl", Directory: "/sessions", Scope: "global",
		TopicID: "manual", TopicTitle: "话题名", CustomTitle: "手动名",
		LastActivityAt: 5, Turns: 1, TurnsState: TurnsValid, Health: HealthOK,
	}
	if err := catalog.UpsertSession(ctx, record); err != nil {
		t.Fatal(err)
	}
	topic, ok, err := catalog.GetTopic(ctx, TopicKey{Scope: "global", TopicID: "manual"})
	if err != nil || !ok {
		t.Fatalf("get topic: ok=%v err=%v", ok, err)
	}
	if topic.Title != "手动名" {
		t.Fatalf("catalog-only topic title = %q, want the custom title %q (directory order)", topic.Title, "手动名")
	}

	// Custom title cleared: derivation falls through to the topic title.
	record.CustomTitle = ""
	record.LastActivityAt = 6
	if err := catalog.UpsertSession(ctx, record); err != nil {
		t.Fatal(err)
	}
	topic, ok, err = catalog.GetTopic(ctx, TopicKey{Scope: "global", TopicID: "manual"})
	if err != nil || !ok {
		t.Fatalf("re-get topic: ok=%v err=%v", ok, err)
	}
	if topic.Title != "话题名" {
		t.Fatalf("catalog-only topic title after clear = %q, want the topic title %q", topic.Title, "话题名")
	}
}
