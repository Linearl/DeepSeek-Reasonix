package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tl1193 before/after benchmarks. Run on the merge-base and on this branch
// with identical pool fixtures to produce the memory_load / first-recall
// comparison the coordination line asked for (boot memory_load was the boot
// prompt stage's largest suspect at 859ms, tl1229 baseline).

// benchPoolStore builds a user+project store with n facts of realistic size
// (~500-rune bodies, the tier the task book measured pinned demand with).
func benchPoolStore(b *testing.B, n int) (string, string) {
	b.Helper()
	root := b.TempDir()
	user := filepath.Join(root, "user")
	proj := filepath.Join(root, "project")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		b.Fatal(err)
	}
	store := StoreFor(user, proj)
	for i := 0; i < n; i++ {
		body := fmt.Sprintf("Fact %d: %s", i, strings.Repeat("细节内容块，用于模拟真实记忆体的长度与分词压力。", 12))
		if _, err := store.Save(Memory{
			Name:        fmt.Sprintf("bench-fact-%03d", i),
			Description: fmt.Sprintf("bench fact %d description for retrieval", i),
			Type:        TypeProject, Scope: FactScopeProject,
			Activation: ActivationRelevant, Body: body,
		}); err != nil {
			b.Fatal(err)
		}
	}
	return user, proj
}

func BenchmarkMemoryLoad(b *testing.B) {
	user, proj := benchPoolStore(b, 200)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if s := Load(Options{CWD: proj, UserDir: user}); len(s.PinnedGuidance) < 0 {
			b.Fatal("unreachable")
		}
	}
}

// BenchmarkFirstRecall measures Load + the first AutoRecall of each fresh
// snapshot — the "snapshot swap" unit the pre-1193 code rebuilt the whole
// index for, and the unit the resident cache collapses to a fingerprint
// check.
func BenchmarkFirstRecall(b *testing.B) {
	user, proj := benchPoolStore(b, 200)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		set := Load(Options{CWD: proj, UserDir: user})
		if r := set.AutoRecall("retrieval pressure", RecallOptions{}); r.Suppressed == "" && len(r.Hits) == 0 && r.UsedChars < 0 {
			b.Fatal("unreachable")
		}
	}
}
