package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/tool"
)

// infoFixture builds one addressable session with a topic id + titles, plus a
// SessionCollabConfig pointing at it (controlFixture's shape, extended for the
// task-285 fields).
func infoFixture(t *testing.T) (SessionCollabConfig, sessionInfoRefs) {
	t.Helper()
	dir := t.TempDir()
	refs := sessionInfoRefs{
		contact:   "ct_info",
		topic:     "topic-audit-3",
		sessionID: "sess-info",
	}
	refs.sessionPath = filepath.Join(dir, refs.sessionID+".jsonl")
	if err := os.WriteFile(refs.sessionPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := `{"id":"b1","contact_id":"` + refs.contact + `","custom_title":"审计-3-桌面渲染","topic_id":"` + refs.topic + `","scope":"global","workspace_root":"","purpose":"桌面渲染审计"}`
	if err := os.WriteFile(refs.sessionPath+".meta", []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := SessionCollabConfig{SessionDir: dir, WorkspaceRoot: t.TempDir()}
	return cfg, refs
}

type sessionInfoRefs struct {
	contact     string
	topic       string
	sessionID   string
	sessionPath string
}

// 验收①（分组可见性）：目录行带 group 字段，group= 过滤按行生效且 total
// 尊重过滤（与 query 过滤同语义）。
func TestDirectoryRowsCarryGroupAndFilterByGroup(t *testing.T) {
	cfg, refs := infoFixture(t)
	other := filepath.Join(cfg.SessionDir, "other.jsonl")
	if err := os.WriteFile(other, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	otherMeta := `{"id":"b2","contact_id":"ct_other","custom_title":"Other","topic_id":"topic-other"}`
	if err := os.WriteFile(other+".meta", []byte(otherMeta), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.SessionGroup = func(topicID string) (string, bool) {
		if topicID == refs.topic {
			return "审计", true
		}
		return "", false
	}

	page, err := directoryPageFiltered(cfg, 10, nil, "", "", false)
	if err != nil {
		t.Fatalf("directoryPageFiltered: %v", err)
	}
	if !strings.Contains(page, `"group":"审计"`) {
		t.Fatalf("known-topic row missing its group: %s", page)
	}
	if strings.Contains(page, `"contactId":"ct_other","topicId":"topic-other","archived":false`) &&
		strings.Contains(page, `"group":"审计","topicId":"topic-other"`) {
		t.Fatalf("unknown-topic row guessed a group: %s", page)
	}

	// 大小写不敏感的组名过滤：只留命中组，total 只数过滤后的行。
	page, err = directoryPageFiltered(cfg, 10, nil, "", "审计", false)
	if err != nil {
		t.Fatalf("directoryPageFiltered(group): %v", err)
	}
	var out struct {
		Total    int `json:"total"`
		Returned int `json:"returned"`
	}
	if err := json.Unmarshal([]byte(page), &out); err != nil {
		t.Fatalf("page is not JSON: %v\n%s", err, page)
	}
	if out.Total != 1 || out.Returned != 1 {
		t.Fatalf("group filter total/returned = %d/%d, want 1/1: %s", out.Total, out.Returned, page)
	}
	if strings.Contains(page, "ct_other") {
		t.Fatalf("group filter leaked the other session: %s", page)
	}

	// 未命中组：零行但请求成功（空页，不是错误）。
	page, err = directoryPageFiltered(cfg, 10, nil, "", "不存在组", false)
	if err != nil {
		t.Fatalf("group filter miss: %v", err)
	}
	if err := json.Unmarshal([]byte(page), &out); err != nil {
		t.Fatalf("miss page is not JSON: %v", err)
	}
	if out.Total != 0 || out.Returned != 0 {
		t.Fatalf("group miss total/returned = %d/%d, want 0/0: %s", out.Total, out.Returned, page)
	}

	// 探针为 nil：行的 group 字段整体缺席（payload 的 group 回显不算）。
	page, err = directoryPageFiltered(SessionCollabConfig{SessionDir: cfg.SessionDir, WorkspaceRoot: cfg.WorkspaceRoot}, 10, nil, "", "", false)
	if err != nil {
		t.Fatalf("nil-probe page: %v", err)
	}
	var nilPage struct {
		Sessions []struct {
			Group string `json:"group"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(page), &nilPage); err != nil {
		t.Fatalf("nil-probe page is not JSON: %v", err)
	}
	for i, row := range nilPage.Sessions {
		if row.Group != "" {
			t.Fatalf("nil probe leaked row %d group %q: %s", i, row.Group, page)
		}
	}
}

// 验收④（get_session_info 结构化 meta）：字段与 .meta 文件一致；宿主探针
// 注入 group/model/lineage；fields 子集生效且未知字段被报告而非静默丢弃。
func TestGetSessionInfoReturnsStructuredMeta(t *testing.T) {
	cfg, refs := infoFixture(t)
	cfg.SessionInfo = func(contact string) (string, string, bool) {
		if contact == refs.contact {
			return "mimo-api/mimo-v2.6-flash", "mimo-api", true
		}
		return "", "", false
	}
	cfg.SessionGroup = func(topicID string) (string, bool) {
		if topicID == refs.topic {
			return "审计", true
		}
		return "", false
	}
	cfg.SessionVersions = func(scope, root, topic, sessionPath string) ([]SessionVersionInfo, bool) {
		if topic != refs.topic {
			return nil, false
		}
		return []SessionVersionInfo{
			{VersionID: "head-1", Path: refs.sessionPath, Role: "preferred", VersionKind: "recovery", Selected: true, SizeBytes: 123},
			{VersionID: "abcd1234", Path: refs.sessionPath + "-recovery-1", Role: "covered_copy", SizeBytes: 456},
		}, true
	}

	exec := NewGetSessionInfoTool(cfg)
	raw, err := exec.Execute(t.Context(), []byte(`{"target":"`+refs.contact+`","fields":["group","workspaceRoot","recoveryRole","noSuchField"]}`))
	if err != nil {
		t.Fatalf("get_session_info: %v", err)
	}
	var payload struct {
		Session       map[string]any `json:"session"`
		UnknownFields []string       `json:"unknownFields"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("payload is not JSON: %v\n%s", err, raw)
	}
	if got := payload.Session["group"]; got != "审计" {
		t.Fatalf("group = %v, want 审计", got)
	}
	if got := payload.Session["recoveryRole"]; got != "preferred" {
		t.Fatalf("recoveryRole = %v, want preferred (this session's lineage role)", got)
	}
	if _, ok := payload.Session["title"]; ok {
		t.Fatalf("fields subset leaked unrequested title: %s", raw)
	}
	if len(payload.UnknownFields) != 1 || payload.UnknownFields[0] != "noSuchField" {
		t.Fatalf("unknownFields = %v, want [noSuchField]", payload.UnknownFields)
	}

	// 全字段查询：结构化 meta 与文件一致。
	raw, err = exec.Execute(t.Context(), []byte(`{"target":"`+refs.contact+`"}`))
	if err != nil {
		t.Fatalf("full get_session_info: %v", err)
	}
	var full struct {
		Session map[string]any `json:"session"`
	}
	if err := json.Unmarshal([]byte(raw), &full); err != nil {
		t.Fatalf("full payload is not JSON: %v", err)
	}
	want := map[string]any{
		"contactId": refs.contact, "topicId": refs.topic, "scope": "global",
		"purpose": "桌面渲染审计", "title": "审计-3-桌面渲染", "modelRef": "mimo-api/mimo-v2.6-flash",
		"provider": "mimo-api", "versionCount": float64(2), "recoverySelected": true,
	}
	for k, v := range want {
		if full.Session[k] != v {
			t.Fatalf("session.%s = %v (%T), want %v", k, full.Session[k], full.Session[k], v)
		}
	}
}

// 自指目标（省略 target）：调用会话不在通讯录也能从自身 meta 构造身份。
func TestGetSessionInfoSelfTargetFromMeta(t *testing.T) {
	cfg, refs := infoFixture(t)
	live := refs.sessionPath
	cfg.ResolveSessionPath = func() string { return live }
	exec := NewGetSessionInfoTool(cfg)
	raw, err := exec.Execute(t.Context(), []byte(`{}`))
	if err != nil {
		t.Fatalf("self get_session_info: %v", err)
	}
	if !strings.Contains(raw, `"contactId":"`+refs.contact+`"`) || !strings.Contains(raw, refs.sessionID) {
		t.Fatalf("self info missing meta identity: %s", raw)
	}
}

// 验收③（版本谱系）：探针 nil 给可操作拒绝；谱系缺失明说；命中返回与 UI
// 同源的数据。工具按值捕获 config——每段先设探针再构造工具。
func TestListSessionVersionsProbeLifecycle(t *testing.T) {
	cfg, refs := infoFixture(t)

	if _, err := NewListSessionVersionsTool(cfg).Execute(t.Context(), []byte(`{"target":"`+refs.contact+`"}`)); err == nil ||
		!strings.Contains(err.Error(), "does not expose the recovery lineage") {
		t.Fatalf("nil probe err = %v, want the actionable refusal", err)
	}

	cfg.SessionVersions = func(scope, root, topic, sessionPath string) ([]SessionVersionInfo, bool) {
		return nil, false
	}
	if _, err := NewListSessionVersionsTool(cfg).Execute(t.Context(), []byte(`{"target":"`+refs.contact+`"}`)); err == nil ||
		!strings.Contains(err.Error(), refs.topic) {
		t.Fatalf("no-lineage err = %v, want it to name the topic", err)
	}

	member := SessionVersionInfo{VersionID: "head-9", Path: refs.sessionPath, Role: "preferred", Selected: true, SizeBytes: 42}
	cfg.SessionVersions = func(scope, root, topic, sessionPath string) ([]SessionVersionInfo, bool) {
		if topic != refs.topic || sessionPath != refs.sessionPath {
			t.Fatalf("probe got (%q,%q,%q,%q), want the resolved identity", scope, root, topic, sessionPath)
		}
		return []SessionVersionInfo{member}, true
	}
	raw, err := NewListSessionVersionsTool(cfg).Execute(t.Context(), []byte(`{"target":"`+refs.contact+`"}`))
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	for _, want := range []string{`"versionId":"head-9"`, `"role":"preferred"`, `"selected":true`, `"count":1`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("versions payload missing %q: %s", want, raw)
		}
	}
}

// 验收②（adopt 防误切）：confirm 缺省必拒且探针不被触达；确认后参数齐全地
// 到达宿主既有路径；宿主错误原样上抛。
func TestAdoptSessionVersionGateAndDispatch(t *testing.T) {
	cfg, refs := infoFixture(t)
	called := false
	cfg.AdoptSessionVersion = func(scope, root, topic, sessionPath, versionID string) error {
		called = true
		if versionID != "head-9" || topic != refs.topic || sessionPath != refs.sessionPath {
			t.Fatalf("adopt probe got (%q,%q,%q,%q,%q)", scope, root, topic, sessionPath, versionID)
		}
		return nil
	}
	exec := NewAdoptSessionVersionTool(cfg)
	if exec.ReadOnly() {
		t.Fatal("adopt_session_version must not be read-only")
	}

	if _, err := exec.Execute(t.Context(), []byte(`{"target":"`+refs.contact+`","version_id":"head-9"}`)); err == nil ||
		!strings.Contains(err.Error(), "confirm=true") {
		t.Fatalf("missing confirm err = %v, want the confirm refusal", err)
	}
	if called {
		t.Fatal("adopt probe ran without confirm")
	}

	raw, err := exec.Execute(t.Context(), []byte(`{"target":"`+refs.contact+`","version_id":"head-9","confirm":true}`))
	if err != nil {
		t.Fatalf("adopt with confirm: %v", err)
	}
	if !called || !strings.Contains(raw, `"adopted":true`) {
		t.Fatalf("adopt result = %s (called=%v)", raw, called)
	}

	cfg.AdoptSessionVersion = func(scope, root, topic, sessionPath, versionID string) error {
		return os.ErrPermission
	}
	// 工具按值捕获 config：换探针必须换工具。
	if _, err := NewAdoptSessionVersionTool(cfg).Execute(t.Context(), []byte(`{"target":"`+refs.contact+`","version_id":"head-9","confirm":true}`)); err == nil {
		t.Fatal("host error swallowed")
	}
}

// 只读契约：两个读工具必须 ReadOnly。
func TestSessionInfoToolsReadOnlyContract(t *testing.T) {
	cfg, _ := infoFixture(t)
	if !NewGetSessionInfoTool(cfg).(tool.Tool).ReadOnly() {
		t.Fatal("get_session_info must be read-only")
	}
	if !NewListSessionVersionsTool(cfg).(tool.Tool).ReadOnly() {
		t.Fatal("list_session_versions must be read-only")
	}
	if NewGetSessionInfoTool(cfg).(tool.Tool).Name() != "get_session_info" ||
		NewListSessionVersionsTool(cfg).(tool.Tool).Name() != "list_session_versions" ||
		NewAdoptSessionVersionTool(cfg).(tool.Tool).Name() != "adopt_session_version" {
		t.Fatal("tool names drifted from the task-285 contract")
	}
}
