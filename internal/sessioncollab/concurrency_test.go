package sessioncollab

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func writeSession(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
}

// Regression: every tool call builds its own CardStore, so a per-instance
// mutex cannot serialize read-modify-write across calls. Concurrent updates to
// one card must not lose a node.
func TestCardStoreConcurrentUpdatesDoNotLoseNodes(t *testing.T) {
	root := t.TempDir()
	created, err := NewCardStore(root).Create(Card{Title: "concurrent"})
	if err != nil {
		t.Fatal(err)
	}
	const writers = 8
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func(n int) {
			defer wg.Done()
			_, _ = NewCardStore(root).Update(created.ID, func(c *Card) error {
				c.Nodes = append(c.Nodes, CardNode{Note: "n"})
				return nil
			})
		}(i)
	}
	wg.Wait()
	got, err := NewCardStore(root).Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Nodes) != writers {
		t.Fatalf("lost updates: want %d nodes, got %d", writers, len(got.Nodes))
	}
}

// ScanDir must publish the workspace so cross-project delivery resolves the
// target's own mailbox root instead of silently using the sender's.
func TestScanDirPublishesWorkspace(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, filepath.Join(dir, "a.jsonl"))
	got := ScanDir(dir, filepath.Join(dir, "..", "proj"), func(string) (string, string, string, string, bool) {
		return "sc_a", "expert", "topic_a", "A", true
	})
	if len(got) != 1 {
		t.Fatalf("scan: %+v", got)
	}
	if got[0].Workspace == "" {
		t.Fatal("scan must publish the workspace root for mailbox routing")
	}
}
