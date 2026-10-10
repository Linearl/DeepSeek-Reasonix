package agent

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/subagentmailbox"
	"reasonix/internal/tool"
)

// blockingProbeTool blocks inside Execute until released, holding the
// subagent's tool loop open so the test can deliver a mailbox message at a
// deterministic point (mid-run, between rounds).
type blockingProbeTool struct {
	release chan struct{}
	once    sync.Once
}

func (b *blockingProbeTool) Name() string        { return "probe" }
func (b *blockingProbeTool) Description() string { return "test probe" }
func (b *blockingProbeTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (b *blockingProbeTool) ReadOnly() bool { return true }
func (b *blockingProbeTool) Execute(context.Context, json.RawMessage) (string, error) {
	<-b.release
	return "probe ok", nil
}

func TestRunSubAgentWithSessionPublishesHandleConsumesMailboxAndUnpublishes(t *testing.T) {
	ref := "sa_20261008_121500_000000000_feedface"
	hub := &subagentmailbox.Hub{Dir: t.TempDir()}
	mb := hub.MailboxFor(ref)

	release := make(chan struct{})
	reg := tool.NewRegistry()
	reg.Add(&blockingProbeTool{release: release})

	mp := testutil.NewMock("mailbox-mock",
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "p1", Name: "probe", Arguments: `{}`}}},
		testutil.Turn{Text: "finished with guidance"},
	)
	sess := NewSession("sys")

	// Matrix 2 also demands the visible Steer event on the sink chain.
	steerSeen := make(chan string, 1)
	sink := event.FuncSink(func(e event.Event) {
		if e.Kind == event.Steer {
			select {
			case steerSeen <- e.Text:
			default:
			}
		}
	})

	done := make(chan error, 1)
	go func() {
		_, err := RunSubAgentWithSession(context.Background(), mp, reg, sess, "run the probe", Options{
			MaxSteps:        4,
			SubagentDepth:   1,
			SubagentMailbox: mb,
		}, sink)
		done <- err
	}()

	// Wait for the published handle (set before the run loop starts).
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := subagentmailbox.GlobalRegistry.Lookup(ref); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("steer handle was never published for the running subagent")
		}
		time.Sleep(2 * time.Millisecond)
	}

	// Deliver while the run is alive: persist first, then steer.
	receipt, err := hub.Deliver(ref, subagentmailbox.FromUser, "", "Prefer reading the file before writing.")
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if receipt.Disposition != subagentmailbox.DispositionSteered {
		t.Fatalf("disposition = %q, want steered", receipt.Disposition)
	}
	// Unblock the tool; the next round's consumeSteer lands the message in
	// the following provider request.
	close(release)

	if err := <-done; err != nil {
		t.Fatalf("subagent run failed: %v", err)
	}

	// The visible Steer event rode the sink (matrix 2: live visibility path).
	select {
	case text := <-steerSeen:
		if !strings.Contains(text, "Prefer reading the file before writing.") {
			t.Fatalf("Steer event text = %q", text)
		}
	case <-time.After(time.Second):
		t.Fatal("no event.Steer emitted for the consumed mailbox message")
	}

	// The handle is gone once the run returns (defer unpublish, panic-safe).
	if _, ok := subagentmailbox.GlobalRegistry.Lookup(ref); ok {
		t.Fatal("steer handle survived the run end")
	}
	// The consumed message left the mailbox (loader marks delivered).
	if got := mb.PendingCount(); got != 0 {
		t.Fatalf("pending mailbox entries = %d, want 0", got)
	}
	// Some provider request carried the persisted message as mid-turn steer
	// guidance — the acceptance "the subagent consumes it and executes".
	found := false
	for _, req := range mp.Requests() {
		for _, m := range req.Messages {
			if strings.Contains(m.Content, MidTurnSteerPrefix) && strings.Contains(m.Content, "Prefer reading the file before writing.") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("no provider request contained the delivered mailbox message as mid-turn steer")
	}
}

func TestRunSubAgentWithoutMailboxPublishesNothing(t *testing.T) {
	ref := "sa_20261008_121600_000000000_feedface"
	mp := testutil.NewMock("plain-mock", testutil.Turn{Text: "done"})
	sess := NewSession("sys")
	if _, err := RunSubAgentWithSession(context.Background(), mp, tool.NewRegistry(), sess, "plain run", Options{
		MaxSteps:      2,
		SubagentDepth: 1,
	}, event.Discard); err != nil {
		t.Fatalf("plain run failed: %v", err)
	}
	if _, ok := subagentmailbox.GlobalRegistry.Lookup(ref); ok {
		t.Fatal("handle published without a mailbox")
	}
}

func TestSubagentRegistriesNeverInheritSendMessage(t *testing.T) {
	parent := tool.NewRegistry()
	parent.Add(subagentRegistryTool{name: "send_message"})
	parent.Add(subagentRegistryTool{name: "read_file", readOnly: true})
	for _, tc := range []struct {
		name string
		reg  *tool.Registry
	}{
		{"writer depth1", SubagentToolRegistryForDepth(parent, nil, 1, 2)},
		{"writer depth-capped", SubagentToolRegistryForDepth(parent, nil, 2, 2)},
		{"read-only depth1", ReadOnlySubagentToolRegistryForDepth(parent, nil, 1, 2)},
		{"read-only depth-capped", ReadOnlySubagentToolRegistryForDepth(parent, nil, 2, 2)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := tc.reg.Get("send_message"); ok {
				t.Fatalf("subagent registry inherited send_message: %v", tc.reg.Names())
			}
			if _, ok := tc.reg.Get("read_file"); !ok {
				t.Fatalf("subagent registry lost read_file: %v", tc.reg.Names())
			}
		})
	}
	if !slices.Contains(AlwaysHiddenSubagentTools(), "send_message") {
		t.Fatal("AlwaysHiddenSubagentTools does not list send_message (UI tool pickers would offer it)")
	}
}

func TestBoundarySummaryMentionsParentOnlyMessaging(t *testing.T) {
	// The clause is provider-visible (task tool description + tools schema),
	// so its presence is pinned here and its size is budget-checked
	// separately (see the 616 slice report).
	if !strings.Contains(subagentToolBoundarySummary, "send_message") ||
		!strings.Contains(subagentToolBoundarySummary, "never inherited") {
		t.Fatalf("boundary summary missing the send_message clause: %s", subagentToolBoundarySummary)
	}
}

func TestSendMessageToolDeliversThreeStates(t *testing.T) {
	hub := &subagentmailbox.Hub{Dir: t.TempDir()}
	t.Run("parked without a running handle", func(t *testing.T) {
		ref := "sa_20261008_121700_000000000_aaaaaaaa"
		toolOut, err := NewSendMessageTool(hub).Execute(context.Background(), json.RawMessage(
			`{"to":"`+ref+`","summary":"change plan","message":"Stop after the first file."}`))
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if !strings.Contains(toolOut, "parked") || !strings.Contains(toolOut, "continue_from="+ref) {
			t.Fatalf("parked receipt = %q", toolOut)
		}
		if hub.PendingCount(ref) != 1 {
			t.Fatalf("parked message not pending")
		}
	})
	t.Run("steered through a live handle", func(t *testing.T) {
		ref := "sa_20261008_121700_000000000_bbbbbbbb"
		consumed := ""
		un := subagentmailbox.GlobalRegistry.Publish(ref, func(itemID string, load func() (string, error)) bool {
			text, err := load()
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			consumed = text
			return true
		})
		defer un()
		toolOut, err := NewSendMessageTool(hub).Execute(context.Background(), json.RawMessage(
			`{"to":"`+ref+`","message":"Focus on the parser."}`))
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if !strings.Contains(toolOut, "steered") {
			t.Fatalf("steered receipt = %q", toolOut)
		}
		if consumed != "Focus on the parser." {
			t.Fatalf("consume text = %q", consumed)
		}
	})
	t.Run("queued when admission loses the terminal race", func(t *testing.T) {
		ref := "sa_20261008_121700_000000000_cccccccc"
		un := subagentmailbox.GlobalRegistry.Publish(ref, func(itemID string, load func() (string, error)) bool {
			return false
		})
		defer un()
		toolOut, err := NewSendMessageTool(hub).Execute(context.Background(), json.RawMessage(
			`{"to":"`+ref+`","message":"Late message."}`))
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if !strings.Contains(toolOut, "queued") {
			t.Fatalf("queued receipt = %q", toolOut)
		}
		if hub.PendingCount(ref) != 1 {
			t.Fatalf("queued message must stay pending on disk")
		}
	})
}

func TestSendMessageToolValidationAndDisabledChannel(t *testing.T) {
	hub := &subagentmailbox.Hub{Dir: t.TempDir()}
	sender := NewSendMessageTool(hub)
	if _, err := sender.Execute(context.Background(), json.RawMessage(`{"message":"x"}`)); err == nil {
		t.Fatal("missing to accepted")
	}
	if _, err := sender.Execute(context.Background(), json.RawMessage(`{"to":"sa_20261008_121800_000000000_dddddddd"}`)); err == nil {
		t.Fatal("missing message accepted")
	}
	if _, err := sender.Execute(context.Background(), json.RawMessage(`{"to":"../escape","message":"x"}`)); err == nil {
		t.Fatal("traversal ref accepted")
	}
	if _, err := sender.Execute(context.Background(), json.RawMessage(`{"to":"sa_20261008_121800_000000000_dddddddd","message":"x","bogus":1}`)); err == nil {
		t.Fatal("unknown field accepted")
	}
	// A nil hub (switch off) refuses instead of silently parking.
	_, err := NewSendMessageTool(nil).Execute(context.Background(), json.RawMessage(
		`{"to":"sa_20261008_121800_000000000_dddddddd","message":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "experimental_subagent_messaging") {
		t.Fatalf("nil-hub send error = %v, want the disabled-channel error", err)
	}
}
