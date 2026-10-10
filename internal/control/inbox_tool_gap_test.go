package control

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// 任务461-P9 验收：长 turn 中入队的补充指示（followup 意图，注入点扩展前只能
// 等 turn 边界）在 ≤1 个工具调用周期内被模型看到——round1 的工具运行中入队，
// 工具一结束的间隙即拉入 steer 队列并回灌，round2 的模型请求上下文必须已含
// 该消息。队列语义与去重不变（同一条目、一次性消费、拒绝则留在队里）。

// gapGateTool blocks until its gate opens, so the test controls exactly when
// round 1's tool call completes (the gap where injection must land).
type gapGateTool struct {
	name    string
	started chan struct{}
	gate    chan struct{}
	once    sync.Once
}

func (t *gapGateTool) Name() string        { return t.name }
func (t *gapGateTool) Description() string { return "gated" }
func (t *gapGateTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (t *gapGateTool) ReadOnly() bool { return true }
func (t *gapGateTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	t.once.Do(func() { close(t.started) })
	<-t.gate
	return t.name + " ok", nil
}

// capturingTurns replays scripted rounds and records every provider request,
// so the test asserts what the MODEL actually saw in each round's context.
type capturingTurns struct {
	turns    [][]provider.Chunk
	mu       sync.Mutex
	requests [][]provider.Message
	call     int
}

func (s *capturingTurns) Name() string { return "capturing" }

func (s *capturingTurns) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	s.mu.Lock()
	i := s.call
	if i >= len(s.turns) {
		i = len(s.turns) - 1
	}
	s.call++
	s.requests = append(s.requests, req.Messages)
	s.mu.Unlock()
	ch := make(chan provider.Chunk, len(s.turns[i]))
	for _, c := range s.turns[i] {
		ch <- c
	}
	close(ch)
	return ch, nil
}

func gapToolTurn(name string) []provider.Chunk {
	return []provider.Chunk{
		{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{ID: name + "-1", Name: name, Arguments: `{"path":"x"}`}},
		{Type: provider.ChunkDone},
	}
}

// The acceptance: enqueue while round 1's tool is RUNNING → the guidance must
// be inside round 2's model request (≤1 tool cycle).
func TestToolGapInjectsQueuedGuidanceWithinOneCycle(t *testing.T) {
	prov := &capturingTurns{turns: [][]provider.Chunk{
		gapToolTurn("gap_read"),
		gapToolTurn("gap_read"),
		textTurn("done"),
	}}
	gated := &gapGateTool{name: "gap_read", started: make(chan struct{}), gate: make(chan struct{})}
	reg := tool.NewRegistry()
	reg.Add(gated)
	ag := agent.New(prov, reg, agent.NewSession(""), agent.Options{}, event.Discard)

	dir := t.TempDir()
	c := New(Options{
		Runner:      ag,
		Executor:    ag,
		SessionDir:  dir,
		SessionPath: dir + "/p9.session.jsonl",
		Label:       "p9",
		Sink:        event.Discard,
	})
	t.Cleanup(c.Close)

	turnDone := make(chan error, 1)
	go func() {
		turnDone <- c.runTurnWithRaw(context.Background(), "跑一个长任务", "跑一个长任务")
	}()

	// Round 1's tool is RUNNING (constraint ①: never interrupt it).
	<-gated.started

	// Mid-turn enqueue with the plain followup intent — the slow lane that
	// before P9 waited for the turn boundary.
	const injected = "补充指示：顺便检查构建日志"
	if _, err := c.EnqueueInbox(InboxRequest{Display: injected, Raw: injected}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// Release round 1's tool → the gap fires → the queue head must be pulled
	// into the steer queue and consumed in this very gap.
	close(gated.gate)

	if err := <-turnDone; err != nil {
		t.Fatalf("turn: %v", err)
	}

	prov.mu.Lock()
	rounds := prov.call
	var round2 []provider.Message
	if rounds >= 2 {
		round2 = prov.requests[1]
	}
	prov.mu.Unlock()
	if rounds < 2 {
		t.Fatalf("provider rounds = %d, want ≥2", rounds)
	}
	inRound2 := false
	for _, m := range round2 {
		if strings.Contains(m.Content, injected) {
			inRound2 = true
		}
	}
	if !inRound2 {
		t.Fatalf("the injected guidance must be inside round 2's model request (≤1 tool cycle), rounds=%d round2=%d msgs", rounds, len(round2))
	}
	if !ag.SteerConsumed() {
		t.Fatal("the steer queue must be fully drained after the turn")
	}
}

// The gates hold: with the inbox paused, the gap must NOT pull queued items —
// queue semantics (pause) are unchanged.
func TestToolGapRespectsPausedInbox(t *testing.T) {
	prov := &capturingTurns{turns: [][]provider.Chunk{
		gapToolTurn("gap_read"),
		textTurn("done"),
	}}
	gated := &gapGateTool{name: "gap_read", started: make(chan struct{}), gate: make(chan struct{})}
	reg := tool.NewRegistry()
	reg.Add(gated)
	ag := agent.New(prov, reg, agent.NewSession(""), agent.Options{}, event.Discard)

	dir := t.TempDir()
	c := New(Options{
		Runner:      ag,
		Executor:    ag,
		SessionDir:  dir,
		SessionPath: dir + "/p9p.session.jsonl",
		Label:       "p9-paused",
		Sink:        event.Discard,
	})
	t.Cleanup(c.Close)

	if _, err := c.EnqueueInbox(InboxRequest{Display: "排队一条", Raw: "排队一条"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := c.SetInboxPaused(true); err != nil {
		t.Fatalf("pause: %v", err)
	}

	turnDone := make(chan error, 1)
	go func() {
		turnDone <- c.runTurnWithRaw(context.Background(), "跑", "跑")
	}()
	<-gated.started
	close(gated.gate)
	if err := <-turnDone; err != nil {
		t.Fatalf("turn: %v", err)
	}

	for _, req := range prov.requests {
		for _, m := range req {
			if strings.Contains(m.Content, "排队一条") {
				t.Fatal("a paused inbox must never inject mid-turn")
			}
		}
	}
}
