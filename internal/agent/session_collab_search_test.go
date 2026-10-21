package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// search_sessions must find a session by a fragment of its title even when it
// would fall outside the first page of list_addressable_sessions.
func TestSearchSessionsByTitleKeyword(t *testing.T) {
	// 任务822: scanAddressable also sweeps the host-global directories
	// (config.ArchiveDir, config.AllProjectSessionDirs, the workspace's
	// project session dir), which on a developer machine carry live
	// conversations — the search then matched real sessions (e.g.
	// "崩溃分析-20261011", "fork 开发主对话") and the exclusive Total==1
	// assertion failed on an otherwise clean fixture. 任务 1009 起该隔离由
	// 包级 TestMain 的 testenv.IsolateUserState 统一承担，本测试不再自己 setenv。
	dir := t.TempDir()
	// A file whose title we control, plus one we do not match.
	hit := filepath.Join(dir, "hit.jsonl")
	miss := filepath.Join(dir, "miss.jsonl")
	writeEmpty(t, hit)
	writeEmpty(t, miss)
	if err := UpdateBranchMeta(hit, true, func(m *BranchMeta) error {
		m.CustomTitle = "Release Notes Draft"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := UpdateBranchMeta(miss, true, func(m *BranchMeta) error {
		m.CustomTitle = "Something else"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	out, err := NewSearchSessionsTool(SessionCollabConfig{
		Enabled: true, SessionDir: dir, WorkspaceRoot: dir,
	}).Execute(nil, []byte(`{"query":"release"}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Total    int `json:"total"`
		Sessions []struct {
			Title string `json:"title"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("search must return JSON: %v\n%s", err, out)
	}
	if payload.Total != 1 || len(payload.Sessions) != 1 || payload.Sessions[0].Title != "Release Notes Draft" {
		t.Fatalf("search missed the title match: %s", out)
	}
	if strings.Contains(out, "Something else") {
		t.Fatalf("search must not return non-matching sessions: %s", out)
	}
}

func TestSearchSessionsRequiresQuery(t *testing.T) {
	tool := NewSearchSessionsTool(SessionCollabConfig{Enabled: true, SessionDir: t.TempDir(), WorkspaceRoot: t.TempDir()})
	if _, err := tool.Execute(nil, []byte(`{}`)); err == nil {
		t.Fatal("empty query must be rejected")
	}
}

// With session_storage=v4 dual-write the same conversation is mirrored into a
// sessions-v4/ directory. The directory must count it once, which it does by
// never scanning v4 and by deduping on the shared basename stem.
func TestScanAddressableDedupesDualWriteStem(t *testing.T) {
	dir := t.TempDir()
	// Same stem in two directories models a legacy copy plus a v4 mirror.
	legacy := filepath.Join(dir, "sessions", "20260917-101010.000.jsonl")
	v4 := filepath.Join(dir, "sessions-v4", "20260917-101010.000.jsonl")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(v4), 0o755); err != nil {
		t.Fatal(err)
	}
	writeEmpty(t, legacy)
	writeEmpty(t, v4)
	// scanAddressable also walks the machine's real session dirs, so the
	// assertion is about *uniqueness of stems*, not about being the only row.
	ids := scanAddressable(filepath.Dir(legacy), dir)
	seenStem := map[string]bool{}
	found := 0
	for _, id := range ids {
		stem := strings.ToLower(strings.TrimSuffix(filepath.Base(id.SessionPath), filepath.Ext(id.SessionPath)))
		if seenStem[stem] {
			t.Fatalf("dual-write stem listed twice: %s", id.SessionPath)
		}
		seenStem[stem] = true
		if stem == "20260917-101010.000" {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("the dual-write conversation must appear exactly once, got %d", found)
	}
}
