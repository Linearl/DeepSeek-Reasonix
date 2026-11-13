package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1554: the DIRECT tool-call path (parseToolCall → applyArgumentValidation →
// execute) previously had zero sanitization — only the use_capability proxy
// cleaned ids and argument key names (task 975, tl1310). These pins lock the
// direct-path heals and the error-echo cleanups for BOTH pollution forms:
// real control bytes (JSON "\r" decodes to a bare CR) and literal escape
// text (backslash+r as two characters). The negative assertions check that
// no bare CR byte AND no literal `\r` text reaches the model-facing output —
// echoing the polluted form back was the observed self-reinforcement channel
// (2026-11-11 eight consecutive failures).

// recordingStrictTool mirrors the heartbeat_task_upsert shape: strict schema,
// records exactly what it received.
type tl1554StrictTool struct {
	received string
	executed bool
}

func (t *tl1554StrictTool) Name() string        { return "tl1554_upsert" }
func (t *tl1554StrictTool) Description() string { return "strict upsert" }
func (t *tl1554StrictTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"expected_revision":{"type":"integer"}},"required":["name","expected_revision"],"additionalProperties":false}`)
}
func (t *tl1554StrictTool) ReadOnly() bool { return true }
func (t *tl1554StrictTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	t.received = string(args)
	t.executed = true
	return "upserted", nil
}

func newTL1554Agent(target tool.Tool) *Agent {
	reg := tool.NewRegistry()
	reg.Add(target)
	return New(nil, reg, NewSession(""), Options{}, event.Discard)
}

func assertCleanEcho(t *testing.T, label, text string) {
	t.Helper()
	if strings.ContainsRune(text, '\r') {
		t.Fatalf("%s echoes a bare CR byte: %q", label, text)
	}
	if strings.Contains(text, `\r`) {
		t.Fatalf("%s echoes the literal escape form: %q", label, text)
	}
}

// TestParseToolCallHealsPollutedName: the model-generated tool name is healed
// before resolution — the direct-call counterpart of the capability_id heal.
// A healable polluted name resolves and executes; an unresolvable one echoes
// the cleaned form in "unknown tool %q", never the pollution.
func TestParseToolCallHealsPollutedName(t *testing.T) {
	target := &tl1554StrictTool{}
	a := newTL1554Agent(target)

	// Real CR byte: healed, resolves, executes.
	out := a.executeOne(context.Background(), &a.turn, provider.ToolCall{
		Name:      "tl1554\r_upsert",
		Arguments: `{"name":"hb","expected_revision":3}`,
	})
	if !target.executed {
		t.Fatalf("healed name must resolve: output=%q errMsg=%q", out.output, out.errMsg)
	}

	// Literal escape text: same heal.
	target2 := &tl1554StrictTool{}
	a2 := newTL1554Agent(target2)
	out = a2.executeOne(context.Background(), &a2.turn, provider.ToolCall{
		Name:      `tl1554\r_upsert`,
		Arguments: `{"name":"hb","expected_revision":3}`,
	})
	if !target2.executed {
		t.Fatalf("literal-escape name must heal and resolve: output=%q errMsg=%q", out.output, out.errMsg)
	}

	// Unresolvable after heal: echo carries the cleaned form only.
	a3 := newTL1554Agent(&tl1554StrictTool{})
	out = a3.executeOne(context.Background(), &a3.turn, provider.ToolCall{Name: "zzz\rq"})
	assertCleanEcho(t, "unknown-tool output", out.output)
	assertCleanEcho(t, "unknown-tool errMsg", out.errMsg)
	if !strings.Contains(out.output, `"zzzq"`) {
		t.Fatalf("unknown-tool echo must show the cleaned name, got %q", out.output)
	}
}

// TestDirectCallDirtyArgumentKeyDispatches is the direct-path counterpart of
// TestUseCapabilityDirtyArgumentKeyDispatches: polluted key names (both
// forms) are healed at the direct-call choke so the REAL schema gate passes
// and the target receives cleaned keys — previously this shape failed
// validation with the polluted path echoed back.
func TestDirectCallDirtyArgumentKeyDispatches(t *testing.T) {
	tests := []struct {
		name string
		args string
	}{
		{name: "real CR key", args: `{"name":"hb","expected\r_revision":3}`},
		{name: "literal escape key", args: `{"name":"hb","expected\\r_revision":3}`},
	}
	// (Nested-key healing is pinned at the tool choke: TestSanitizeArgumentKeys
	// in usecapability_argument_key_test.go and the literal-form table in
	// internal/tool.)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := &tl1554StrictTool{}
			a := newTL1554Agent(target)
			out := a.executeOne(context.Background(), &a.turn, provider.ToolCall{
				Name:      "tl1554_upsert",
				Arguments: test.args,
			})
			if out.errMsg != "" {
				t.Fatalf("dirty-key direct call must dispatch after the heal, errMsg=%q output=%q", out.errMsg, out.output)
			}
			if !target.executed {
				t.Fatalf("target not executed, output=%q", out.output)
			}
			assertCleanEcho(t, "output", out.output)
			var decoded map[string]json.RawMessage
			if err := json.Unmarshal([]byte(target.received), &decoded); err != nil {
				t.Fatalf("received: %v", err)
			}
			if _, ok := decoded["expected_revision"]; !ok {
				t.Fatalf("key not cleaned in dispatched arguments: %s", target.received)
			}
		})
	}
}

// TestDirectCallCleanKeysStayByteIdentical guards the boundary: a clean
// payload must keep the historical path — no rewrite, dispatch unchanged.
func TestDirectCallCleanKeysStayByteIdentical(t *testing.T) {
	target := &tl1554StrictTool{}
	a := newTL1554Agent(target)
	raw := `{"expected_revision":3,"name":"hb"}`
	out := a.executeOne(context.Background(), &a.turn, provider.ToolCall{
		Name:      "tl1554_upsert",
		Arguments: raw,
	})
	if out.errMsg != "" || !target.executed {
		t.Fatalf("clean call must dispatch unchanged: errMsg=%q", out.errMsg)
	}
	if target.received != raw {
		t.Fatalf("clean payload rewritten: %s", target.received)
	}
}

// TestDescribeActualArgumentsEchoesCleanKeys: the "Received envelope shape" /
// "Actual arguments received" lines render top-level keys verbatim — both
// pollution forms must come out cleaned, never confirmed back.
func TestDescribeActualArgumentsEchoesCleanKeys(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "real CR keys", raw: `{"capability\r_id":"x","expected\r_revision":1}`},
		{name: "literal escape keys", raw: `{"capability\\r_id":"x","expected\\r_revision":1}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := describeActualArguments(json.RawMessage(test.raw))
			assertCleanEcho(t, "actual-arguments echo", got)
			if !strings.Contains(got, "capability_id") || !strings.Contains(got, "expected_revision") {
				t.Fatalf("cleaned key forms missing: %q", got)
			}
		})
	}
	// The string-wrapped variant rides the same cleanup.
	got := describeActualArguments(json.RawMessage(`"{\"capability\\r_id\":\"x\"}"`))
	assertCleanEcho(t, "wrapped echo", got)
	if !strings.Contains(got, "capability_id") {
		t.Fatalf("cleaned key form missing from wrapped echo: %q", got)
	}
}
