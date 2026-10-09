package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// workDetailFixture 复刻变更前黄金捕获（20261009，基线 baae63433）的固定现场：
// 单会话 sc_a、探针报 running、信箱为空。零回归断言的黄金字节来自该现场在
// 变更前代码上的真实输出（record 字节 + 顶层键集），不是手推。
func workDetailFixture(t *testing.T) SessionCollabConfig {
	t.Helper()
	dir := t.TempDir()
	statusFixture(t, dir, "a", "Alpha", "sc_a", "top_a")
	return SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
		MailDir:       filepath.Join(t.TempDir(), "mail"),
		SessionStatus: func(contactID string) (bool, int64, int, bool) {
			if contactID == "sc_a" {
				return true, 1700000000000, 0, true
			}
			return false, 0, 0, true
		},
	}
}

// TestGetSessionStatus667NoFlagByteIdentical pins the zero-regression rule:
// 不带 include_tasks_and_subagents（或显式 false）时，输出与变更前逐字节一致，
// 且宿主是否接线 work-detail 探针完全不影响无参路径（探针绝不被调用）。
func TestGetSessionStatus667NoFlagByteIdentical(t *testing.T) {
	probeCalled := false
	spy := func(string) (SessionWorkDetail, bool) {
		probeCalled = true
		return SessionWorkDetail{}, true
	}

	withProbe := workDetailFixture(t)
	withProbe.SessionWorkDetail = spy
	withoutProbe := workDetailFixture(t)

	args := []string{
		`{}`,
		`{"targets":["sc_a"]}`,
		`{"targets":["sc_a"],"include_tasks_and_subagents":false}`,
	}
	for _, raw := range args {
		outWith, err := NewGetSessionStatusTool(withProbe).Execute(context.Background(), []byte(raw))
		if err != nil {
			t.Fatalf("args %s: %v", raw, err)
		}
		outWithout, err := NewGetSessionStatusTool(withoutProbe).Execute(context.Background(), []byte(raw))
		if err != nil {
			t.Fatalf("args %s: %v", raw, err)
		}
		if outWith != outWithout {
			t.Fatalf("args %s: wiring the probe must not change the no-flag answer:\nwith=%s\nwithout=%s", raw, outWith, outWithout)
		}

		var payload map[string]any
		if err := json.Unmarshal([]byte(outWith), &payload); err != nil {
			t.Fatalf("args %s: %v", raw, err)
		}
		// 变更前顶层键集（黄金捕获）：7 个键，一个不多一个不少。
		gotTop := mapKeys(payload)
		wantTop := []string{"query", "returned", "sessions", "states", "total", "totalNote", "unmatched"}
		sort.Strings(gotTop)
		if !reflect.DeepEqual(gotTop, wantTop) {
			t.Fatalf("args %s: top-level keys = %v, want the pre-667 set %v", raw, gotTop, wantTop)
		}
		// record 黄金字节（变更前捕获）：targets 命中时唯一记录必须逐字节一致。
		sessions, _ := payload["sessions"].([]any)
		for _, s := range sessions {
			rec, _ := s.(map[string]any)
			if rec["contactId"] != "sc_a" {
				continue
			}
			b, _ := json.Marshal(rec)
			want := `{"contactId":"sc_a","lastActivity":1700000000000,"scope":"global","state":"running","title":"Alpha","topicId":"top_a","unreadInbox":0}`
			if string(b) != want {
				t.Fatalf("args %s: record bytes drifted from the pre-667 golden:\ngot  %s\nwant %s", raw, b, want)
			}
		}
	}
	if probeCalled {
		t.Fatal("the work-detail probe must never run on a no-flag call")
	}
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// TestGetSessionStatus667WorkDetailEnrichment pins the opt-in contract:
// include_tasks_and_subagents=true 时每条命中记录携带 workDetail（任务行 +
// 运行中子代理行，含时长与批次），且跨会话 targets 走同一接口拿到各自的明细。
func TestGetSessionStatus667WorkDetailEnrichment(t *testing.T) {
	dir := t.TempDir()
	statusFixture(t, dir, "a", "Alpha", "sc_a", "top_a")
	statusFixture(t, dir, "b", "Beta", "sc_b", "top_b")

	queried := map[string]bool{}
	cfg := SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
		MailDir:       filepath.Join(t.TempDir(), "mail"),
		SessionStatus: func(contactID string) (bool, int64, int, bool) {
			return contactID == "sc_a", 1700000000000, 0, true
		},
		SessionWorkDetail: func(contactID string) (SessionWorkDetail, bool) {
			queried[contactID] = true
			switch contactID {
			case "sc_a":
				return SessionWorkDetail{
					Tasks: []TaskRuntimeRow{
						{ID: "job1", Kind: "bash", Label: "长测试", Status: "running", StartedAt: 1700000000000, Tps: 42},
					},
					Subagents: []SubagentRuntimeRow{
						{Ref: "sa_1", Name: "调研", Status: "running", StartedAt: 1700000000000, ParentToolCallID: "call_9"},
						{Ref: "sa_2", Name: "审计", Status: "running", StartedAt: 1700000000000},
					},
				}, true
			case "sc_b":
				return SessionWorkDetail{}, true
			}
			return SessionWorkDetail{}, false
		},
	}

	out, err := NewGetSessionStatusTool(cfg).Execute(context.Background(), []byte(`{"targets":["sc_a","sc_b","nope"],"include_tasks_and_subagents":true}`))
	if err != nil {
		t.Fatal(err)
	}
	// 未命中目标照旧显式上报，不受明细开关影响。
	if !strings.Contains(out, `"unmatched":["nope"]`) {
		t.Fatalf("unmatched target must stay reported: %s", out)
	}

	var payload struct {
		Sessions []struct {
			ContactID  string           `json:"contactId"`
			WorkDetail map[string]any   `json:"workDetail"`
			Tasks      []map[string]any `json:"-"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	byContact := map[string]map[string]any{}
	for _, s := range payload.Sessions {
		byContact[s.ContactID] = s.WorkDetail
	}

	da := byContact["sc_a"]
	if da == nil || da["known"] != true {
		t.Fatalf("sc_a workDetail must be known: %v", da)
	}
	tasks, _ := da["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("sc_a tasks = %v, want 1 row", da["tasks"])
	}
	task, _ := tasks[0].(map[string]any)
	if task["label"] != "长测试" || task["status"] != "running" || task["tps"] != float64(42) {
		t.Fatalf("sc_a task row = %v, want the job row fields", task)
	}
	if d, _ := task["durationMs"].(float64); d < 0 {
		t.Fatalf("durationMs must never be negative: %v", task)
	}
	subs, _ := da["subagents"].([]any)
	if len(subs) != 2 {
		t.Fatalf("sc_a subagents = %v, want 2 running rows", da["subagents"])
	}
	first, _ := subs[0].(map[string]any)
	if first["batch"] != "call_9" {
		t.Fatalf("batch key must pass through the parent tool-call id: %v", first)
	}
	if _, has := subs[1].(map[string]any)["batch"]; has {
		t.Fatalf("a row without a parent tool-call id must omit batch: %v", subs[1])
	}

	db := byContact["sc_b"]
	if db == nil || db["known"] != true {
		t.Fatalf("sc_b workDetail must be known with an empty set: %v", db)
	}
	if rows, _ := db["tasks"].([]any); len(rows) != 0 {
		t.Fatalf("sc_b tasks must be empty, got %v", db["tasks"])
	}

	// 跨会话：探针必须恰好被 sc_a / sc_b 各查询一次（同接口同探针）。
	if !queried["sc_a"] || !queried["sc_b"] || len(queried) != 2 {
		t.Fatalf("probe queries = %v, want exactly sc_a and sc_b", queried)
	}
}

// TestGetSessionStatus667WorkDetailWithoutProbe pins the honest-no-host rule:
// 宿主没接探针时开关打开得到 known=false + 提示，而不是伪装的空集。
func TestGetSessionStatus667WorkDetailWithoutProbe(t *testing.T) {
	cfg := workDetailFixture(t)
	out, err := NewGetSessionStatusTool(cfg).Execute(context.Background(), []byte(`{"targets":["sc_a"],"include_tasks_and_subagents":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"known":false`) || !strings.Contains(out, "work-detail probe") {
		t.Fatalf("a host without the probe must answer known=false with a hint: %s", out)
	}
	if strings.Contains(out, `"tasks":[`) || strings.Contains(out, `"subagents":[`) {
		t.Fatalf("no rows may be fabricated without a probe: %s", out)
	}
}

// TestGetSessionStatus667WorkDetailRuntimeInvisible pins the unknown rule:
// 探针在但看不到该运行时（跨进程）时同样 known=false —— 与 state=unknown 同一规则。
func TestGetSessionStatus667WorkDetailRuntimeInvisible(t *testing.T) {
	cfg := workDetailFixture(t)
	cfg.SessionWorkDetail = func(string) (SessionWorkDetail, bool) {
		return SessionWorkDetail{}, false
	}
	out, err := NewGetSessionStatusTool(cfg).Execute(context.Background(), []byte(`{"targets":["sc_a"],"include_tasks_and_subagents":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"known":false`) || !strings.Contains(out, "cannot see the session's runtime") {
		t.Fatalf("an invisible runtime must answer known=false with the unknown-rule hint: %s", out)
	}
}
