package agent

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/event"
)

func TestAskRejectsMoreThanThreeQuestions(t *testing.T) {
	_, err := NewAskTool().Execute(context.Background(), []byte(`{
		"questions":[
			{"header":"A","question":"a?","options":[{"label":"1"},{"label":"2"}]},
			{"header":"B","question":"b?","options":[{"label":"1"},{"label":"2"}]},
			{"header":"C","question":"c?","options":[{"label":"1"},{"label":"2"}]},
			{"header":"D","question":"d?","options":[{"label":"1"},{"label":"2"}]}
		]
	}`))
	if err == nil || !strings.Contains(err.Error(), "at most 3") {
		t.Fatalf("error = %v", err)
	}
}

func TestAskReusesAcceptedDecisionWithoutNewEvidence(t *testing.T) {
	turn := &turnRuntime{}
	turn.loop.rememberDecision("dec-1", "Which path?", "Keep going")
	ctx := withTurnState(context.Background(), turn)
	out, err := NewAskTool().Execute(ctx, []byte(`{
		"decision_id":"dec-1",
		"questions":[{"header":"Direction","question":"Which path?","options":[{"label":"Keep going"},{"label":"Stop"}]}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "reused accepted decision") {
		t.Fatalf("got %q", out)
	}
}

func TestAskAllowsReopenWithNewEvidence(t *testing.T) {
	turn := &turnRuntime{}
	turn.loop.rememberDecision("dec-1", "Which path?", "Keep going")
	asker := &recordingAsker{}
	ctx := withCallContext(withTurnState(context.Background(), turn), "c1", event.Discard, asker, false)
	out, err := NewAskTool().Execute(ctx, []byte(`{
		"decision_id":"dec-1",
		"new_evidence":"new metric from the latest run",
		"questions":[{"header":"Direction","question":"Which path?","options":[{"label":"Keep going"},{"label":"Stop"}]}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "decision_id") {
		t.Fatalf("got %q", out)
	}
	if len(asker.questions) != 1 {
		t.Fatalf("asker questions = %d", len(asker.questions))
	}
}

// 任务926（显式重发放行）: allow_replay=true 放行用户显式重发——精确 decision_id
// 与「同歧义改述」两条去重臂都要让面板真实弹出；不带标志的重复调度（无意图）
// 必须照旧去重（防 ask 风暴）；伪造 decision_id 不因 allow_replay 豁免。
func TestAskAllowReplayBypassesDedupOnlyOnExplicitRequest(t *testing.T) {
	turn := &turnRuntime{}
	asker := &recordingAsker{}
	ctx := withCallContext(withTurnState(context.Background(), turn), "c1", event.Discard, asker, false)
	const body = `{"questions":[{"header":"Direction","question":"Which path?","options":[{"label":"Keep going"},{"label":"Stop"}]}]}`

	// 第一次提问：面板弹出，决策入账并带出 decision_id。
	first, err := NewAskTool().Execute(ctx, []byte(body))
	if err != nil {
		t.Fatalf("first ask: %v", err)
	}
	if asker.calls != 1 {
		t.Fatalf("asker calls = %d, want 1", asker.calls)
	}
	const marker = "decision_id: "
	idx := strings.LastIndex(first, marker)
	if idx < 0 {
		t.Fatalf("first ask = %q, want a %q tail", first, marker)
	}
	decID := strings.TrimSpace(first[idx+len(marker):])
	if !strings.HasPrefix(decID, "dec-") {
		t.Fatalf("decision id = %q, want dec- prefix", decID)
	}

	// ① 无用户意图的重复调度（同题）→ 仍去重，面板不弹。
	dup, err := NewAskTool().Execute(ctx, []byte(body))
	if err != nil {
		t.Fatalf("duplicate ask: %v", err)
	}
	if !strings.Contains(dup, "reused accepted decision") || asker.calls != 1 {
		t.Fatalf("duplicate = %q calls=%d, want dedup with the panel untouched", dup, asker.calls)
	}

	// ② 同歧义改述（不带 decision_id）→ 仍去重。
	rephrase, err := NewAskTool().Execute(ctx, []byte(`{"questions":[{"header":"Direction","question":"Please choose the direction now.","options":[{"label":"Keep going"},{"label":"Stop"}]}]}`))
	if err != nil {
		t.Fatalf("rephrased ask: %v", err)
	}
	if !strings.Contains(rephrase, "same ambiguity") || asker.calls != 1 {
		t.Fatalf("rephrased = %q calls=%d, want the ambiguity dedup", rephrase, asker.calls)
	}

	// ③ allow_replay + 同题（精确去重臂放行）→ 面板真实弹出可答。
	replay, err := NewAskTool().Execute(ctx, []byte(`{"allow_replay":true,`+body[1:]))
	if err != nil {
		t.Fatalf("allow_replay ask: %v", err)
	}
	if strings.Contains(replay, "reused accepted decision") {
		t.Fatalf("allow_replay was still deduplicated: %q", replay)
	}
	if asker.calls != 2 {
		t.Fatalf("allow_replay calls = %d, want the panel to pop (2)", asker.calls)
	}
	if !strings.Contains(replay, "decision_id") {
		t.Fatalf("replayed ask = %q, want the decision_id tail", replay)
	}

	// ④ allow_replay + 同歧义改述（歧义去重臂放行）。
	replayRephrased, err := NewAskTool().Execute(ctx, []byte(`{"allow_replay":true,"questions":[{"header":"Direction","question":"So, which way do we go this time?","options":[{"label":"Keep going"},{"label":"Stop"}]}]}`))
	if err != nil {
		t.Fatalf("allow_replay rephrase: %v", err)
	}
	if strings.Contains(replayRephrased, "reused accepted decision") || asker.calls != 3 {
		t.Fatalf("allow_replay rephrase = %q calls=%d, want the ambiguity arm bypassed (3)", replayRephrased, asker.calls)
	}

	// ⑤ allow_replay 不豁免伪造 id：未知 decision_id 依旧报错、面板不弹。
	_, err = NewAskTool().Execute(ctx, []byte(`{"allow_replay":true,"decision_id":"dec-fabricated","new_evidence":"made up","questions":[{"header":"Again","question":"Ask again?","options":[{"label":"Yes"},{"label":"No"}]}]}`))
	if err == nil || !strings.Contains(err.Error(), "unknown decision_id") || asker.calls != 3 {
		t.Fatalf("fabricated id under allow_replay: err=%v calls=%d, want the unknown-id error and no panel", err, asker.calls)
	}
}
