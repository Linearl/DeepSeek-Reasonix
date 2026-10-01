package baseproc

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFrameKindClassification(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		want  FrameKind
		valid bool
	}{
		{name: "request", raw: `{"jsonrpc":"2.0","id":7,"method":"base.ping","params":{}}`, want: FrameRequest, valid: true},
		{name: "request string id", raw: `{"jsonrpc":"2.0","id":"abc","method":"base.ping"}`, want: FrameRequest, valid: true},
		{name: "notification", raw: `{"jsonrpc":"2.0","method":"base.dying","params":{"reason":"x"}}`, want: FrameNotification, valid: true},
		{name: "response result", raw: `{"jsonrpc":"2.0","id":7,"result":{"ok":true}}`, want: FrameResponse, valid: true},
		{name: "response error", raw: `{"jsonrpc":"2.0","id":7,"error":{"code":-32601,"message":"no"}}`, want: FrameResponse, valid: true},
		{name: "response null id", raw: `{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"parse"}}`, want: FrameResponse, valid: true},
		{name: "invalid empty", raw: `{"jsonrpc":"2.0"}`, want: FrameInvalid, valid: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var f Frame
			if err := json.Unmarshal([]byte(tt.raw), &f); err != nil {
				t.Fatalf("unmarshal fixture: %v", err)
			}
			if got := f.Kind(); got != tt.want {
				t.Fatalf("Kind() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFrameRoundTripPreservesFields(t *testing.T) {
	original := Frame{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`42`),
		Method:  MethodToolCall,
		Params:  json.RawMessage(`{"call_id":"c1","tool":"bash","args":{"cmd":"ls"}}`),
	}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded Frame
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Kind() != FrameRequest {
		t.Fatalf("kind = %v, want request", decoded.Kind())
	}
	if string(decoded.ID) != "42" {
		t.Fatalf("id = %s, want 42", decoded.ID)
	}
	if decoded.Method != MethodToolCall {
		t.Fatalf("method = %q, want %q", decoded.Method, MethodToolCall)
	}
	var params ToolCallParams
	if err := json.Unmarshal(decoded.Params, &params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if params.CallID != "c1" || params.Tool != "bash" || string(params.Args) != `{"cmd":"ls"}` {
		t.Fatalf("params roundtrip mismatch: %+v", params)
	}
}

func TestRPCErrorFormat(t *testing.T) {
	e := &RPCError{Code: CodeMethodNotFound, Message: `method "x" not found`}
	if got := e.Error(); !strings.Contains(got, "-32601") || !strings.Contains(got, "not found") {
		t.Fatalf("Error() = %q, want code and message present", got)
	}
	var nilErr *RPCError
	if nilErr.Error() != "<nil>" {
		t.Fatalf("nil RPCError Error() = %q", nilErr.Error())
	}
}

func TestNegotiateVersion(t *testing.T) {
	t.Run("client at server version", func(t *testing.T) {
		v, rpcErr := negotiateVersion(1, ProtocolVersion)
		if rpcErr != nil || v != 1 {
			t.Fatalf("negotiate(1) = %d, %v; want 1, nil", v, rpcErr)
		}
	})
	t.Run("older client accepted (v1 only grows)", func(t *testing.T) {
		v, rpcErr := negotiateVersion(1, 2)
		if rpcErr != nil || v != 1 {
			t.Fatalf("negotiate(1, max 2) = %d, %v; want 1, nil", v, rpcErr)
		}
	})
	t.Run("newer client rejected with version mismatch (F3)", func(t *testing.T) {
		v, rpcErr := negotiateVersion(2, ProtocolVersion)
		if rpcErr == nil {
			t.Fatal("negotiate(2, max 1) = nil error, want version mismatch")
		}
		if v != 0 {
			t.Fatalf("version = %d on mismatch, want 0", v)
		}
		if rpcErr.Code != CodeVersionMismatch {
			t.Fatalf("code = %d, want %d", rpcErr.Code, CodeVersionMismatch)
		}
	})
	t.Run("non-positive version invalid", func(t *testing.T) {
		_, rpcErr := negotiateVersion(0, ProtocolVersion)
		if rpcErr == nil || rpcErr.Code != CodeInvalidParams {
			t.Fatalf("negotiate(0) = %v, want invalid params", rpcErr)
		}
	})
}

// TestV1WireTypesFreeze locks the JSON field names of every v1 message against
// silent renames: the design freezes the wire contract (§5 v1 只增不改), so a
// field rename here would break mixed-version fleets during upgrades.
func TestV1WireTypesFreeze(t *testing.T) {
	tests := []struct {
		name string
		msg  any
		want string
	}{
		{"hello params", HelloParams{ProtocolVersion: 1, ClientPID: 4242}, `{"protocol_version":1,"client_pid":4242}`},
		{"hello result", HelloResult{ProtocolVersion: 1, ServerVersion: "v1.2.3", Capabilities: []string{CapTools}}, `{"protocol_version":1,"server_version":"v1.2.3","capabilities":["tools"]}`},
		{"attach params", AttachParams{SessionID: "s1", Root: "/r", WorkspaceScope: "ws"}, `{"session_id":"s1","root":"/r","workspace_scope":"ws"}`},
		{"attach result", AttachResult{LeaseID: "l1"}, `{"lease_id":"l1"}`},
		{"detach params", DetachParams{LeaseID: "l1"}, `{"lease_id":"l1"}`},
		{"detach result", DetachResult{OK: true}, `{"ok":true}`},
		{"ping result", PingResult{OK: true}, `{"ok":true}`},
		{"shutdown result", ShutdownResult{OK: true}, `{"ok":true}`},
		{"catalog params", ToolCatalogParams{Scope: "all"}, `{"scope":"all"}`},
		{"catalog result", ToolCatalogResult{Tools: []ToolDescriptor{{Name: "bash", Description: "d", Schema: json.RawMessage(`{"type":"object"}`), ReadOnly: false}}}, `{"tools":[{"name":"bash","description":"d","schema":{"type":"object"}}]}`},
		{"tool call params", ToolCallParams{CallID: "c1", Tool: "bash", Args: json.RawMessage(`{}`), SessionID: "s1"}, `{"call_id":"c1","tool":"bash","args":{},"session_id":"s1"}`},
		{"tool call result", ToolCallResult{Content: "out", Err: "boom"}, `{"content":"out","error":"boom"}`},
		{"provider resolve params", ProviderResolveParams{ModelRef: "gpt-5", Effort: "high"}, `{"model_ref":"gpt-5","effort":"high"}`},
		{"provider resolve result", ProviderResolveResult{Available: true, Provider: "openai", ModelRef: "gpt-5"}, `{"available":true,"provider":"openai","model_ref":"gpt-5"}`},
		{"tool progress", ToolProgressParams{CallID: "c1", Chunk: "line"}, `{"call_id":"c1","chunk":"line"}`},
		{"catalog changed", CatalogChangedParams{Reason: "plugin"}, `{"reason":"plugin"}`},
		{"dying", DyingParams{Reason: "parent lost"}, `{"reason":"parent lost"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := json.Marshal(tt.msg)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(encoded) != tt.want {
				t.Fatalf("wire shape changed:\n got %s\nwant %s", encoded, tt.want)
			}
		})
	}
}

func TestMethodAndCapabilityConstants(t *testing.T) {
	// The method strings are the contract (design §5 table); a rename is a
	// protocol break, so they are locked here.
	wantMethods := map[string]string{
		MethodHello:           "base.hello",
		MethodAttach:          "base.attach",
		MethodDetach:          "base.detach",
		MethodToolCatalog:     "base.toolCatalog",
		MethodToolCall:        "base.toolCall",
		MethodProviderResolve: "base.providerResolve",
		MethodPing:            "base.ping",
		MethodShutdown:        "base.shutdown",
	}
	for got, want := range wantMethods {
		if got != want {
			t.Fatalf("method constant drifted: got %q, want %q", got, want)
		}
	}
	wantNotifications := map[string]string{
		NotifyToolProgress:   "base.toolProgress",
		NotifyCatalogChanged: "base.catalogChanged",
		NotifyDying:          "base.dying",
	}
	for got, want := range wantNotifications {
		if got != want {
			t.Fatalf("notification constant drifted: got %q, want %q", got, want)
		}
	}
	if ProtocolVersion != 1 {
		t.Fatalf("ProtocolVersion = %d, want 1 (S1a ships v1)", ProtocolVersion)
	}
	wantCaps := map[string]string{
		CapTools:           "tools",
		CapSessions:        "sessions",
		CapProviderResolve: "provider_resolve",
	}
	for got, want := range wantCaps {
		if got != want {
			t.Fatalf("capability constant drifted: got %q, want %q", got, want)
		}
	}
}
