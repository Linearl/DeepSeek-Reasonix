package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/sessioncatalog"
)

// tl1485e: a registry shell row (metadataProjectTopics) used to carry no
// activity value, so the FE topicActivityAt fallback (lastActivityAt ||
// createdAt) aged the row by its creation time — the "1 个月" sidebar form —
// while the catalog row for the same topic was fresh and healthy. The shell
// must surface the catalog topic's last_activity_at whenever the catalog has
// the topic; createdAt stays the not-indexed fallback. The JSON assertion pins
// the wire key too: a struct field that never reaches the FE under its
// camelCase name is invisible to topicActivityAt no matter what the Go side
// holds.
func TestMetadataProjectTopicsCarriesCatalogLastActivityTL1485e(t *testing.T) {
	isolateDesktopUserDirs(t)
	projects := `{"globalTopics":["topic_tl1485e"]}`
	if err := os.WriteFile(filepath.Join(desktopConfigDir(), desktopProjectsFile), []byte(projects), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(desktopConfigDir(), "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "20261105-175900-session.jsonl")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	catalog, err := sessioncatalog.Open(ctx, sessioncatalog.Options{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close(ctx)
	const fresh = int64(1793872762639) // 2026-11-05 17:59:22 +08, the live-row value
	record := sessioncatalog.SessionRecord{
		Path: path, Directory: dir, Scope: "global", TopicID: "topic_tl1485e",
		CreatedAt: fresh - 1000, LastActivityAt: fresh, Turns: 14,
		TurnsState: sessioncatalog.TurnsValid, Health: sessioncatalog.HealthOK,
	}
	if err := catalog.UpsertSession(ctx, record); err != nil {
		t.Fatal(err)
	}

	a := NewApp()
	a.ctx = ctx
	a.sessionCatalog.Store(catalog)
	assertFreshWire := func(source string, node ProjectNode) {
		t.Helper()
		if node.LastActivityAt != fresh {
			t.Fatalf("%s: lastActivityAt = %d, want catalog activity %d (tl1485e: the FE lastActivityAt||createdAt fallback must not age rows by their creation time)", source, node.LastActivityAt, fresh)
		}
		wire, err := json.Marshal(node)
		if err != nil {
			t.Fatal(err)
		}
		want := `"lastActivityAt":` + string(mustJSON(t, fresh))
		if !strings.Contains(string(wire), want) {
			t.Fatalf("%s: wire json %s does not carry %s the FE topicActivityAt fallback reads — the value would silently never reach the sidebar", source, wire, want)
		}
	}
	findTopic := func(items []ProjectNode) (ProjectNode, bool) {
		for _, item := range items {
			if item.TopicID == "topic_tl1485e" {
				return item, true
			}
		}
		return ProjectNode{}, false
	}

	// Phase 1 — the registry shell painter (metadata fallback path).
	shells := a.metadataProjectTopics("global", "")
	shell, ok := findTopic(shells)
	if !ok {
		t.Fatalf("topic_tl1485e shell row missing from metadataProjectTopics output (%d nodes)", len(shells))
	}
	assertFreshWire("metadata shell", shell)

	// Phase 2 — the exact RPC the expanded folder pulls (catalog page + merge).
	page, err := a.ListProjectTopics(ProjectTopicPageRequest{Scope: "global", Limit: 50, SortMode: "updated"})
	if err != nil {
		t.Fatal(err)
	}
	pageRow, ok := findTopic(page.Items)
	if !ok {
		t.Fatalf("topic_tl1485e missing from ListProjectTopics output (%d items, complete=%v)", len(page.Items), page.Complete)
	}
	assertFreshWire("ListProjectTopics page", pageRow)
}

func mustJSON(t *testing.T, v int64) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
