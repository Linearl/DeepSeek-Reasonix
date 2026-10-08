package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// spawnChildFixture builds a serve test host around a seeded source session.
// The controller carries a real executor with the same content as the saved
// file, so BranchToFile has something to snapshot (production always does).
func spawnChildFixture(t *testing.T, exp bool) (*Server, *control.SessionLeaseKeeper, *httptest.Server, string, string) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.jsonl")
	saveServeTestSession(t, source)

	exec := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard)
	exec.Session().Add(provider.Message{Role: provider.RoleUser, Content: "hi"})

	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc, SessionDir: dir, SessionPath: source, Executor: exec})
	t.Cleanup(ctrl.Close)
	server := New(ctrl, bc, config.ServeConfig{ExperimentalGCChildSession: exp})
	leases := control.NewSessionLeaseKeeper()
	t.Cleanup(leases.Release)
	if err := leases.Rebind(source); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	server.SetSessionLeases(leases)
	srv := httptest.NewServer(server.Handler())
	t.Cleanup(srv.Close)
	return server, leases, srv, dir, source
}

// 任务540 验收②：开关关闭时端点不存在（404）、能力不宣告——关闭态零行为。
func TestSpawnChildSessionDisabledIsZeroBehavior(t *testing.T) {
	_, leases, srv, _, source := spawnChildFixture(t, false)
	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Post(srv.URL+"/spawn-child-session", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	readAll(resp)
	// 405 is what the mux answered before task 540 as well: the path falls
	// through to the GET / catch-all with no POST route, so the off state is
	// byte-for-byte the pre-540 behavior (route must not exist).
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("spawn with switch off = %d, want 405 (the pre-540 mux answer)", resp.StatusCode)
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
		if c == "child-session" {
			t.Fatal("child-session capability advertised while the switch is off")
		}
	}

	if agent.CanonicalSessionPath(leases.HeldPath()) != agent.CanonicalSessionPath(source) {
		t.Fatalf("switch-off request moved the lease: %q, want %q", leases.HeldPath(), source)
	}
}

// 任务540 验收①③：开关开启后派生成功——新文件、前台与租约一起走到子会话、
// 源会话保留且 /resume 可回切（单一租约机制，无第二套归属）。
func TestSpawnChildSessionDerivesResumableChild(t *testing.T) {
	server, leases, srv, dir, source := spawnChildFixture(t, true)
	client := &http.Client{Timeout: 10 * time.Second}

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
	found := false
	for _, c := range caps.Capabilities {
		if c == "child-session" {
			found = true
		}
	}
	if !found {
		t.Fatalf("child-session capability missing while the switch is on: %v", caps.Capabilities)
	}

	resp, err := client.Post(srv.URL+"/spawn-child-session", "application/json", strings.NewReader(`{"name":"gc-child"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Path   string `json:"path"`
		Source string `json:"source"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("spawn = %d body=%+v", resp.StatusCode, got)
	}
	if got.Path == "" || got.Path == source || !strings.HasSuffix(got.Path, ".jsonl") {
		t.Fatalf("child path = %q, want a new transcript path", got.Path)
	}
	if agent.CanonicalSessionPath(got.Source) != agent.CanonicalSessionPath(source) {
		t.Fatalf("source = %q, want %q", got.Source, source)
	}

	// 前台与租约必须一起落在子会话上（单一归属机制）。
	if agent.CanonicalSessionPath(server.ctl().SessionPath()) != agent.CanonicalSessionPath(got.Path) {
		t.Fatalf("controller on %q, want child %q", server.ctl().SessionPath(), got.Path)
	}
	if agent.CanonicalSessionPath(leases.HeldPath()) != agent.CanonicalSessionPath(got.Path) {
		t.Fatalf("lease guards %q, want child %q", leases.HeldPath(), got.Path)
	}
	// 派生要带着父会话历史。
	loaded, err := agent.LoadSession(got.Path)
	if err != nil {
		t.Fatalf("load child: %v", err)
	}
	if len(loaded.Messages) == 0 {
		t.Fatal("child session lost the parent history")
	}
	// 源会话文件必须原样保留。
	if entries, _ := filepath.Glob(filepath.Join(dir, "source.jsonl")); len(entries) != 1 {
		t.Fatal("source transcript vanished after spawn")
	}

	// 会话列表要能看到两个会话（GC 按 path 列表）。
	listResp, err := client.Get(srv.URL + "/sessions")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&rows); err != nil {
		t.Fatal(err)
	}
	listResp.Body.Close()
	paths := map[string]bool{}
	for _, row := range rows {
		paths[agent.CanonicalSessionPath(row.Path)] = true
	}
	if !paths[agent.CanonicalSessionPath(source)] || !paths[agent.CanonicalSessionPath(got.Path)] {
		t.Fatalf("session list = %v, want both source and child", paths)
	}

	// /resume 回切源会话，租约跟回（GC 的切回通道）。
	form := url.Values{}
	_ = form
	resumeResp, err := client.Post(srv.URL+"/resume", "application/json", strings.NewReader(`{"path":`+quoteJSON(source)+`}`))
	if err != nil {
		t.Fatal(err)
	}
	readAll(resumeResp)
	if resumeResp.StatusCode != http.StatusNoContent {
		t.Fatalf("resume back to source = %d", resumeResp.StatusCode)
	}
	if agent.CanonicalSessionPath(server.ctl().SessionPath()) != agent.CanonicalSessionPath(source) {
		t.Fatalf("controller after resume = %q, want source", server.ctl().SessionPath())
	}
	if agent.CanonicalSessionPath(leases.HeldPath()) != agent.CanonicalSessionPath(source) {
		t.Fatalf("lease after resume = %q, want source", leases.HeldPath())
	}
}

func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
