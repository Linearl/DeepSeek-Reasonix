package main

// tl1606 基础体验族 A 档防线·共享 harness（断言①②③共用，只放测试装置不放断言）：
//
//   - tl1606PromptProvider：按回合脚本出流的 provider，可按轮次持流（hold）
//     构造「回合在飞 / 收尾窗口」现场，并逐次捕获 provider.Request —— 那是
//     模型真正看到的完整提示词（系统块 + 历史 + 本轮输入），即「历史提示词
//     含」的唯一权威证据面。
//   - tl1606EventLog：控制器 sink 的事件留痕（UI 可见的 live 面）。
//   - newTl1606Session / newTl1606Tab：落盘会话 + App/tab/控制器装配，
//     与 app_history_tail_layer_test.go、turn_submission_correlation_test.go
//     的既有骨架同构。
//
// 面边界申报：本文件与三个 tl1606* 断言文件均为新增测试面，不改任何生产
// 代码；与 wt-tl1600-1601 的投影/渲染修复面零触碰。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1606Round scripts one provider streaming round.
type tl1606Round struct {
	text string
	hold bool // block the stream until releaseHold, arming an in-flight turn
}

// tl1606PromptProvider replays scripted rounds and captures every request.
// Each captured provider.Request.Messages IS the composed model prompt —
// the authoritative "历史提示词" evidence the three-way consistency nail
// asserts against.
type tl1606PromptProvider struct {
	mu       sync.Mutex
	rounds   []tl1606Round
	call     int
	requests []provider.Request
	release  chan struct{}
}

func newTl1606Provider(rounds []tl1606Round) *tl1606PromptProvider {
	return &tl1606PromptProvider{rounds: rounds}
}

func (p *tl1606PromptProvider) Name() string { return "tl1606-scripted" }

func (p *tl1606PromptProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	i := p.call
	if i >= len(p.rounds) {
		i = len(p.rounds) - 1
	}
	round := p.rounds[i]
	p.call++
	var release chan struct{}
	if round.hold {
		release = make(chan struct{})
		p.release = release
	}
	p.mu.Unlock()

	ch := make(chan provider.Chunk, 2)
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			// Cancel 中断形态：流随 ctx 结束，回合按取消收场。
			close(ch)
			return ch, nil
		}
	}
	ch <- provider.Chunk{Type: provider.ChunkText, Text: round.text}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

// waitHeld blocks until the scripted hold round is actually streaming (the
// in-flight / finishing-window premise is armed).
func (p *tl1606PromptProvider) waitHeld(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		held := p.release != nil
		p.mu.Unlock()
		if held {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("tl1606 provider hold round never started streaming")
}

// releaseHold opens the held stream. Idempotent under the provider lock.
func (p *tl1606PromptProvider) releaseHold() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.release != nil {
		close(p.release)
		p.release = nil
	}
}

func (p *tl1606PromptProvider) requestsSnapshot() []provider.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]provider.Request(nil), p.requests...)
}

// promptText concatenates every user/assistant message the provider has seen —
// the full model-visible prompt surface across rounds.
func (p *tl1606PromptProvider) promptText() string {
	var b strings.Builder
	for _, req := range p.requestsSnapshot() {
		for _, m := range req.Messages {
			b.WriteString(m.Content)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// tl1606EventLog is a controller sink that records every event for the
// "events 在册 / UI live 面" assertions.
type tl1606EventLog struct {
	mu     sync.Mutex
	events []event.Event
}

func (l *tl1606EventLog) Emit(e event.Event) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.events = append(l.events, e)
	l.mu.Unlock()
}

func (l *tl1606EventLog) sawKind(kind event.Kind) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.events {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

func (l *tl1606EventLog) snapshot() []event.Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]event.Event(nil), l.events...)
}

// tl1606Harness bundles one live tab: App + WorkspaceTab + real controller
// (Runner = Executor = the real agent over the scripted provider).
type tl1606Harness struct {
	t        *testing.T
	app      *App
	tab      *WorkspaceTab
	ctrl     *control.Controller
	prov     *tl1606PromptProvider
	events   *tl1606EventLog
	sink     *tabEventSink
	sessionP string
}

// newTl1606Harness builds a live tab on a seeded on-disk session at
// dir/tl1606-<name>.jsonl. seedTurns writes one authored user turn (+reply)
// so the session file exists before any admission check reads it.
func newTl1606Harness(t *testing.T, name string, rounds []tl1606Round, seedUser, seedAssistant string) *tl1606Harness {
	t.Helper()
	dir := t.TempDir()
	path := newTl1606Session(t, dir, seedUser, seedAssistant)
	h := attachTl1606Harness(t, dir, path, rounds)
	h.t = t
	return h
}

// newTl1606Session writes a minimal session file with one authored turn and
// returns its path. The seed keeps the admission entity checks (tabSession
// liveness) honest: the jsonl exists before any submit.
func newTl1606Session(t *testing.T, dir, seedUser, seedAssistant string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sess := agent.NewSession("system")
	if seedUser != "" {
		sess.Add(provider.Message{Role: provider.RoleUser, Content: seedUser, Origin: provider.MessageOriginUser})
		sess.Add(provider.Message{Role: provider.RoleAssistant, Content: seedAssistant})
	}
	path := filepath.Join(dir, "tl1606-session.jsonl")
	if err := sess.Save(path); err != nil {
		t.Fatalf("seed session save: %v", err)
	}
	return path
}

// attachTl1606Harness stands a live controller on an existing session path —
// the restart 形态's second lifecycle reuses this on the same path.
func attachTl1606Harness(t *testing.T, dir, path string, rounds []tl1606Round) *tl1606Harness {
	t.Helper()
	prov := newTl1606Provider(rounds)
	events := &tl1606EventLog{}
	loaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("load session %s: %v", path, err)
	}
	ag := agent.New(prov, tool.NewRegistry(), loaded, agent.Options{}, event.Discard)
	ctrl := control.New(control.Options{
		Runner:      ag,
		Executor:    ag,
		Sink:        events,
		SessionDir:  dir,
		SessionPath: path,
	})
	t.Cleanup(func() { ctrl.Close() })
	app := NewApp()
	app.ctx = context.Background()
	tab := &WorkspaceTab{
		ID:            "tl1606_tab_" + filepath.Base(path),
		Ctrl:          ctrl,
		Scope:         "global",
		WorkspaceRoot: "",
		Ready:         true,
		SessionPath:   path,
		disabledMCP:   map[string]ServerView{},
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app, ctx: context.Background()}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.activeTabID = tab.ID
	// 生产装配同款：控制器自主派发收件箱项前先过 App 准入（beforeInboxDispatch）。
	ctrl.SetBeforeInboxDispatch(func(c *control.Controller) (func(), error) {
		return app.beforeInboxDispatch(c)
	})
	return &tl1606Harness{
		t:        t,
		app:      app,
		tab:      tab,
		ctrl:     ctrl,
		prov:     prov,
		events:   events,
		sink:     tab.sink,
		sessionP: path,
	}
}

// waitIdle waits until no turn is running and the TurnDone fan-out closed.
func (h *tl1606Harness) waitIdle() {
	h.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		st := h.ctrl.RuntimeStatus()
		if !st.Running && !st.Finishing && !st.PendingPrompt {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("tl1606 turn never went idle: %+v", h.ctrl.RuntimeStatus())
}

// waitForTurns waits until the provider observed at least n streaming rounds.
func (h *tl1606Harness) waitForTurns(n int) {
	h.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if len(h.prov.requestsSnapshot()) >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("tl1606 provider saw %d rounds, want >= %d", len(h.prov.requestsSnapshot()), n)
}

// snapshotToDisk persists the live transcript through the production surface
// and waits until the needle reached the jsonl (turn-end durability is async
// relative to TurnDone fan-out; the snapshot contract itself is what the nail
// pins —「落盘后必在」).
func (h *tl1606Harness) snapshotToDisk(needle string) {
	h.t.Helper()
	if err := h.ctrl.Snapshot(); err != nil {
		h.t.Fatalf("tl1606 snapshot: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(h.sessionP); err == nil && strings.Contains(string(b), needle) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatalf("session jsonl %s never contained %q after snapshot", h.sessionP, needle)
}

func (h *tl1606Harness) readSessionJSONL() string {
	h.t.Helper()
	b, err := os.ReadFile(h.sessionP)
	if err != nil {
		h.t.Fatalf("read session jsonl: %v", err)
	}
	return string(b)
}

// uiRows converts the live transcript through the production history
// conversion — exactly what the frontend transcript would render.
func (h *tl1606Harness) uiRows() []HistoryMessage {
	h.t.Helper()
	return historyMessages(h.ctrl.History(), sessionDisplayResolver(filepath.Dir(h.sessionP), h.sessionP))
}

// sliceEntries reads the reuse-path slice API (what a re-attached tab asks
// for) through the App.
func (h *tl1606Harness) sliceEntries() HistorySlice {
	h.t.Helper()
	return h.app.HistorySliceForTab(h.tab.ID, HistorySliceRequest{})
}

// turnLedgerSubmissions reads the turns.jsonl ledger envelopes via the
// controller's production read API — the "events 在册" durable face.
func (h *tl1606Harness) turnLedgerSubmissions() []string {
	h.t.Helper()
	envs, err := h.ctrl.TurnEventsAfter(0)
	if err != nil {
		h.t.Fatalf("turn events after 0: %v", err)
	}
	ids := make([]string, 0, len(envs))
	for _, e := range envs {
		if e.SubmissionID != "" {
			ids = append(ids, e.SubmissionID)
		}
	}
	return ids
}
