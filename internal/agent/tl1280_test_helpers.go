package agent

// tl1280 测试夹具收敛：写路径慢车道（save_dag 主路由/plan/observation/shutdown、
// session_head_ops/stale、turn_markers、materialization_gap、fold_landing 等）
// 删除后，仍被迁移/重放/缓存/维护测试引用的最小共享夹具集中在此。

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// NOTE: dagTestSession/dagMsg/dagMessageEntry/dagAppend/dagReplay/dagContents/
// dagLinearLog/dagChain live in session_dag_replay_test.go (shared DAG replay
// fixtures, kept because the replay/rotate/migrate/tail suites still use them).

// p18RoundSession builds the P18 probe's round-shaped session in memory
// (system + n rounds of user/assistant/tool). tl1280: the round session is a
// flat schema-1 shape now; the fixture stays so the load-cache probe keeps its
// realistic transcript weight.
func p18RoundSession(rounds int) *Session {
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

// tl1228BuildDAGSession writes a schema-2 events log of ~mb MiB (header +
// chained user/assistant messages with ~4KiB bodies). tl1280: the schema-2
// write path is gone, so this fixture writes the log bytes directly through
// the replay helpers (used by the tl1281 sealed-session checkpoint tests).
func tl1228BuildDAGSession(t testing.TB, mb int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bench.jsonl")
	const bodyBytes = 4096
	bodyRunes := make([]byte, bodyBytes)
	for i := range bodyRunes {
		bodyRunes[i] = byte('a' + i%26)
	}
	body := string(bodyRunes)
	base := time.Date(2026, 1, 8, 10, 0, 0, 0, time.UTC)
	entryOverhead := 768
	perEntry := bodyBytes + entryOverhead
	n := mb<<20 / perEntry
	if n < 8 {
		n = 8
	}
	entries := make([]sessionDAGEntry, 0, n+1)
	entries = append(entries, sessionDAGEntry{Type: sessionDAGTypeLog, Generation: 1, At: base})
	parent := ""
	for i := 0; i < n; i++ {
		role := provider.RoleUser
		if i%2 == 1 {
			role = provider.RoleAssistant
		}
		e, err := newSessionDAGMessageEntry(SessionMainHead, parent, "", "t1",
			provider.Message{Role: role, Content: body, ID: fmt.Sprintf("B%d", i)}, base.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatalf("entry %d: %v", i, err)
		}
		entries = append(entries, e)
		parent = fmt.Sprintf("B%d", i)
	}
	if _, err := appendSessionDAGEntries(path, entries, false); err != nil {
		t.Fatalf("append: %v", err)
	}
	return path
}

// dagWideLog writes a wide schema-2 log (header + count chained messages) for
// the tail-window fixtures that still pin the sealed checkpoint fallback.
func dagWideLog(t *testing.T, sessionPath string, count int) (ids []string, base time.Time) {
	t.Helper()
	base = time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC)
	entries := []sessionDAGEntry{{Type: sessionDAGTypeLog, Generation: 1, At: base}}
	parent := ""
	for i := 0; i < count; i++ {
		role := provider.RoleUser
		if i%2 == 1 {
			role = provider.RoleAssistant
		}
		id := fmt.Sprintf("M%d", i)
		e, err := newSessionDAGMessageEntry(SessionMainHead, parent, "", "t1",
			provider.Message{Role: role, Content: fmt.Sprintf("m%d:%s", i, strings.Repeat("x", 400)), ID: id}, base.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatalf("message entry: %v", err)
		}
		entries = append(entries, e)
		parent = id
		ids = append(ids, id)
	}
	if _, err := appendSessionDAGEntries(sessionPath, entries, false); err != nil {
		t.Fatalf("append: %v", err)
	}
	return ids, base
}

// dagSavedSession saves a fresh schema-1 session with the given alternating
// user/assistant contents (used by content-probe and fork fixtures).
func dagSavedSession(t *testing.T, path string, contents ...string) *Session {
	t.Helper()
	s := NewSession("sys")
	for i, c := range contents {
		role := provider.RoleUser
		if i%2 == 1 {
			role = provider.RoleAssistant
		}
		s.Add(provider.Message{Role: role, Content: c})
	}
	if err := s.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	return s
}

// resetGraphCacheForTest empties the process graph cache under its lock so
// cache tests start from a clean slate. Counters are process-wide and read as
// before/after deltas by the tests that care.
func resetGraphCacheForTest() {
	sessionGraphCacheMu.Lock()
	defer sessionGraphCacheMu.Unlock()
	sessionGraphCache = map[string]*sessionGraphCacheEntry{}
	sessionGraphCacheBytes = 0
}

// t923SyntheticMessages builds the 923 benchmark transcript: a system prompt
// plus n alternating user/assistant turns of realistic size.
func t923SyntheticMessages(n int) []provider.Message {
	msgs := make([]provider.Message, 0, n+1)
	msgs = append(msgs, provider.Message{Role: provider.RoleSystem, Content: "sys"})
	for i := 0; i < n; i++ {
		role := provider.RoleUser
		content := fmt.Sprintf("t923 user turn %d: %s", i, strings.Repeat("u", 400))
		if i%2 == 1 {
			role = provider.RoleAssistant
			content = fmt.Sprintf("t923 assistant turn %d: %s", i, strings.Repeat("a", 1200))
		}
		msgs = append(msgs, provider.Message{Role: role, Content: content})
	}
	return msgs
}

// selfHealFixture builds the 1011 self-heal incident shape: a schema-1
// session whose projection overflows a tiny window (image + long text), so
// the diet path has something to act on. tl1280: the schema-2 dominated-head
// fixture left with the slow lane; this flat fixture keeps the diet tests
// honest without any DAG bytes.
func selfHealFixture(t *testing.T) (path string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "heal.jsonl")
	s := NewSession("sys")
	bigImage := "data:image/png;base64," + strings.Repeat("A", 2<<20) // 2 MiB
	m := provider.Message{Role: provider.RoleUser, Content: "look", Images: []string{bigImage}}
	s.Add(m)
	for i := 0; i < 40; i++ {
		s.Add(provider.Message{Role: provider.RoleAssistant, Content: strings.Repeat("x", 40000)})
	}
	for i := 0; i < 3; i++ {
		s.Add(provider.Message{Role: provider.RoleUser, Content: fmt.Sprintf("tail-%d", i)})
	}
	if err := s.Save(path); err != nil {
		t.Fatalf("fixture save: %v", err)
	}
	return path
}

// hashSessionTree fingerprints every regular file in the session directory so
// the zero-write acceptance (健康会话零改动) is byte-exact, not mtime-guessy.
func hashSessionTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		blob, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(blob)
		out[e.Name()] = fmt.Sprintf("%x", sum[:])
	}
	return out
}
