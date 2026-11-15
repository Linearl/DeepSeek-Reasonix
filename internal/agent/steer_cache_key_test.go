package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

func captureSlogForSteerKey(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

// steerQueueAndReturnTool queues a steer mid-turn and returns normally, so
// the run loop's very next gap consumes it — the exact path the tl1608
// observation pair brackets.
type steerQueueAndReturnTool struct {
	agent     *Agent
	steerText string
	queued    bool
}

func (t *steerQueueAndReturnTool) Name() string        { return "queue_steer" }
func (t *steerQueueAndReturnTool) Description() string { return "queues a steer mid-turn and returns" }
func (t *steerQueueAndReturnTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (t *steerQueueAndReturnTool) ReadOnly() bool { return true }
func (t *steerQueueAndReturnTool) Execute(context.Context, json.RawMessage) (string, error) {
	t.queued = t.agent.Steer(t.steerText)
	return "ok", nil
}

// tl1608 wiring pin: a consumed steer emits exactly the before/after
// steer_cache_key pair, and a pure-append injection keeps changed=false —
// the one unavoidable suffix miss is not a prefix rewrite.
func TestSteerCacheKeyLogsBeforeAndAfterInjection(t *testing.T) {
	buf := captureSlogForSteerKey(t)
	mp := testutil.NewMock("m",
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "queue_steer", Arguments: `{}`}}},
		testutil.Turn{Text: "steered answer"},
	)
	queuer := &steerQueueAndReturnTool{steerText: "switch to plan B"}
	reg := tool.NewRegistry()
	reg.Add(queuer)
	a := New(mp, reg, NewSession(""), Options{}, event.FuncSink(func(event.Event) {}))
	queuer.agent = a

	if err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if !queuer.queued {
		t.Fatal("mid-turn steer should be accepted while the turn is active")
	}
	out := buf.String()
	if n := strings.Count(out, "agent: steer_cache_key"); n != 2 {
		t.Fatalf("steer_cache_key lines = %d, want exactly 2 (before+after); slog:%s", n, out)
	}
	if !strings.Contains(out, "phase=before") || !strings.Contains(out, "phase=after") {
		t.Fatalf("steer_cache_key pair missing before/after phases; slog:%s", out)
	}
	if !strings.Contains(out, "changed=false") {
		t.Fatalf("a pure-append steer must keep changed=false (suffix miss only); slog:%s", out)
	}
}

// tl1608 verdict pin: rewriting an already-visible message between the
// probes flips changed=true — that is the "prefix rewrite → provider cache
// fully invalidated → full prefill" shape the observation exists to catch.
func TestSteerCacheKeyProbeDetectsPrefixRewrite(t *testing.T) {
	buf := captureSlogForSteerKey(t)
	a := newBreakerAgent(t, &fakeProvider{reply: "x"}, &recordingSink{})

	before := a.logSteerCacheKeyBefore()
	a.sess.conversation.Replace([]provider.Message{{Role: provider.RoleUser, Content: "rewritten prefix"}})
	a.logSteerCacheKeyAfter(before)

	if !strings.Contains(buf.String(), "changed=true") {
		t.Fatalf("a prefix rewrite must flip changed=true; slog:%s", buf.String())
	}
}

// tl1608 verdict pin: appending after the observed prefix must NOT flip
// changed — prefix-to-prefix comparison, not whole-list, is what makes the
// verdict meaningful.
func TestSteerCacheKeyProbeStaysQuietOnPureAppend(t *testing.T) {
	buf := captureSlogForSteerKey(t)
	a := newBreakerAgent(t, &fakeProvider{reply: "x"}, &recordingSink{})

	before := a.logSteerCacheKeyBefore()
	a.sess.conversation.Add(provider.Message{
		Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "appended steer",
	})
	a.logSteerCacheKeyAfter(before)

	if !strings.Contains(buf.String(), "changed=false") {
		t.Fatalf("a pure append must keep changed=false; slog:%s", buf.String())
	}
}
