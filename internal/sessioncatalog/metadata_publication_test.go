package sessioncatalog

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
)

// Task 981 (sidebar flicker): the desktop re-syncs the same registry every 30s.
// When the registry projection is byte-identical to the previous sync, the
// catalog must not publish a metadata revision event — every such event
// force-reloads all expanded sidebar folders in the frontend. A changed
// registry (title edit) must still publish.
func TestSyncMetadataSkipsPublicationWhenRegistryUnchanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var mu sync.Mutex
	reasons := []string{}
	catalog, err := Open(ctx, Options{
		Path:          filepath.Join(t.TempDir(), "catalog.sqlite"),
		DisableRepair: true,
		OnRevision: func(_ uint64, _ []string, reason string) {
			mu.Lock()
			reasons = append(reasons, reason)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })

	projects := []ProjectRecord{{Scope: "project", WorkspaceRoot: "/workspace", Title: "W"}}
	topics := []TopicMetadata{{Scope: "project", WorkspaceRoot: "/workspace", TopicID: "topic-1", Title: "Topic"}}

	published := func() int {
		mu.Lock()
		defer mu.Unlock()
		count := 0
		for _, reason := range reasons {
			if reason == "metadata" {
				count++
			}
		}
		return count
	}

	if err := catalog.SyncMetadata(ctx, projects, topics); err != nil {
		t.Fatal(err)
	}
	if got := published(); got != 1 {
		t.Fatalf("first sync metadata publications = %d, want 1", got)
	}

	// The 30s idle tick: identical registry, must stay silent.
	for range 3 {
		if err := catalog.SyncMetadata(ctx, projects, topics); err != nil {
			t.Fatal(err)
		}
	}
	if got := published(); got != 1 {
		t.Fatalf("identical registry metadata publications = %d, want still 1", got)
	}

	// A real registry edit publishes again.
	topics[0].Title = "Renamed"
	if err := catalog.SyncMetadata(ctx, projects, topics); err != nil {
		t.Fatal(err)
	}
	if got := published(); got < 2 {
		t.Fatalf("changed registry metadata publications = %d, want at least 2", got)
	}
	topic, _, ok, err := catalog.GetTopicWithSessionCount(ctx, TopicKey{Scope: "project", WorkspaceRoot: "/workspace", TopicID: "topic-1"})
	if err != nil || !ok || topic.Title != "Renamed" {
		t.Fatalf("topic after renamed sync = %+v ok=%v err=%v, want title Renamed", topic, ok, err)
	}
}

// The skip must not suppress session-derived convergence: a session written
// between two identical metadata syncs still keeps its aggregates after the
// silent sync commits its idempotent transaction.
func TestSyncMetadataSilentSyncStillConvergesSessionAggregates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	catalog, err := Open(ctx, Options{InMemory: true, DisableRepair: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })

	topics := []TopicMetadata{{Scope: "global", TopicID: "t1", Title: "One"}}
	if err := catalog.SyncMetadata(ctx, nil, topics); err != nil {
		t.Fatal(err)
	}
	if err := catalog.UpsertSession(ctx, SessionRecord{
		Path: "/sessions/t1.jsonl", Directory: "/sessions", Scope: "global",
		TopicID: "t1", LastActivityAt: 5, Turns: 2, TurnsState: TurnsValid, Health: HealthOK,
	}); err != nil {
		t.Fatal(err)
	}
	// Same registry again: no publication, but the committed transaction must
	// not have disturbed the session-derived topic aggregates.
	if err := catalog.SyncMetadata(ctx, nil, topics); err != nil {
		t.Fatal(err)
	}
	topic, ok, err := catalog.GetTopic(ctx, TopicKey{Scope: "global", TopicID: "t1"})
	if err != nil || !ok || topic.Turns != 2 || topic.LastActivityAt != 5 {
		t.Fatalf("topic after silent sync = %+v ok=%v err=%v, want turns 2 activity 5", topic, ok, err)
	}
}
