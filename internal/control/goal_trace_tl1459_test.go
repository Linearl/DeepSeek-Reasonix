package control

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1459 P0-a pins: every Goal loop exit writes one line with the real binding
// values, and every continuationEpoch change names its source.

func goalTraceLines(logs, msg string) []map[string]string {
	var out []map[string]string
	for _, line := range strings.Split(logs, "\n") {
		if !strings.Contains(line, `msg="`+msg+`"`) {
			continue
		}
		out = append(out, parseSlogTextLine(line))
	}
	return out
}

func lastGoalExit(t *testing.T, logs string) map[string]string {
	t.Helper()
	lines := goalTraceLines(logs, goalExitLogMessage)
	if len(lines) == 0 {
		t.Fatalf("no %q line in logs:\n%s", goalExitLogMessage, logs)
	}
	return lines[len(lines)-1]
}

// goalTraceController is an executor-only controller with an active Goal and a
// bound turn recorder, the state advanceGoalAfterTurn sees at a Goal turn end.
func goalTraceController(t *testing.T) (*Controller, *goalTurnRecorder) {
	t.Helper()
	ag := agent.New(nil, nil, agent.NewSession(""), agent.Options{}, event.Discard)
	c := New(Options{Executor: ag, Sink: event.Discard})
	c.SetGoal("ship the trace")
	scope, _, ok := c.goals.deliveryScope()
	if !ok {
		t.Fatal("goal not active after SetGoal")
	}
	rec := c.goals.newTurnRecorder(scope, c.goals.continuationToken())
	c.goalUsageTee.setActiveRecorder(rec)
	return c, rec
}

func TestGoalExitGateLogsRealBindingValues(t *testing.T) {
	t.Run("recorder epoch mismatch", func(t *testing.T) {
		logs := captureForkTraceLog(t)
		c, rec := goalTraceController(t)
		expected := rec.epoch + 5
		res := newTurnOrchestrator(c).advanceGoalAfterTurn(context.Background(), expected, nil)
		if res.cont {
			t.Fatal("gate must stop the loop")
		}
		got := lastGoalExit(t, logs.String())
		want := map[string]string{
			"goal_exit": goalExitGate, "site": goalSiteAdvance, "gate_reason": "recorder_epoch_mismatch",
			"expected_epoch": strconv.FormatUint(expected, 10), "recorder_epoch": strconv.FormatUint(rec.epoch, 10),
			"machine_epoch": strconv.FormatUint(rec.epoch, 10), "turn_active": "true", "goal_status": GoalStatusRunning,
		}
		for k, v := range want {
			if got[k] != v {
				t.Fatalf("%s = %q, want %q (line %v)", k, got[k], v, got)
			}
		}
	})
	t.Run("turn no longer active", func(t *testing.T) {
		logs := captureForkTraceLog(t)
		c, rec := goalTraceController(t)
		c.stopGoal(GoalStatusStopped)
		newTurnOrchestrator(c).advanceGoalAfterTurn(context.Background(), rec.epoch, nil)
		got := lastGoalExit(t, logs.String())
		if got["goal_exit"] != goalExitGate || got["gate_reason"] != "turn_inactive" || got["turn_active"] != "false" ||
			got["goal_status"] != GoalStatusStopped || got["machine_epoch"] != strconv.FormatUint(rec.epoch+1, 10) {
			t.Fatalf("gate line = %v", got)
		}
	})
	t.Run("ordinary turn stays silent", func(t *testing.T) {
		logs := captureForkTraceLog(t)
		ag := agent.New(nil, nil, agent.NewSession(""), agent.Options{}, event.Discard)
		c := New(Options{Executor: ag, Sink: event.Discard})
		newTurnOrchestrator(c).advanceGoalAfterTurn(context.Background(), c.goals.continuationToken(), nil)
		if lines := goalTraceLines(logs.String(), goalExitLogMessage); len(lines) != 0 {
			t.Fatalf("a non-Goal turn wrote goal exit lines: %v", lines)
		}
	})
}

func TestGoalExitTerminalErrorKeepsGoalRunningAndSaysSo(t *testing.T) {
	logs := captureForkTraceLog(t)
	c, rec := goalTraceController(t)
	res := newTurnOrchestrator(c).advanceGoalAfterTurn(context.Background(), rec.epoch, errors.New("provider exploded"))
	if res.cont {
		t.Fatal("terminal error must stop auto-continue")
	}
	if got := c.GoalStatus(); got != GoalStatusRunning {
		t.Fatalf("GoalStatus = %q, want running (exit ② leaves the FSM untouched)", got)
	}
	got := lastGoalExit(t, logs.String())
	if got["goal_exit"] != goalExitTerminalError || got["err"] != "provider exploded" || got["goal_status"] != GoalStatusRunning ||
		got["turn_active"] != "true" || got["expected_epoch"] != strconv.FormatUint(rec.epoch, 10) {
		t.Fatalf("terminal-error line = %v", got)
	}
}

func TestGoalExitFSMDecisionCarriesOutcome(t *testing.T) {
	logs := captureForkTraceLog(t)
	c, rec := goalTraceController(t)
	if _, err := rec.RecordGoalReport(tool.GoalReport{Status: "continue", Reason: "more to do", NextAction: "write the test"}); err != nil {
		t.Fatal(err)
	}
	res := newTurnOrchestrator(c).advanceGoalAfterTurn(context.Background(), rec.epoch, nil)
	if !res.cont {
		t.Fatalf("continue report must continue: %+v", res)
	}
	got := lastGoalExit(t, logs.String())
	if got["goal_exit"] != goalExitFSM || got["cont"] != "true" || got["report"] != GoalStatusRunning ||
		got["epoch_after"] != strconv.FormatUint(res.continuationEpoch, 10) || got["notice_set"] != "false" {
		t.Fatalf("fsm line = %v", got)
	}
}

func TestGoalExitEvaluatorFailClosedIsTagged(t *testing.T) {
	logs := captureForkTraceLog(t)
	c, rec := goalTraceController(t) // no evaluator configured: fail closed
	res := newTurnOrchestrator(c).advanceGoalAfterTurn(context.Background(), rec.epoch, nil)
	if res.cont || c.GoalStatus() != GoalStatusBlocked {
		t.Fatalf("evaluator-less turn must fail closed: cont=%v status=%q", res.cont, c.GoalStatus())
	}
	got := lastGoalExit(t, logs.String())
	if got["goal_exit"] != goalExitEvaluatorFailClosed || got["evaluator"] != "failed" || got["stop_cause"] != stopCauseEvaluator ||
		got["goal_status"] != GoalStatusBlocked || got["notice_set"] != "true" || !strings.Contains(got["evaluator_err"], "unavailable") {
		t.Fatalf("evaluator line = %v", got)
	}
}

// poisonLedgerTool simulates the 2026-11-06 failure: mid-turn, a tool
// checkpoint hit a snapshot conflict, the durable turn-event sink failed, and
// failTurnEventLedger cancelled the turn under a running Goal.
type poisonLedgerTool struct{ c **Controller }

func (poisonLedgerTool) Name() string            { return "poison_ledger" }
func (poisonLedgerTool) Description() string     { return "" }
func (poisonLedgerTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (poisonLedgerTool) ReadOnly() bool          { return true }
func (p poisonLedgerTool) Execute(context.Context, json.RawMessage) (string, error) {
	(*p.c).failTurnEventLedger(errors.New("checkpoint tool transcript: session snapshot conflicts with newer transcript"))
	return "ok", nil
}

// TestGoalExitDurabilityCancelReproducesIncidentShape is the in-test replay of
// one incident round: a Goal turn is cancelled by a durability failure, and the
// exit line names it (turn_cancelled + durability_err) instead of nothing.
func TestGoalExitDurabilityCancelReproducesIncidentShape(t *testing.T) {
	logs := captureForkTraceLog(t)
	var c *Controller
	reg := goalRegistry()
	reg.Add(poisonLedgerTool{c: &c})
	prov := &scriptedTurns{turns: [][]provider.Chunk{
		{toolCallChunk("p1", "poison_ledger", `{}`), {Type: provider.ChunkDone}},
		textTurn("unreachable"),
	}}
	ag := agent.New(prov, reg, agent.NewSession(""), agent.Options{}, event.Discard)
	events := make(chan event.Event, 32)
	c = New(Options{
		Runner:   ag,
		Executor: ag,
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.TurnDone || e.Kind == event.Notice {
				events <- e
			}
		}),
	})
	c.Submit("/goal finish the constitution")
	waitGoalTurnDone(t, events)

	var cancelled map[string]string
	for _, line := range goalTraceLines(logs.String(), goalExitLogMessage) {
		if line["goal_exit"] == goalExitTurnCancelled {
			cancelled = line
		}
	}
	if cancelled == nil {
		t.Fatalf("durability cancel left no turn_cancelled exit line:\n%s", logs.String())
	}
	if !strings.Contains(cancelled["durability_err"], "snapshot conflicts") || cancelled["site"] != goalSiteFirstTurn ||
		cancelled["recorder"] != "true" || cancelled["cancel_cause"] != goalCancelDurability {
		t.Fatalf("turn_cancelled line = %v", cancelled)
	}
	t.Logf("incident-shape exit line: %v", cancelled)
}

func TestGoalEpochChangesNameTheirSource(t *testing.T) {
	logs := captureForkTraceLog(t)
	ag := agent.New(nil, nil, agent.NewSession(""), agent.Options{}, event.Discard)
	c := New(Options{Executor: ag, Sink: event.Discard})
	c.SetGoal("trace every epoch")
	c.PauseGoal()
	c.ResumeGoal()
	c.goals.advance(goalAdvanceInput{report: &goalTurnReport{status: GoalStatusRunning, reason: "more", nextAction: "next"}})
	c.stopGoal(GoalStatusStopped)
	c.SetGoal("")

	wantSources := []string{goalEpochInstall, goalEpochPause, goalEpochResume, goalEpochAdvance, goalEpochStop, goalEpochClear}
	lines := goalTraceLines(logs.String(), goalEpochLogMessage)
	if len(lines) != len(wantSources) {
		t.Fatalf("epoch lines = %d, want %d:\n%s", len(lines), len(wantSources), logs.String())
	}
	for i, line := range lines {
		if line["source"] != wantSources[i] {
			t.Fatalf("epoch line %d source = %q, want %q", i, line["source"], wantSources[i])
		}
		from, _ := strconv.ParseUint(line["from"], 10, 64)
		to, _ := strconv.ParseUint(line["to"], 10, 64)
		if to != from+1 {
			t.Fatalf("epoch line %d: from %d to %d is not one step", i, from, to)
		}
	}
	if got := lines[len(lines)-1]["to"]; got != strconv.FormatUint(c.goals.continuationToken(), 10) {
		t.Fatalf("last epoch line to=%s, machine epoch=%d", got, c.goals.continuationToken())
	}
}

// TestGoalEpochOnlyMovesThroughBump is the E4 guard: continuationEpoch moves
// only inside bumpEpochLocked, so a new mutation site (a recovery hook that
// "just bumps the epoch") cannot appear without declaring a source.
func TestGoalEpochOnlyMovesThroughBump(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	mutation := regexp.MustCompile(`continuationEpoch\s*(\+\+|--|\+=|-=|=[^=])`)
	var offenders []string
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if !mutation.MatchString(line) {
				continue
			}
			if file == "goal_trace.go" && strings.Contains(line, "g.continuationEpoch++") {
				continue // bumpEpochLocked itself
			}
			offenders = append(offenders, file+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("continuationEpoch mutated outside bumpEpochLocked; route it through bumpEpochLocked(<source>):\n%s", strings.Join(offenders, "\n"))
	}
}

// TestForkDecisionLineCarriesGoalBinding: the tl1443 decision line (reused,
// not duplicated) now says which Goal epoch/status the recovery saw.
func TestForkDecisionLineCarriesGoalBinding(t *testing.T) {
	logs := captureForkTraceLog(t)
	c, _ := goalTraceController(t)
	trace := c.beginForkTrace(filepath.Join(t.TempDir(), "s.jsonl"), "snapshot", errors.New("not a conflict"))
	trace.decide(forkDecisionDrop, forkReasonUnclassified)
	c.finishForkTrace(trace, conflictDropped, nil)
	recs := forkTraceRecords(logs.String())
	if len(recs) != 1 {
		t.Fatalf("decision lines = %d, want 1:\n%s", len(recs), logs.String())
	}
	if recs[0]["goal_status"] != GoalStatusRunning || recs[0]["goal_epoch"] != strconv.FormatUint(c.goals.continuationToken(), 10) {
		t.Fatalf("decision line goal binding = status %q epoch %q", recs[0]["goal_status"], recs[0]["goal_epoch"])
	}
}

// TestListingRepairLedgerBumpIsTracedAsForkPeer pins the observability half of
// the stage-4 root cause: a listing repair that rewrites the CAS ledger is a
// traced in-process commit, classified as listing_repair.
func TestListingRepairLedgerBumpIsTracedAsForkPeer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "20260925-150958.0-collab.jsonl")
	s := agent.NewSession("system prompt")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "question"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "answer"})
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	meta, ok, err := agent.LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta ok=%v err=%v", ok, err)
	}
	// A ledger digest that does not describe the transcript: the repair's
	// "stale ledger" trigger.
	meta.ContentDigest = strings.Repeat("0", 64)
	if err := agent.SaveBranchMeta(path, meta); err != nil {
		t.Fatal(err)
	}
	result, err := agent.RepairSessionListingProjection(context.Background(), path)
	if err != nil || !result.LedgerRepaired {
		t.Fatalf("repair = %+v err=%v, want a ledger repair", result, err)
	}
	repaired, _, err := agent.LoadBranchMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	commit, ok := agent.RecentLedgerCommit(path, repaired.Revision)
	if !ok {
		t.Fatalf("listing repair revision %d not in the commit trace", repaired.Revision)
	}
	if origin := classifyForkFrames(forkFramesFromPCs(commit.PCs)); origin.trigger != forkTriggerListingRepair {
		t.Fatalf("peer trigger = %q (caller %s), want %q", origin.trigger, origin.caller, forkTriggerListingRepair)
	}
}
