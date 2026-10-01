package baseproc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

// dialWithServer returns a Dial that serves the real v1 core protocol on an
// in-memory pipe pair, plus a channel that receives nil when the server's
// Serve loop exits (shutdown propagation for assertions).
func dialWithServer(t *testing.T, prepare func(s *Server)) (func(ctx context.Context) (io.ReadWriteCloser, func(), error), <-chan error) {
	t.Helper()
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	s := NewServer("test-server")
	if prepare != nil {
		prepare(s)
	}
	served := make(chan error, 1)
	go func() { served <- s.Serve(context.Background(), serverIn, serverOut) }()
	t.Cleanup(func() {
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = serverIn.Close()
		_ = serverOut.Close()
		select {
		case <-served:
		case <-time.After(5 * time.Second):
			t.Log("dialWithServer serve loop did not exit during cleanup")
		}
	})
	dial := func(ctx context.Context) (io.ReadWriteCloser, func(), error) {
		return &pipedRW{r: clientIn, w: clientOut}, func() {}, nil
	}
	return dial, served
}

func newTestLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

func TestStartDisabledReturnsInlineWithoutSideEffects(t *testing.T) {
	// R4 默认态：开关 off = 纯 inline，不 spawn、不拨号、零行为变化。
	log, buf := newTestLogger()
	dialCalls := 0
	client := Start(context.Background(), Options{
		ServerVersion: "v-test",
		Log:           log,
		Dial: func(ctx context.Context) (io.ReadWriteCloser, func(), error) {
			dialCalls++
			return nil, nil, errors.New("must not dial while disabled")
		},
	})
	if client.Mode() != ModeInline {
		t.Fatalf("mode = %q, want inline", client.Mode())
	}
	if dialCalls != 0 {
		t.Fatalf("dial invoked %d times while disabled", dialCalls)
	}
	if buf.Len() != 0 {
		t.Fatalf("disabled start logged %q, want silence", buf.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("inline ping: %v", err)
	}
}

func TestStartEnabledHandshakeSucceedsRemote(t *testing.T) {
	// 设计 §10 S1a 验收：开关 on，协议握手过 → remote + `boot: base remote`。
	log, buf := newTestLogger()
	dial, served := dialWithServer(t, nil)
	client := Start(context.Background(), Options{
		Enabled:       true,
		ServerVersion: "v-parent",
		Log:           log,
		Dial:          dial,
	})
	if client.Mode() != ModeRemote {
		t.Fatalf("mode = %q, want remote", client.Mode())
	}
	if !strings.Contains(buf.String(), "boot: base remote") {
		t.Fatalf("log = %q, want boot: base remote", buf.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("remote ping: %v", err)
	}
	if err := client.Shutdown(ctx); err != nil {
		t.Fatalf("remote shutdown: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("serve loop: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve loop did not exit after shutdown")
	}
}

func TestStartEnabledDialFailureFallsBackInline(t *testing.T) {
	// R1：spawn 失败 → 即刻 inline 接管 + `boot: base fallback`，永不失败。
	log, buf := newTestLogger()
	client := Start(context.Background(), Options{
		Enabled:       true,
		ServerVersion: "v-parent",
		Log:           log,
		Dial: func(ctx context.Context) (io.ReadWriteCloser, func(), error) {
			return nil, nil, errors.New("binary missing")
		},
	})
	if client.Mode() != ModeInline {
		t.Fatalf("mode = %q, want inline fallback", client.Mode())
	}
	if !strings.Contains(buf.String(), "boot: base fallback") || !strings.Contains(buf.String(), "binary missing") {
		t.Fatalf("log = %q, want fallback with reason", buf.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("inline ping after fallback: %v", err)
	}
}

func TestStartEnabledHandshakeVersionMismatchFallsBackInline(t *testing.T) {
	// F3：协议协商失败 → fallback inline + warn，不重试轰炸。
	log, buf := newTestLogger()
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
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
				Error: &RPCError{Code: CodeVersionMismatch, Message: "protocol too old"}})
			if err != nil {
				return
			}
			if WriteFrame(serverOut, resp) != nil {
				return
			}
		}
	}()

	client := Start(context.Background(), Options{
		Enabled:       true,
		ServerVersion: "v-parent",
		Log:           log,
		Dial: func(ctx context.Context) (io.ReadWriteCloser, func(), error) {
			return &pipedRW{r: clientIn, w: clientOut}, func() {}, nil
		},
	})
	if client.Mode() != ModeInline {
		t.Fatalf("mode = %q, want inline fallback on version mismatch", client.Mode())
	}
	if !strings.Contains(buf.String(), "boot: base fallback") {
		t.Fatalf("log = %q, want fallback", buf.String())
	}
	// 拨号恰好一次：fallback 不重试。
	if n := strings.Count(buf.String(), "boot: base fallback"); n != 1 {
		t.Fatalf("fallback logged %d times, want exactly 1 (no retry storm)", n)
	}
}

func TestStartHandshakeTimeoutFallsBackInline(t *testing.T) {
	// 握手卡死（spawn 挂起、服务器不应答）→ 预算内放弃，回落 inline。
	log, buf := newTestLogger()
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	t.Cleanup(func() {
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = serverIn.Close()
		_ = serverOut.Close()
	})
	// A reader that consumes but never answers leaves the handshake pending.
	go func() { _, _ = io.Copy(io.Discard, serverIn) }()

	start := time.Now()
	client := Start(context.Background(), Options{
		Enabled:          true,
		ServerVersion:    "v-parent",
		HandshakeTimeout: 150 * time.Millisecond,
		Log:              log,
		Dial: func(ctx context.Context) (io.ReadWriteCloser, func(), error) {
			return &pipedRW{r: clientIn, w: clientOut}, func() {}, nil
		},
	})
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("handshake fallback took %s, want within the 150ms budget scale", elapsed)
	}
	if client.Mode() != ModeInline {
		t.Fatalf("mode = %q, want inline fallback on timeout", client.Mode())
	}
	if !strings.Contains(buf.String(), "boot: base fallback") {
		t.Fatalf("log = %q, want fallback", buf.String())
	}
}

func TestServeArgvDefaultCommandShape(t *testing.T) {
	// D4：默认命令 = 当前可执行文件 + `base serve --stdio`（不新增二进制）。
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	argv, err := serveArgv(nil)
	if err != nil {
		t.Fatalf("serveArgv(nil): %v", err)
	}
	if len(argv) != 4 {
		t.Fatalf("default argv = %v, want 4 elements", argv)
	}
	if argv[0] != exe {
		t.Fatalf("argv[0] = %q, want the current executable %q", argv[0], exe)
	}
	if argv[1] != "base" || argv[2] != "serve" || argv[3] != "--stdio" {
		t.Fatalf("argv tail = %v, want [base serve --stdio]", argv[1:])
	}

	// 显式命令原样透传（S1c 崩溃注入 / 测试替身的入口）。
	explicit := []string{"/custom/reasonix", "base", "serve", "--stdio"}
	got, err := serveArgv(explicit)
	if err != nil {
		t.Fatalf("serveArgv(explicit): %v", err)
	}
	if len(got) != len(explicit) || got[0] != explicit[0] {
		t.Fatalf("explicit argv = %v, want passthrough", got)
	}
}

func TestRunStdioServerServesProtocolOverRealPipes(t *testing.T) {
	// `base serve --stdio` 的进程内等价路径：os.Pipe 两端跑完整协议——
	// 握手 → ping → shutdown → 退出码 0。执行(exec)差异由 subprocessDial
	// 的 argv 契约测试锁定。
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe stdin: %v", err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe stdout: %v", err)
	}
	defer func() {
		_ = stdinR.Close()
		_ = stdinW.Close()
		_ = stdoutR.Close()
		_ = stdoutW.Close()
	}()

	exit := make(chan int, 1)
	go func() {
		exit <- RunStdioServer(context.Background(), "served-version", stdinR, stdoutW, io.Discard)
	}()

	client := newConn(&pipedRW{r: stdoutR, w: stdinW}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var hello HelloResult
	if err := client.call(ctx, MethodHello, HelloParams{ProtocolVersion: ProtocolVersion, ClientPID: os.Getpid()}, &hello); err != nil {
		t.Fatalf("hello over serve pipes: %v", err)
	}
	if hello.ServerVersion != "served-version" {
		t.Fatalf("server version = %q, want served-version", hello.ServerVersion)
	}
	if err := client.call(ctx, MethodPing, nil, nil); err != nil {
		t.Fatalf("ping over serve pipes: %v", err)
	}
	if err := client.call(ctx, MethodShutdown, nil, nil); err != nil {
		t.Fatalf("shutdown over serve pipes: %v", err)
	}

	select {
	case code := <-exit:
		if code != 0 {
			t.Fatalf("RunStdioServer exit code = %d, want 0", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunStdioServer did not exit within 10s of shutdown")
	}
}
