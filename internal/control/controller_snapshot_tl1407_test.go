package control

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// tl1407 面二/面三:快照守卫终局与落盘止损。
//
// 现场(20261105 desktop.log):无头会话每个保存周期(30s autosave)全量
// Snapshot 后被拒绝,WARN 风暴 15:10~15:25 跨多会话;消息只在内存堆积
// (201→248 从未落盘),app 崩溃即全丢;turn 继续跑但用户只看到卡死。
//
// 终局选型=补头续跑(而非终止 turn):防护的目的是「reload 后有身份契约」,
// 把本 controller 的 boot base 前插进活会话即恢复契约且当周期落盘——可用性、
// 数据安全、身份契约三者兼得;终止 turn 三者全失(turn 丢、盘上文件仍无头、
// 下轮开轮再中招)。

func tl1407CaptureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func tl1407HeadlessSession() *agent.Session {
	sess := agent.NewSession("") // 无 system 头
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "user turn one"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "assistant reply"})
	return sess
}

// TestTL1407HeadlessSnapshotRepairsInsteadOfStorming pins the terminal guard:
// repeated save cycles on a content-bearing headless session must not storm
// WARNs, must restore the identity header, and must land the transcript on
// disk within the same cycle (crash-safety).
func TestTL1407HeadlessSnapshotRepairsInsteadOfStorming(t *testing.T) {
	logs := tl1407CaptureLogs(t)
	dir := schemaOneTempDir(t)
	path := filepath.Join(dir, "headless.jsonl")
	exec := agent.New(nil, nil, tl1407HeadlessSession(), agent.Options{}, event.Discard)
	c := New(Options{Executor: exec, SessionDir: dir, SessionPath: path, Label: "tl1407"})

	for i := 0; i < 5; i++ { // 模拟 5 个 autosave 周期
		if err := c.Snapshot(); err != nil {
			t.Fatalf("cycle %d: snapshot: %v", i, err)
		}
	}

	if storms := strings.Count(logs.String(), "refusing to snapshot"); storms > 2 {
		t.Fatalf("refusal WARN storm: %d cycles logged, want <= 2", storms)
	}
	if !exec.Session().HasSystemMessage() {
		t.Fatal("guard must repair the session with an identity header instead of refusing forever")
	}
	loaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("repaired transcript must persist within the same cycle: %v", err)
	}
	msgs := loaded.Snapshot()
	if len(msgs) == 0 || msgs[0].Role != provider.RoleSystem || msgs[0].Content == "" {
		t.Fatalf("persisted file lost the identity header: first=%+v", msgs)
	}
	found := false
	for _, m := range msgs {
		if m.Role == provider.RoleUser && m.Content == "user turn one" {
			found = true
		}
	}
	if !found {
		t.Fatal("in-memory messages must reach disk after the repair (crash-safety)")
	}
}

// TestTL1407ResumeRepairsHeadlessStock is the stock-heal: a headless session
// loaded from disk (pre-fix corruption) gets the identity header on resume, so
// the very first autosave succeeds and the model request carries the identity
// contract again.
func TestTL1407ResumeRepairsHeadlessStock(t *testing.T) {
	dir := schemaOneTempDir(t)
	exec := agent.New(nil, nil, agent.NewSession("healthy base"), agent.Options{}, event.Discard)
	c := New(Options{Executor: exec, SystemPrompt: "healthy base", SessionDir: dir, Label: "tl1407"})

	c.Resume(tl1407HeadlessSession(), filepath.Join(dir, "stock.jsonl"))

	if !exec.Session().HasSystemMessage() {
		t.Fatal("resume must repair a headless stock session in place")
	}
	if got := exec.Session().Snapshot()[0].Content; got != "healthy base" {
		t.Fatalf("stock repair must reuse the controller base prompt, got %q", got)
	}
}

// TestTL1407PathlessRefusalDumpsAndLimitsWarns pins the crash-safety net for
// the remaining refusal branch (content but no session path, #4414 family):
// the WARN is bounded and the in-memory transcript is dumped beside the
// session dir instead of being lost to the next crash.
func TestTL1407PathlessRefusalDumpsAndLimitsWarns(t *testing.T) {
	logs := tl1407CaptureLogs(t)
	dir := schemaOneTempDir(t)
	exec := agent.New(nil, nil, agent.NewSession("identity"), agent.Options{}, event.Discard)
	exec.Session().Add(provider.Message{Role: provider.RoleUser, Content: "unsaved treasure"})
	c := New(Options{Executor: exec, SessionDir: dir, Label: "tl1407"}) // 无 SessionPath

	for i := 0; i < 6; i++ {
		if err := c.Snapshot(); err == nil {
			t.Fatalf("cycle %d: missing session path must surface as an error", i)
		}
	}

	if storms := strings.Count(logs.String(), "no session path"); storms > 2 {
		t.Fatalf("no-path WARN storm: %d cycles logged, want <= 2", storms)
	}
	dumps, err := filepath.Glob(filepath.Join(dir, "*.unsaved-dump.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(dumps) != 1 {
		t.Fatalf("want exactly one crash-safety dump in %s, got %v", dir, dumps)
	}
	blob, err := os.ReadFile(dumps[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(blob), "unsaved treasure") {
		t.Fatalf("dump must carry the in-memory messages, got %s", blob)
	}
}
