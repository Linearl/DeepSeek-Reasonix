package baseproc

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

// TestToolCatalogInlineRemoteParity is the S1b对照测试 for toolCatalog:
// the same registry content behind an InlineBaseClient (switch off = current
// path) and behind an attached server over the framed channel (switch on =
// base.toolCatalog over IPC) must answer byte-identically, scope for scope.
func TestToolCatalogInlineRemoteParity(t *testing.T) {
	tools := []*stubTool{
		{name: "alpha", desc: "first", readOnly: true, schema: json.RawMessage(`{"type":"object","properties":{}}`)},
		{name: "beta", desc: "second"},
	}
	inlineReg := newStubRegistry(tools...)
	remoteReg := newStubRegistry(tools...) // same content, independent instances
	inlineReg.SetProviderVisibleTools([]string{"alpha"})
	remoteReg.SetProviderVisibleTools([]string{"alpha"})

	inline := InlineBaseClient{ServerVersion: "v-test", Surface: &RegistrySurface{Reg: inlineReg}}
	client, _, _ := startTestServer(t, func(s *Server) {
		s.AttachToolSurface(&RegistrySurface{Reg: remoteReg})
	})
	remote := newRemoteClient(newConn(client, nil), nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hello, err := remote.Hello(ctx, HelloParams{ProtocolVersion: ProtocolVersion, ClientPID: 1})
	if err != nil {
		t.Fatalf("hello: %v", err)
	}
	if !containsCapability(hello.Capabilities, CapTools) {
		t.Fatalf("capabilities = %v, want %q advertised after AttachToolSurface", hello.Capabilities, CapTools)
	}

	for _, scope := range []string{ScopeAll, ScopeProvider} {
		want, err := inline.ToolCatalog(ctx, ToolCatalogParams{Scope: scope})
		if err != nil {
			t.Fatalf("inline catalog %q: %v", scope, err)
		}
		got, err := remote.ToolCatalog(ctx, ToolCatalogParams{Scope: scope})
		if err != nil {
			t.Fatalf("remote catalog %q: %v", scope, err)
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("catalog %q diverges:\n inline = %+v\n remote = %+v", scope, want, got)
		}
	}
	if len(mustCatalog(t, ctx, remote, ScopeAll).Tools) != 2 {
		t.Fatal("remote catalog lost tools")
	}
}

// TestToolCatalogUnknownScopeRejectedOverIPC pins the protocol negative path:
// an unknown scope answers -32602 and the channel stays usable.
func TestToolCatalogUnknownScopeRejectedOverIPC(t *testing.T) {
	client, _, _ := startTestServer(t, func(s *Server) {
		s.AttachToolSurface(&RegistrySurface{Reg: newStubRegistry(&stubTool{name: "alpha"})})
	})
	c := newConn(client, nil)
	defer c.close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var res ToolCatalogResult
	err := c.call(ctx, MethodToolCatalog, ToolCatalogParams{Scope: "everything"}, &res)
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeInvalidParams {
		t.Fatalf("toolCatalog unknown scope err = %v, want -32602", err)
	}
	// Contract discipline: the connection survives a rejected request.
	var pong PingResult
	if err := c.call(ctx, MethodPing, nil, &pong); err != nil {
		t.Fatalf("ping after rejection: %v", err)
	}
}

// TestToolCatalogWithoutSurfaceKeepsCoreBehaviour pins the S1a baseline for a
// serve process that has not built a tool surface: no capability advertised,
// the method answers -32601, the connection stays usable (design §5).
func TestToolCatalogWithoutSurfaceKeepsCoreBehaviour(t *testing.T) {
	client, _, _ := startTestServer(t, nil)
	c := newConn(client, nil)
	defer c.close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var hello HelloResult
	if err := c.call(ctx, MethodHello, HelloParams{ProtocolVersion: ProtocolVersion, ClientPID: 1}, &hello); err != nil {
		t.Fatalf("hello: %v", err)
	}
	if containsCapability(hello.Capabilities, CapTools) {
		t.Fatalf("capabilities = %v, want no tools capability without a surface", hello.Capabilities)
	}
	var res ToolCatalogResult
	err := c.call(ctx, MethodToolCatalog, ToolCatalogParams{Scope: ScopeAll}, &res)
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeMethodNotFound {
		t.Fatalf("toolCatalog err = %v, want -32601", err)
	}
	var pong PingResult
	if err := c.call(ctx, MethodPing, nil, &pong); err != nil {
		t.Fatalf("ping after -32601: %v", err)
	}
}

// TestInlineClientWithoutSurfaceStaysNotWired freezes the S1a-era inline
// semantics: no surface attached → ErrNotWired, and Hello reports no tools
// capability (so capability gating behaves identically on both paths).
func TestInlineClientWithoutSurfaceStaysNotWired(t *testing.T) {
	c := InlineBaseClient{ServerVersion: "v-test"}
	ctx := context.Background()
	if _, err := c.ToolCatalog(ctx, ToolCatalogParams{Scope: ScopeAll}); !errors.Is(err, ErrNotWired) {
		t.Fatalf("toolCatalog err = %v, want ErrNotWired", err)
	}
	hello, err := c.Hello(ctx, HelloParams{ProtocolVersion: ProtocolVersion, ClientPID: 1})
	if err != nil {
		t.Fatalf("hello: %v", err)
	}
	if containsCapability(hello.Capabilities, CapTools) {
		t.Fatalf("capabilities = %v, want none without a surface", hello.Capabilities)
	}
}

func mustCatalog(t *testing.T, ctx context.Context, c BaseClient, scope string) ToolCatalogResult {
	t.Helper()
	res, err := c.ToolCatalog(ctx, ToolCatalogParams{Scope: scope})
	if err != nil {
		t.Fatalf("toolCatalog %q: %v", scope, err)
	}
	return res
}

func containsCapability(caps []string, want string) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}
