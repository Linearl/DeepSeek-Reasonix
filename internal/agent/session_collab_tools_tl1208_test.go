package agent

import (
	"testing"
	"time"

	"reasonix/internal/sessioncollab"
	"reasonix/internal/topicregistry"
)

// tl1208 三读口直查（agent 地址簿侧）：同 topic 多物理文件分属不同 scope
// 目录时，注册表唯一归属 scope 的文件在内容承载同级里优先；注册表无条目
// （headless 冷机）保持 1197 启发式。

func resetTL1208Registry(t *testing.T, configure func()) {
	t.Helper()
	topicregistry.ResetForTest()
	if configure != nil {
		configure()
	}
	t.Cleanup(topicregistry.ResetForTest)
}

func identityProbe(topicID, path string, updatedAt time.Time) sessioncollab.Identity {
	return sessioncollab.Identity{TopicID: topicID, SessionPath: path, UpdatedAt: updatedAt.UnixMilli()}
}

// TestAddressableCandidateBetterPrefersOwnerScope 钉：同 turns（同为空壳或
// 同为正主）时，归属 scope 的候选胜过更新时间更新的非归属候选。
func TestAddressableCandidateBetterPrefersOwnerScope(t *testing.T) {
	resetTL1208Registry(t, func() {
		topicregistry.Configure("", "D:\\fake-projects.json", func() topicregistry.SourceView {
			return topicregistry.SourceView{Projects: []topicregistry.ProjectTopics{{Root: "D:\\proj", Topics: []string{"topic_owner"}}}}
		})
	})
	owner, _ := topicregistry.OwnerOf("topic_owner")
	if owner.Scope != topicregistry.ScopeProject {
		t.Fatalf("registry setup failed: %+v", owner)
	}

	newer := time.Now()
	older := newer.Add(-time.Hour)
	// 候选=归属 scope（较旧），在位=非归属（较新）——归属优先级高于时间。
	if !addressableTopicCandidateBetter(
		identityProbe("topic_owner", "owner.jsonl", older), "project", `D:\proj\`,
		identityProbe("topic_owner", "foreign.jsonl", newer),
	) {
		t.Fatal("owner-scope candidate must beat a newer foreign-scope incumbent")
	}
	// 反向：候选=非归属（global），在位=归属（project 正主，scope 已随
	// 收敛写入 Identity）——即便候选更新也不换。
	incumbent := identityProbe("topic_owner", "owner.jsonl", older)
	incumbent.Scope = "project"
	incumbent.Workspace = `D:\proj`
	if addressableTopicCandidateBetter(
		identityProbe("topic_owner", "foreign.jsonl", newer), "global", "",
		incumbent,
	) {
		t.Fatal("foreign candidate must not displace the owner-scope incumbent")
	}
}

// TestAddressableCandidateBetterMissKeepsT1197 钉：注册表查无条目时保持
// 1197 启发式（内容同级看 UpdatedAt）。
func TestAddressableCandidateBetterMissKeepsT1197(t *testing.T) {
	resetTL1208Registry(t, nil)
	newer := time.Now()
	older := newer.Add(-time.Hour)
	if !addressableTopicCandidateBetter(
		identityProbe("topic_x", "newer.jsonl", newer), "global", "",
		identityProbe("topic_x", "older.jsonl", older),
	) {
		t.Fatal("registry miss must fall back to the newer-update heuristic")
	}
}

// TestAddressableCandidateBetterTurnsStillFirst 钉：内容承载者仍然第一优先
// （归属裁决只作用于同内容级比较）。
func TestAddressableCandidateBetterTurnsStillFirst(t *testing.T) {
	resetTL1208Registry(t, func() {
		topicregistry.Configure("", "D:\\fake-projects.json", func() topicregistry.SourceView {
			return topicregistry.SourceView{Projects: []topicregistry.ProjectTopics{{Root: "D:\\proj", Topics: []string{"topic_owner"}}}}
		})
	})
	shell := identityProbe("topic_owner", "shell.jsonl", time.Now())
	shell.Turns = 0
	content := identityProbe("topic_owner", "content.jsonl", time.Now().Add(-time.Hour))
	content.Turns = 7
	if !addressableTopicCandidateBetter(content, "global", "", shell) {
		t.Fatal("content-bearing candidate must beat the owner-scope shell regardless of time")
	}
	if addressableTopicCandidateBetter(shell, "project", `D:\proj`, content) {
		t.Fatal("owner-scope shell must not displace a content-bearing incumbent")
	}
}
