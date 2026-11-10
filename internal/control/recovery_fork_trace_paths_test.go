package control

import (
	"errors"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

func TestForkTraceShutdownFork(t *testing.T) {
	logs := captureForkTraceLog(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "shutdown.jsonl")
	base := agent.NewSession("sys")
	base.Add(provider.Message{Role: provider.RoleUser, Content: "persisted"})
	if err := base.SaveSnapshot(path); err != nil {
		t.Fatal(err)
	}
	current, err := agent.LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	current.Add(provider.Message{Role: provider.RoleAssistant, Content: "shutdown tail"})
	exec := agent.New(nil, nil, current, agent.Options{}, event.Discard)
	c := New(Options{Executor: exec, SessionDir: dir, SessionPath: path, Label: "shutdown", Sink: event.Discard})
	recoveryPath, err := c.recoverShutdownSnapshot(path, agent.ErrSessionFileLockHeld)
	if err != nil {
		t.Fatalf("recoverShutdownSnapshot: %v", err)
	}
	rec := onlyForkTraceDecision(t, logs, forkDecisionFork)
	requireForkTraceFields(t, rec, map[string]string{
		"fork_reason":      forkReasonShutdownLock,
		"mode":             "shutdown",
		"src_msgs":         "3",
		"recovery":         agent.BranchID(recoveryPath),
		"controller_forks": "0",
	})
	requireShortDigest(t, rec, "src_digest", "base_digest")
	if _, present := rec["disk_rev"]; present {
		t.Fatalf("a lock-timeout fork has no disk side to report: %v", rec)
	}
	last := lastConflictDiagnostic(t, path)
	if last.Outcome != "forked_file_lock_recovery" || last.Decision != forkDecisionFork || last.Reason != forkReasonShutdownLock {
		t.Fatalf("shutdown record = %+v", last)
	}
}

// TestForkTraceToolCheckpointConflict pins the earliest detection point seen
// in the field: the tool checkpoint save meets the conflict and returns it
// without recovery, which must still leave one decision line.
func TestForkTraceToolCheckpointConflict(t *testing.T) {
	logs := captureForkTraceLog(t)
	path := filepath.Join(t.TempDir(), "checkpoint.jsonl")
	live := agent.NewSession("sys")
	live.AddBatch(provider.Message{Role: "user", Content: "m1"}, provider.Message{Role: "assistant", Content: "m2"})
	if err := live.SaveSnapshot(path); err != nil {
		t.Fatal(err)
	}
	other, err := agent.LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	other.Add(provider.Message{Role: "user", Content: "peer-tail"})
	if err := other.SaveSnapshot(path); err != nil {
		t.Fatal(err)
	}
	// tl1459 leftover B: one tail message keeps the live snapshot NOT strictly
	// ahead of disk (4 vs 4), so the checkpoint's rev-lag takeover escalation
	// declines and this pin keeps testing the terminal-conflict trace. The
	// takeover-eligible shape (live strictly ahead) is pinned by
	// TestCheckpointConflictRevLagTakeoverKeepsCheckpointAlive.
	live.AddBatch(provider.Message{Role: "user", Content: "live3"})
	c := newTakeoverLadderController(t, path, live)
	if err := c.checkpointToolTranscript(); !errors.Is(err, agent.ErrSessionSnapshotConflict) {
		t.Fatalf("checkpoint err = %v, want the snapshot conflict returned unchanged", err)
	}
	rec := onlyForkTraceDecision(t, logs, forkDecisionError)
	requireForkTraceFields(t, rec, map[string]string{
		"fork_reason":   forkReasonCheckpoint,
		"fork_trigger":  forkTriggerToolCheckpoint,
		"fork_entry":    forkEntryCheckpoint,
		"mode":          "checkpoint",
		"conflict_kind": string(agent.SessionSnapshotConflictDiverged),
		"disk_writer":   agent.LedgerWriterSelf,
		"peer_trigger":  forkTriggerOther,
	})
	if !strings.Contains(rec["peer_caller"], "TestForkTraceToolCheckpointConflict") {
		t.Fatalf("peer_caller = %q, want the peer writer's frame", rec["peer_caller"])
	}
	if _, present := rec["brake_active"]; present {
		t.Fatalf("a save-path line has no controller brake view: %v", rec)
	}
}

func TestTraceConflictEscalationDecisions(t *testing.T) {
	logs := captureForkTraceLog(t)
	conflict := &agent.SessionSnapshotConflictError{Path: "x.jsonl", Kind: agent.SessionSnapshotConflictDiverged, BaseRevision: 3, DiskRevision: 3}
	traceConflictEscalation(nil, "x.jsonl", conflict, forkReasonSameRevRewrite, nil, false)
	traceConflictEscalation(nil, "x.jsonl", conflict, forkReasonPendingRewrite, conflict, false)
	traceConflictEscalation(nil, "x.jsonl", conflict, forkReasonPendingRewrite, errors.New("authority lost"), false)
	traceConflictEscalation(nil, "x.jsonl", conflict, forkReasonSameRevRewrite, conflict, true)
	records := forkTraceRecords(logs.String())
	if len(records) != 3 {
		t.Fatalf("escalation lines = %d, want 3 (a still-conflicting snapshot escalation defers to the ladder)\n%s", len(records), logs.String())
	}
	want := [][2]string{
		{forkDecisionRewrite, forkReasonSameRevRewrite},
		{forkDecisionError, forkReasonPendingRewrite},
		{forkDecisionError, forkReasonSameRevRewrite},
	}
	for i, rec := range records {
		if rec["fork_decision"] != want[i][0] || rec["fork_reason"] != want[i][1] {
			t.Fatalf("line %d = %s/%s, want %s/%s", i, rec["fork_decision"], rec["fork_reason"], want[i][0], want[i][1])
		}
	}
	if records[2]["mode"] != "checkpoint" {
		t.Fatalf("checkpoint escalation mode = %q", records[2]["mode"])
	}
}

func TestForkBrakeObservationCountsWindowConflicts(t *testing.T) {
	c := &Controller{}
	now := time.Now()
	if got := c.observeForkBrakeLocked(now); got.active || got.seq != 0 || got.sinceLast != -1 {
		t.Fatalf("never forked = %+v", got)
	}
	c.lastSnapshotForkAt = now.Add(-30 * time.Second)
	first := c.observeForkBrakeLocked(now)
	second := c.observeForkBrakeLocked(now)
	if !first.active || first.seq != 1 || second.seq != 2 || first.remaining != snapshotForkBrakeWindow-30*time.Second {
		t.Fatalf("inside window = %+v then %+v", first, second)
	}
	c.lastSnapshotForkAt = now.Add(-snapshotForkBrakeWindow - time.Second)
	if outside := c.observeForkBrakeLocked(now); outside.active || outside.seq != 0 || outside.sinceLast <= snapshotForkBrakeWindow {
		t.Fatalf("outside window = %+v", outside)
	}
}

func forkFuncName(fn any) string {
	return runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
}

// TestForkTriggerRowsMatchControlFunctions pins every control-side trigger and
// entry row to the real method: a rename breaks compilation here instead of
// silently degrading the field to "other".
func TestForkTriggerRowsMatchControlFunctions(t *testing.T) {
	triggers := []struct {
		fn   any
		want string
	}{
		{(*Controller).autosaveWhileRunning, forkTriggerTurnAutosave},
		{(*Controller).finishInFlightTurn, forkTriggerTurnEnd},
		{(*Controller).snapshotActivityIfChanged, forkTriggerTurnEnd},
		{(*Controller).replaceSessionAfterCancel, forkTriggerTurnCancel},
		{(*Controller).recoverInterruptedTurn, forkTriggerTurnInterrupted},
		{(*Controller).checkpointToolTranscript, forkTriggerToolCheckpoint},
		{(*Controller).onInboxTurnDone, forkTriggerInbox},
		{(*Controller).submitCommandOrTurnReady, forkTriggerCompact},
		{conversationApplier.ApplyConversationTruncate, forkTriggerRewind},
		{conversationApplier.RestoreConversation, forkTriggerRewind},
		{(*Controller).forkNamedReady, forkTriggerBranchOp},
		{(*Controller).branchTip, forkTriggerBranchOp},
		{(*Controller).Branches, forkTriggerBranchOp},
		{(*Controller).NewSession, forkTriggerNewSession},
		{(*Controller).SelfHealOpenSession, forkTriggerSelfHeal},
	}
	for _, tc := range triggers {
		name := forkFuncName(tc.fn)
		if got := ForkTriggerForFunction(name); got != tc.want {
			t.Fatalf("ForkTriggerForFunction(%s) = %q, want %q", name, got, tc.want)
		}
	}
	entries := []struct {
		fn   any
		want string
	}{
		{(*Controller).Snapshot, forkEntrySnapshot},
		{(*Controller).SnapshotDurable, forkEntryDurable},
		{(*Controller).SnapshotActivity, forkEntryActivity},
		{(*Controller).SnapshotRewrite, forkEntryRewrite},
		{(*Controller).SnapshotForShutdown, forkEntryShutdown},
		{(*Controller).SnapshotIfSavePathFree, forkEntryIfPathFree},
		{(*Controller).snapshotActivityIfChanged, forkEntryActivityIfChanged},
		{(*Controller).checkpointToolTranscript, forkEntryCheckpoint},
	}
	for _, tc := range entries {
		pkg, fn := splitForkFunction(forkFuncName(tc.fn))
		if got := forkFrameClass(forkFrame{pkg: pkg, fn: fn}, forkEntryRows); got != tc.want {
			t.Fatalf("entry(%s) = %q, want %q", fn, got, tc.want)
		}
	}
	cores := []any{
		(*Controller).recoverSnapshotConflict, (*Controller).recoverSnapshotConflictLadder,
		(*Controller).recoverShutdownSnapshot, (*Controller).recoverShutdownSave,
		(*Controller).recoverExternallyRemovedSession, (*Controller).takeoverDivergedRevLag,
		(*Controller).snapshotWithDurability, (*Controller).snapshot, (*Controller).beginForkTrace,
		persistSessionSnapshot, persistSessionSnapshotMode, persistSessionSnapshotIfPathFree,
		retrySameRevisionDivergedRewrite, newForkTrace, traceConflictEscalation,
	}
	for _, fn := range cores {
		pkg, name := splitForkFunction(forkFuncName(fn))
		if !forkFrameIsCore(forkFrame{pkg: pkg, fn: name}) {
			t.Fatalf("%s must be a core frame", name)
		}
	}
}

func TestForkFrameMatchingAndChain(t *testing.T) {
	cases := []struct {
		fn, row string
		want    bool
	}{
		{"(*Controller).Snapshot", "(*Controller).Snapshot", true},
		{"(*Controller).Snapshot.func1", "(*Controller).Snapshot", true},
		{"(*Controller).Snapshot-fm", "(*Controller).Snapshot", true},
		{"(*Controller).SnapshotDurable", "(*Controller).Snapshot", false},
		{"(*Controller).snapshotWithDurability", "(*Controller).snapshot", false},
	}
	for _, tc := range cases {
		if got := forkFrameMatches(tc.fn, tc.row); got != tc.want {
			t.Fatalf("forkFrameMatches(%q, %q) = %v, want %v", tc.fn, tc.row, got, tc.want)
		}
	}
	frames := []forkFrame{
		{pkg: "runtime", fn: "Callers", file: "extern.go", line: 1},
		{pkg: "control", fn: "(*Controller).snapshotWithDurability", file: "controller.go", line: 2},
		{pkg: "agent", fn: "dietSessionProjection", file: "session_selfheal.go", line: 3},
		{pkg: "control", fn: "(*Controller).SnapshotDurable", file: "controller.go", line: 4},
		{pkg: "main", fn: "(*App).snapshotTabDurable", file: "app_tab_controller.go", line: 5},
		{pkg: "main", fn: "(*App).closeTabRuntime", file: "tabs.go", line: 6},
	}
	origin := classifyForkFrames(frames)
	if origin.trigger != forkTriggerSelfHeal || origin.entry != forkEntryDurable {
		t.Fatalf("origin = %+v, want the nearest session-layer trigger and the durable entry", origin)
	}
	want := "Controller.SnapshotDurable@controller.go:4<App.snapshotTabDurable@app_tab_controller.go:5<App.closeTabRuntime@tabs.go:6"
	if origin.caller != want {
		t.Fatalf("caller = %q, want %q", origin.caller, want)
	}
	if got := classifyForkFrames(frames[3:]); got.trigger != forkTriggerTabClose {
		t.Fatalf("desktop close path trigger = %q, want %q", got.trigger, forkTriggerTabClose)
	}
	if got := classifyForkFrames(nil); got.trigger != forkTriggerOther || got.entry != forkEntryDirect || got.caller != "" {
		t.Fatalf("empty stack origin = %+v", got)
	}
}
