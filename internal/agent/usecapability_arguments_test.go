package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/capability"
)

func TestNormalizeMCPToolArguments(t *testing.T) {
	tests := []struct {
		name    string
		raw     json.RawMessage
		want    string
		wantErr bool
	}{
		{name: "object", raw: json.RawMessage(`{"filesToRebuild":["a.go"]}`), want: `{"filesToRebuild":["a.go"]}`},
		{name: "string object", raw: json.RawMessage(`"{\"filesToRebuild\":[\"a.go\"]}"`), wantErr: true},
		{name: "missing", want: `{}`},
		{name: "null", raw: json.RawMessage(`null`), want: `{}`},
		{name: "array", raw: json.RawMessage(`[]`), wantErr: true},
		{name: "number", raw: json.RawMessage(`1`), wantErr: true},
		{name: "boolean", raw: json.RawMessage(`true`), wantErr: true},
		{name: "bad string", raw: json.RawMessage(`"{"`), wantErr: true},
		{name: "nested string", raw: json.RawMessage(`"\"{}\""`), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeMCPToolArguments(test.raw)
			if test.wantErr {
				if err == nil {
					t.Fatalf("normalize(%s) succeeded with %s", test.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalize(%s): %v", test.raw, err)
			}
			if string(got) != test.want {
				t.Fatalf("normalize(%s) = %s, want %s", test.raw, got, test.want)
			}
		})
	}
}

func TestUseCapabilitySelfHealsStringWrappedMCPArguments(t *testing.T) {
	// Task 457: a JSON-string arguments value whose content is a plain
	// target-arguments object now parses instead of hard-failing before
	// resolution (the old rejection was the fourth format variant). The
	// target here is unavailable, so the observable effect is the audit
	// counter plus the unwrapped arguments reaching resolution.
	audit := &capability.Audit{}
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, nil, nil, audit, nil)

	resolved, err := proxy.ResolveCall(t.Context(), json.RawMessage(`{
			"action":"call",
			"capability_id":"mcp-tool:missing/tool",
			"arguments":"{\"value\":1}"
		}`))
	if err != nil {
		t.Fatalf("ResolveCall rejected a JSON-string-wrapped object: %v", err)
	}
	if string(resolved.Args) != `{"value":1}` {
		t.Fatalf("Args = %s, want the parsed object", resolved.Args)
	}
	if got := audit.Snapshot().Arguments.SelfHealed; got != 1 {
		t.Fatalf("SelfHealed = %d, want 1", got)
	}
}

func TestUseCapabilityStringifiedNonJSONMCPArgumentsStillFailsWithSnapshot(t *testing.T) {
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, nil, nil, nil, nil)
	_, err := proxy.ResolveCall(t.Context(), json.RawMessage(`{
			"action":"call",
			"capability_id":"mcp-tool:missing/tool",
			"arguments":"not json at all"
		}`))
	if err == nil {
		t.Fatal("ResolveCall accepted a non-JSON string arguments value")
	}
	for _, want := range []string{
		"Actual arguments received: a JSON string, not an object",
		`Expected shape: {"action":"call","capability_id":"mcp-tool:<server>/<tool>"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q:\n%s", want, err.Error())
		}
	}
}
