package plugin

import (
	"context"
	"errors"
	"testing"
)

// noteClientForTest registers c under the scope carried by ctx, holding the
// Host write lock as noteClientLocked requires. It stands in for the retired
// zero-caller ReplaceServerBackend (tl1754) as the scope-journal test entry:
// same noteClientFromContext attribution, and like that face a same-name
// registration replaces the live instance so instance-scoped rollback stays
// testable. The proxy swap face is not reproduced (tests assert clients /
// scope journals, not proxies).
func noteClientForTest(h *Host, ctx context.Context, c *Client) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, prev := range h.clients {
		if prev != nil && prev.name == c.name {
			h.clients = append(h.clients[:i], h.clients[i+1:]...)
			prev.close()
		}
	}
	return h.noteClientFromContext(ctx, c)
}

func TestRegistrationScopeRollbackIsInstanceScoped(t *testing.T) {
	host := NewHost()
	// Pre-existing sibling client accepted under scope A.
	siblingA := &Client{name: "sibling"}
	scopeA := host.BeginRegistrationScope()
	ctxA := ContextWithRegistrationScope(context.Background(), scopeA)
	if err := noteClientForTest(host, ctxA, siblingA); err != nil {
		t.Fatal(err)
	}
	refsA := scopeA.Snapshot()
	if len(refsA) != 1 || refsA[0].Name != "sibling" || refsA[0].ID == 0 {
		t.Fatalf("scope A = %+v", refsA)
	}
	idA := refsA[0].ID

	// Stale build journals a replacement for the same name plus a new server.
	staleSibling := &Client{name: "sibling"}
	staleNew := &Client{name: "stale-new"}
	scopeStale := host.BeginRegistrationScope()
	ctxStale := ContextWithRegistrationScope(context.Background(), scopeStale)
	if err := noteClientForTest(host, ctxStale, staleSibling); err != nil {
		t.Fatal(err)
	}
	if err := noteClientForTest(host, ctxStale, staleNew); err != nil {
		t.Fatal(err)
	}
	refsStale := scopeStale.Snapshot()
	if len(refsStale) != 2 {
		t.Fatalf("stale scope len = %d, want 2: %+v", len(refsStale), refsStale)
	}
	var staleSiblingID uint64
	for _, ref := range refsStale {
		if ref.Name == "sibling" {
			staleSiblingID = ref.ID
		}
	}
	if staleSiblingID == 0 || staleSiblingID == idA {
		t.Fatalf("stale sibling id = %d, original = %d", staleSiblingID, idA)
	}

	// Sibling/new-generation resource created AFTER the stale scope must survive
	// rollback (name-diff would delete it incorrectly). No scope token.
	siblingNewGen := &Client{name: "sibling-new-gen"}
	if err := noteClientForTest(host, context.Background(), siblingNewGen); err != nil {
		t.Fatal(err)
	}
	// Newer generation also replaces the shared name after the stale scope ends.
	newerSibling := &Client{name: "sibling"}
	if err := noteClientForTest(host, context.Background(), newerSibling); err != nil {
		t.Fatal(err)
	}
	newerID := newerSibling.instanceID
	if newerID == 0 || newerID == staleSiblingID {
		t.Fatalf("newer sibling id = %d, stale = %d", newerID, staleSiblingID)
	}

	scopeStale.AbortAndRollback()

	names := map[string]bool{}
	for _, name := range host.ServerNames() {
		names[name] = true
	}
	if names["stale-new"] {
		t.Fatal("stale-new survived instance-scoped rollback")
	}
	if !names["sibling-new-gen"] {
		t.Fatal("sibling-new-gen was incorrectly removed by stale scope rollback")
	}
	if !names["sibling"] {
		t.Fatal("newer-generation sibling instance was incorrectly removed")
	}
	if host.lookupClient("sibling") == nil || host.lookupClient("sibling").instanceID != newerID {
		t.Fatalf("sibling live instance = %+v, want id %d", host.lookupClient("sibling"), newerID)
	}
	if host.RemoveIfInstance("sibling", idA) {
		t.Fatal("RemoveIfInstance removed a non-matching/missing instance")
	}
	if host.RemoveIfInstance("sibling", staleSiblingID) {
		t.Fatal("RemoveIfInstance removed the newer instance via stale id")
	}
}

// TestRegistrationScopeIgnoresUnrelatedHostWrites is the deterministic
// interleaving that Host-global regJournalActive fails: while a stale build
// holds an open scope, a sibling hot-add without the scope token must not be
// attributed to the stale build or deleted by its rollback.
func TestRegistrationScopeIgnoresUnrelatedHostWrites(t *testing.T) {
	host := NewHost()
	scopeStale := host.BeginRegistrationScope()
	ctxStale := ContextWithRegistrationScope(context.Background(), scopeStale)

	// Sibling hot-add during the open scope WITHOUT the token.
	sibling := &Client{name: "sibling-hot-add"}
	if err := noteClientForTest(host, context.Background(), sibling); err != nil {
		t.Fatal(err)
	}
	// Stale build also registers its own server under the scope.
	staleOnly := &Client{name: "stale-only"}
	if err := noteClientForTest(host, ctxStale, staleOnly); err != nil {
		t.Fatal(err)
	}

	refs := scopeStale.Snapshot()
	for _, ref := range refs {
		if ref.Name == "sibling-hot-add" {
			t.Fatalf("journal captured unrelated Host write: %+v", refs)
		}
	}
	if len(refs) != 1 || refs[0].Name != "stale-only" {
		t.Fatalf("stale scope refs = %+v, want only stale-only", refs)
	}

	scopeStale.AbortAndRollback()

	names := map[string]bool{}
	for _, name := range host.ServerNames() {
		names[name] = true
	}
	if names["stale-only"] {
		t.Fatal("stale-only survived AbortAndRollback")
	}
	if !names["sibling-hot-add"] {
		t.Fatal("sibling-hot-add was deleted by stale scope rollback")
	}
}

func TestRegistrationScopeRejectsLateRegistrationAfterAbort(t *testing.T) {
	host := NewHost()
	scope := host.BeginRegistrationScope()
	ctx := ContextWithRegistrationScope(context.Background(), scope)
	scope.AbortAndRollback()

	late := &Client{name: "late"}
	err := noteClientForTest(host, ctx, late)
	if !errors.Is(err, ErrRegistrationScopeAborted) {
		t.Fatalf("late registration err = %v, want ErrRegistrationScopeAborted", err)
	}
	for _, name := range host.ServerNames() {
		if name == "late" {
			t.Fatal("aborted scope accepted a late client")
		}
	}
}

func TestRegistrationScopeCommitPreservesClientReusedByNewerBuild(t *testing.T) {
	host := NewHost()
	older := host.BeginRegistrationScope()
	olderCtx := ContextWithRegistrationScope(context.Background(), older)
	client := &Client{name: "shared", toolCatalog: toolCatalogSnapshot{listed: true}}
	if err := noteClientForTest(host, olderCtx, client); err != nil {
		t.Fatal(err)
	}

	newer := host.BeginRegistrationScope()
	newerCtx := ContextWithRegistrationScope(context.Background(), newer)
	if _, err := host.ToolsFor(newerCtx, "shared"); err != nil {
		t.Fatalf("newer build failed to reuse shared client: %v", err)
	}
	if !newer.Commit() {
		t.Fatal("newer build could not commit its shared-client claim")
	}

	older.AbortAndRollback()
	if got := host.lookupClient("shared"); got != client {
		t.Fatalf("older rollback removed client committed by newer build: got=%p want=%p", got, client)
	}
}

func TestRegistrationScopeActiveClaimDefersCreatorRollback(t *testing.T) {
	host := NewHost()
	creator := host.BeginRegistrationScope()
	creatorCtx := ContextWithRegistrationScope(context.Background(), creator)
	client := &Client{name: "shared", toolCatalog: toolCatalogSnapshot{listed: true}}
	if err := noteClientForTest(host, creatorCtx, client); err != nil {
		t.Fatal(err)
	}

	consumer := host.BeginRegistrationScope()
	consumerCtx := ContextWithRegistrationScope(context.Background(), consumer)
	if _, err := host.ToolsFor(consumerCtx, "shared"); err != nil {
		t.Fatal(err)
	}
	creator.AbortAndRollback()
	if !host.HasClient("shared") {
		t.Fatal("creator rollback removed a client claimed by an active consumer")
	}

	consumer.AbortAndRollback()
	if host.HasClient("shared") {
		t.Fatal("client survived after every uncommitted scope aborted")
	}
	if got := host.lookupClient("shared"); got != nil {
		t.Fatalf("proxy still exposed rolled-back client: %+v", got)
	}
}

func TestCommittedScopeAcceptsLateLazyRegistration(t *testing.T) {
	host := NewHost()
	scope := host.BeginRegistrationScope()
	ctx := ContextWithRegistrationScope(context.Background(), scope)
	if !scope.Commit() {
		t.Fatal("commit failed")
	}

	late := &Client{name: "late"}
	if err := noteClientForTest(host, ctx, late); err != nil {
		t.Fatalf("published scope rejected a late lazy registration: %v", err)
	}
	scope.AbortAndRollback()
	if got := host.lookupClient("late"); got != late {
		t.Fatal("committed scope rollback removed its late lazy registration")
	}
}

func TestRegistrationScopesRemainIndependentWhileBothActive(t *testing.T) {
	host := NewHost()
	first := host.BeginRegistrationScope()
	second := host.BeginRegistrationScope()
	firstCtx := ContextWithRegistrationScope(context.Background(), first)
	secondCtx := ContextWithRegistrationScope(context.Background(), second)
	if err := noteClientForTest(host, firstCtx, &Client{name: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := noteClientForTest(host, secondCtx, &Client{name: "b"}); err != nil {
		t.Fatal(err)
	}
	if len(first.Snapshot()) != 1 || len(second.Snapshot()) != 1 {
		t.Fatalf("active scope claims leaked: first=%+v second=%+v", first.Snapshot(), second.Snapshot())
	}
}
