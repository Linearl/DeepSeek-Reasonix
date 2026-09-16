package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionReplayBudgetForMissingFile(t *testing.T) {
	b := SessionReplayBudgetFor(filepath.Join(t.TempDir(), "nope.jsonl"))
	if b.Exists || b.Size != 0 {
		t.Fatalf("missing file budget = %+v", b)
	}
	if b.ApproachingFence(0) {
		t.Fatal("missing file must not approach the fence")
	}
}

func TestSessionReplayBudgetApproachingFence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(path, []byte(`{"role":"user","content":"hi"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := SessionReplayBudgetFor(path)
	if !b.Exists || b.Size <= 0 {
		t.Fatalf("budget = %+v", b)
	}
	if b.ApproachingFence(0) {
		t.Fatalf("tiny file must not approach the fence: %+v", b)
	}
	synthetic := SessionReplayBudget{Exists: true, Size: 100, Limit: 100, Ratio: 1.0}
	if !synthetic.ApproachingFence(0) {
		t.Fatal("ratio 1.0 must approach the fence")
	}
	if (SessionReplayBudget{Exists: true, Size: 89, Limit: 100, Ratio: 0.89}).ApproachingFence(0.90) {
		t.Fatal("0.89 must not approach a 0.90 fence")
	}
}
