package baseproc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

// echoServerConn pairs a conn with a tiny raw server loop that answers every
// request with a fixed result, for testing conn-side correlation without the
// full Server.
func echoServerConn(t *testing.T) *conn {
	t.Helper()
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	client := &pipedRW{r: clientIn, w: clientOut}
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
			if json.Unmarshal(payload, &f) != nil || f.Kind() != FrameRequest {
				continue
			}
			resp := Frame{JSONRPC: JSONRPCVersion, ID: f.ID}
			var params map[string]any
			_ = json.Unmarshal(f.Params, &params)
			if want, ok := params["force_error"].(string); ok {
				resp.Error = &RPCError{Code: CodeInternalError, Message: want}
			} else {
				resp.Result = json.RawMessage(`{"echo":true}`)
			}
			encoded, err := json.Marshal(resp)
			if err != nil {
				return
			}
			if WriteFrame(serverOut, encoded) != nil {
				return
			}
		}
	}()
	return newConn(client, nil)
}

func TestConnCorrelatesConcurrentCalls(t *testing.T) {
	// C1 的客户端侧前提：并发调用的响应按 id 归位，不串线。
	c := echoServerConn(t)
	defer c.close()

	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var result struct {
				Echo bool `json:"echo"`
			}
			if err := c.call(ctx, MethodPing, nil, &result); err != nil {
				t.Errorf("call: %v", err)
				return
			}
			if !result.Echo {
				t.Error("echo result = false")
			}
		}()
	}
	wg.Wait()
}

func TestConnMapsPeerErrorToRPCError(t *testing.T) {
	c := echoServerConn(t)
	defer c.close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := c.call(ctx, MethodPing, map[string]any{"force_error": "boom"}, nil)
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("err = %v (%T), want *RPCError", err, err)
	}
	if rpcErr.Code != CodeInternalError || rpcErr.Message != "boom" {
		t.Fatalf("rpc error = %+v, want internal/boom", rpcErr)
	}
}

func TestConnPendingCallsFailWhenPeerDies(t *testing.T) {
	// 对端消失（管道断裂）时，在途调用必须立刻失败，不得悬挂——
	// R1 fallback 链路依赖这一语义。
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	raw := &pipedRW{r: clientIn, w: clientOut}
	c := newConn(raw, nil)
	defer c.close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		// No one answers on the far side; the call parks until the pipe dies.
		errCh <- c.call(ctx, MethodPing, nil, nil)
	}()
	// Give the call time to register its pending entry, then kill the pipe.
	time.Sleep(50 * time.Millisecond)
	_ = serverOut.Close() // server's write end dies → client read loop gets EOF
	_ = serverIn.Close()
	_ = clientOut.Close()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("call survived peer death with nil error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending call did not fail within 5s of peer death")
	}

	// 后续调用立刻拿到连接已死错误，而不是再次悬挂。
	if err := c.call(context.Background(), MethodPing, nil, nil); err == nil {
		t.Fatal("call on dead connection = nil error")
	}
}

func TestConnCallAfterCloseFailsFast(t *testing.T) {
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	raw := &pipedRW{r: clientIn, w: clientOut}
	c := newConn(raw, nil)
	t.Cleanup(func() {
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = serverIn.Close()
		_ = serverOut.Close()
	})
	if err := c.close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Close is idempotent.
	if err := c.close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if err := c.call(context.Background(), MethodPing, nil, nil); !errors.Is(err, errConnClosed) {
		t.Fatalf("call after close err = %v, want errConnClosed", err)
	}
}

func TestConnContextCancellationAbortsCall(t *testing.T) {
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	raw := &pipedRW{r: clientIn, w: clientOut}
	c := newConn(raw, nil)
	// Drain the request side so the call's frame write completes; nobody
	// answers, which is exactly the condition the timeout must rescue.
	drained := make(chan struct{})
	go func() { defer close(drained); _, _ = io.Copy(io.Discard, serverIn) }()
	t.Cleanup(func() {
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = serverIn.Close()
		_ = serverOut.Close()
		<-drained
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := c.call(ctx, MethodPing, nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled call err = %v, want context.DeadlineExceeded", err)
	}
}
