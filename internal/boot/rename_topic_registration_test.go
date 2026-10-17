package boot

// 任务 949：rename_topic 接线验收（boot 侧）——
// ①宿主给了 rename 能力（desktop 同款 OnRenameSession）时，工具进注册表，
//   且通讯录索引段（sessionCollabIndexBlock）把 rename_topic 教给模型
//   （949 的根因就是「能力存在但模型看不见」：collab 工具一律走
//   use_capability 隐藏面，索引段不教 = 协调线找不到改名工具）；
// ②宿主没给能力时不注册（与 rename_session 同一道 host-capability 门）。

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/agent/testutil"
	"reasonix/internal/control"
	"reasonix/internal/event"
)

func buildRenameTopicBoot(t *testing.T, hook agent.RenameSessionFunc) (*control.Controller, *testutil.MockProvider) {
	t.Helper()
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	writeFile(t, dir, "reasonix.toml", `
default_model = "test-model"

[agent]
system_prompt = "BASE"
experimental_session_collab = true

[[providers]]
name = "test-model"
kind = "boot-token-profile-test"
model = "x"
`)
	registerBootTokenProfileTestProvider()
	prov := testutil.NewMock("rename-topic-boot", testutil.Turn{Text: "done"})
	setBootTokenProfileTestProvider(t, prov)
	ctrl, err := Build(context.Background(), Options{Sink: event.Discard, OnRenameSession: hook})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return ctrl, prov
}

func TestRenameTopicRegistersWithHostRenameCapability(t *testing.T) {
	stub := func(topicID, sessionPath, title string) (agent.RenameSessionResult, error) {
		return agent.RenameSessionResult{TopicID: topicID, SessionPath: sessionPath, Title: title}, nil
	}
	ctrl, prov := buildRenameTopicBoot(t, stub)
	defer ctrl.Close()

	// ① 注册表接线：rename_topic 与 rename_session 同门、同注册。
	found := map[string]bool{}
	for _, e := range ctrl.AllToolContractEntries() {
		found[e.Name] = true
	}
	for _, want := range []string{"rename_topic", "rename_session"} {
		if !found[want] {
			t.Fatalf("registry missing %q (collab rename tools); have %d entries", want, len(found))
		}
	}
	// 其余 tool 面不受影响：rename_topic 走 use_capability 隐藏面，不进 schema。
	if err := ctrl.Run(context.Background(), "hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	reqs := mainConversationRequests(prov.Requests())
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	if requestHasTool(reqs[0], "rename_topic") {
		t.Fatalf("rename_topic must stay on the use_capability surface, got %v", toolSchemaNames(reqs[0].Tools))
	}
	// ② 通讯录索引段教了它——没有这行，协调线依旧「找不到改名工具」。
	if sys := systemMessage(reqs[0].Messages); !strings.Contains(sys, "rename_topic(") {
		t.Fatalf("system prompt must teach rename_topic, got:\n%s", sys)
	}
}

func TestRenameTopicUnregisteredWithoutHostCapability(t *testing.T) {
	ctrl, _ := buildRenameTopicBoot(t, nil)
	defer ctrl.Close()
	for _, e := range ctrl.AllToolContractEntries() {
		if e.Name == "rename_topic" {
			t.Fatal("rename_topic must not register when the host has no rename capability")
		}
	}
}
