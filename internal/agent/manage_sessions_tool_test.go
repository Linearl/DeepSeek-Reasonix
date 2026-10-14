package agent

// 任务 892A：manage_sessions 工具（agent 侧）验收——参数校验、目标解析
// 转发、宿主 nil 拒绝、schema 形状。宿主行为（7 action / 提案制 / 级联
// 语义 / metadata v2 事件）由 desktop 侧 manage_sessions_test.go 覆盖。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func manageSessionsFixture(t *testing.T) (SessionCollabConfig, map[string]json.RawMessage) {
	t.Helper()
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "sess-manage.jsonl")
	if err := os.WriteFile(sessionPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := `{"id":"b1","contact_id":"ct_manage","custom_title":"整理目标","topic_id":"topic-manage","scope":"global","workspace_root":""}`
	if err := os.WriteFile(sessionPath+".meta", []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	return SessionCollabConfig{SessionDir: dir, WorkspaceRoot: t.TempDir()}, map[string]json.RawMessage{}
}

func manageSessionsCall(t *testing.T, cfg SessionCollabConfig, captured map[string]json.RawMessage, args string) (string, error) {
	t.Helper()
	tool := NewManageSessionsTool(cfg, func(action string, payload json.RawMessage) (json.RawMessage, error) {
		captured["action"] = json.RawMessage(`"` + action + `"`)
		captured["args"] = payload
		return json.RawMessage(`{"status":"ok"}`), nil
	})
	return tool.Execute(context.Background(), json.RawMessage(args))
}

func TestManageSessionsToolNilHostRefuses(t *testing.T) {
	cfg, _ := manageSessionsFixture(t)
	tool := NewManageSessionsTool(cfg, nil)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err == nil || !strings.Contains(err.Error(), "cannot manage sessions") {
		t.Fatalf("nil host error = %v", err)
	}
}

func TestManageSessionsToolValidatesActions(t *testing.T) {
	cfg, captured := manageSessionsFixture(t)
	cases := []struct {
		name string
		args string
		want string
	}{
		{"missing action", `{}`, "action is required"},
		{"unknown action", `{"action":"drop_everything"}`, "unknown action"},
		{"rename without title", `{"action":"rename_session","target":"ct_manage"}`, "title is required"},
		{"rename without target", `{"action":"rename_session","title":"X"}`, "target is required"},
		{"move without group", `{"action":"move_session","target":"ct_manage"}`, "group or group_id is required"},
		{"reorder without group_id", `{"action":"reorder_session","target":"ct_manage","index":1}`, "group_id is required"},
		{"create without title", `{"action":"group_create"}`, "title is required"},
		{"rename group without id", `{"action":"group_rename","title":"X"}`, "group_id and title are required"},
		{"delete without id", `{"action":"group_delete"}`, "group_id is required"},
		{"rollback without op", `{"action":"rollback"}`, "op_id is required"},
	}
	for _, tc := range cases {
		if _, err := manageSessionsCall(t, cfg, captured, tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: error = %v, want contains %q", tc.name, err, tc.want)
		}
	}
}

// rename_session/move_session 把 target 解析成宿主可直接使用的
// topic_id/session_path/scope/workspace_root，其余 action 原样转发。
func TestManageSessionsToolResolvesTargetAndForwards(t *testing.T) {
	cfg, captured := manageSessionsFixture(t)
	if _, err := manageSessionsCall(t, cfg, captured, `{"action":"rename_session","target":"ct_manage","title":"新名"}`); err != nil {
		t.Fatalf("rename_session: %v", err)
	}
	if string(captured["action"]) != `"rename_session"` {
		t.Fatalf("action = %s", captured["action"])
	}
	var rename struct {
		TopicID     string `json:"topic_id"`
		SessionPath string `json:"session_path"`
		Title       string `json:"title"`
	}
	if err := json.Unmarshal(captured["args"], &rename); err != nil {
		t.Fatalf("decode forwarded args: %v", err)
	}
	if rename.TopicID != "topic-manage" || rename.Title != "新名" || !strings.HasSuffix(rename.SessionPath, "sess-manage.jsonl") {
		t.Fatalf("forwarded = %#v", rename)
	}

	if _, err := manageSessionsCall(t, cfg, captured, `{"action":"move_session","target":"topic-manage","group":"Team"}`); err != nil {
		t.Fatalf("move_session: %v", err)
	}
	var move struct {
		TopicID string `json:"topic_id"`
		Scope   string `json:"scope"`
		Group   string `json:"group"`
	}
	if err := json.Unmarshal(captured["args"], &move); err != nil {
		t.Fatalf("decode move args: %v", err)
	}
	if move.TopicID != "topic-manage" || move.Group != "Team" {
		t.Fatalf("move forwarded = %#v", move)
	}

	// group_delete 原样转发（含提案开关），宿主侧负责两段式。
	if _, err := manageSessionsCall(t, cfg, captured, `{"action":"group_delete","kind":"project","group_id":"pg","cascade":true}`); err != nil {
		t.Fatalf("group_delete: %v", err)
	}
	var del struct {
		Kind    string `json:"kind"`
		GroupID string `json:"group_id"`
		Cascade bool   `json:"cascade"`
		Confirm bool   `json:"confirm"`
	}
	if err := json.Unmarshal(captured["args"], &del); err != nil {
		t.Fatalf("decode delete args: %v", err)
	}
	if del.Kind != "project" || del.GroupID != "pg" || !del.Cascade || del.Confirm {
		t.Fatalf("delete forwarded = %#v", del)
	}
}

func TestManageSessionsToolSchemaShape(t *testing.T) {
	cfg, _ := manageSessionsFixture(t)
	tool := NewManageSessionsTool(cfg, func(string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	})
	var schema map[string]any
	if err := json.Unmarshal(tool.Schema(), &schema); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties missing: %s", tool.Schema())
	}
	action, ok := properties["action"].(map[string]any)
	if !ok {
		t.Fatalf("action property missing: %s", tool.Schema())
	}
	enum, ok := action["enum"].([]any)
	if !ok || len(enum) != len(manageSessionsActions) {
		t.Fatalf("action enum = %#v, want %d entries", action["enum"], len(manageSessionsActions))
	}
	if tool.ReadOnly() {
		t.Fatal("manage_sessions must not be read-only")
	}
}
