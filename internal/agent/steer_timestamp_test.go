package agent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// blockingSteerProbeTool blocks inside Execute so the test can queue a steer
// at a deterministic mid-run point, then release it for the next round's
// consumeSteer injection.
type blockingSteerProbeTool struct {
	agent   *Agent
	release chan struct{}
	once    sync.Once
}

func (b *blockingSteerProbeTool) Name() string        { return "steer_probe" }
func (b *blockingSteerProbeTool) Description() string { return "test probe that parks the tool loop" }
func (b *blockingSteerProbeTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (b *blockingSteerProbeTool) ReadOnly() bool { return true }
func (b *blockingSteerProbeTool) Execute(context.Context, json.RawMessage) (string, error) {
	<-b.release
	return "probe ok", nil
}

// runWithMidTurnSteer parks the tool loop, queues one steer via `queue` while
// the loop is blocked, then releases so the loop's consume point injects it.
// Returns the agent and every event the run emitted, in order.
func runWithMidTurnSteer(t *testing.T, queue func(a *Agent)) (*Agent, []event.Event) {
	t.Helper()
	release := make(chan struct{})
	hijack := &blockingSteerProbeTool{release: release}
	reg := tool.NewRegistry()
	reg.Add(hijack)
	mp := testutil.NewMock("steer-ts-mock",
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "p1", Name: "steer_probe", Arguments: `{}`}}},
		testutil.Turn{Text: "done"},
	)
	events := make(chan event.Event, 64)
	sink := event.FuncSink(func(e event.Event) { events <- e })
	a := New(mp, reg, NewSession(""), Options{}, sink)
	hijack.agent = a

	done := make(chan error, 1)
	go func() { done <- a.Run(context.Background(), "go") }()

	// Queue while the probe tool holds the loop open (steerRunActive=true).
	// Wait for the intake to arm first — queueing before Run flips the flag
	// would bounce the steer and race the assertion.
	deadline := time.Now().Add(5 * time.Second)
	for !a.SteerRunActive() {
		if time.Now().After(deadline) {
			t.Fatal("steer intake never armed; the run loop did not start")
		}
		time.Sleep(2 * time.Millisecond)
	}
	queue(a)
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	close(events)
	drained := make([]event.Event, 0, len(events))
	for e := range events {
		drained = append(drained, e)
	}
	return a, drained
}

// injectedSteerMessage returns the session's consumed mid-turn steer record.
func injectedSteerMessage(t *testing.T, a *Agent, body string) *provider.Message {
	t.Helper()
	for i := range a.Session().Messages {
		m := &a.Session().Messages[i]
		if m.Role != provider.RoleUser {
			continue
		}
		if text, isSteer := SteerText(m.Content); isSteer && text == body {
			return m
		}
	}
	t.Fatalf("consumed steer %q never landed in the session transcript", body)
	return nil
}

// TestConsumedSteerPersistsDurableTime pins tl1611: a durable steer carries
// its enqueue wall clock (payload.At) onto BOTH the injected provider message
// (history replay then shows the real time instead of dropping the field via
// omitempty) and the visible Steer event (tl1152 contract: "UserInput/Steer
// carry the inbox item's CreatedAt").
func TestConsumedSteerPersistsDurableTime(t *testing.T) {
	const enqueueAt = int64(1794751003946) // fixed domain time, not time.Now()
	a, drained := runWithMidTurnSteer(t, func(a *Agent) {
		if !a.SteerItem("item-ts-1", func() (SteerPayload, error) {
			return SteerPayload{Text: "use plan B", At: enqueueAt}, nil
		}) {
			t.Fatal("durable steer should be accepted mid-run")
		}
	})
	injected := injectedSteerMessage(t, a, "use plan B")
	if injected.CreatedAt != enqueueAt {
		t.Fatalf("injected steer CreatedAt = %d, want the durable enqueue time %d", injected.CreatedAt, enqueueAt)
	}
	steerEvents := 0
	for _, e := range drained {
		if e.Kind != event.Steer {
			continue
		}
		steerEvents++
		if e.At != enqueueAt {
			t.Fatalf("Steer event At = %d, want the durable enqueue time %d", e.At, enqueueAt)
		}
	}
	if steerEvents != 1 {
		t.Fatalf("consumed steer should emit exactly one visible Steer event, got %d", steerEvents)
	}
}

// TestConsumedSteerMemoryFaceStampsInjectionTime pins the in-memory face:
// Steer(string) has no durable record, so the injection instant is the floor.
// CreatedAt=0 would reproduce the tl1611 gap (timestamp-less replay rows) for
// exactly this face.
func TestConsumedSteerMemoryFaceStampsInjectionTime(t *testing.T) {
	a, drained := runWithMidTurnSteer(t, func(a *Agent) {
		if !a.Steer("remember to write the board") {
			t.Fatal("memory-face steer should be accepted mid-run")
		}
	})
	injected := injectedSteerMessage(t, a, "remember to write the board")
	if injected.CreatedAt <= 0 {
		t.Fatalf("injected steer CreatedAt = %d, want the injection instant (>0)", injected.CreatedAt)
	}
	steerEvents := 0
	for _, e := range drained {
		if e.Kind != event.Steer {
			continue
		}
		steerEvents++
		if e.At != injected.CreatedAt {
			t.Fatalf("Steer event At = %d, want the persisted CreatedAt %d", e.At, injected.CreatedAt)
		}
	}
	if steerEvents != 1 {
		t.Fatalf("consumed steer should emit exactly one visible Steer event, got %d", steerEvents)
	}
}
