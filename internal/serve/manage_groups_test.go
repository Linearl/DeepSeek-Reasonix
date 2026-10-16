package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/organization"
	"reasonix/internal/provider"
)

// manageGroupsFixture builds a serve host the same way spawnChildFixture does,
// minus the session-lease plumbing: group management never touches the lease.
func manageGroupsFixture(t *testing.T, exp bool) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.jsonl")
	saveServeTestSession(t, source)

	exec := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard)
	exec.Session().Add(provider.Message{Role: provider.RoleUser, Content: "hi"})

	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc, SessionDir: dir, SessionPath: source, Executor: exec})
	t.Cleanup(ctrl.Close)
	server := New(ctrl, bc, config.ServeConfig{ExperimentalGCManageSessions: exp})
	srv := httptest.NewServer(server.Handler())
	t.Cleanup(srv.Close)
	// Point the organization store at the same temp home the serve host uses.
	organization.SetHomeDir(func() string { return dir })
	t.Cleanup(func() { organization.SetHomeDir(nil) })
	return srv
}

func postJSON(t *testing.T, srv *httptest.Server, path, body string) *http.Response {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(srv.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// 任务911 验收（fail-closed）：开关关闭时 /manage-groups 不挂载（GET 落进
// GET / catch-all 返回 404，POST 405——与 540 同判据）、"manage-groups" 不宣告。
func TestManageGroupsDisabledIsZeroBehavior(t *testing.T) {
	srv := manageGroupsFixture(t, false)
	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Get(srv.URL + "/manage-groups")
	if err != nil {
		t.Fatal(err)
	}
	body, err := readAll(resp)
	if err != nil {
		t.Fatal(err)
	}
	// A switch-off GET falls through to the GET / catch-all (the mux answer
	// every unknown GET path gets) — the assertion is that it is NOT the
	// manage-groups JSON list.
	if strings.Contains(body, `"projectGroups"`) {
		t.Fatalf("GET /manage-groups with switch off served the list payload: %.80s", body)
	}

	resp = postJSON(t, srv, "/manage-groups/create", `{"kind":"project","title":"x"}`)
	readAll(resp)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST /manage-groups/create with switch off = %d, want 405 (the pre-911 mux answer)", resp.StatusCode)
	}

	capResp, err := client.Get(srv.URL + "/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	var caps struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := json.NewDecoder(capResp.Body).Decode(&caps); err != nil {
		t.Fatal(err)
	}
	capResp.Body.Close()
	for _, c := range caps.Capabilities {
		if c == "manage-groups" {
			t.Fatal("manage-groups capability advertised while the switch is off")
		}
	}
}

// 任务911 验收（开关开启）：GET 列表、建/删除提案+确认全链路落到 organization
// store（与桌面同一 sidecar 文件），删除只清目标组不碰另一体系。
func TestManageGroupsEnabledEndToEnd(t *testing.T) {
	srv := manageGroupsFixture(t, true)

	// Create a project group and a session group through the gateway.
	if resp := postJSON(t, srv, "/manage-groups/create", `{"kind":"project","groupId":"pg-gc","title":"GC组"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("create project group = %d", resp.StatusCode)
	}
	if resp := postJSON(t, srv, "/manage-groups/create", `{"kind":"session","scope":"global","groupId":"g-gc","title":"手机组"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("create session group = %d", resp.StatusCode)
	}

	// List shows both.
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(srv.URL + "/manage-groups")
	if err != nil {
		t.Fatal(err)
	}
	var list organization.ListResult
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(list.ProjectGroups) != 1 || list.ProjectGroups[0].ID != "pg-gc" {
		t.Fatalf("list projectGroups = %+v", list.ProjectGroups)
	}
	if gs := list.Groups["global"]; len(gs) != 1 || gs[0].ID != "g-gc" {
		t.Fatalf("list global groups = %+v", gs)
	}

	// Delete proposes first: confirm=false writes nothing.
	resp = postJSON(t, srv, "/manage-groups/delete", `{"kind":"project","groupId":"pg-gc","confirm":false}`)
	var proposal organization.GroupResult
	if err := json.NewDecoder(resp.Body).Decode(&proposal); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if proposal.Applied || proposal.Plan == nil {
		t.Fatalf("proposal = applied=%v plan=%+v", proposal.Applied, proposal.Plan)
	}
	f := organization.LoadFile()
	if len(f.ProjectGroups) != 1 {
		t.Fatalf("proposal wrote anyway: catalog = %+v", f.ProjectGroups)
	}

	// Confirm executes; the session roster stays untouched.
	if resp := postJSON(t, srv, "/manage-groups/delete", `{"kind":"project","groupId":"pg-gc","confirm":true}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("confirmed delete = %d", resp.StatusCode)
	}
	f = organization.LoadFile()
	if len(f.ProjectGroups) != 0 {
		t.Fatalf("catalog after delete = %+v, want empty", f.ProjectGroups)
	}
	if gs := f.GlobalGroups; len(gs) != 1 || gs[0].ID != "g-gc" {
		t.Fatalf("session roster after project delete = %+v, want untouched", gs)
	}
}
