package baseproc

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sync"
	"testing"
	"time"
)

// startTestServer runs a Server against an in-memory duplex pipe and returns
// the raw client end. Tests that want request/response semantics wrap it in
// newConn; tests that poke raw framing use the end directly.
func startTestServer(t *testing.T, prepare func(s *Server)) (io.ReadWriteCloser, *Server, <-chan error) {
	t.Helper()
	serverIn, clientOut := io.Pipe() // server reads what the client writes
	clientIn, serverOut := io.Pipe() // client reads what the server writes
	client := &pipedRW{r: clientIn, w: clientOut}

	s := NewServer("test-server")
	if prepare != nil {
		prepare(s)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	served := make(chan struct{})
	go func() {
		err := s.Serve(ctx, serverIn, serverOut)
		close(served)
		done <- err
	}()
	t.Cleanup(func() {
		cancel()
		_ = client.Close()
		// Unblock the server's reader on both pipe ends.
		_ = serverIn.Close()
		_ = serverOut.Close()
		select {
		case <-served:
		case <-time.After(5 * time.Second):
			t.Log("server Serve did not exit within 5s during cleanup")
		}
	})
	return client, s, done
}

// pipedRW joins one read end and one write end into a ReadWriteCloser.
type pipedRW struct {
	r io.ReadCloser
	w io.WriteCloser
}

func (p *pipedRW) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *pipedRW) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pipedRW) Close() error                { return errors.Join(p.r.Close(), p.w.Close()) }

func TestServeHelloHandshake(t *testing.T) {
	// 设计 §10 S1a 验收：开关 on 协议握手过。
	client, _, _ := startTestServer(t, func(s *Server) { s.AddCapabilities(CapTools) })
	c := newConn(client, nil)
	defer c.close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var result HelloResult
	if err := c.call(ctx, MethodHello, HelloParams{ProtocolVersion: ProtocolVersion, ClientPID: 1234}, &result); err != nil {
		t.Fatalf("hello handshake: %v", err)
	}
	if result.ProtocolVersion != ProtocolVersion {
		t.Fatalf("negotiated version = %d, want %d", result.ProtocolVersion, ProtocolVersion)
	}
	if result.ServerVersion != "test-server" {
		t.Fatalf("server version = %q, want test-server", result.ServerVersion)
	}
	if !reflect.DeepEqual(result.Capabilities, []string{CapTools}) {
		t.Fatalf("capabilities = %v, want [tools]", result.Capabilities)
	}
}

func TestServeHelloVersionMismatch(t *testing.T) {
	// F3：协议协商失败 → 明确的 VersionMismatch 错误（客户端据此 fallback inline）。
	client, _, _ := startTestServer(t, nil)
	c := newConn(client, nil)
	defer c.close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := c.call(ctx, MethodHello, HelloParams{ProtocolVersion: ProtocolVersion + 1, ClientPID: 1}, nil)
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("hello with newer version err = %v, want *RPCError", err)
	}
	if rpcErr.Code != CodeVersionMismatch {
		t.Fatalf("code = %d, want %d (F3 mismatch)", rpcErr.Code, CodeVersionMismatch)
	}
}

func TestServeUnknownMethodKeepsConnection(t *testing.T) {
	// 设计 §5：未知方法返回 -32601 而非断连（S2 前向兼容的前提）。
	client, _, _ := startTestServer(t, nil)
	c := newConn(client, nil)
	defer c.close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := c.call(ctx, "base.doesNotExist", nil, nil)
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeMethodNotFound {
		t.Fatalf("err = %v, want -32601 method not found", err)
	}

	// 同一条连接随后仍能应答 ping：未知方法不断连。
	if err := c.call(ctx, MethodPing, nil, nil); err != nil {
		t.Fatalf("ping after unknown method: %v", err)
	}
}

func TestServePingAndShutdown(t *testing.T) {
	client, s, done := startTestServer(t, nil)
	c := newConn(client, nil)
	defer c.close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var pong PingResult
	if err := c.call(ctx, MethodPing, nil, &pong); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if !pong.OK {
		t.Fatal("ping result ok = false")
	}
	if s.ShutdownRequested() {
		t.Fatal("shutdown requested before base.shutdown")
	}

	var res ShutdownResult
	if err := c.call(ctx, MethodShutdown, nil, &res); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if !res.OK {
		t.Fatal("shutdown result ok = false")
	}
	if !s.ShutdownRequested() {
		t.Fatal("ShutdownRequested = false after base.shutdown")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v after shutdown, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return within 5s of base.shutdown")
	}
}

func TestServeRefusesRequestsAfterShutdownAccepted(t *testing.T) {
	client, _, _ := startTestServer(t, nil)
	c := newConn(client, nil)
	defer c.close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.call(ctx, MethodShutdown, nil, nil); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	err := c.call(ctx, MethodPing, nil, nil)
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeShuttingDown {
		t.Fatalf("ping after shutdown err = %v, want CodeShuttingDown", err)
	}
}

func TestServeParseErrorKeepsReading(t *testing.T) {
	// 帧体不是 JSON → -32700（ID null），流继续可用（长度前缀完好）。
	client, _, _ := startTestServer(t, nil)
	if err := WriteFrame(client, []byte(`{not-json`)); err != nil {
		t.Fatalf("write garbage frame: %v", err)
	}
	payload, err := ReadFrame(client)
	if err != nil {
		t.Fatalf("read parse-error response: %v", err)
	}
	var f Frame
	if err := json.Unmarshal(payload, &f); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if f.Error == nil || f.Error.Code != CodeParseError {
		t.Fatalf("response = %+v, want parse error", f)
	}
	if string(f.ID) != "null" {
		t.Fatalf("parse error id = %s, want null", f.ID)
	}

	// 流仍然健康：常规 ping 通过。
	c := newConn(client, nil)
	defer c.close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.call(ctx, MethodPing, nil, nil); err != nil {
		t.Fatalf("ping after parse error: %v", err)
	}
}

func TestServeInvalidFrameKind(t *testing.T) {
	client, _, _ := startTestServer(t, nil)
	if err := WriteFrame(client, []byte(`{"jsonrpc":"2.0"}`)); err != nil {
		t.Fatalf("write empty frame: %v", err)
	}
	payload, err := ReadFrame(client)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var f Frame
	if err := json.Unmarshal(payload, &f); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if f.Error == nil || f.Error.Code != CodeInvalidRequest {
		t.Fatalf("response = %+v, want invalid request", f)
	}
}

func TestServeHelloInvalidParams(t *testing.T) {
	client, _, _ := startTestServer(t, nil)
	if err := WriteFrame(client, mustFrame(t, Frame{
		JSONRPC: JSONRPCVersion, ID: []byte("9"), Method: MethodHello, Params: json.RawMessage(`{"protocol_version":"one"}`),
	})); err != nil {
		t.Fatalf("write: %v", err)
	}
	payload, err := ReadFrame(client)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var f Frame
	if err := json.Unmarshal(payload, &f); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if f.Error == nil || f.Error.Code != CodeInvalidParams {
		t.Fatalf("response = %+v, want invalid params", f)
	}
}

func TestServeNotificationDeliveryBeforeCompletion(t *testing.T) {
	// D3 通道形状：进度通知先于 RPC 完成响应到达，且按写序保序。
	client, s, _ := startTestServer(t, nil)
	var mu sync.Mutex
	var got []ToolProgressParams
	c := newConn(client, func(method string, params json.RawMessage) {
		if method != NotifyToolProgress {
			t.Errorf("unexpected notification %q", method)
			return
		}
		var p ToolProgressParams
		if err := json.Unmarshal(params, &p); err != nil {
			t.Errorf("unmarshal progress: %v", err)
			return
		}
		mu.Lock()
		got = append(got, p)
		mu.Unlock()
	})
	defer c.close()

	s.Register("base.testEmit", func(ctx context.Context, _ json.RawMessage) (any, error) {
		if err := s.Notify(NotifyToolProgress, ToolProgressParams{CallID: "c1", Chunk: "one"}); err != nil {
			return nil, err
		}
		if err := s.Notify(NotifyToolProgress, ToolProgressParams{CallID: "c1", Chunk: "two"}); err != nil {
			return nil, err
		}
		return PingResult{OK: true}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.call(ctx, "base.testEmit", nil, nil); err != nil {
		t.Fatalf("testEmit call: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0].Chunk != "one" || got[1].Chunk != "two" {
		t.Fatalf("progress chunks = %+v, want [one two] in order", got)
	}
}

func TestServeConcurrentRequestsInterleave(t *testing.T) {
	// 矩阵 C1 的骨架前提：dispatch 不得串行化 handler——两个慢请求并行完成。
	const concurrency = 2
	release := make(chan struct{})
	var started sync.WaitGroup
	started.Add(concurrency)
	client, _, _ := startTestServer(t, func(s *Server) {
		s.Register("base.slowProbe", func(ctx context.Context, _ json.RawMessage) (any, error) {
			started.Done()
			select {
			case <-release:
				return PingResult{OK: true}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		})
	})
	c := newConn(client, nil)
	defer c.close()

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := c.call(ctx, "base.slowProbe", nil, nil); err != nil {
				t.Errorf("slowProbe: %v", err)
			}
		}()
	}
	// 两个 handler 必须都已开跑才放行：串行 dispatch 会卡死这道屏障，
	// 由下面的 5s 超时判负。
	barrier := make(chan struct{})
	go func() { started.Wait(); close(release); close(barrier) }()
	select {
	case <-barrier:
	case <-time.After(5 * time.Second):
		t.Fatal("handlers did not run concurrently: dispatch is serialising requests")
	}
	wg.Wait()
}

func TestServerCoreRegistryAndCapabilityDedupe(t *testing.T) {
	s := NewServer("v")
	s.AddCapabilities(CapTools, CapTools, CapSessions) // duplicate ignored
	s.mu.Lock()
	caps := append([]string(nil), s.caps...)
	methods := len(s.methods)
	s.mu.Unlock()
	if methods != 3 {
		t.Fatalf("core method count = %d, want 3 (hello/ping/shutdown)", methods)
	}
	if !reflect.DeepEqual(caps, []string{CapTools, CapSessions}) {
		t.Fatalf("caps = %v, want [tools sessions]", caps)
	}
}

func TestServerNotifyBeforeServeErrors(t *testing.T) {
	s := NewServer("v")
	if err := s.Notify(NotifyDying, DyingParams{Reason: "test"}); err == nil {
		t.Fatal("Notify before Serve = nil error, want error")
	}
}

// --- frame codec ---

func TestWriteReadFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	payloads := [][]byte{
		[]byte(`{"jsonrpc":"2.0"}`),
		{}, // 空帧是合法封装（下游按 invalid JSON 处理，codec 必须能承载）
		bytes.Repeat([]byte{0x00, 0xff, 0x0a, 0x0d}, 512), // 二进制安全，含换行/回车
	}
	for _, p := range payloads {
		if err := WriteFrame(&buf, p); err != nil {
			t.Fatalf("WriteFrame: %v", err)
		}
	}
	for i, want := range payloads {
		got, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("ReadFrame %d: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("frame %d mismatch: got %d bytes, want %d bytes", i, len(got), len(want))
		}
	}
	if buf.Len() != 0 {
		t.Fatalf("buffer has %d trailing bytes, want 0", buf.Len())
	}
}

func TestReadFrameAcrossChunkedReads(t *testing.T) {
	// 真实管道按任意块交付帧；ReadFrame 内的 io.ReadFull 必须重组。
	var buf bytes.Buffer
	frameBytes(t, &buf, Frame{JSONRPC: JSONRPCVersion, ID: []byte("1"), Method: MethodPing})
	raw := buf.Bytes()

	var reassembled bytes.Buffer
	for _, chunk := range splitBytes(raw, 3) {
		reassembled.Write(chunk)
	}
	payload, err := ReadFrame(&reassembled)
	if err != nil {
		t.Fatalf("ReadFrame over chunked stream: %v", err)
	}
	var f Frame
	if err := json.Unmarshal(payload, &f); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if f.Method != MethodPing {
		t.Fatalf("method = %q, want %q", f.Method, MethodPing)
	}
}

func splitBytes(b []byte, size int) [][]byte {
	var out [][]byte
	for len(b) > size {
		out = append(out, b[:size])
		b = b[size:]
	}
	return append(out, b)
}

func TestReadFrameSizeLimit(t *testing.T) {
	// 头部宣告超过 maxFrameSize → 拒绝，连接不可恢复。
	var header [lengthPrefixSize]byte
	binary.BigEndian.PutUint32(header[:], maxFrameSize+1)
	_, err := ReadFrame(bytes.NewReader(header[:]))
	if !errors.Is(err, errFrameTooLarge) {
		t.Fatalf("ReadFrame oversized header err = %v, want errFrameTooLarge", err)
	}

	// WriteFrame 对超限载荷对称拒绝。
	if err := WriteFrame(io.Discard, make([]byte, maxFrameSize+1)); !errors.Is(err, errFrameTooLarge) {
		t.Fatalf("WriteFrame oversized err = %v, want errFrameTooLarge", err)
	}
}

func TestReadFrameEOFAndPartialHeader(t *testing.T) {
	if _, err := ReadFrame(bytes.NewReader(nil)); !errors.Is(err, io.EOF) {
		t.Fatalf("empty reader err = %v, want io.EOF", err)
	}
	if _, err := ReadFrame(bytes.NewReader([]byte{0x00, 0x00})); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("partial header err = %v, want io.ErrUnexpectedEOF", err)
	}
}

// --- helpers ---

func frameBytes(t *testing.T, buf *bytes.Buffer, v Frame) {
	t.Helper()
	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}
	if err := WriteFrame(buf, encoded); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

func mustFrame(t *testing.T, f Frame) []byte {
	t.Helper()
	encoded, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}
	return encoded
}
