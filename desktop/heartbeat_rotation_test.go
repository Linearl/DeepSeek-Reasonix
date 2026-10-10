package main

// Task 500 — tests for the reuse-session rotation mechanism (handoff relay).
// Acceptance matrix: eligibility partition, threshold verdicts (size / turns /
// suggest-only age), the requested→pending→rotating→done relay with grouped
// topics (shared-session tasks rotate together; manual and guard tasks never
// do), pending re-carry + attempts cap, bootstrap one-shot delivery, and
// byte-for-byte normal behavior when nothing is over threshold or the
// mechanism is disabled.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/control"
)

func writeRotationConfigFile(t *testing.T, cfg heartbeatRotationConfig) {
	t.Helper()
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := heartbeatRotationConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// idleAllHeartbeatConversations clears every open tab's turn reservation —
// the relay fixture accumulates two tabs (old topic + successor topic), and
// the single-tab helper in the reuse tests cancels only the first sink.
func idleAllHeartbeatConversations(t *testing.T, app *App, ctrl *heartbeatExecuteTaskCtrlStub) {
	t.Helper()
	app.mu.Lock()
	var sinks []*tabEventSink
	for _, tab := range app.tabs {
		if tab != nil && tab.sink != nil {
			sinks = append(sinks, tab.sink)
		}
	}
	app.mu.Unlock()
	for _, sink := range sinks {
		sink.cancelTurnStart()
	}
	ctrl.status = control.RuntimeStatus{}
}

func engineRotationState(t *testing.T, engine *HeartbeatEngine) *heartbeatRotationState {
	t.Helper()
	engine.mu.Lock()
	defer engine.mu.Unlock()
	return engine.loadRotationStateLocked()
}

func TestHeartbeatRotationConfigDefaultsAndOverrides(t *testing.T) {
	isolateDesktopUserDirs(t)

	// Missing file → defaults (the 2026-10-05 ruling values).
	def := loadHeartbeatRotationConfig()
	if !def.Enabled || !def.AutoRotate || def.EventsMB != 128 || def.Turns != 1000 || def.AgeDays != 14 || def.MaxHandoffAttempts != 3 {
		t.Fatalf("defaults = %+v, want enabled autoRotate 128MB/1000turns/14d/3attempts", def)
	}

	// Explicit file → honored, including criterion-off zeros.
	writeRotationConfigFile(t, heartbeatRotationConfig{
		SchemaVersion: heartbeatRotationSchemaVersion,
		Enabled:       true,
		AutoRotate:    false,
		EventsMB:      50,
		Turns:         0, // explicit off
		AgeDays:       0,
	})
	cfg := loadHeartbeatRotationConfig()
	if cfg.AutoRotate || cfg.EventsMB != 50 || cfg.Turns != 0 || cfg.AgeDays != 0 {
		t.Fatalf("parsed = %+v, want autoRotate off, 50MB, turns/age off", cfg)
	}

	// Corrupt file → defaults (never take the scheduler down).
	if err := os.WriteFile(heartbeatRotationConfigPath(), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg = loadHeartbeatRotationConfig()
	if cfg.EventsMB != 128 || cfg.Turns != 1000 {
		t.Fatalf("corrupt config fell back to %+v, want defaults", cfg)
	}

	// maxHandoffAttempts<=0 falls back to the default: zero attempts would
	// strand the relay in handoff_requested forever.
	writeRotationConfigFile(t, heartbeatRotationConfig{Enabled: true, MaxHandoffAttempts: 0})
	if cfg = loadHeartbeatRotationConfig(); cfg.MaxHandoffAttempts != 3 {
		t.Fatalf("maxHandoffAttempts = %d, want default 3", cfg.MaxHandoffAttempts)
	}
}

func TestHeartbeatRotationEligibleMatrix(t *testing.T) {
	cases := []struct {
		name string
		task HeartbeatTask
		want bool
	}{
		{"reuse on", HeartbeatTask{ID: "a", Enabled: true, ReuseSession: true}, true},
		{"legacy fixed topic", HeartbeatTask{ID: "b", Enabled: true}, true},
		{"fresh per run", HeartbeatTask{ID: "c", Enabled: true, NewConversationEachRun: true}, false},
		{"fresh+reuse (reuse wins, still a reuser)", HeartbeatTask{ID: "d", Enabled: true, NewConversationEachRun: true, ReuseSession: true}, true},
		{"guard", HeartbeatTask{ID: "autoguard-topic_x", Enabled: true, ReuseSession: true}, false},
		{"goal mode", HeartbeatTask{ID: "e", Enabled: true, GoalMode: true}, false},
		{"disabled", HeartbeatTask{ID: "f", Enabled: false, ReuseSession: true}, false},
	}
	for _, tc := range cases {
		if got := heartbeatRotationEligibleTask(tc.task); got != tc.want {
			t.Errorf("%s: eligible = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestHeartbeatTopicAgeFromID(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.Local)
	age, ok := heartbeatTopicAge("topic_20260927-022238_292035408dc781db", now)
	if !ok {
		t.Fatal("well-formed topic id should parse")
	}
	if want := 321*time.Hour + 37*time.Minute + 22*time.Second; age != want { // 2026-09-27 → 2026-10-10 = 13 天 9h37m22s
		t.Fatalf("age = %v, want %v", age, want)
	}
	if _, ok := heartbeatTopicAge("not-a-topic-id", now); ok {
		t.Fatal("garbage id must not parse")
	}
	future, ok := heartbeatTopicAge("topic_29991231-235959_x", now)
	if !ok || future != 0 {
		t.Fatalf("future id age = %v ok=%v, want clamped 0", future, ok)
	}
}

func TestHeartbeatRotationVerdictThresholds(t *testing.T) {
	cfg := defaultHeartbeatRotationConfig()
	mb := func(n int64) heartbeatSessionStats {
		return heartbeatSessionStats{EventsBytes: n * 1024 * 1024, Turns: 10}
	}

	// Size hit → auto.
	auto, _, reason := heartbeatRotationVerdict(mb(130), cfg)
	if !auto || !strings.Contains(reason, "128") {
		t.Fatalf("130MB: auto=%v reason=%q, want size hit", auto, reason)
	}
	// Turn hit → auto.
	auto, _, reason = heartbeatRotationVerdict(heartbeatSessionStats{EventsBytes: 1024, Turns: 1000}, cfg)
	if !auto || !strings.Contains(reason, "1000") {
		t.Fatalf("1000 turns: auto=%v reason=%q, want turn hit", auto, reason)
	}
	// Under everything → nothing.
	if auto, suggest, _ := heartbeatRotationVerdict(mb(127), cfg); auto || suggest {
		t.Fatal("127MB must not trigger")
	}
	// Age alone → suggest-only, never auto.
	auto, suggest, reason := heartbeatRotationVerdict(heartbeatSessionStats{EventsBytes: 1024, Turns: 5, Age: 15 * 24 * time.Hour, AgeKnown: true}, cfg)
	if auto || !suggest || !strings.Contains(reason, "建议级") {
		t.Fatalf("old+tiny: auto=%v suggest=%v reason=%q, want suggest-only", auto, suggest, reason)
	}
	// Nothing measurable → nothing, ever.
	if auto2, suggest2, _ := heartbeatRotationVerdict(heartbeatSessionStats{}, cfg); auto2 || suggest2 {
		t.Fatal("unmeasurable topic must not trigger")
	}
	// Criterion off by explicit zero.
	cfgZero := cfg
	cfgZero.Turns = 0
	if auto, _, _ := heartbeatRotationVerdict(heartbeatSessionStats{EventsBytes: 1024, Turns: 99999}, cfgZero); auto {
		t.Fatal("turns=0 must disable the turn criterion")
	}
}

func TestHeartbeatRotationSuccessorID(t *testing.T) {
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.Local)
	existing := map[string]bool{}
	if got := heartbeatRotationSuccessorID("guard-fork3-rot20261005", existing, now); got != "guard-fork3-rot20261011" {
		t.Fatalf("successor id = %q, want stable canonical id", got)
	}
	existing["guard-fork3-rot20261011"] = true
	if got := heartbeatRotationSuccessorID("guard-fork3-rot20261005", existing, now); got != "guard-fork3-rot20261011-2" {
		t.Fatalf("same-day second rotation id = %q, want -2 dedup", got)
	}
	if got := heartbeatRotationSuccessorID("plain", existing, now); got != "plain-rot20261011" {
		t.Fatalf("plain id = %q", got)
	}
}

// rotationRelayFixture seeds two reuse tasks + one manual task + one guard,
// all bound to one shared conversation, with an over-threshold stats resolver
// and a threshold config marking the manual task.
type rotationRelayFixture struct {
	engine *HeartbeatEngine
	app    *App
	ctrl   *heartbeatExecuteTaskCtrlStub
	topic  string
}

func newRotationRelayFixture(t *testing.T, manualID string) *rotationRelayFixture {
	t.Helper()
	engine, app := newReuseSessionTestEngine(t)
	ctrl := &heartbeatExecuteTaskCtrlStub{}
	old, err := app.CreateTopic("global", "", "old crowded session")
	if err != nil {
		t.Fatal(err)
	}
	tasks := []HeartbeatTask{
		{ID: "task-a", Title: "任务A", Prompt: "tick-a", Interval: "15m", Enabled: true, ReuseSession: true, TopicID: old.ID},
		{ID: "task-b", Title: "任务B", Prompt: "tick-b", Interval: "15m", Enabled: true, ReuseSession: true, TopicID: old.ID},
		{ID: manualID, Title: "手动线", Prompt: "tick-m", Interval: "15m", Enabled: true, ReuseSession: true, TopicID: old.ID},
		{ID: "autoguard-" + old.ID, Title: "Autopilot guard", Prompt: autopilotGuardPrompt, Interval: "15m", Enabled: true, ReuseSession: true, TopicID: old.ID, ApprovalMode: "ask"},
	}
	if err := engine.saveTasks(tasks); err != nil {
		t.Fatal(err)
	}
	engine.ReloadConfig()
	engine.rotationStats = func(topicID string) (heartbeatSessionStats, bool) {
		if topicID != old.ID {
			return heartbeatSessionStats{}, false
		}
		return heartbeatSessionStats{TopicID: topicID, EventsBytes: 130 * 1024 * 1024, Turns: 42}, true
	}
	writeRotationConfigFile(t, heartbeatRotationConfig{
		SchemaVersion:      heartbeatRotationSchemaVersion,
		Enabled:            true,
		AutoRotate:         true,
		EventsMB:           128,
		Turns:              1000,
		MaxHandoffAttempts: 3,
		ManualTaskIDs:      []string{manualID},
	})
	return &rotationRelayFixture{engine: engine, app: app, ctrl: ctrl, topic: old.ID}
}

func (f *rotationRelayFixture) taskByID(t *testing.T, id string) HeartbeatTask {
	t.Helper()
	for _, task := range f.engine.ListTasks() {
		if task.ID == id {
			return task
		}
	}
	t.Fatalf("task %q not found", id)
	return HeartbeatTask{}
}

// The full relay: detect → handoff carried (and re-carried while pending) →
// marker → rows relayed for the whole group only → successor bootstrap once →
// successor steady state.
func TestHeartbeatRotationHandoffRelayEndToEnd(t *testing.T) {
	f := newRotationRelayFixture(t, "task-manual")

	// 1) Detection: one rotation for the shared topic, group = [task-a task-b]
	// (manual and guard excluded).
	f.engine.reconcileRotationsCheap()
	state := engineRotationState(t, f.engine)
	rot, ok := state.Rotations[f.topic]
	if !ok || rot.Phase != rotationPhaseRequested {
		t.Fatalf("rotation state = %+v, want requested entry for %s", rot, f.topic)
	}
	if len(rot.TaskIDs) != 2 || rot.TaskIDs[0] != "task-a" || rot.TaskIDs[1] != "task-b" {
		t.Fatalf("group = %v, want [task-a task-b]", rot.TaskIDs)
	}
	if rot.MarkerPath == "" || !strings.HasSuffix(rot.MarkerPath, ".handoff.done") {
		t.Fatalf("marker path = %q", rot.MarkerPath)
	}

	// 2) First due run of a group member carries the handoff prompt.
	injected := injectHeartbeatCtrl(t, f.app, f.ctrl)
	f.engine.executeTask(f.taskByID(t, "task-a"))
	<-injected
	if len(f.ctrl.submitted) != 1 {
		t.Fatalf("submitted = %v, want exactly the handoff prompt", f.ctrl.submitted)
	}
	if !strings.Contains(f.ctrl.submitted[0], "交接") || !strings.Contains(f.ctrl.submitted[0], rot.MarkerPath) {
		t.Fatalf("handoff prompt missing marker instruction: %q", f.ctrl.submitted[0])
	}
	if strings.Contains(f.ctrl.submitted[0], "tick-a") {
		t.Fatal("handoff wake must not carry the task's own prompt")
	}
	state = engineRotationState(t, f.engine)
	if state.Rotations[f.topic].Phase != rotationPhasePending || state.Rotations[f.topic].Attempts != 1 {
		t.Fatalf("phase = %q attempts = %d, want pending/1",
			state.Rotations[f.topic].Phase, state.Rotations[f.topic].Attempts)
	}

	// 3) While pending, the next due run re-carries the instruction (the
	// previous wake produced no marker yet) — that is the retry path, and it
	// applies to any group member.
	idleAllHeartbeatConversations(t, f.app, f.ctrl)
	f.engine.executeTask(f.taskByID(t, "task-b"))
	if len(f.ctrl.submitted) != 2 || !strings.Contains(f.ctrl.submitted[1], "交接") {
		t.Fatalf("pending re-carry submitted %v, want the handoff prompt again", f.ctrl.submitted)
	}
	if got := engineRotationState(t, f.engine).Rotations[f.topic].Attempts; got != 2 {
		t.Fatalf("attempts = %d, want 2 after re-carry", got)
	}

	// 4) A marker naming a missing handoff document must not be trusted.
	if err := os.MkdirAll(filepath.Dir(rot.MarkerPath), 0o755); err != nil {
		t.Fatal(err)
	}
	handoffPath := filepath.Join(t.TempDir(), "handoff.md")
	if err := os.WriteFile(rot.MarkerPath, []byte(handoffPath+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.engine.reconcileRotationsCheap()
	if phase := engineRotationState(t, f.engine).Rotations[f.topic].Phase; phase != rotationPhasePending {
		t.Fatalf("phase = %q with missing handoff doc, want still pending", phase)
	}

	// 5) The handoff document lands → relay completes: one new topic, one
	// successor per group task, old rows retired in place, manual and guard
	// rows untouched.
	if err := os.WriteFile(handoffPath, []byte("# 交接\n五节内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.engine.reconcileRotationsCheap()
	state = engineRotationState(t, f.engine)
	if state.Rotations[f.topic].Phase != rotationPhaseDone {
		t.Fatalf("phase = %q, want done after valid marker", state.Rotations[f.topic].Phase)
	}
	newTopic := state.Rotations[f.topic].NewTopicID
	if newTopic == "" || newTopic == f.topic {
		t.Fatalf("new topic = %q, want a fresh id", newTopic)
	}
	if len(state.Rotations[f.topic].SuccessorIDs) != 2 {
		t.Fatalf("successors = %v, want two", state.Rotations[f.topic].SuccessorIDs)
	}

	snapshot, err := f.engine.readConfigSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]HeartbeatTask{}
	for _, task := range snapshot.cfg.Tasks {
		byID[task.ID] = task
	}
	for _, id := range []string{"task-a", "task-b"} {
		if byID[id].Enabled {
			t.Fatalf("%s still enabled after relay", id)
		}
		if !strings.Contains(byID[id].Title, "已退役") {
			t.Fatalf("%s title = %q, want 退役 mark", id, byID[id].Title)
		}
	}
	var succA HeartbeatTask
	for _, id := range state.Rotations[f.topic].SuccessorIDs {
		if strings.HasPrefix(id, "task-a") {
			succA = byID[id]
		} else if succ := byID[id]; !succ.Enabled || succ.TopicID != newTopic || !succ.ReuseSession {
			t.Fatalf("successor %s = %+v, want enabled on the new topic with reuseSession", id, succ)
		}
	}
	if !succA.Enabled || succA.TopicID != newTopic || !succA.ReuseSession {
		t.Fatalf("successor A = %+v, want enabled on the new topic with reuseSession", succA)
	}
	if _, booting := state.Bootstrap[succA.ID]; !booting {
		t.Fatalf("successor A missing from bootstrap map: %v", state.Bootstrap)
	}
	if !byID["task-manual"].Enabled || byID["task-manual"].TopicID != f.topic {
		t.Fatal("manual task must stay enabled on the old topic")
	}
	if !byID["autoguard-"+f.topic].Enabled {
		t.Fatal("guard task must stay untouched")
	}
	if _, stale := state.Bootstrap["task-manual"]; stale {
		t.Fatal("manual task must never get a bootstrap entry")
	}

	// 6) Successor's first wake = bootstrap (read handoff first), exactly once.
	idleAllHeartbeatConversations(t, f.app, f.ctrl)
	injected2 := injectHeartbeatCtrl(t, f.app, f.ctrl)
	f.engine.executeTask(f.taskByID(t, succA.ID))
	<-injected2
	if len(f.ctrl.submitted) != 3 || !strings.Contains(f.ctrl.submitted[2], "轮换接手") {
		t.Fatalf("successor first submit = %v, want the bootstrap prompt", f.ctrl.submitted)
	}
	if !strings.Contains(f.ctrl.submitted[2], "tick-a") {
		t.Fatalf("bootstrap must keep the task's own prompt: %q", f.ctrl.submitted[2])
	}
	if _, pending := engineRotationState(t, f.engine).Bootstrap[succA.ID]; pending {
		t.Fatal("bootstrap must be one-shot")
	}

	// 7) Successor's second wake = the task's own prompt again (steady state).
	idleAllHeartbeatConversations(t, f.app, f.ctrl)
	f.engine.executeTask(f.taskByID(t, succA.ID))
	if f.ctrl.submitted[len(f.ctrl.submitted)-1] != "tick-a" {
		t.Fatalf("successor steady-state submit = %q, want the task prompt", f.ctrl.submitted[len(f.ctrl.submitted)-1])
	}
}

// Attempts cap: carries without a valid marker end in failed, and a failed
// topic's runs go back to the task's own prompt. A manual task never carries.
func TestHeartbeatRotationAttemptsCapAndManualSkip(t *testing.T) {
	f := newRotationRelayFixture(t, "task-manual")
	prevGrace := heartbeatRotationGraceWindow
	heartbeatRotationGraceWindow = time.Millisecond
	defer func() { heartbeatRotationGraceWindow = prevGrace }()

	f.engine.reconcileRotationsCheap()

	// The manual task never carries the handoff, even when due first.
	injected := injectHeartbeatCtrl(t, f.app, f.ctrl)
	f.engine.executeTask(f.taskByID(t, "task-manual"))
	<-injected
	if len(f.ctrl.submitted) != 1 || f.ctrl.submitted[0] != "tick-m" {
		t.Fatalf("manual task submitted %v, want its own prompt", f.ctrl.submitted)
	}

	// Three group carries (cap=3) without a marker → failed.
	for i := 0; i < 3; i++ {
		idleAllHeartbeatConversations(t, f.app, f.ctrl)
		f.engine.executeTask(f.taskByID(t, "task-a"))
	}
	if got := engineRotationState(t, f.engine).Rotations[f.topic].Attempts; got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
	f.engine.reconcileRotationsCheap() // grace window forced to 1ms → fail now
	if phase := engineRotationState(t, f.engine).Rotations[f.topic].Phase; phase != rotationPhaseFailed {
		t.Fatalf("phase = %q, want failed after cap", phase)
	}
	idleAllHeartbeatConversations(t, f.app, f.ctrl)
	f.engine.executeTask(f.taskByID(t, "task-a"))
	if f.ctrl.submitted[len(f.ctrl.submitted)-1] != "tick-a" {
		t.Fatalf("post-failure submit = %q, want the task prompt", f.ctrl.submitted[len(f.ctrl.submitted)-1])
	}
}

// Zero-regression: with nothing over threshold the run is byte-for-byte the
// task's own prompt, and a disabled mechanism short-circuits detection even
// for an over-threshold session.
func TestHeartbeatRotationIdleAndDisabled(t *testing.T) {
	engine, app := newReuseSessionTestEngine(t)
	ctrl := &heartbeatExecuteTaskCtrlStub{}
	old, err := app.CreateTopic("global", "", "healthy session")
	if err != nil {
		t.Fatal(err)
	}
	seed := HeartbeatTask{ID: "healthy", Title: "健康任务", Prompt: "tick", Interval: "15m", Enabled: true, ReuseSession: true, TopicID: old.ID}
	if err := engine.saveTasks([]HeartbeatTask{seed}); err != nil {
		t.Fatal(err)
	}
	engine.ReloadConfig()
	// Defaults (config file absent): enabled, 128MB/1000 — nothing is
	// measurable here, so nothing may arm.
	engine.reconcileRotationsCheap()
	if state := engineRotationState(t, engine); len(state.Rotations) != 0 {
		t.Fatalf("rotations = %v, want none for a healthy session", state.Rotations)
	}

	injected := injectHeartbeatCtrl(t, app, ctrl)
	got := engine.executeTask(seed)
	<-injected
	if len(ctrl.submitted) != 1 || ctrl.submitted[0] != "tick" {
		t.Fatalf("submitted = %v, want the task's own prompt", ctrl.submitted)
	}
	if got.TopicID != old.ID {
		t.Fatalf("topic = %q, want the binding", got.TopicID)
	}

	// Explicit disabled: even an over-threshold session arms nothing.
	engine2, _ := newReuseSessionTestEngine(t)
	writeRotationConfigFile(t, heartbeatRotationConfig{SchemaVersion: heartbeatRotationSchemaVersion, Enabled: false})
	engine2.rotationStats = func(string) (heartbeatSessionStats, bool) {
		return heartbeatSessionStats{EventsBytes: 999 * 1024 * 1024, Turns: 99999}, true
	}
	engine2.reconcileRotationsCheap()
	if state := engineRotationState(t, engine2); len(state.Rotations) != 0 {
		t.Fatalf("disabled mechanism armed %v", state.Rotations)
	}
}
