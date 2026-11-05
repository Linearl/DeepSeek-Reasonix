package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTestFact plants one production-shaped fact file and pins its mtime so
// the reconcile fingerprint is deterministic across a fast test run.
func writeTestFact(t *testing.T, dir, name, title, body string, modTime time.Time) string {
	t.Helper()
	return writeScopedTestFact(t, dir, name, title, body, FactScopeProject, modTime)
}

// writeScopedTestFact is writeTestFact with an explicit scope so tests can
// plant the project/global twin pairs the shadow gate reasons about.
func writeScopedTestFact(t *testing.T, dir, name, title, body string, scope FactScope, modTime time.Time) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	content := fmt.Sprintf(`---
id: mem-%s-%s
revision: 1
created_at: "2026-01-01T00:00:00.0000000Z"
updated_at: "2026-01-01T00:00:00.0000000Z"
name: %s
title: %s
description: test fact for the durable recall index
metadata:
  type: project
  scope: %s
  activation: relevant
  volatility: stable
---

%s
`, scope, name, name, title, scope, body)
	path := filepath.Join(dir, name+".md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fact %s: %v", name, err)
	}
	if !modTime.IsZero() {
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatalf("chtimes %s: %v", name, err)
		}
	}
	return path
}

// TestRecallFTSIncrementalReconcile is acceptance criterion 1: a restart must
// not rebuild the whole pool. The second pass here stands in for a fresh
// process — it shares no in-memory state with the first.
func TestRecallFTSIncrementalReconcile(t *testing.T) {
	dir := t.TempDir()
	store := Store{Dir: dir}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	writeTestFact(t, dir, "alpha", "alpha fact", "alpha body mentions proxies", base)
	writeTestFact(t, dir, "beta", "beta fact", "beta body mentions ports", base.Add(time.Second))

	docs, stats, err := ensureRecallFTS(store)
	if err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if stats.Added != 2 || stats.Reused != 0 || stats.Updated != 0 || stats.Removed != 0 {
		t.Fatalf("first reconcile stats = %+v, want added=2 and nothing else", stats)
	}
	if len(docs) != 2 {
		t.Fatalf("docs after first reconcile = %d, want 2", len(docs))
	}

	// Restart case: nothing changed, so every row must be reused and no fact
	// file may be re-read.
	docs, stats, err = ensureRecallFTS(store)
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if stats.Added != 0 || stats.Updated != 0 || stats.Removed != 0 {
		t.Fatalf("second reconcile must be pure reuse, got %+v", stats)
	}
	if stats.Reused != 2 {
		t.Fatalf("second reconcile reused = %d, want 2", stats.Reused)
	}
	if len(docs) != 2 {
		t.Fatalf("docs after reuse = %d, want 2", len(docs))
	}

	// One file changes: exactly one row is re-read and rewritten.
	writeTestFact(t, dir, "alpha", "alpha fact v2", "alpha body rewritten with longer text", base.Add(10*time.Second))
	_, stats, err = ensureRecallFTS(store)
	if err != nil {
		t.Fatalf("third reconcile: %v", err)
	}
	if stats.Updated != 1 || stats.Reused != 1 || stats.Added != 0 || stats.Removed != 0 {
		t.Fatalf("third reconcile stats = %+v, want updated=1 reused=1", stats)
	}

	// One file disappears: its row must go with it.
	if err := os.Remove(filepath.Join(dir, "beta.md")); err != nil {
		t.Fatalf("remove beta: %v", err)
	}
	docs, stats, err = ensureRecallFTS(store)
	if err != nil {
		t.Fatalf("fourth reconcile: %v", err)
	}
	if stats.Removed != 1 {
		t.Fatalf("fourth reconcile removed = %d, want 1", stats.Removed)
	}
	if len(docs) != 1 {
		t.Fatalf("docs after delete = %d, want 1", len(docs))
	}
}

// TestRecallFTSMatchesFullRebuild guards the invariant that made this design
// worth doing: the durable path must produce the same retrieval state as the
// pre-existing full rebuild. If these ever diverge, recall silently changed.
func TestRecallFTSMatchesFullRebuild(t *testing.T) {
	dir := t.TempDir()
	store := Store{Dir: dir}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	writeTestFact(t, dir, "gamma", "记忆召回 gamma", "body about 代理端口 and proxy aliases", base)

	fromFTS := buildRecallIndexFromFTS(store)
	fromDisk := BuildRecallIndex(store)
	if fromFTS == nil || fromDisk == nil {
		t.Fatalf("both builds must yield an index, got fts=%v disk=%v", fromFTS, fromDisk)
	}
	if len(fromFTS.docs) != len(fromDisk.docs) {
		t.Fatalf("doc count mismatch: fts=%d disk=%d", len(fromFTS.docs), len(fromDisk.docs))
	}
	byName := make(map[string]autoRecallDoc, len(fromDisk.docs))
	for _, doc := range fromDisk.docs {
		byName[doc.memory.Name] = doc
	}
	for _, doc := range fromFTS.docs {
		want, ok := byName[doc.memory.Name]
		if !ok {
			t.Fatalf("fts doc %q missing from the full rebuild", doc.memory.Name)
		}
		if doc.length != want.length {
			t.Fatalf("term count for %q: fts=%d disk=%d", doc.memory.Name, doc.length, want.length)
		}
		if len(doc.counts) != len(want.counts) {
			t.Fatalf("distinct terms for %q: fts=%d disk=%d", doc.memory.Name, len(doc.counts), len(want.counts))
		}
		if doc.text != want.text {
			t.Fatalf("search text for %q differs between the durable and full paths", doc.memory.Name)
		}
		if doc.memory.Body != want.memory.Body {
			t.Fatalf("body for %q differs between the durable and full paths", doc.memory.Name)
		}
	}
}

// TestRecallFTSRebuildsOnSchemaMismatch covers the "derived data" promise: a
// stale schema file is thrown away rather than trusted.
func TestRecallFTSRebuildsOnSchemaMismatch(t *testing.T) {
	dir := t.TempDir()
	store := Store{Dir: dir}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	writeTestFact(t, dir, "delta", "delta fact", "delta body", base)

	if _, _, err := ensureRecallFTS(store); err != nil {
		t.Fatalf("initial reconcile: %v", err)
	}

	path := recallFTSPath(store)
	db, err := openRecallFTS(path)
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	if _, err := db.Exec(`UPDATE metadata SET value='0' WHERE key='schema_version'`); err != nil {
		db.Close()
		t.Fatalf("downgrade schema version: %v", err)
	}
	db.Close()

	docs, _, err := ensureRecallFTS(store)
	if err != nil {
		t.Fatalf("reconcile after schema downgrade: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("docs after schema rebuild = %d, want 1", len(docs))
	}
}

// TestRecallFTSEmptyStore pins the zero-store behaviour: no directory means no
// index file, and recall still reports an empty pool.
func TestRecallFTSEmptyStore(t *testing.T) {
	store := Store{}
	index := buildRecallIndexFromFTS(store)
	if index != nil {
		t.Fatalf("zero store must yield a nil index, got %+v", index)
	}
}

// TestRecallFTSHonorsProjectOverGlobalShadow pins the scope-shadow contract on
// the durable path. BuildRecallIndex runs the markdown pool through
// recallMemories, which drops a global fact whose identity is claimed by a
// project fact; the FTS rows are one per fact file, so the durable path must
// gate identically or the shadowed global twin silently re-enters automatic
// recall (the desktop TestMemoryViewIncludesRecallFreshnessAndOverrides red
// was exactly this). Both paths must agree here.
func TestRecallFTSHonorsProjectOverGlobalShadow(t *testing.T) {
	dir := t.TempDir()
	store := Store{Dir: filepath.Join(dir, "project"), GlobalDir: filepath.Join(dir, "global")}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	writeScopedTestFact(t, store.GlobalDir, "deploy-target", "Deploy target",
		"Deploy payments to the legacy cluster.", FactScopeGlobal, base)
	writeScopedTestFact(t, store.Dir, "deploy-target", "Deploy target",
		"Deploy payments to the green cluster.", FactScopeProject, base)

	fromFTS := buildRecallIndexFromFTS(store)
	if fromFTS == nil || len(fromFTS.docs) != 1 {
		t.Fatalf("fts pool docs = %v, want exactly the project fact", fromFTS)
	}
	if got := NormalizeFactScope(string(fromFTS.docs[0].memory.Scope)); got != FactScopeProject {
		t.Fatalf("served fact scope = %q, want project", got)
	}
	fromDisk := BuildRecallIndex(store)
	if fromDisk == nil || len(fromDisk.docs) != 1 || NormalizeFactScope(string(fromDisk.docs[0].memory.Scope)) != FactScopeProject {
		t.Fatalf("full rebuild docs = %+v, want exactly the project fact", fromDisk)
	}

	// Without the project twin the global fact must come back — the gate is a
	// shadow, not a ban on global scope.
	if err := os.Remove(filepath.Join(store.Dir, "deploy-target.md")); err != nil {
		t.Fatalf("remove project twin: %v", err)
	}
	index := buildRecallIndexFromFTS(store)
	if index == nil || len(index.docs) != 1 || NormalizeFactScope(string(index.docs[0].memory.Scope)) != FactScopeGlobal {
		t.Fatalf("orphaned global fact docs = %+v, want exactly the global fact", index)
	}
}
