package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// tl1197（1074 家族第 3 形态）: 同一 topic 身份的多个物理会话文件
// （正主 + 空壳/跨 scope 壳）在地址簿只出一条 Identity，且收敛到内容
// 承载者。回归钉：get_session_info 按标题不再命中「多条同 id」。
func TestScanAddressableCollapsesSameTopicToContentBearingIdentity(t *testing.T) {
	dir := t.TempDir()
	topicID := "topic_20261025-152234_t1197"

	mkSession := func(name string, turns int, scope string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		if err := SaveBranchMeta(path, BranchMeta{
			CreatedAt:     time.Now().Add(-time.Hour),
			UpdatedAt:     time.Now(),
			Scope:         scope,
			WorkspaceRoot: "D:\\proj",
			TopicID:       topicID,
			TopicTitle:    "剧情核对",
			Turns:         turns,
		}); err != nil {
			t.Fatalf("save meta %s: %v", name, err)
		}
		return path
	}
	// 三处物理注册位：项目空壳（turns=0）、项目正主（turns=7, 有 contact）、
	// global 空壳（turns=0）——用户实报「剧情核对」的同构。
	shellA := mkSession("20261025-152234.750433100-owner.jsonl", 0, "project")
	owner := mkSession("20261025-152313.841311900-owner.jsonl", 7, "project")
	if err := UpdateBranchMeta(owner, true, func(m *BranchMeta) error {
		m.ContactID = "sc_t1197_owner"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	shellC := mkSession("20261027-082459.297801600-session.jsonl", 0, "global")
	_ = shellA
	_ = shellC

	ids := scanAddressable(dir, dir)
	var hits []sessionIdentityProbe
	for _, id := range ids {
		if id.TopicID == topicID {
			hits = append(hits, sessionIdentityProbe{path: id.SessionPath, turns: id.Turns})
		}
	}
	if len(hits) != 1 {
		t.Fatalf("same topic produced %d identities (%v), want exactly 1", len(hits), hits)
	}
	if hits[0].path != owner {
		t.Fatalf("collapsed identity = %q, want the content-bearing owner %q", hits[0].path, owner)
	}
	if hits[0].turns != 7 {
		t.Fatalf("collapsed identity turns = %d, want 7", hits[0].turns)
	}
}

// 不同 topic 的条目互不受去重影响——收敛只发生在同 topic 身份内部。
func TestScanAddressableTopicDedupKeepsDistinctTopics(t *testing.T) {
	dir := t.TempDir()
	for _, topic := range []string{"topic_t1197_a", "topic_t1197_b"} {
		path := filepath.Join(dir, topic+".jsonl")
		if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := SaveBranchMeta(path, BranchMeta{
			TopicID: topic, TopicTitle: topic, Scope: "global",
		}); err != nil {
			t.Fatal(err)
		}
	}
	ids := scanAddressable(dir, dir)
	seen := map[string]int{}
	for _, id := range ids {
		if id.TopicID != "" {
			seen[id.TopicID]++
		}
	}
	for _, topic := range []string{"topic_t1197_a", "topic_t1197_b"} {
		if seen[topic] != 1 {
			t.Fatalf("distinct topic %s appeared %d times, want 1", topic, seen[topic])
		}
	}
}

// 验收 1 的端到端钉：get_session_info 按标题解析（ResolveTarget）在
// 正主+空壳同 topic 同题的场景下唯一命中正主，不再报「matches N sessions」。
func TestResolveTargetByTitleUniqueAfterTopicCollapse(t *testing.T) {
	dir := t.TempDir()
	topicID := "topic_20261025-152234_t1197"
	shell := filepath.Join(dir, "20261027-082459.297801600-session.jsonl")
	owner := filepath.Join(dir, "20261025-152313.841311900-owner.jsonl")
	for _, p := range []string{shell, owner} {
		if err := os.WriteFile(p, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := SaveBranchMeta(shell, BranchMeta{TopicID: topicID, TopicTitle: "剧情核对", Scope: "global"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveBranchMeta(owner, BranchMeta{TopicID: topicID, TopicTitle: "剧情核对", Scope: "project", Turns: 7}); err != nil {
		t.Fatal(err)
	}

	ids := scanAddressable(dir, dir)
	resolved, err := ResolveTarget(ids, "剧情核对")
	if err != nil {
		t.Fatalf("ResolveTarget by title must be unique after collapse: %v", err)
	}
	if resolved.SessionPath != owner {
		t.Fatalf("resolved to %q, want the content-bearing owner %q", resolved.SessionPath, owner)
	}
}

type sessionIdentityProbe struct {
	path  string
	turns int
}
