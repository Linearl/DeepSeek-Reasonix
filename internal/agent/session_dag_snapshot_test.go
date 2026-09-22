package agent

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Block2 m2 (audit H1-1): the shared-cached graph is mutated in place by
// replayFrom while readers take snapshots. Pin that snapshotNodes and
// replayFrom are mutually safe — run with -race to make the guard real.
func TestDAGStateSnapshotConcurrentWithReplay(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "sess.events.jsonl")
	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := replaySessionDAG(context.Background(), logPath, defaultSessionReplayLimits)
	if err != nil {
		t.Fatalf("initial replay: %v", err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_ = st.replayFrom(context.Background(), st.lastGoodEnd, defaultSessionReplayLimits)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			for range st.snapshotNodes() {
			}
		}
	}()
	wg.Wait()
}
