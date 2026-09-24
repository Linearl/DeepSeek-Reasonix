package historycatalog

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
)

var errFakeQuery = errors.New("query failed")

// TestReconcilePrefersIncrementalAppend pins task 195's core chain end to end:
// after the first scan, a plain append-only save must be picked up by
// reconcile through the increment path — the counters show an append hit while
// the full-reload count stays where the first scan put it.
func TestReconcilePrefersIncrementalAppend(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "incremental.jsonl")
	saveMessages(t, path, provider.Message{Role: provider.RoleUser, Content: "stable prefix marker"})
	catalog, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "history.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })
	target := Root{Path: root, Source: "project", Scope: "project", WorkspaceRoot: root}
	if err := catalog.ReconcileRoot(ctx, target); err != nil {
		t.Fatal(err)
	}
	first := catalog.IndexStats()
	if first.FullReloads == 0 {
		t.Fatalf("first scan must full-load at least once: %+v", first)
	}
	if first.AppendHits != 0 {
		t.Fatalf("nothing was appended yet: %+v", first)
	}

	session, err := agent.LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	session.Add(provider.Message{Role: provider.RoleAssistant, Content: "appended phoenix tail"})
	if err := session.SaveSnapshot(path); err != nil {
		t.Fatal(err)
	}
	if err := catalog.ReconcileRoot(ctx, target); err != nil {
		t.Fatal(err)
	}
	second := catalog.IndexStats()
	if second.AppendHits != first.AppendHits+1 {
		t.Fatalf("append-only save must index through the increment path: before=%+v after=%+v", first, second)
	}
	if second.FullReloads != first.FullReloads {
		t.Fatalf("append-only save must not full-reload: before=%+v after=%+v", first, second)
	}
	result, err := catalog.Search(ctx, SearchRequest{Query: "phoenix", Roots: []string{root}})
	if err != nil || len(result.Items) != 1 {
		t.Fatalf("appended term not indexed: items=%d err=%v", len(result.Items), err)
	}
}

// TestSourceProjectionUnchangedDigestAuthoritative pins the rule task 195
// switched to: when both sides carry a content digest the digest decides, so
// a size:mtime change alone never forces a reload; sources without an identity
// keep the old strict fingerprint comparison.
func TestSourceProjectionUnchangedDigestAuthoritative(t *testing.T) {
	t.Parallel()
	if !sourceProjectionUnchanged(nil, "100:1", "101:2", "m1", "m1", "digest-a", "digest-a") {
		t.Fatal("same digest with changed content fingerprint must stay unchanged")
	}
	if sourceProjectionUnchanged(nil, "100:1", "101:2", "m1", "m1", "digest-a", "digest-b") {
		t.Fatal("a digest change must invalidate the projection")
	}
	if sourceProjectionUnchanged(nil, "100:1", "101:2", "m1", "m2", "digest-a", "digest-a") {
		t.Fatal("a meta change must invalidate the projection")
	}
	if !sourceProjectionUnchanged(nil, "100:1", "100:1", "m1", "m1", "", "") {
		t.Fatal("identity-less sources keep the strict fingerprint comparison")
	}
	if sourceProjectionUnchanged(nil, "100:1", "101:2", "m1", "m1", "", "") {
		t.Fatal("identity-less sources must still see a fingerprint change")
	}
	if sourceProjectionUnchanged(errFakeQuery, "1", "1", "m", "m", "d", "d") {
		t.Fatal("a failed lookup is never unchanged")
	}
}

// TestMetaOnlyEditRefreshesProjection pins task 195's third path: a title edit
// moves the branch-meta sidecar (and its CAS revision) without touching the
// transcript, so the projection's metadata columns refresh without a full
// reload.
func TestMetaOnlyEditRefreshesProjection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "metaedit.jsonl")
	saveMessages(t, path, provider.Message{Role: provider.RoleUser, Content: "phoenix marker"})
	catalog, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "history.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })
	target := Root{Path: root, Source: "project", Scope: "project", WorkspaceRoot: root}
	if err := catalog.ReconcileRoot(ctx, target); err != nil {
		t.Fatal(err)
	}
	base := catalog.IndexStats()

	meta, ok, err := agent.LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("meta load: ok=%v err=%v", ok, err)
	}
	meta.CustomTitle = "renamed phoenix"
	if err := agent.SaveBranchMetaPreserveUpdated(path, meta); err != nil {
		t.Fatal(err)
	}
	if err := catalog.indexPath(ctx, target, path, 0, -1); err != nil {
		t.Fatal(err)
	}
	after := catalog.IndexStats()
	if after.MetaRefreshes != base.MetaRefreshes+1 {
		t.Fatalf("meta-only edit must refresh without a reload: before=%+v after=%+v", base, after)
	}
	if after.FullReloads != base.FullReloads {
		t.Fatalf("meta-only edit must not full-reload: before=%+v after=%+v", base, after)
	}
	var title string
	if err := catalog.db.QueryRow(`SELECT custom_title FROM history_sources WHERE path=?`, path).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "renamed phoenix" {
		t.Fatalf("custom_title = %q, want renamed phoenix", title)
	}
}
