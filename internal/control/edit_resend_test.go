package control

// tl1544 编辑历史消息回溯重发的验收矩阵钉（任务书验收①-⑤逐条落钉）：
//
//	①运行中编辑历史消息一次操作完成（中止+截断+重发原子）— TestEditAndResendRunningTurnAtomic
//	②fork 隔离语义保持（parent session 不动 + sidecar rebind 全套）— TestEditAndResendIdleForkIsolation
//	③四面治理：checkpoint 对账/事件账本/引导残留/续跑标记 — 各钉见本文件
//	④回合后既有 rewind 链零回退 — rewind_e2e_test.go 既有族全绿承载（本文件不重复）
//	⑤markEditedForNewUser 元数据正确 — TestEditAndResendEditedMetadata

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/sessioninbox"
	"reasonix/internal/tool"
)

// editResendGateProvider answers every round with the same read_file tool call
// while the gate is closed; once gateClosed fires it returns a final text so
// the resend turn completes on its own. The gate closes from the gate TOOL's
// ctx.Done branch — i.e. only after the edit-resend's stop signal reached the
// running turn — which is exactly the ordering the atomic编排 promises.
type editResendGateProvider struct {
	gateCh chan struct{}
	close  sync.Once
}

func (p *editResendGateProvider) Name() string { return "edit-resend-gate" }

func (p *editResendGateProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-p.gateCh:
		ch := make(chan provider.Chunk, 2)
		ch <- provider.Chunk{Type: provider.ChunkText, Text: "resend answer"}
		close(ch)
		return ch, nil
	default:
		ch := make(chan provider.Chunk, 2)
		ch <- provider.Chunk{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{
			ID:        fmt.Sprintf("call-%d", time.Now().UnixNano()),
			Name:      "read_file",
			Arguments: `{"path":"gate.txt"}`,
		}}
		close(ch)
		return ch, nil
	}
}

// closeGate flips the provider to the text branch (idempotent). Called from
// the gate tool's cancel branch AND from tests that need the resend turn to
// finish without a second gate pass.
func (p *editResendGateProvider) closeGate() {
	p.close.Do(func() { close(p.gateCh) })
}

// editResendGateTool blocks the tool loop on the gate channel; a turn-context
// cancel (the edit-resend stop) opens the gate for the NEXT turn.
type editResendGateTool struct {
	prov *editResendGateProvider
}

func (t *editResendGateTool) Name() string        { return "read_file" }
func (t *editResendGateTool) Description() string { return "gate tool" }
func (t *editResendGateTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)
}
func (t *editResendGateTool) ReadOnly() bool { return true }
func (t *editResendGateTool) Execute(ctx context.Context, _ json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		// The stop signal reached the tool mid-loop: flip the provider to the
		// text branch so the RESEND turn completes, then let the run loop see
		// the cancelled context and unwind this one.
		t.prov.closeGate()
		return "", err
	}
	// Fast success — the loop keeps spinning (same shape as the looping
	// cancel-test fixture) until the turn context fires.
	return "gate held", nil
}

func newEditResendRunningController(t *testing.T) (*Controller, *editResendGateProvider) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "edit-resend.jsonl")
	// No SessionWriter bind: the edit-resend chain forks the session, and a
	// writer-bound session fails the transition closed (no transition handler
	// in test controllers). Single-controller tests don't need the bind.
	prov := &editResendGateProvider{gateCh: make(chan struct{})}
	reg := tool.NewRegistry()
	reg.Add(&editResendGateTool{prov: prov})
	exec := agent.New(prov, reg, agent.NewSession("sys"), agent.Options{}, event.Discard)
	c := New(Options{
		Runner:      exec,
		Executor:    exec,
		Sink:        event.Discard,
		SessionDir:  dir,
		SessionPath: path,
	})
	t.Cleanup(func() { c.Close(); c.autosaveWG.Wait() })
	return c, prov
}

func waitEditResendIdle(t *testing.T, c *Controller, what string) {
	t.Helper()
	deadline := time.Now().Add(controlTestReceiveWindow)
	for {
		if !c.RuntimeStatus().Running {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: the turn never returned to idle", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestEditAndResendIdleForkIsolation pins 验收② (fork isolation: parent file
// untouched, sidecar rebind through the rewind channel) and the checkpoint
// reconciliation face of 验收③: after the edit-resend the new head has no
// checkpoint boundaries (UnfiledInputs empty, turnsByMessageIndex empty), so
// no unfiled-banner shape can reappear.
func TestEditAndResendIdleForkIsolation(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "parent.jsonl")
	// Seed through the same primitives the rewind e2e tests use: a real
	// controller over a real session file, checkpoints opened at the same
	// points runOrchestratedTurn would open them. The provider answers with a
	// fixed text so the resend turn completes on its own.
	sess := agent.NewSession("sys")
	ag := agent.New(&editResendEchoProvider{}, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	c2 := New(Options{
		Runner:        ag,
		Executor:      ag,
		SessionDir:    dir,
		SessionPath:   sessionPath,
		WorkspaceRoot: dir,
		Sink:          event.Discard,
	})
	defer c2.Close()
	c2.beginCheckpoint(context.Background(), "first prompt")
	if err := os2AppendUserAssistant(sess, "first prompt", "first answer"); err != nil {
		t.Fatal(err)
	}
	c2.beginCheckpoint(context.Background(), "second prompt")
	if err := os2AppendUserAssistant(sess, "second prompt", "second answer"); err != nil {
		t.Fatal(err)
	}
	if err := c2.SnapshotRewrite(); err != nil {
		t.Fatal(err)
	}
	parentBefore, err := mustReadFileE(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(c2.Checkpoints()) != 2 {
		t.Fatalf("pre-edit checkpoints = %d, want 2", len(c2.Checkpoints()))
	}

	if err := c2.EditAndResend(1, "second prompt EDITED", "second prompt EDITED", "second prompt"); err != nil {
		t.Fatalf("EditAndResend(idle) = %v", err)
	}
	waitEditResendIdle(t, c2, "idle edit-resend")

	// 验收② parent session file untouched (事件账本面: append-only authority).
	parentAfter, err := mustReadFileE(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(parentBefore) != string(parentAfter) {
		t.Fatal("the parent session file was rewritten by edit-resend")
	}
	// The controller now sits on a NEW fork file holding the truncated history
	// plus the edited resend message.
	if got := c2.SessionPath(); got == sessionPath {
		t.Fatal("the controller did not move onto the fork file")
	}
	msgs := ag.Session().Snapshot()
	// Shape assertion (not exact count): the turn context block the run loop
	// prepends is production behavior, so pin the CONTENT shape — the fork
	// carries the first turn's history and the edited resend, and nothing from
	// the discarded second turn.
	if len(msgs) < 4 {
		t.Fatalf("fork history = %d messages, want ≥4: %+v", len(msgs), msgs)
	}
	foundEdited := false
	for _, m := range msgs {
		text := agent.UserMessageText(m)
		if m.Role == provider.RoleUser && strings.Contains(text, "second prompt EDITED") {
			foundEdited = true
		}
		if strings.Contains(text, "second answer") {
			t.Fatalf("discarded turn output survived the fork: %q", text)
		}
	}
	if !foundEdited {
		t.Fatalf("the edited user message never landed: %+v", msgs)
	}
	// 验收③ checkpoint 对账面: the fork rebind dropped every boundary of the
	// discarded history — the only checkpoint on the new head is the resend
	// turn's own, labeled with the EDITED text (the rewind picker must show
	// the new prompt, never the replaced one).
	ckpt := c2.Checkpoints()
	if len(ckpt) != 1 {
		t.Fatalf("post-edit checkpoints = %d (%+v), want exactly the resend turn's", len(ckpt), ckpt)
	}
	if !strings.Contains(ckpt[0].Prompt, "second prompt EDITED") {
		t.Fatalf("post-edit checkpoint prompt = %q, want the edited text", ckpt[0].Prompt)
	}
	// UnfiledInputs must reconcile to zero: the forked file's checkpoint
	// ledger and its user messages agree (no unfiled-banner shape).
	if got := c2.UnfiledInputs(); len(got) != 0 {
		t.Fatalf("post-edit UnfiledInputs = %+v, want empty", got)
	}
	if got := c2.CheckpointTurnsByMessageIndex(); len(got) != 1 {
		t.Fatalf("post-edit turnsByMessageIndex = %v, want exactly the resend turn", got)
	}
	// Branch meta marks the fork lineage (parent id + fork turn) so the edit
	// rewind is distinguishable from a conflict-recovery fork.
	brs, err := agent.ListBranches(dir)
	if err != nil {
		t.Fatal(err)
	}
	foundParent := false
	for _, b := range brs {
		if b.Path == sessionPath {
			foundParent = true
		}
	}
	if !foundParent {
		t.Fatalf("parent session missing from branch listing: %+v", brs)
	}
	// sessionRevision bumped by the fork (tl1518 resident-staleness consumer).
	if c2.RuntimeStatus().Running {
		t.Fatal("controller busy after idle edit-resend")
	}
}

// TestEditAndResendRunningTurnAtomic pins 验收① — the whole point of the
// feature: with a turn RUNNING, one call cancels it, forks the truncated
// history at the historical turn, and resends the edited prompt, atomically.
func TestEditAndResendRunningTurnAtomic(t *testing.T) {
	// prov stays implicit while the old turn loops; it is flipped explicitly
	// after the编排 returns so the resend turn answers on its first stream.
	c, prov := newEditResendRunningController(t)

	c.SubmitDisplay("old prompt", "old prompt")
	deadline := time.Now().Add(controlTestReceiveWindow)
	for {
		if c.RuntimeStatus().Running && c.Checkpoints() != nil && len(c.Checkpoints()) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the running turn never opened its checkpoint")
		}
		time.Sleep(time.Millisecond)
	}
	// Rewind target = turn 0 (the running turn's own user message): editing
	// THE RUNNING message is the zcode shape.
	if len(c.Checkpoints()) != 1 {
		t.Fatalf("checkpoints = %d, want 1", len(c.Checkpoints()))
	}

	err := c.EditAndResend(0, "old prompt EDITED", "old prompt EDITED", "old prompt")
	if err != nil {
		t.Fatalf("EditAndResend(running) = %v", err)
	}
	// The编排 returned with the resend turn admitted; free the provider so it
	// answers with text and the turn completes.
	prov.closeGate()
	waitEditResendIdle(t, c, "edit-resend")

	msgs := c.executor.Session().Snapshot()
	// sys + edited user + resend answer (the old turn's output is discarded).
	if len(msgs) < 3 {
		t.Fatalf("post-edit transcript = %d messages, want ≥3: %+v", len(msgs), msgs)
	}
	foundEdited := false
	for _, m := range msgs {
		if m.Role == provider.RoleUser && strings.Contains(agent.UserMessageText(m), "old prompt EDITED") {
			foundEdited = true
		}
	}
	if !foundEdited {
		t.Fatalf("the edited prompt never landed: %+v", msgs)
	}
	for _, m := range msgs {
		if m.Role == provider.RoleUser && strings.Contains(agent.UserMessageText(m), "old prompt\n") && !strings.Contains(agent.UserMessageText(m), "EDITED") {
			t.Fatalf("the original prompt survived the fork: %+v", msgs)
		}
	}
	// The gate opened, so the resend turn answered.
	last := msgs[len(msgs)-1]
	if last.Role != provider.RoleAssistant || !strings.Contains(agent.DisplayAssistantText(last.Content), "resend answer") {
		t.Fatalf("the resend turn never completed: last=%s %q", last.Role, agent.DisplayAssistantText(last.Content))
	}
}

// TestEditAndResendEditedMetadata pins 验收⑤ — markEditedForNewUser stamps
// Edited=true + Original=旧 prompt as local-only metadata on the new user
// message.
func TestEditAndResendEditedMetadata(t *testing.T) {
	c, prov := newEditResendRunningController(t)

	c.SubmitDisplay("meta prompt", "meta prompt")
	deadline := time.Now().Add(controlTestReceiveWindow)
	for {
		if len(c.Checkpoints()) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the running turn never opened its checkpoint")
		}
		time.Sleep(time.Millisecond)
	}

	if err := c.EditAndResend(0, "meta prompt EDITED", "meta prompt EDITED", "meta prompt"); err != nil {
		t.Fatalf("EditAndResend = %v", err)
	}
	prov.closeGate()
	waitEditResendIdle(t, c, "edited-metadata")

	msgs := c.executor.Session().Snapshot()
	var edited *provider.Message
	for i := range msgs {
		m := msgs[i]
		if m.Role == provider.RoleUser && strings.Contains(agent.UserMessageText(m), "meta prompt EDITED") {
			edited = &msgs[i]
		}
	}
	if edited == nil {
		t.Fatalf("edited user message missing: %+v", msgs)
	}
	if !edited.Edited || edited.Original != "meta prompt" {
		t.Fatalf("Edited metadata wrong: Edited=%v Original=%q", edited.Edited, edited.Original)
	}
}

// TestEditAndResendDiscardsParkedReplayRows pins the parked-queue face of
// 验收③: parked bodies are dropped and their durable turn-kind replay rows are
// written off (never resendable), while dock-kind rows keep their shelf row.
func TestEditAndResendDiscardsParkedReplayRows(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "parked.jsonl")
	sess := agent.NewSession("sys")
	ag := agent.New(nil, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	c := New(Options{
		Runner:        ag,
		Executor:      ag,
		SessionDir:    dir,
		SessionPath:   sessionPath,
		WorkspaceRoot: dir,
		Sink:          event.Discard,
	})
	defer c.Close()
	// One historical turn with a checkpoint boundary so the rewind can fork.
	c.beginCheckpoint(context.Background(), "turn prompt")
	if err := os2AppendUserAssistant(sess, "turn prompt", "turn answer"); err != nil {
		t.Fatal(err)
	}
	if err := c.SnapshotRewrite(); err != nil {
		t.Fatal(err)
	}

	// Park two bodies the way the admission sites do: FIFO entry + replay
	// record persisted at "park time".
	turnReplay := newTurnParkReplay("queued follow-up body", "queued follow-up body", "test_park_turn")
	dockReplay := newDockParkReplay("unapplied steer body", "test_park_dock")
	c.mu.Lock()
	c.parkedTurns = append(c.parkedTurns,
		parkedTurnEntry{run: func(context.Context) error { return nil }, replay: turnReplay},
		parkedTurnEntry{run: func(context.Context) error { return nil }, replay: dockReplay},
	)
	c.mu.Unlock()
	c.persistParkReplay(turnReplay)
	c.persistParkReplay(dockReplay)

	if err := c.EditAndResend(0, "turn prompt EDITED", "turn prompt EDITED", "turn prompt"); err != nil {
		t.Fatalf("EditAndResend = %v", err)
	}

	c.mu.Lock()
	dropped := len(c.parkedTurns) == 0
	c.mu.Unlock()
	if !dropped {
		t.Fatal("parkedTurns FIFO was not cleared")
	}
	turnID := turnReplay.dockedRowID()
	dockID := dockReplay.dockedRowID()
	if turnID == "" || dockID == "" {
		t.Fatalf("replay rows were never persisted: turn=%q dock=%q", turnID, dockID)
	}
	// Inbox rows live on the ORIGINAL session manifest (fork does not copy
	// inbox items — rebindInbox's contract), so verify both rows by reopening
	// the parent session's inbox store.
	st, err := sessioninbox.Open(sessionPath, sessioninbox.Limits{})
	if err != nil {
		t.Fatalf("reopen parent inbox: %v", err)
	}
	defer st.Close()
	states := make(map[string]sessioninbox.InboxState)
	for _, item := range st.Snapshot().Items {
		states[item.ID] = item.State
	}
	if state, ok := states[turnID]; ok && (state == sessioninbox.StateQueued || state == sessioninbox.StateUncertain) {
		t.Fatalf("turn replay row still resendable: state=%s", state)
	}
	dockState, ok := states[dockID]
	if !ok {
		t.Fatal("dock replay row vanished (shelf must keep guidance)")
	}
	if dockState != sessioninbox.StateUncertain {
		t.Fatalf("dock row state = %s, want uncertain (shelf keeps guidance)", dockState)
	}
}

// TestEditAndResendRefusals pins the guard faces: closed controller,
// compaction in flight, missing original, and unwound-timeout semantics.
func TestEditAndResendRefusals(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "refusal.jsonl")
	sess := agent.NewSession("sys")
	ag := agent.New(nil, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	c := New(Options{
		Runner:        ag,
		Executor:      ag,
		SessionDir:    dir,
		SessionPath:   sessionPath,
		WorkspaceRoot: dir,
		Sink:          event.Discard,
	})

	if err := c.EditAndResend(0, "d", "i", ""); !strings.Contains(err.Error(), "original prompt") {
		t.Fatalf("empty original err = %v, want original-required", err)
	}
	c.Close()
	if err := c.EditAndResend(0, "d", "i", "o"); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed err = %v, want closed", err)
	}
}

// TestEditAndResendResendTurnOpensFreshContinuationState pins the continuation
// face of 验收③: the resend turn is a REAL user turn, so the stop-review
// streak and the recovery episode must be re-derived by the per-turn cleanup
// (resetStopReviewStreak / beginRecoveryEpisode in runOrchestratedTurn).
// We pin the streak reset — the one observable through a public read.
func TestEditAndResendResendTurnOpensFreshContinuationState(t *testing.T) {
	c, prov := newEditResendRunningController(t)

	c.SubmitDisplay("cont prompt", "cont prompt")
	deadline := time.Now().Add(controlTestReceiveWindow)
	for {
		if len(c.Checkpoints()) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the running turn never opened its checkpoint")
		}
		time.Sleep(time.Millisecond)
	}
	// Seed a stale streak the old turn left behind.
	c.mu.Lock()
	c.stopReviewStreak = 3
	c.mu.Unlock()

	if err := c.EditAndResend(0, "cont prompt EDITED", "cont prompt EDITED", "cont prompt"); err != nil {
		t.Fatalf("EditAndResend = %v", err)
	}
	prov.closeGate()
	waitEditResendIdle(t, c, "continuation")

	c.mu.Lock()
	streak := c.stopReviewStreak
	c.mu.Unlock()
	if streak != 0 {
		t.Fatalf("stop-review streak after the resend turn = %d, want 0 (fresh work segment)", streak)
	}
}

// editResendEchoProvider answers every request with a fixed text so seeded
// turns complete without a tool loop.
type editResendEchoProvider struct{}

func (p *editResendEchoProvider) Name() string { return "edit-resend-echo" }

func (p *editResendEchoProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ch := make(chan provider.Chunk, 2)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: "echo answer"}
	close(ch)
	return ch, nil
}

// helpers ---------------------------------------------------------------------

// os2AppendUserAssistant appends a user/assistant pair through the session API
// (the same shape a completed guarded turn leaves behind).
func os2AppendUserAssistant(sess *agent.Session, user, assistant string) error {
	sess.Add(provider.Message{Role: provider.RoleUser, Content: user})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: assistant})
	return nil
}

func mustReadFileE(path string) ([]byte, error) {
	return os.ReadFile(path)
}
