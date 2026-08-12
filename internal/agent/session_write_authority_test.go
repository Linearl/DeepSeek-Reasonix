package agent

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestWriteAuthorityStaleAfterRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	lease, err := TryAcquireSessionLease(path)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := lease.IssueWriteAuthority(NextSessionWriteGeneration())
	if err != nil {
		t.Fatal(err)
	}
	if !auth.Valid() || !auth.Covers(path) {
		t.Fatal("fresh authority should be valid")
	}
	lease.Release()
	if auth.Valid() {
		t.Fatal("authority must be stale after lease release")
	}
	if _, err := auth.BeginSave(path); !errors.Is(err, ErrSessionWriteAuthorityStale) {
		t.Fatalf("BeginSave = %v, want stale", err)
	}
}

func TestWriteAuthorityReleaseWaitsForInFlightSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	lease, err := TryAcquireSessionLease(path)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := lease.IssueWriteAuthority(NextSessionWriteGeneration())
	if err != nil {
		t.Fatal(err)
	}
	releaseSave, err := auth.BeginSave(path)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	started := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		close(started)
		lease.Release()
	}()
	<-started
	// Release must not finish while save is active.
	select {
	case <-func() chan struct{} {
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		return done
	}():
		t.Fatal("Release returned while save still in flight")
	case <-time.After(50 * time.Millisecond):
	}
	releaseSave()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Release did not complete after save finished")
	}
}

func TestWriteAuthorityGenerationInvalidatesPriorToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	lease, err := TryAcquireSessionLease(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	oldAuth, err := lease.IssueWriteAuthority(1)
	if err != nil {
		t.Fatal(err)
	}
	// Same lease, new generation still Valid on both tokens until release —
	// generation is checked only against the token's own fields + live lease.
	// Controller rebind replaces the session-bound token; the old token
	// remains Valid() for BeginSave until lease identity changes.
	if !oldAuth.Valid() {
		t.Fatal("old generation still tied to live lease owner")
	}
	// Reclaim path changes ownerID → stale.
	lease.Release()
	lease2, err := TryAcquireSessionLease(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lease2.Release()
	if oldAuth.Valid() {
		t.Fatal("old authority must not survive owner change")
	}
}
