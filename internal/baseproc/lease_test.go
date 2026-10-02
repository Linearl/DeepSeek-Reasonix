package baseproc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func leaseCall(t *testing.T, c *conn, method string, params, result any) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.call(ctx, method, params, result)
}

func leaseHello(t *testing.T, c *conn, pid int) HelloResult {
	t.Helper()
	var res HelloResult
	if err := leaseCall(t, c, MethodHello, HelloParams{ProtocolVersion: ProtocolVersion, ClientPID: pid}, &res); err != nil {
		t.Fatalf("hello: %v", err)
	}
	return res
}

func leaseAttach(t *testing.T, c *conn, sessionID, root string) string {
	t.Helper()
	var res AttachResult
	err := leaseCall(t, c, MethodAttach, AttachParams{SessionID: sessionID, Root: root, WorkspaceScope: "workspace"}, &res)
	if err != nil {
		t.Fatalf("attach(%s): %v", sessionID, err)
	}
	if res.LeaseID == "" {
		t.Fatalf("attach(%s) returned an empty lease id", sessionID)
	}
	return res.LeaseID
}

// rpcCode extracts the JSON-RPC code from an error, failing the test when the
// error is not a protocol-level one.
func rpcCode(t *testing.T, err error) int {
	t.Helper()
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error = %v, want an *RPCError", err)
	}
	return rpcErr.Code
}

func TestAttachDetachRoundTripAdvertisesSessions(t *testing.T) {
	// 设计 §5/§6：serve 进程开 session face 后，hello 广告 CapSessions，
	// attach 回 lease、detach 回 ok——客户端侧的 CapSessions 闸因此放行。
	client, s, _ := startTestServer(t, func(s *Server) { s.AttachSessionAccounting() })
	c := newConn(client, nil)
	defer c.close()

	hello := leaseHello(t, c, os.Getpid())
	if !containsCapability(hello.Capabilities, CapSessions) {
		t.Fatalf("capabilities = %v, want %q advertised by AttachSessionAccounting", hello.Capabilities, CapSessions)
	}

	leaseID := leaseAttach(t, c, "sess-1", `/workspace/root`)
	if !strings.Contains(leaseID, ".") {
		t.Fatalf("lease id = %q, want the pid.sequence shape", leaseID)
	}
	if got := s.sessionLeases().count(); got != 1 {
		t.Fatalf("lease count = %d, want 1", got)
	}

	var det DetachResult
	if err := leaseCall(t, c, MethodDetach, DetachParams{LeaseID: leaseID}, &det); err != nil {
		t.Fatalf("detach: %v", err)
	}
	if !det.OK {
		t.Fatal("detach of a live lease answered ok=false, want true")
	}
	if got := s.sessionLeases().count(); got != 0 {
		t.Fatalf("lease count after detach = %d, want 0", got)
	}
}

func TestDetachReleasesOnlyItsOwnLease(t *testing.T) {
	// 矩阵 C3：会话 A detach 只清 A 的 lease，B 完全不受影响。
	client, s, _ := startTestServer(t, func(s *Server) { s.AttachSessionAccounting() })
	c := newConn(client, nil)
	defer c.close()

	leaseHello(t, c, os.Getpid())
	leaseA := leaseAttach(t, c, "sess-a", "/root/a")
	leaseB := leaseAttach(t, c, "sess-b", "/root/b")
	if got := s.sessionLeases().count(); got != 2 {
		t.Fatalf("lease count = %d, want 2 (two sessions attached)", got)
	}

	var det DetachResult
	if err := leaseCall(t, c, MethodDetach, DetachParams{LeaseID: leaseA}, &det); err != nil {
		t.Fatalf("detach A: %v", err)
	}
	if !det.OK {
		t.Fatal("detach A answered ok=false, want true")
	}
	if got := s.sessionLeases().count(); got != 1 {
		t.Fatalf("lease count after detaching A = %d, want 1 (B untouched)", got)
	}

	// B is still usable: its detach must still be acknowledged.
	if err := leaseCall(t, c, MethodDetach, DetachParams{LeaseID: leaseB}, &det); err != nil {
		t.Fatalf("detach B: %v", err)
	}
	if !det.OK {
		t.Fatal("B's lease was collateral damage of A's detach (C3 violation)")
	}
	if got := s.sessionLeases().count(); got != 0 {
		t.Fatalf("lease count = %d after both detaches, want 0", got)
	}
}

func TestHelloSweepsOrphanLeasesByClientPID(t *testing.T) {
	// 矩阵 C4：客户端崩溃漏 detach → 下一个 base.hello 按 client_pid 批量回收
	// 孤儿 lease；回收过的 id 再 detach 是 no-op（ok=false）而不是错误。
	client, s, _ := startTestServer(t, func(s *Server) { s.AttachSessionAccounting() })
	c := newConn(client, nil)
	defer c.close()

	// The doomed client attaches and never detaches.
	leaseHello(t, c, 4242)
	orphan := leaseAttach(t, c, "sess-orphan", "/root/dead")
	if got := s.sessionLeases().count(); got != 1 {
		t.Fatalf("lease count = %d, want 1 before the sweep", got)
	}

	// Its pid is gone; the next hello comes from a live client.
	orig := leaseAlive
	leaseAlive = func(pid int) bool { return pid != 4242 }
	defer func() { leaseAlive = orig }()

	leaseHello(t, c, 9999)
	if got := s.sessionLeases().count(); got != 0 {
		t.Fatalf("lease count after the C4 sweep = %d, want 0", got)
	}

	var det DetachResult
	if err := leaseCall(t, c, MethodDetach, DetachParams{LeaseID: orphan}, &det); err != nil {
		t.Fatalf("detach of a swept lease errored: %v (want an idempotent no-op)", err)
	}
	if det.OK {
		t.Fatal("detach of a swept lease answered ok=true, want false")
	}
}

func TestHelloKeepsLeasesOwnedByALiveClient(t *testing.T) {
	// C4 的反面：owner 还活着就绝不能回收——重用 pid 的误判只能推迟清扫，
	// 不能丢掉在用的 claim。
	client, s, _ := startTestServer(t, func(s *Server) { s.AttachSessionAccounting() })
	c := newConn(client, nil)
	defer c.close()

	// Every pid looks alive here: the sweep must be a no-op.
	orig := leaseAlive
	leaseAlive = func(int) bool { return true }
	defer func() { leaseAlive = orig }()

	leaseHello(t, c, 1111)
	alive := leaseAttach(t, c, "sess-live", "/root/live")

	leaseHello(t, c, 2222) // another live client reconnects
	if got := s.sessionLeases().count(); got != 1 {
		t.Fatalf("lease count = %d after a second hello, want 1 (owner 1111 still alive)", got)
	}

	var det DetachResult
	if err := leaseCall(t, c, MethodDetach, DetachParams{LeaseID: alive}, &det); err != nil || !det.OK {
		t.Fatalf("detach = (%+v, %v), want (ok=true, nil)", det, err)
	}
}

func TestAttachRejectsMissingSessionOrRoot(t *testing.T) {
	client, _, _ := startTestServer(t, func(s *Server) { s.AttachSessionAccounting() })
	c := newConn(client, nil)
	defer c.close()
	leaseHello(t, c, os.Getpid())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.call(ctx, MethodAttach, AttachParams{Root: "/root"}, nil); rpcCode(t, err) != CodeInvalidParams {
		t.Fatalf("attach without session_id = %v, want -32602", err)
	}
	// Root is the whole point of the call (design §5): silently attaching
	// without one would leave the base unable to bind a workspace later.
	if err := c.call(ctx, MethodAttach, AttachParams{SessionID: "s"}, nil); rpcCode(t, err) != CodeInvalidParams {
		t.Fatalf("attach without root = %v, want -32602", err)
	}
	// Valid JSON, wrong type: the server-side decode must reject it too.
	if err := c.call(ctx, MethodAttach, json.RawMessage(`{"session_id":123,"root":"/r"}`), nil); rpcCode(t, err) != CodeInvalidParams {
		t.Fatalf("attach with malformed params = %v, want -32602", err)
	}
}

func TestDetachRejectsEmptyLeaseID(t *testing.T) {
	client, _, _ := startTestServer(t, func(s *Server) { s.AttachSessionAccounting() })
	c := newConn(client, nil)
	defer c.close()
	leaseHello(t, c, os.Getpid())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.call(ctx, MethodDetach, DetachParams{}, nil); rpcCode(t, err) != CodeInvalidParams {
		t.Fatalf("detach without lease_id = %v, want -32602", err)
	}
	// Unknown id: no error, ok=false — a lease already swept must not fail
	// the caller (see leaseTable.release).
	var det DetachResult
	if err := c.call(ctx, MethodDetach, DetachParams{LeaseID: "l-nope.9"}, &det); err != nil {
		t.Fatalf("detach of an unknown lease errored: %v", err)
	}
	if det.OK {
		t.Fatal("detach of an unknown lease answered ok=true, want false")
	}
}

func TestBareCoreServerDoesNotAdvertiseSessions(t *testing.T) {
	// S1a 契约仍是基线：裸 core server 不挂 session face（attach/detach →
	// -32601 不断连、客户端本地 ErrNotWired 零往返），挂载是显式动作。
	client, _, _ := startTestServer(t, nil)
	c := newConn(client, nil)
	defer c.close()

	hello := leaseHello(t, c, os.Getpid())
	if containsCapability(hello.Capabilities, CapSessions) {
		t.Fatalf("capabilities = %v, want no session capability on a bare core server", hello.Capabilities)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.call(ctx, MethodAttach, AttachParams{SessionID: "s", Root: "/r"}, nil); rpcCode(t, err) != CodeMethodNotFound {
		t.Fatalf("attach on a bare core server = %v, want -32601", err)
	}
}

func TestRunStdioServerAdvertisesSessions(t *testing.T) {
	// 生产路径（reasonix base serve --stdio）必须自带 session face——
	// AttachSessionAccounting 挂在 RunStdioServer 里而不是 NewServer 上，
	// 就是为了让这条断言钉死「真进程有、裸 core 没有」。
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = stdinR.Close()
		_ = stdinW.Close()
		_ = stdoutR.Close()
		_ = stdoutW.Close()
	})

	exit := make(chan int, 1)
	go func() {
		exit <- RunStdioServer(context.Background(), "stdio-version", stdinR, stdoutW, io.Discard)
	}()

	c := newConn(&pipedRW{r: stdoutR, w: stdinW}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var hello HelloResult
	if err := c.call(ctx, MethodHello, HelloParams{ProtocolVersion: ProtocolVersion, ClientPID: os.Getpid()}, &hello); err != nil {
		t.Fatalf("hello over serve pipes: %v", err)
	}
	if !containsCapability(hello.Capabilities, CapSessions) {
		t.Fatalf("capabilities = %v, want %q on the real subprocess", hello.Capabilities, CapSessions)
	}

	if err := c.call(ctx, MethodShutdown, nil, nil); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	select {
	case code := <-exit:
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunStdioServer did not exit within 10s of shutdown")
	}
}
