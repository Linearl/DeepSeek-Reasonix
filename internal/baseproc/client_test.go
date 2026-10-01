package baseproc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"
)

func TestInlineBaseClientIdentityAndHealth(t *testing.T) {
	// R1：inline 是默认路径而非异常路径——握手与健康检查在进程内直接成立。
	c := InlineBaseClient{ServerVersion: "v-test"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if c.Mode() != ModeInline {
		t.Fatalf("Mode = %q, want inline", c.Mode())
	}
	hello, err := c.Hello(ctx, HelloParams{ProtocolVersion: ProtocolVersion, ClientPID: 1})
	if err != nil {
		t.Fatalf("hello: %v", err)
	}
	if hello.ProtocolVersion != ProtocolVersion || hello.ServerVersion != "v-test" {
		t.Fatalf("hello = %+v, want version %d from v-test", hello, ProtocolVersion)
	}
	if len(hello.Capabilities) != 0 {
		t.Fatalf("inline capabilities = %v, want none in S1a", hello.Capabilities)
	}
	if err := c.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if err := c.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestInlineBaseClientRejectsNewerProtocol(t *testing.T) {
	c := InlineBaseClient{ServerVersion: "v-test"}
	_, err := c.Hello(context.Background(), HelloParams{ProtocolVersion: ProtocolVersion + 1, ClientPID: 1})
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeVersionMismatch {
		t.Fatalf("hello err = %v, want version mismatch", err)
	}
}

func TestInlineBaseClientToolSurfaceNotWired(t *testing.T) {
	// S1a 骨架边界：工具面/会话面在双实现上都返回 ErrNotWired，S1b 填充。
	c := InlineBaseClient{ServerVersion: "v-test"}
	ctx := context.Background()

	if _, err := c.Attach(ctx, AttachParams{SessionID: "s"}); !errors.Is(err, ErrNotWired) {
		t.Fatalf("attach err = %v, want ErrNotWired", err)
	}
	if _, err := c.Detach(ctx, DetachParams{LeaseID: "l"}); !errors.Is(err, ErrNotWired) {
		t.Fatalf("detach err = %v, want ErrNotWired", err)
	}
	if _, err := c.ToolCatalog(ctx, ToolCatalogParams{}); !errors.Is(err, ErrNotWired) {
		t.Fatalf("toolCatalog err = %v, want ErrNotWired", err)
	}
	if _, err := c.ToolCall(ctx, ToolCallParams{CallID: "c", Tool: "bash"}); !errors.Is(err, ErrNotWired) {
		t.Fatalf("toolCall err = %v, want ErrNotWired", err)
	}
	if _, err := c.ProviderResolve(ctx, ProviderResolveParams{ModelRef: "m"}); !errors.Is(err, ErrNotWired) {
		t.Fatalf("providerResolve err = %v, want ErrNotWired", err)
	}
}

func TestRemoteBaseClientHandshakeAndShutdown(t *testing.T) {
	client, _, done := startTestServer(t, nil)
	c := newRemoteClient(newConn(client, nil), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	hello, err := c.Hello(ctx, HelloParams{ProtocolVersion: ProtocolVersion, ClientPID: 4321})
	if err != nil {
		t.Fatalf("hello: %v", err)
	}
	if c.Mode() != ModeRemote {
		t.Fatalf("Mode = %q, want remote", c.Mode())
	}
	if c.Handshake().ServerVersion != hello.ServerVersion {
		t.Fatal("recorded handshake differs from returned hello")
	}
	if err := c.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if err := c.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("server Serve did not exit after remote shutdown")
	}
}

func TestRemoteBaseClientHandshakeMalformedNegotiation(t *testing.T) {
	// 服务器协商出高于请求的版本 = 对端失格，按握手失败处理（F3 触发条件）。
	// 用 stub 服务器在线路上真的回一个 protocol_version:9。
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	raw := &pipedRW{r: clientIn, w: clientOut}
	t.Cleanup(func() {
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = serverIn.Close()
		_ = serverOut.Close()
	})
	go func() {
		for {
			payload, err := ReadFrame(serverIn)
			if err != nil {
				return
			}
			var f Frame
			if json.Unmarshal(payload, &f) != nil || f.Method != MethodHello {
				continue
			}
			resp, err := json.Marshal(Frame{JSONRPC: JSONRPCVersion, ID: f.ID,
				Result: json.RawMessage(`{"protocol_version":9,"server_version":"evil"}`)})
			if err != nil {
				return
			}
			if WriteFrame(serverOut, resp) != nil {
				return
			}
		}
	}()

	c := newRemoteClient(newConn(raw, nil), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := c.Hello(ctx, HelloParams{ProtocolVersion: ProtocolVersion, ClientPID: 1})
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeVersionMismatch {
		t.Fatalf("malformed negotiation err = %v, want CodeVersionMismatch", err)
	}
}

func TestRemoteBaseClientToolSurfaceCapabilityGated(t *testing.T) {
	// 无能力应答 → 本地 ErrNotWired（不发注定失败的往返）。
	client, _, _ := startTestServer(t, nil)
	c := newRemoteClient(newConn(client, nil), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := c.Hello(ctx, HelloParams{ProtocolVersion: ProtocolVersion, ClientPID: 1}); err != nil {
		t.Fatalf("hello: %v", err)
	}
	if _, err := c.ToolCatalog(ctx, ToolCatalogParams{}); !errors.Is(err, ErrNotWired) {
		t.Fatalf("toolCatalog err = %v, want ErrNotWired (no tools capability)", err)
	}
	if _, err := c.ToolCall(ctx, ToolCallParams{CallID: "c", Tool: "bash"}); !errors.Is(err, ErrNotWired) {
		t.Fatalf("toolCall err = %v, want ErrNotWired", err)
	}
	if _, err := c.Attach(ctx, AttachParams{SessionID: "s"}); !errors.Is(err, ErrNotWired) {
		t.Fatalf("attach err = %v, want ErrNotWired", err)
	}
	if _, err := c.ProviderResolve(ctx, ProviderResolveParams{ModelRef: "m"}); !errors.Is(err, ErrNotWired) {
		t.Fatalf("providerResolve err = %v, want ErrNotWired", err)
	}
}

func TestRemoteBaseClientToolCatalogWithCapability(t *testing.T) {
	// 能力应答后 toolCatalog 真实往返，双实现共享同一 ToolDescriptor 结构（R2）。
	client, _, _ := startTestServer(t, func(s *Server) {
		s.AddCapabilities(CapTools)
		s.Register(MethodToolCatalog, func(ctx context.Context, params json.RawMessage) (any, error) {
			return ToolCatalogResult{Tools: []ToolDescriptor{{
				Name:        "bash",
				Description: "run a shell command",
				Schema:      json.RawMessage(`{"type":"object"}`),
			}}}, nil
		})
	})
	c := newRemoteClient(newConn(client, nil), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := c.Hello(ctx, HelloParams{ProtocolVersion: ProtocolVersion, ClientPID: 1}); err != nil {
		t.Fatalf("hello: %v", err)
	}
	res, err := c.ToolCatalog(ctx, ToolCatalogParams{})
	if err != nil {
		t.Fatalf("toolCatalog: %v", err)
	}
	if len(res.Tools) != 1 || res.Tools[0].Name != "bash" {
		t.Fatalf("tools = %+v, want one bash descriptor", res.Tools)
	}
}
