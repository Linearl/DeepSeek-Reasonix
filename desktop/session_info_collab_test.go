package main

import (
	"strings"
	"testing"
)

// 任务 285 验收①（宿主侧）：分组解析与侧栏同一事实源（loadProjectsFile 的
// groups 表），首个命中组生效；空标题组跳过；未命中给 not-ok（字段缺席，
// 绝不猜）。纯表驱动，不触碰用户真实配置。
func TestSessionGroupTitleForTopic(t *testing.T) {
	groups := []desktopGroup{
		{ID: "g1", Title: "审计", TopicIDs: []string{"topic-audit"}},
		{ID: "g2", Title: "", TopicIDs: []string{"topic-untitled"}},
		{ID: "g3", Title: "调研", TopicIDs: []string{"topic-research", "topic-research-2"}},
	}
	for _, tc := range []struct {
		name   string
		topic  string
		want   string
		wantOK bool
	}{
		{"命中组", "topic-audit", "审计", true},
		{"多成员组的第二个成员", "topic-research-2", "调研", true},
		{"空标题组不算已知", "topic-untitled", "", false},
		{"未命中", "topic-nowhere", "", false},
		{"空 topic 直接 miss", "  ", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := sessionGroupTitleForTopic(groups, tc.topic)
			if got != tc.want || (got != "") != tc.wantOK {
				t.Fatalf("sessionGroupTitleForTopic = %q, want %q (ok=%v)", got, tc.want, tc.wantOK)
			}
		})
	}
	if got := sessionGroupTitleForTopic(nil, "topic-audit"); got != "" {
		t.Fatalf("nil groups = %q, want empty", got)
	}
}

// 无宿主运行时（零值 App，无 catalog）：版本谱系探针必须返回 not-ok，切换
// 探针必须给可操作错误——CLI/测试形态下工具面给诚实的拒绝而不是猜或崩。
func TestCollabSessionVersionProbesWithoutCatalog(t *testing.T) {
	a := &App{}
	if members, ok := a.collabSessionVersions("global", "", "topic-x", "C:\\sessions\\a.jsonl"); ok || members != nil {
		t.Fatalf("no-catalog versions = (%v, %v), want not-ok", members, ok)
	}
	err := a.collabAdoptSessionVersion("global", "", "topic-x", "C:\\sessions\\a.jsonl", "head-1")
	// 空 catalog 下谱系为空表：拒绝时给出下一步（list_session_versions），
	// 绝不静默成功。
	if err == nil || !strings.Contains(err.Error(), "list_session_versions first") {
		t.Fatalf("no-catalog adopt err = %v, want the actionable refusal", err)
	}
}
