package agent

// P18-R1 tests: the load path may serve from the task-196 session graph cache
// only when the cached graph still matches the bytes on disk. These tests pin
// the equivalence (cached load == replay load), the miss matrix (append,
// generation rotation, tail-truncated state, disabled gate), and expose the
// hit/miss counters for on-device proof. Wall-clock numbers are logged for
// measurement, never asserted (scheduling jitter lesson, 451 §8.6).

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

func timeNow() time.Time { return time.Now() }

func timeSince(t time.Time) string { return time.Since(t).String() }

func p18RoundSession(t *testing.T, rounds int) *Session {
	t.Helper()
	s := NewSession("You are Reasonix, a coding agent.")
	for i := 0; i < rounds; i++ {
		s.Add(provider.Message{Role: provider.RoleUser, Content: fmt.Sprintf("probe prompt %d", i)})
		s.Add(provider.Message{
			Role:    provider.RoleAssistant,
			Content: fmt.Sprintf("round %d answer", i),
			ToolCalls: []provider.ToolCall{{
				ID:        fmt.Sprintf("call_%08d", i),
				Name:      "write_file",
				Arguments: fmt.Sprintf(`{"path":"docs/p%d.md","content":"%s"}`, i, string(make([]byte, 512))),
			}},
		})
		s.Add(provider.Message{Role: provider.RoleTool, ToolCallID: fmt.Sprintf("call_%08d", i), Name: "write_file", Content: "wrote 512 bytes"})
	}
	return s
}

func p18Digest(t *testing.T, s *Session) string {
	t.Helper()
	d, err := s.ContentDigest()
	if err != nil {
		t.Fatalf("content digest: %v", err)
	}
	return d
}

func TestP18LoadCacheSecondLoadHitsAndMatches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p18.jsonl")
	if err := p18RoundSession(t, 40).Save(path); err != nil {
		t.Fatal(err)
	}

	first, err := LoadSession(path) // populates the graph cache (miss)
	if err != nil {
		t.Fatal(err)
	}
	hitsBefore, _ := SessionDAGLoadCacheStats()
	second, err := LoadSession(path) // must serve from the cache
	if err != nil {
		t.Fatal(err)
	}
	hitsAfter, _ := SessionDAGLoadCacheStats()
	if hitsAfter != hitsBefore+1 {
		t.Fatalf("second LoadSession did not hit the load cache: hits %d -> %d", hitsBefore, hitsAfter)
	}
	if got, want := p18Digest(t, second), p18Digest(t, first); got != want {
		t.Fatalf("cached load digest %s != replay load digest %s", got, want)
	}
	if second.Len() != first.Len() {
		t.Fatalf("cached load %d messages != replay load %d", second.Len(), first.Len())
	}
	t.Logf("P18: second full LoadSession served from cache; messages=%d digest=%s", second.Len(), got2s(p18Digest(t, second)))
}

func got2s(s string) string {
	if len(s) > 16 {
		return s[:16]
	}
	return s
}

func TestP18LoadCacheSeesAppendsAfterSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p18.jsonl")
	s := p18RoundSession(t, 10)
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSession(path); err != nil { // populate cache
		t.Fatal(err)
	}
	s.Add(provider.Message{Role: provider.RoleUser, Content: "appended after cache fill"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "appended answer"})
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadSession(path) // save extended the cached state: hit with newer tail
	if err != nil {
		t.Fatal(err)
	}
	last := reloaded.Snapshot()[reloaded.Len()-1]
	if last.Content != "appended answer" {
		t.Fatalf("cached load missed the appended turn: last=%q", last.Content)
	}
}

func TestP18LoadCacheUserMessagesMatchReplay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p18.jsonl")
	if err := p18RoundSession(t, 8).Save(path); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSession(path); err != nil { // populate cache
		t.Fatal(err)
	}
	viaCache, err := LoadSessionUserMessages(path)
	if err != nil {
		t.Fatal(err)
	}
	full, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, m := range full.Snapshot() {
		if m.Role == provider.RoleUser {
			want = append(want, m.Content)
		}
	}
	if len(viaCache) != len(want) {
		t.Fatalf("user turns via cache %d != via replay %d", len(viaCache), len(want))
	}
	for i := range want {
		if viaCache[i].Message.Content != want[i] {
			t.Fatalf("user turn %d: cache %q != replay %q", i, viaCache[i].Message.Content, want[i])
		}
	}
}

func TestP18LoadCacheMissOnGenerationRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p18.jsonl")
	if err := p18RoundSession(t, 6).Save(path); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSession(path); err != nil {
		t.Fatal(err)
	}
	logPath := store.SessionEventLog(path)
	st := sessionGraphCacheGet(logPath)
	if st == nil {
		t.Fatal("cache did not retain the replayed state")
	}
	// A rotation the size guard cannot see: same bytes, different generation.
	// The state carries its own RWMutex, so doctors go through it.
	st.mu.Lock()
	st.generation = st.generation + 1
	st.mu.Unlock()

	hitsBefore, _ := SessionDAGLoadCacheStats()
	reloaded, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	_, missesAfter := SessionDAGLoadCacheStats()
	_ = missesAfter
	hitsAfter, _ := SessionDAGLoadCacheStats()
	if hitsAfter != hitsBefore {
		t.Fatalf("stale generation must miss the load cache: hits %d -> %d", hitsBefore, hitsAfter)
	}
	if reloaded.Len() == 0 {
		t.Fatal("fallthrough replay produced an empty session")
	}
}

func TestP18LoadCacheMissOnTailTruncatedState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p18.jsonl")
	if err := p18RoundSession(t, 6).Save(path); err != nil {
		t.Fatal(err)
	}
	logPath := store.SessionEventLog(path)
	// The Put side refuses tail-truncated states (task-196 contract); prove the
	// Get-side guard too by flipping the flag on a cached full state in place.
	if _, err := LoadSession(path); err != nil {
		t.Fatal(err)
	}
	st := sessionGraphCacheGet(logPath)
	if st == nil {
		t.Fatal("cache did not retain the replayed state")
	}
	st.mu.Lock()
	st.tailTruncated = true
	st.mu.Unlock()

	hitsBefore, _ := SessionDAGLoadCacheStats()
	reloaded, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	hitsAfter, _ := SessionDAGLoadCacheStats()
	if hitsAfter != hitsBefore {
		t.Fatal("tail-truncated state must never serve a full load")
	}
	if reloaded.Len() == 0 {
		t.Fatal("fallthrough replay produced an empty session")
	}
}

func TestP18LoadCacheEnvKillSwitch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p18.jsonl")
	if err := p18RoundSession(t, 6).Save(path); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSession(path); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REASONIX_DAG_LOAD_CACHE", "0")
	hitsBefore, _ := SessionDAGLoadCacheStats()
	reloaded, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	hitsAfter, _ := SessionDAGLoadCacheStats()
	if hitsAfter != hitsBefore {
		t.Fatal("kill switch must disable the load-cache fast path")
	}
	if reloaded.Len() == 0 {
		t.Fatal("replay path produced an empty session")
	}
}

func TestP18LoadCacheTimingAtScale(t *testing.T) {
	if os.Getenv("P18_TIMING") == "" {
		t.Skip("measurement-only; set P18_TIMING=1 to run at incident scale")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "p18.jsonl")
	s := p18RoundSession(t, 3100) // ≈9300 messages at the incident's 3-messages-per-round shape
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSession(path); err != nil {
		t.Fatal(err)
	}
	start := timeNow()
	cold, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	coldMs := timeSince(start)
	if _, err := LoadSession(path); err != nil {
		t.Fatal(err)
	}
	start = timeNow()
	warm, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	warmMs := timeSince(start)
	t.Logf("P18 timing: messages=%d cold(replay-ish after cache fill)=%s warm(cached)=%s", warm.Len(), coldMs, warmMs)
	if p18Digest(t, warm) != p18Digest(t, cold) {
		t.Fatal("cached and replayed digests diverge at scale")
	}
}
