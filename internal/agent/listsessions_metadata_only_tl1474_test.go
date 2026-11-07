package agent

// tl1474 pins for ListSessionsMetadataOnly — the trash listing's read mode.
//
// Contract under test:
//  1. Display-field parity with ListSessions (the shared sessionsFromOrder
//     projection) on every listing shape: fresh, legacy-stale, turn-0,
//     meta-less empty, titled sidecar.
//  2. No transcript replay: a sidecar carrying RecoveryPreferred + digest must
//     not make the metadata-only read touch the save-path lock (the proving
//     read mode — ListSessionOrder's default resolver — LoadSessions the
//     transcript and blocks on that lock; pinning completion under a held
//     lock goes red if anyone re-couples this read to the proof).

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/provider"
)

func tl1474WriteSession(t *testing.T, path string, msgs []provider.Message) {
	t.Helper()
	writeSessionFile(t, path, msgs)
}

func tl1474CompareDisplayFields(t *testing.T, probing, metadataOnly []SessionInfo) {
	t.Helper()
	if len(probing) != len(metadataOnly) {
		t.Fatalf("record count drift: probing=%d metadataOnly=%d", len(probing), len(metadataOnly))
	}
	for i := range probing {
		a, b := probing[i], metadataOnly[i]
		if a.Path != b.Path ||
			a.Preview != b.Preview ||
			a.Turns != b.Turns ||
			a.CountsKnown != b.CountsKnown ||
			a.Scope != b.Scope ||
			a.WorkspaceRoot != b.WorkspaceRoot ||
			a.TopicID != b.TopicID ||
			a.TopicTitle != b.TopicTitle ||
			a.CustomTitle != b.CustomTitle ||
			a.Recovered != b.Recovered ||
			a.RecoveryDigest != b.RecoveryDigest ||
			a.RecoveryReason != b.RecoveryReason ||
			a.ParentID != b.ParentID ||
			a.VersionKind != b.VersionKind ||
			a.VersionState != b.VersionState ||
			!a.CreatedAt.Equal(b.CreatedAt) ||
			!a.LastActivityAt.Equal(b.LastActivityAt) {
			t.Fatalf("display-field drift at %d:\n  probing     %+v\n  metadataOnly %+v", i, a, b)
		}
	}
}

// Pin 1: display-field parity across the listing shapes the trash buckets
// actually exhibit (tl1429b meta-less buckets included — they surface as the
// stale-projection "History is being indexed" shape here).
func TestTL1474MetadataOnlyMatchesListSessionsDisplayFields(t *testing.T) {
	dir := t.TempDir()

	// Fresh session with sidecar counts + titles.
	fresh := filepath.Join(dir, "20260101-000001-model-fresh.jsonl")
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "fresh session first question"},
		{Role: provider.RoleAssistant, Content: "answer"},
	}
	tl1474WriteSession(t, fresh, msgs)
	digest, err := digestSessionMessages(msgs)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveBranchMeta(fresh, BranchMeta{ID: BranchID(fresh), Revision: 1, ContentDigest: digestString(digest), CustomTitle: "custom title", TopicTitle: "topic title"}); err != nil {
		t.Fatalf("SaveBranchMeta: %v", err)
	}
	if err := UpdateSessionMeta(fresh, "", "fresh preview line", 1, true); err != nil {
		t.Fatalf("UpdateSessionMeta: %v", err)
	}

	// Legacy session with no sidecar: stale projection, file has content.
	legacy := filepath.Join(dir, "20260101-000002-model-legacy.jsonl")
	tl1474WriteSession(t, legacy, []provider.Message{
		{Role: provider.RoleUser, Content: "legacy content"},
		{Role: provider.RoleAssistant, Content: "x"},
	})

	// Fresh zero-turn session: dropped by the shared projection.
	empty := filepath.Join(dir, "20260101-000003-model-empty.jsonl")
	tl1474WriteSession(t, empty, []provider.Message{
		{Role: provider.RoleSystem, Content: "only system"},
	})
	emptyDigest, err := digestSessionMessages([]provider.Message{{Role: provider.RoleSystem, Content: "only system"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveBranchMeta(empty, BranchMeta{ID: BranchID(empty), Revision: 1, ContentDigest: digestString(emptyDigest)}); err != nil {
		t.Fatalf("SaveBranchMeta: %v", err)
	}
	if err := UpdateSessionMeta(empty, "", "", 0, true); err != nil {
		t.Fatalf("UpdateSessionMeta: %v", err)
	}

	probing, err := ListSessions(dir)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	metadataOnly, err := ListSessionsMetadataOnly(dir)
	if err != nil {
		t.Fatalf("ListSessionsMetadataOnly: %v", err)
	}
	tl1474CompareDisplayFields(t, probing, metadataOnly)
	if len(probing) != 2 {
		t.Fatalf("expected 2 visible records (fresh + legacy), got %d", len(probing))
	}
}

// Pin 2: the metadata-only read must not replay a transcript even when the
// sidecar carries an unproven recovery preference. Observable without timing:
// with the transcript's save-path lock held by another holder, the proving
// read (ListSessionOrder's default resolver → LoadSession) blocks on
// lockSessionSavePath, while the metadata-only read never asks for it and
// returns the record regardless.
func TestTL1474MetadataOnlySkipsPreferredProofUnderSavePathLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "20260101-000004-model-preferred.jsonl")
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "preferred branch content"},
		{Role: provider.RoleAssistant, Content: "a"},
	}
	tl1474WriteSession(t, path, msgs)
	digest, err := digestSessionMessages(msgs)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveBranchMeta(path, BranchMeta{
		ID:                      BranchID(path),
		Revision:                1,
		ContentDigest:           digestString(digest),
		Recovered:               true,
		RecoveryDigest:          digestString(digest),
		RecoveryPreferred:       true,
		RecoveryPreferredDigest: digestString(digest),
	}); err != nil {
		t.Fatalf("SaveBranchMeta: %v", err)
	}
	if err := UpdateSessionMeta(path, "", "preferred preview", 1, true); err != nil {
		t.Fatalf("UpdateSessionMeta: %v", err)
	}

	unlock, ok := tryLockSessionSavePath(path)
	if !ok {
		t.Fatalf("could not take the save-path lock for the pin")
	}
	defer unlock()

	done := make(chan []SessionInfo, 1)
	errCh := make(chan error, 1)
	go func() {
		infos, err := ListSessionsMetadataOnly(dir)
		if err != nil {
			errCh <- err
			return
		}
		done <- infos
	}()
	select {
	case infos := <-done:
		if len(infos) != 1 || filepath.Base(infos[0].Path) != filepath.Base(path) {
			t.Fatalf("expected the preferred record to surface, got %+v", infos)
		}
	case err := <-errCh:
		t.Fatalf("metadata-only read failed: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatalf("metadata-only read blocked on the save-path lock — the preferred-proof got re-coupled")
	}
}

// Guard the fixture assumption the lock pin relies on: the PROVING read really
// does try to LoadSession (so the pin above has teeth — it goes red if the
// default resolver ever stops proving, or if this fixture stops exercising it).
func TestTL1474ProvingReadReallyLoadsUnderPreference(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "20260101-000005-model-prove.jsonl")
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "to be proven"},
		{Role: provider.RoleAssistant, Content: "a"},
	}
	tl1474WriteSession(t, path, msgs)
	digest, err := digestSessionMessages(msgs)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveBranchMeta(path, BranchMeta{
		ID:                      BranchID(path),
		Revision:                1,
		ContentDigest:           digestString(digest),
		RecoveryPreferred:       true,
		RecoveryPreferredDigest: digestString(digest),
	}); err != nil {
		t.Fatalf("SaveBranchMeta: %v", err)
	}

	unlock, ok := tryLockSessionSavePath(path)
	if !ok {
		t.Fatalf("could not take the save-path lock for the pin")
	}
	defer unlock()

	done := make(chan struct{}, 1)
	go func() {
		// The proving read; ListSessions on this fixture re-derives the
		// preference and therefore must block on the held lock.
		_, _ = ListSessions(dir)
		done <- struct{}{}
	}()
	select {
	case <-done:
		t.Fatalf("proving read did not block on the save-path lock — fixture lost its teeth")
	case <-time.After(300 * time.Millisecond):
		// expected: still blocked
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("fixture vanished: %v", err)
	}
}
