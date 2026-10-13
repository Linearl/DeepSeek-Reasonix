package main

import (
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// 任务 831③（切入 history 尾部分层）的 desktop 层验收：
// ① 大日志（超过首绘尾部阈值）的首屏页从尾窗加载：HasOlder 强制为真（窗口
//    之前必有未载入的更早内容），指纹取持久层身份（与全量页同源，前端驻留
//    缓存的秒开校验不被尾窗打破）；
// ② 小日志走原全量路径，行为不变；
// ③ 翻旧页（beforeTurn>0）走全量加载，把窗口之前的更早内容补全——首屏分层
//    不吞历史。
//
// fixture 说明：history 分层的 idle 读分支要求 ctrl 无未保存变更（空 executor
// session），盘上内容由本测试直接经 agent 层写入，controllerWithContent 会
// 覆写盘上会话，故不用。

// appWithIdleTabAt builds a tab whose controller session is loaded from the
// on-disk session at path (a fresh in-memory session would report unsaved
// changes and keep RuntimeStatus non-idle, so the durable-read branch under
// test never runs).
func appWithIdleTabAt(t *testing.T, path string) (*App, *WorkspaceTab) {
	t.Helper()
	sess, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession fixture: %v", err)
	}
	ag := agent.New(stubProvider{}, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	ctrl := control.New(control.Options{Executor: ag, SessionDir: filepath.Dir(path), SessionPath: path, Sink: event.Discard})
	tab := &WorkspaceTab{
		ID:            "test_tab",
		Ctrl:          ctrl,
		Scope:         "global",
		WorkspaceRoot: "",
		Ready:         true,
		disabledMCP:   map[string]ServerView{},
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: nil}
	a := &App{
		tabs:        map[string]*WorkspaceTab{"test_tab": tab},
		activeTabID: "test_tab",
	}
	tab.sink.app = a
	return a, tab
}

// buildLargeSessionLog grows a schema-2 log past the first-paint tail
// threshold with append-only saves (each save appends only the new message, so
// this stays cheap). Each block opens a user turn, because paging counts turns
// by user messages.
func buildLargeSessionLog(t *testing.T, path string) int {
	t.Helper()
	sess := agent.NewSession("system")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "seed"})
	if err := sess.SaveSnapshot(path); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	big := strings.Repeat("x", 1<<20) // 1MiB per message
	turns := 1
	// 40 user turns + 40 assistant turns of 1MiB ≈ 40MiB, safely past the
	// first-paint tail threshold (32MiB).
	for i := 0; i < 80; i++ {
		if i%2 == 0 {
			sess.Add(provider.Message{Role: provider.RoleUser, Content: "turn " + string(rune('a'+i/2))})
			turns++
		} else {
			sess.Add(provider.Message{Role: provider.RoleAssistant, Content: big})
		}
		if err := sess.SaveSnapshot(path); err != nil {
			t.Fatalf("grow save %d: %v", i, err)
		}
	}
	return turns
}

func TestHistoryPageFirstPaintLoadsTailWindowOnLargeLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.jsonl")
	buildLargeSessionLog(t, path)

	a, tab := appWithIdleTabAt(t, path)

	page := a.HistoryPageForTab(tab.ID, 0, 60)
	if !page.HasOlder {
		t.Fatal("first-paint page on a large log reports HasOlder=false; the older-history entry would disappear")
	}
	if len(page.Messages) == 0 {
		t.Fatal("first-paint tail page came back empty")
	}
	// Fingerprint must come from the durable ledger identity (the same pair a
	// full page carries), not from the window content — otherwise the
	// frontend's resident-cache freshness check mismatches on the next switch
	// and the fast path regresses.
	state, ok, err := agent.SessionContentIdentity(path)
	if err != nil || !ok {
		t.Fatalf("SessionContentIdentity: ok=%v err=%v", ok, err)
	}
	if page.Digest != state.DigestHex {
		t.Fatal("tail page digest does not match the ledger identity; resident-cache checks would mismatch")
	}
	if state.RevisionKnown && page.Revision != state.Revision {
		t.Fatalf("tail page revision = %d, want %d", page.Revision, state.Revision)
	}
	// The tail loader itself must have truncated: the log is past the
	// first-paint threshold, so a full replay did not happen.
	tail, err := agent.LoadSessionTail(path)
	if err != nil {
		t.Fatalf("LoadSessionTail: %v", err)
	}
	if !tail.TailTruncated() {
		t.Fatal("fixture log is below the first-paint tail threshold; the large-log test lost its premise")
	}
}

func TestHistoryPageOlderTurnsRefillFullHistory(t *testing.T) {
	// 翻上再补的分支验证用中日志（轮次超过一页、但体积在尾窗阈值之下）：
	// 首屏=全量，beforeTurn>0 的翻旧请求同样走全量加载，totalTurns 反映
	// 完整历史。（大日志的翻旧同样走这条全量分支——尾窗页只服务首屏。）
	path := filepath.Join(t.TempDir(), "medium.jsonl")
	sess := agent.NewSession("system")
	if err := sess.SaveSnapshot(path); err != nil {
		t.Fatalf("fixture save: %v", err)
	}
	totalTurns := 0
	for i := 0; i < 90; i++ {
		sess.Add(provider.Message{Role: provider.RoleUser, Content: "turn " + string(rune('a'+i))})
		sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "ack " + string(rune('a'+i))})
		totalTurns++
		if err := sess.SaveSnapshot(path); err != nil {
			t.Fatalf("grow save %d: %v", i, err)
		}
	}

	a, tab := appWithIdleTabAt(t, path)

	first := a.HistoryPageForTab(tab.ID, 0, 60)
	if first.TotalTurns != totalTurns {
		t.Fatalf("first page totalTurns = %d, want the full %d (sub-threshold log stays full)", first.TotalTurns, totalTurns)
	}
	if !first.HasOlder || first.StartTurn <= 0 {
		t.Fatalf("precondition: first page = HasOlder=%v startTurn=%d, want an older page to fetch", first.HasOlder, first.StartTurn)
	}
	// 翻上再补：beforeTurn>0 走全量加载，页内容回填更早的轮次。
	older := a.HistoryPageForTab(tab.ID, first.StartTurn, 60)
	if older.TotalTurns != totalTurns {
		t.Fatalf("older page totalTurns = %d, want the full %d", older.TotalTurns, totalTurns)
	}
	if len(older.Messages) == 0 {
		t.Fatal("older page came back empty")
	}
	if older.StartTurn >= first.StartTurn {
		t.Fatalf("older page starts at %d, want earlier than %d", older.StartTurn, first.StartTurn)
	}
}

func TestHistoryPageSmallLogKeepsFullFirstPaint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "small.jsonl")
	sess := agent.NewSession("system")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "hi"})
	if err := sess.SaveSnapshot(path); err != nil {
		t.Fatalf("fixture save: %v", err)
	}

	a, tab := appWithIdleTabAt(t, path)

	page := a.HistoryPageForTab(tab.ID, 0, 60)
	if len(page.Messages) == 0 {
		t.Fatal("small-log first paint came back empty")
	}
	// 小日志在尾窗阈值之下：loader 直通全量，页指纹行为与 831③ 之前一致
	// （digest 来自完整页内容本身）。
	loaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if loaded.TailTruncated() {
		t.Fatal("small log unexpectedly truncated")
	}
	digest, _ := agent.ContentDigestForMessages(loaded.Snapshot())
	if page.Digest != digest {
		t.Fatal("small-log page digest diverged from the content digest")
	}
}
