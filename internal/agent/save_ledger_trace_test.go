package agent

import (
	"crypto/sha256"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"

	"reasonix/internal/provider"
)

func ledgerTraceFunctions(pcs []uintptr) []string {
	var out []string
	frames := runtime.CallersFrames(pcs)
	for {
		frame, more := frames.Next()
		out = append(out, frame.Function)
		if !more {
			break
		}
	}
	return out
}

// TestRecentLedgerCommitNamesTheWriter pins the tl1443 peer attribution: the
// revision a second in-process writer committed is found by path+revision and
// its recorded stack names the save path that wrote it.
func TestRecentLedgerCommitNamesTheWriter(t *testing.T) {
	path := schemaOneSessionPath(t, "peer.jsonl")
	first := NewSession("sys")
	first.Add(provider.Message{Role: provider.RoleUser, Content: "m1"})
	if err := first.SaveSnapshot(path); err != nil {
		t.Fatalf("first save: %v", err)
	}
	other, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	other.Add(provider.Message{Role: provider.RoleUser, Content: "m2"})
	if err := other.SaveSnapshot(path); err != nil {
		t.Fatalf("second writer save: %v", err)
	}
	revision, digest, err := sessionContentRevision(path)
	if err != nil {
		t.Fatal(err)
	}
	trace, ok := RecentLedgerCommit(path, revision)
	if !ok {
		t.Fatalf("revision %d committed in-process must be traced", revision)
	}
	if trace.Revision != revision || trace.Digest != digest || trace.At.IsZero() {
		t.Fatalf("trace = %+v, want revision %d digest %s", trace, revision, digest)
	}
	funcs := strings.Join(ledgerTraceFunctions(trace.PCs), "\n")
	for _, want := range []string{"(*Session).SaveSnapshot", "TestRecentLedgerCommitNamesTheWriter"} {
		if !strings.Contains(funcs, want) {
			t.Fatalf("trace stack lacks %q:\n%s", want, funcs)
		}
	}
	if _, ok := RecentLedgerCommit(path, revision+100); ok {
		t.Fatal("an uncommitted revision must not be traced")
	}
	if _, ok := RecentLedgerCommit(path+".other", revision); ok {
		t.Fatal("another path's commit must not match")
	}
}

func TestLedgerCommitTraceIsBoundedAndConcurrent(t *testing.T) {
	var digest [sha256.Size]byte
	base := fmt.Sprintf("ledger-trace-%p", t)
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 64 {
				noteLedgerCommit(fmt.Sprintf("%s-%d", base, w), int64(i+1), digest)
				_, _ = RecentLedgerCommit(fmt.Sprintf("%s-%d", base, w), int64(i+1))
			}
		}()
	}
	wg.Wait()

	evict := base + "-evict"
	for i := range ledgerCommitTraceCap + 1 {
		noteLedgerCommit(evict, int64(i+1), digest)
	}
	if _, ok := RecentLedgerCommit(evict, 1); ok {
		t.Fatal("ring must evict the oldest commit once full")
	}
	if _, ok := RecentLedgerCommit(evict, int64(ledgerCommitTraceCap+1)); !ok {
		t.Fatal("newest commit must survive eviction")
	}
}
