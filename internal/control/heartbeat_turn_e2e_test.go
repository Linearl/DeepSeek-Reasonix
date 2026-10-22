package control

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// recordingTurns wraps scriptedTurns and captures every provider request, so
// assertions can read what the model actually received (the ask refusal travels
// as the ask tool result of the continuation request).
type recordingTurns struct {
	scriptedTurns
	requests []provider.Request
}

func (r *recordingTurns) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	r.requests = append(r.requests, req)
	return r.scriptedTurns.Stream(ctx, req)
}

// Task 1062 acceptance 1 (端到端实测): a heartbeat-driven turn whose model
// calls ask — the exact 20261022 协调线 hang shape — runs through the real
// submit path (SubmitHeartbeatTurn → runGuarded → turn ctx marker), gets the
// short backstop refusal fed back as the ask result, and the turn CONTINUES
// to completion instead of parking on a panel nobody watches.
func TestHeartbeatTurnAskBackstopEndToEnd(t *testing.T) {
	askArgs, err := json.Marshal(map[string]any{
		"questions": []map[string]any{{
			"header":   "Deploy",
			"question": "Deploy to production now, or hold for the window?",
			"options":  []map[string]string{{"label": "Deploy"}, {"label": "Hold"}},
		}},
	})
	if err != nil {
		t.Fatalf("marshal ask args: %v", err)
	}
	prov := &recordingTurns{scriptedTurns: scriptedTurns{turns: [][]provider.Chunk{
		{
			{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{ID: "ask-1", Name: "ask", Arguments: string(askArgs)}},
			{Type: provider.ChunkDone},
		},
		{
			{Type: provider.ChunkText, Text: "refusal recorded; continuing with the board report"},
			{Type: provider.ChunkDone},
		},
	}}}
	reg := tool.NewRegistry()
	reg.Add(agent.NewAskTool())
	ag := agent.New(prov, reg, agent.NewSession(""), agent.Options{}, event.Discard)

	var notices []event.Event
	var sawAskPanel bool
	c := New(Options{
		Runner:   ag,
		Executor: ag,
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.Notice {
				notices = append(notices, e)
			}
			if e.Kind == event.AskRequest {
				sawAskPanel = true
			}
		}),
	})
	// Production wires the controller in as the agent's asker (boot); without
	// it the ask tool takes its own headless fallback and the backstop is
	// unreachable — wiring this e2e exists to exercise.
	ag.SetAsker(c)

	c.SubmitHeartbeatTurn("心跳巡检：检查队列并汇报", "心跳巡检：检查队列并汇报")

	deadline := time.Now().Add(10 * time.Second)
	for c.RuntimeStatus().Running && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if c.RuntimeStatus().Running {
		t.Fatal("heartbeat turn still running after 10s — the ask backstop did not fire (hang shape reproduced)")
	}
	if prov.call != 2 {
		t.Fatalf("provider called %d times, want 2 (ask turn + continuation turn after the refusal)", prov.call)
	}

	// The refusal reached the model as the ask tool result — assert on the
	// continuation request the model actually received (searchable in the
	// transcript), with the sanctioned way out named.
	any := false
	for _, req := range prov.requests {
		raw, _ := json.Marshal(req.Messages)
		if strings.Contains(string(raw), "talk_to_session") {
			any = true
		}
	}
	if !any {
		t.Fatal("the ask refusal (talk_to_session guidance) never reached any model request")
	}
	found := false
	for _, n := range notices {
		if n.Code == "heartbeat_ask_timeout" {
			found = true
		}
	}
	if !found {
		t.Fatalf("notices = %+v, want heartbeat_ask_timeout (可检索判据锚)", notices)
	}
	if sawAskPanel {
		t.Fatal("a heartbeat ask must not surface an ask panel (立即拒绝，面板未弹出)")
	}
	msgs := ag.Session().Messages
	if got := lastAssistantText(msgs); got != "refusal recorded; continuing with the board report" {
		t.Fatalf("last assistant text = %q, want the continuation turn's answer (回合继续)", got)
	}
}
