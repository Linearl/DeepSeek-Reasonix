package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// steerThenCancelTool queues a steer while the turn is running, then cancels
// the turn so Run exits before the loop's per-iteration consume can deliver it.
type steerThenCancelTool struct {
	agent     *Agent
	cancel    context.CancelFunc
	steerText string
	accepted  bool
}

func (t *steerThenCancelTool) Name() string        { return "steer_then_cancel" }
func (t *steerThenCancelTool) Description() string { return "queues a steer and cancels the turn" }
func (t *steerThenCancelTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (t *steerThenCancelTool) ReadOnly() bool { return true }
func (t *steerThenCancelTool) Execute(context.Context, json.RawMessage) (string, error) {
	t.accepted = t.agent.Steer(t.steerText)
	t.cancel()
	return "ok", nil
}

// TestRunFlushesUnconsumedSteersOnCancel proves a steer that is still queued
// when the turn is cancelled survives in local history but not the next model
// context, and emits an explicit warning instead of presenting it as
// successfully applied guidance.
func TestRunFlushesUnconsumedSteersOnCancel(t *testing.T) {
	mp := testutil.NewMock("m",
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "steer_then_cancel", Arguments: `{}`}}},
		testutil.Turn{Text: "never reached"},
	)
	hijack := &steerThenCancelTool{steerText: "use plan B"}
	reg := tool.NewRegistry()
	reg.Add(hijack)
	var notices []event.Event
	sink := event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice && e.Code == event.NoticeCodeUnappliedSteer {
			notices = append(notices, e)
		}
	})
	a := New(mp, reg, NewSession(""), Options{}, sink)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hijack.agent = a
	hijack.cancel = cancel

	err := a.Run(ctx, "go")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run should exit on the cancelled context, got %v", err)
	}
	if !hijack.accepted {
		t.Fatalf("Steer during an active turn should be accepted")
	}

	var persisted []string
	var localOnly bool
	for _, m := range a.Session().Messages {
		if text, ok := SteerText(m.Content); ok {
			persisted = append(persisted, text)
			localOnly = m.LocalOnly && m.Role == provider.RoleTool &&
				m.ToolCallID == provider.LocalOnlyToolID && m.Name == provider.LocalOnlyToolName
		}
	}
	if len(persisted) != 1 || persisted[0] != "use plan B" {
		t.Fatalf("unconsumed steer should be persisted once and round-trip through SteerText, got %v", persisted)
	}
	if !localOnly {
		t.Fatal("unconsumed steer must use the provider-excluded local-only sentinel")
	}
	for _, m := range provider.ModelMessages(a.Session().Snapshot()) {
		if text, ok := SteerText(m.Content); ok {
			t.Fatalf("unconsumed steer %q leaked into the next model context", text)
		}
	}
	if len(notices) != 1 || notices[0].Level != event.LevelWarn ||
		!strings.Contains(notices[0].Text, "use plan B") ||
		!strings.Contains(notices[0].Text, "not applied") {
		t.Fatalf("flushed steer should emit an explicit warning, got %+v", notices)
	}
	if n := a.steerQueueLen(); n != 0 {
		t.Fatalf("steer queue should be empty after the turn, len=%d", n)
	}
	if !a.HasUnappliedSteer() {
		t.Fatal("host should observe that the cancelled turn left unapplied guidance")
	}
	if a.Steer("after the turn") {
		t.Fatalf("Steer must be rejected once the turn has exited")
	}
}

// TestCloseSteerIntakeIfIdleMakesAdmissionLinearizable pins the normal turn
// exit boundary: once the final queue check observes no pending guidance, a
// later steer must be rejected rather than accepted and flushed as unapplied.
func TestCloseSteerIntakeIfIdleMakesAdmissionLinearizable(t *testing.T) {
	a := New(nil, tool.NewRegistry(), NewSession(""), Options{}, event.Discard)
	a.steerMu.Lock()
	a.steerRunActive = true
	a.steerMu.Unlock()

	if !a.closeSteerIntakeIfIdle() {
		t.Fatal("empty steer intake should close")
	}
	if a.Steer("too late") {
		t.Fatal("steer after the final queue check must be rejected")
	}
	if n := a.steerQueueLen(); n != 0 {
		t.Fatalf("rejected steer remained queued, len=%d", n)
	}
	if a.HasUnappliedSteer() {
		t.Fatal("closing an empty steer intake must not report unapplied guidance")
	}
}

func TestWithdrawnDurableSteerDoesNotEmitUnappliedNotice(t *testing.T) {
	var notices int
	a := New(nil, tool.NewRegistry(), NewSession(""), Options{}, event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice && e.Code == event.NoticeCodeUnappliedSteer {
			notices++
		}
	}))
	a.steerMu.Lock()
	a.steerRunActive = true
	a.steerMu.Unlock()
	if !a.SteerItem("withdrawn-consume", func() (SteerPayload, error) { return SteerPayload{}, ErrSteerWithdrawn }) {
		t.Fatal("active steer should be accepted")
	}
	if payload, itemID, ok := a.consumeSteer(); ok || payload.Text != "" || itemID != "" {
		t.Fatalf("withdrawn consume = (%q, %q, %v), want silent miss", payload.Text, itemID, ok)
	}
	if !a.SteerItem("withdrawn-flush", func() (SteerPayload, error) { return SteerPayload{}, ErrSteerWithdrawn }) {
		t.Fatal("second active steer should be accepted")
	}
	a.flushSteerQueue()
	if notices != 0 {
		t.Fatalf("withdrawn steer emitted %d unapplied notices", notices)
	}
	if len(a.Session().Messages) != 0 {
		t.Fatalf("withdrawn steer wrote transcript messages: %+v", a.Session().Messages)
	}
}

// TestFlushSteerQueueMergesBatchIntoOneCountedNotice pins the tl1303
// anti-storm contract: a dead run holding several guidance messages surfaces
// ONE counted user notice (not one warning per message), while every durable
// entry still emits a restore-only signal so control's inbox sink parks each
// ItemID as uncertain + resendable instead of acking it consumed.
func TestFlushSteerQueueMergesBatchIntoOneCountedNotice(t *testing.T) {
	var notices []event.Event
	a := New(nil, tool.NewRegistry(), NewSession(""), Options{}, event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice && e.Code == event.NoticeCodeUnappliedSteer {
			notices = append(notices, e)
		}
	}))
	a.steerMu.Lock()
	a.steerRunActive = true
	a.steerMu.Unlock()
	if !a.SteerItem("item-1", func() (SteerPayload, error) { return SteerPayload{Text: "first guidance"}, nil }) {
		t.Fatal("first active steer should be accepted")
	}
	if !a.SteerItem("item-2", func() (SteerPayload, error) { return SteerPayload{Text: "second guidance"}, nil }) {
		t.Fatal("second active steer should be accepted")
	}
	a.flushSteerQueue()

	if len(notices) != 3 {
		t.Fatalf("batch flush should emit exactly 2 restore-only signals + 1 merged notice, got %d: %+v", len(notices), notices)
	}
	var userNotices, restoreOnly []event.Event
	for _, n := range notices {
		if n.Detail == unappliedSteerRestoreDetail {
			restoreOnly = append(restoreOnly, n)
			continue
		}
		userNotices = append(userNotices, n)
	}
	if len(userNotices) != 1 || len(restoreOnly) != 2 {
		t.Fatalf("want 1 user notice + 2 restore-only, got %d user / %d restore-only", len(userNotices), len(restoreOnly))
	}
	merged := userNotices[0]
	if merged.ItemID != "" {
		t.Fatalf("merged notice must not carry a single ItemID (restore rides the per-item signals), got %q", merged.ItemID)
	}
	if merged.Level != event.LevelWarn {
		t.Fatalf("merged notice level = %v, want warn", merged.Level)
	}
	for _, want := range []string{"×2", "first guidance", "second guidance", "not applied"} {
		if !strings.Contains(merged.Text, want) {
			t.Fatalf("merged notice text %q missing %q", merged.Text, want)
		}
	}
	byItem := map[string]string{}
	for _, r := range restoreOnly {
		if r.Audience != event.NoticeAudienceOperator {
			t.Fatalf("restore-only notice for %q must be operator-audience (IM bots must stay silent), got %q", r.ItemID, r.Audience)
		}
		byItem[r.ItemID] = r.Text
	}
	if len(byItem) != 2 || byItem["item-1"] == "" || byItem["item-2"] == "" {
		t.Fatalf("restore-only signals must wire both durable ItemIDs, got %+v", byItem)
	}

	// Transcript contract unchanged (#7045): both items archived once each as
	// LocalOnly steer records, none leaked into the model context.
	var persisted []string
	for _, m := range a.Session().Messages {
		if text, ok := SteerText(m.Content); ok {
			persisted = append(persisted, text)
			if !m.LocalOnly {
				t.Fatalf("archived steer %q must stay LocalOnly", text)
			}
		}
	}
	if len(persisted) != 2 {
		t.Fatalf("batch flush should archive both guidances once each, got %v", persisted)
	}
	if !a.HasUnappliedSteer() {
		t.Fatal("batch flush must report unapplied guidance")
	}
}

// TestFlushSteerQueueSingleItemKeepsLegacyNotice pins the zero-regression
// face: a lone unapplied item keeps the pre-tl1303 shape exactly — one
// user-facing notice, no restore-only marker, ItemID wired for restore.
func TestFlushSteerQueueSingleItemKeepsLegacyNotice(t *testing.T) {
	var notices []event.Event
	a := New(nil, tool.NewRegistry(), NewSession(""), Options{}, event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice && e.Code == event.NoticeCodeUnappliedSteer {
			notices = append(notices, e)
		}
	}))
	a.steerMu.Lock()
	a.steerRunActive = true
	a.steerMu.Unlock()
	if !a.SteerItem("item-solo", func() (SteerPayload, error) { return SteerPayload{Text: "solo guidance"}, nil }) {
		t.Fatal("active steer should be accepted")
	}
	a.flushSteerQueue()
	if len(notices) != 1 {
		t.Fatalf("single-item flush should emit exactly one notice, got %d", len(notices))
	}
	n := notices[0]
	if n.ItemID != "item-solo" || n.Detail != "" || n.Audience != event.NoticeAudienceDefault {
		t.Fatalf("single-item notice = %+v, want legacy shape (ItemID wired, no marker, default audience)", n)
	}
	if !strings.Contains(n.Text, "solo guidance") {
		t.Fatalf("single-item notice text %q missing guidance", n.Text)
	}
}

// TestSteerTextSurvivesTurnPreferenceWrapping pins replay: steers are
// persisted through withTurnPreferences, which prepends transient language
// blocks (for Chinese text even in auto mode, and for any text under an
// explicit language) ahead of the steer prefix. SteerText must skip the
// wrapping and return the user's exact original text, or replay degrades the
// steer into a plain user message.
func TestSteerTextSurvivesTurnPreferenceWrapping(t *testing.T) {
	plain := New(nil, nil, NewSession(""), Options{}, event.Discard)
	explicit := New(nil, nil, NewSession(""), Options{}, event.Discard)
	explicit.SetReasoningLanguage("zh")
	explicit.SetResponseLanguage("zh")

	cases := []struct {
		name  string
		agent *Agent
		text  string
	}{
		{"english auto (no blocks)", plain, "use plan B"},
		{"chinese auto (reasoning block)", plain, "请改用方案B"},
		{"explicit zh (both blocks)", explicit, "switch to plan B"},
		{"exact text preserved", plain, "  spaced\ttext  "},
	}
	for _, tc := range cases {
		persisted := tc.agent.withTurnPreferences(midTurnSteerMessage(tc.text))
		got, ok := SteerText(persisted)
		if !ok {
			t.Fatalf("%s: SteerText failed to recognize the persisted steer (head %.80q)", tc.name, persisted)
		}
		if got != tc.text {
			t.Fatalf("%s: SteerText = %q, want %q", tc.name, got, tc.text)
		}
	}

	if _, ok := SteerText(plain.withTurnPreferences("请总结一下这个文件")); ok {
		t.Fatalf("a wrapped ordinary user message must not be detected as a steer")
	}
}

// TestSteerRejectedWithoutActiveTurn proves a steer arriving when no turn is
// running is rejected instead of parked in a queue no loop will consume, so
// the controller can convert it into a regular turn.
func TestSteerRejectedWithoutActiveTurn(t *testing.T) {
	a := New(testutil.NewMock("m", testutil.Turn{Text: "done"}), tool.NewRegistry(), NewSession(""), Options{}, event.Discard)
	if a.Steer("early") {
		t.Fatalf("Steer with no active turn must be rejected")
	}
	if n := a.steerQueueLen(); n != 0 {
		t.Fatalf("rejected steer must not linger in the queue, len=%d", n)
	}
	if err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if a.Steer("between turns") {
		t.Fatalf("Steer between turns must be rejected")
	}
}
