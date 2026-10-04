package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 任务 454 验收（工具层）：group 参数接受侧栏组 title 与扁平组 id 两种拼法，
// 命中行的 group 字段回填组 title，未分组会话不泄漏进任何组的过滤页，无
// group 参数时行为与 task 285 完全一致；宿主探针缺席（CLI/测试形态）退回
// 仅 title 匹配，id 拼法不再静默假命中。
func TestDirectoryGroupFilterAcceptsTitleAndFlatID(t *testing.T) {
	// 隔离机器级扫描根（archive / projects/*/sessions 都归 REASONIX_STATE_HOME），
	// 否则 scanAddressable 会扫进本机真实会话，total 断言失真。
	stateHome := t.TempDir()
	t.Setenv("REASONIX_STATE_HOME", stateHome)
	cfg, refs := infoFixture(t)
	other := filepath.Join(cfg.SessionDir, "other.jsonl")
	if err := os.WriteFile(other, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	otherMeta := `{"id":"b2","contact_id":"ct_other","custom_title":"Other","topic_id":"topic-other"}`
	if err := os.WriteFile(other+".meta", []byte(otherMeta), 0o644); err != nil {
		t.Fatal(err)
	}
	const groupTitle = "reasonix-for-ai"
	const groupID = "collab-reasonix"
	cfg.SessionGroup = func(topicID string) (string, bool) {
		if topicID == refs.topic {
			return groupTitle, true
		}
		return "", false
	}
	// 探针模拟 desktop 宿主（title/id 双拼法 EqualFold + 成员表），同时断言
	// 工具层透传的是 topic 原文与过滤词原文——归属判定只发生在宿主侧。
	probes := 0
	cfg.SessionGroupMatch = func(topicID, group string) bool {
		probes++
		switch topicID {
		case refs.topic, "topic-other":
		default:
			t.Fatalf("probe got unknown topic %q", topicID)
		}
		switch group {
		case groupTitle, groupID, "Collab-Reasonix":
		default:
			t.Fatalf("probe got unexpected group %q", group)
		}
		return topicID == refs.topic && (strings.EqualFold(group, groupID) || strings.EqualFold(group, groupTitle))
	}

	type page struct {
		Total    int `json:"total"`
		Returned int `json:"returned"`
		Sessions []struct {
			ContactID string `json:"contactId"`
			TopicID   string `json:"topicId"`
			Group     string `json:"group"`
		} `json:"sessions"`
	}
	parse := func(raw string) page {
		t.Helper()
		var p page
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			t.Fatalf("page is not JSON: %v\n%s", err, raw)
		}
		return p
	}
	oneRowAbout := func(p page, topic string) {
		t.Helper()
		if p.Total != 1 || p.Returned != 1 || len(p.Sessions) != 1 {
			t.Fatalf("filter total/returned/rows = %d/%d/%d, want 1/1/1", p.Total, p.Returned, len(p.Sessions))
		}
		if p.Sessions[0].TopicID != topic {
			t.Fatalf("filtered row is %q, want %q", p.Sessions[0].TopicID, topic)
		}
	}
	noLeaks := func(raw string) {
		t.Helper()
		if strings.Contains(raw, "ct_other") || strings.Contains(raw, "topic-other") {
			t.Fatalf("ungrouped session leaked into the group page: %s", raw)
		}
	}

	// 验收⑤ 回归：无 group 参数行为不变——两行都在，已知行带组 title，
	// 未分组行不带（验收③的字段形状在此一并可见）。
	full := parse(mustDirectoryPage(t, cfg, ""))
	if full.Total != 2 || full.Returned != 2 || len(full.Sessions) != 2 {
		t.Fatalf("unfiltered total/returned/rows = %d/%d/%d, want 2/2/2", full.Total, full.Returned, len(full.Sessions))
	}
	for _, row := range full.Sessions {
		if row.TopicID == refs.topic && row.Group != groupTitle {
			t.Fatalf("known row group = %q, want %q", row.Group, groupTitle)
		}
		if row.TopicID == "topic-other" && row.Group != "" {
			t.Fatalf("ungrouped row guessed group %q", row.Group)
		}
	}

	// 验收① group=title 命中；行内 group 字段 = 组 title（验收③）。
	byTitle := parse(mustDirectoryPage(t, cfg, groupTitle))
	oneRowAbout(byTitle, refs.topic)
	if byTitle.Sessions[0].Group != groupTitle {
		t.Fatalf("row group = %q, want %q", byTitle.Sessions[0].Group, groupTitle)
	}
	noLeaks(mustDirectoryPage(t, cfg, groupTitle))

	// 验收② group=扁平组 id 命中（目录行只带 title，必须走宿主探针）。
	before := probes
	byID := parse(mustDirectoryPage(t, cfg, groupID))
	oneRowAbout(byID, refs.topic)
	if probes == before {
		t.Fatal("id-typed group filter never consulted the membership probe")
	}
	noLeaks(mustDirectoryPage(t, cfg, groupID))

	// 大小写变体原样透传给探针（宿主负责 EqualFold，工具层不加工）。
	byIDUpper := parse(mustDirectoryPage(t, cfg, "Collab-Reasonix"))
	oneRowAbout(byIDUpper, refs.topic)

	// 验收④（负面面已由 noLeaks 覆盖，这里补探针拒绝路径）：
	// 探针对该组一律 false 时过滤页为空而不是泄漏。
	cfg.SessionGroupMatch = func(topicID, group string) bool { return false }
	denied := parse(mustDirectoryPage(t, cfg, groupID))
	if denied.Total != 0 || denied.Returned != 0 {
		t.Fatalf("denied id filter total/returned = %d/%d, want 0/0", denied.Total, denied.Returned)
	}

	// 探针缺席：退回 task 285 仅 title 匹配——title 仍命中，id 拼法不再假命中。
	titleOnly := cfg
	titleOnly.SessionGroupMatch = nil
	keepTitle := parse(mustDirectoryPage(t, titleOnly, groupTitle))
	oneRowAbout(keepTitle, refs.topic)
	idMiss := parse(mustDirectoryPage(t, titleOnly, groupID))
	if idMiss.Total != 0 || idMiss.Returned != 0 {
		t.Fatalf("nil-probe id filter total/returned = %d/%d, want 0/0 (task 285 fallback)", idMiss.Total, idMiss.Returned)
	}
}

func mustDirectoryPage(t *testing.T, cfg SessionCollabConfig, group string) string {
	t.Helper()
	raw, err := directoryPageFiltered(cfg, 10, nil, "", group)
	if err != nil {
		t.Fatalf("directoryPageFiltered(group=%q): %v", group, err)
	}
	return raw
}
