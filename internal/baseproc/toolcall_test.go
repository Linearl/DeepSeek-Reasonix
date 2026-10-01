package baseproc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/tool"
)

// progressRecorder is a concurrency-safe progress sink for tests.
type progressRecorder struct {
	mu     sync.Mutex
	chunks []string
}

func (r *progressRecorder) sink(chunk string) {
	r.mu.Lock()
	r.chunks = append(r.chunks, chunk)
	r.mu.Unlock()
}

func (r *progressRecorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.chunks...)
}

// remoteToolClient builds a RemoteBaseClient over a surface-backed server.
func remoteToolClient(t *testing.T, surface ToolSurface) (*RemoteBaseClient, *Server) {
	t.Helper()
	client, s, _ := startTestServer(t, func(srv *Server) { srv.AttachToolSurface(surface) })
	return newRemoteClient(newConn(client, nil), nil), s
}

func mustHello(t *testing.T, c BaseClient) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Hello(ctx, HelloParams{ProtocolVersion: ProtocolVersion, ClientPID: 1}); err != nil {
		t.Fatalf("hello: %v", err)
	}
}

// TestToolCallInlineRemoteParity is the S1b对照测试 for toolCall: success,
// tool-level failure and unknown target must answer identically through the
// inline surface (switch off) and over the channel (switch on). Tool failures
// are completed calls on both paths — ToolCallResult.Err, never a Go error.
func TestToolCallInlineRemoteParity(t *testing.T) {
	newTools := func() []*stubTool {
		return []*stubTool{
			{name: "echo", exec: func(_ context.Context, args json.RawMessage) (string, error) {
				return "echo:" + string(args), nil
			}},
			{name: "boom", exec: func(context.Context, json.RawMessage) (string, error) {
				return "", errors.New("tool exploded")
			}},
		}
	}
	inline := InlineBaseClient{ServerVersion: "v-test", Surface: &RegistrySurface{Reg: newStubRegistry(newTools()...)}}
	remote, _ := remoteToolClient(t, &RegistrySurface{Reg: newStubRegistry(newTools()...)})
	mustHello(t, remote)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cases := []ToolCallParams{
		{CallID: "c1", Tool: "echo", Args: json.RawMessage(`{"x":1}`)},
		{CallID: "c2", Tool: "boom"},
		{CallID: "c3", Tool: "missing"},
	}
	for _, p := range cases {
		want, wantErr := inline.ToolCall(ctx, p)
		got, gotErr := remote.ToolCall(ctx, p)
		if wantErr != nil || gotErr != nil {
			t.Fatalf("call %s: inline err=%v remote err=%v (tool failures are completed calls)", p.Tool, wantErr, gotErr)
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("call %s diverges:\n inline = %+v\n remote = %+v", p.Tool, want, got)
		}
	}
	if !strings.Contains(mustCall(t, ctx, inline, ToolCallParams{CallID: "x", Tool: "missing"}).Err, "unknown tool") {
		t.Fatal("unknown tool result should say so")
	}
}

// TestToolCallProgressParityAndSinkShape pins the D3 sink adapter: progress
// stamped into the call context receives the same chunks on both paths —
// inline directly from the tool, remote as base.toolProgress notifications
// routed back by call_id.
func TestToolCallProgressParityAndSinkShape(t *testing.T) {
	streamer := func() *stubTool {
		return &stubTool{name: "streamer", exec: func(ctx context.Context, _ json.RawMessage) (string, error) {
			emit, ok := tool.ProgressFrom(ctx)
			if !ok {
				return "", errors.New("no progress sink stamped on ctx")
			}
			for _, chunk := range []string{"p1", "p2", "p3"} {
				emit(chunk)
			}
			return "done", nil
		}}
	}
	inline := InlineBaseClient{Surface: &RegistrySurface{Reg: newStubRegistry(streamer())}}
	remote, _ := remoteToolClient(t, &RegistrySurface{Reg: newStubRegistry(streamer())})
	mustHello(t, remote)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inlineRec, remoteRec := &progressRecorder{}, &progressRecorder{}
	inlineCtx := tool.WithProgress(ctx, inlineRec.sink)
	remoteCtx := tool.WithProgress(ctx, remoteRec.sink)

	want, err := inline.ToolCall(inlineCtx, ToolCallParams{CallID: "c1", Tool: "streamer"})
	if err != nil {
		t.Fatalf("inline call: %v", err)
	}
	got, err := remote.ToolCall(remoteCtx, ToolCallParams{CallID: "c1", Tool: "streamer"})
	if err != nil {
		t.Fatalf("remote call: %v", err)
	}
	if want != got || want.Content != "done" {
		t.Fatalf("results diverge: inline=%+v remote=%+v", want, got)
	}
	wantChunks := []string{"p1", "p2", "p3"}
	if !reflect.DeepEqual(inlineRec.all(), wantChunks) {
		t.Fatalf("inline chunks = %v, want %v", inlineRec.all(), wantChunks)
	}
	if !reflect.DeepEqual(remoteRec.all(), wantChunks) {
		t.Fatalf("remote chunks = %v, want %v", remoteRec.all(), wantChunks)
	}
}

// TestToolCallConcurrentProgressByCallID is matrix C1: two concurrent calls on
// one shared channel, progress interleaved on the wire but never crossed —
// each call's sink sees exactly its own chunks, in order. The in-tool barrier
// doubles as the no-head-of-line-blocking assertion: if dispatch serialised
// requests, the first call would time out waiting for the second.
func TestToolCallConcurrentProgressByCallID(t *testing.T) {
	var entered atomic.Int32
	bothReady := make(chan struct{})
	barrier := func() error {
		if entered.Add(1) == 2 {
			close(bothReady)
		}
		select {
		case <-bothReady:
			return nil
		case <-time.After(3 * time.Second):
			return errors.New("second call never reached the tool: channel serialised concurrent toolCalls")
		}
	}
	emitStream := func(tag string) func(context.Context, json.RawMessage) (string, error) {
		return func(ctx context.Context, _ json.RawMessage) (string, error) {
			if err := barrier(); err != nil {
				return "", err
			}
			emit, ok := tool.ProgressFrom(ctx)
			if !ok {
				return "", errors.New("no progress sink")
			}
			for i := 1; i <= 3; i++ {
				emit(fmt.Sprintf("%s%d", tag, i))
				time.Sleep(5 * time.Millisecond)
			}
			return "r:" + tag, nil
		}
	}
	remote, _ := remoteToolClient(t, &RegistrySurface{Reg: newStubRegistry(
		&stubTool{name: "wa", exec: emitStream("a")},
		&stubTool{name: "wb", exec: emitStream("b")},
	)})
	mustHello(t, remote)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	recA, recB := &progressRecorder{}, &progressRecorder{}
	type outcome struct {
		res ToolCallResult
		err error
	}
	outA, outB := make(chan outcome, 1), make(chan outcome, 1)
	go func() {
		res, err := remote.ToolCall(tool.WithProgress(ctx, recA.sink), ToolCallParams{CallID: "call-a", Tool: "wa"})
		outA <- outcome{res, err}
	}()
	go func() {
		res, err := remote.ToolCall(tool.WithProgress(ctx, recB.sink), ToolCallParams{CallID: "call-b", Tool: "wb"})
		outB <- outcome{res, err}
	}()
	a, b := <-outA, <-outB
	if a.err != nil || b.err != nil {
		t.Fatalf("concurrent calls failed: a=%v b=%v", a.err, b.err)
	}
	if a.res.Content != "r:a" || b.res.Content != "r:b" {
		t.Fatalf("results crossed: a=%+v b=%+v", a.res, b.res)
	}
	if want := []string{"a1", "a2", "a3"}; !reflect.DeepEqual(recA.all(), want) {
		t.Fatalf("sink A chunks = %v, want %v (cross-talk or reordering)", recA.all(), want)
	}
	if want := []string{"b1", "b2", "b3"}; !reflect.DeepEqual(recB.all(), want) {
		t.Fatalf("sink B chunks = %v, want %v (cross-talk or reordering)", recB.all(), want)
	}
}

// TestToolCallSameToolSerialSemantics is matrix C2: two sessions calling the
// SAME tool concurrently. The channel must not add a second serialisation
// layer (both calls reach the tool at once — the arrival barrier proves it)
// while the tool's own lock keeps its critical section exclusive, exactly as
// the in-process shared-host per-server semantics平移过来.
func TestToolCallSameToolSerialSemantics(t *testing.T) {
	var arrived atomic.Int32
	bothArrived := make(chan struct{})
	var secMu sync.Mutex
	type section struct{ start, end time.Time }
	var sections []section
	serial := &stubTool{name: "serial", exec: func(context.Context, json.RawMessage) (string, error) {
		if arrived.Add(1) == 2 {
			close(bothArrived)
		}
		select {
		case <-bothArrived:
		case <-time.After(3 * time.Second):
			return "", errors.New("second caller never arrived: transport serialised the same tool")
		}
		// The tool's own critical section — the shared-host per-server serial
		// semantics平移: the surface adds no serialisation of its own, the
		// tool still serialises itself.
		secMu.Lock()
		start := time.Now()
		time.Sleep(20 * time.Millisecond)
		sections = append(sections, section{start: start, end: time.Now()})
		secMu.Unlock()
		return "done", nil
	}}
	remote, _ := remoteToolClient(t, &RegistrySurface{Reg: newStubRegistry(serial)})
	mustHello(t, remote)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	results := make([]ToolCallResult, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = remote.ToolCall(ctx, ToolCallParams{
				CallID: fmt.Sprintf("same-%d", i), Tool: "serial",
			})
		}(i)
	}
	wg.Wait()
	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("call %d failed: %v", i, errs[i])
		}
		if results[i].Content != "done" {
			t.Fatalf("call %d result = %+v", i, results[i])
		}
	}
	if arrived.Load() != 2 {
		t.Fatalf("tool arrivals = %d, want 2 (both calls reached one shared instance concurrently)", arrived.Load())
	}
	if len(sections) != 2 {
		t.Fatalf("sections = %d, want 2", len(sections))
	}
	first, second := sections[0], sections[1]
	if first.start.After(second.start) {
		first, second = second, first
	}
	if first.end.After(second.start) {
		t.Fatalf("critical sections overlapped (%v..%v vs %v..%v): per-server serial semantics lost",
			first.start, first.end, second.start, second.end)
	}
}

// TestToolCallAfterChannelCloseFailsFast is the protocol negative path: a dead
// channel must fail the tool call immediately with a clear transport error —
// never hang the caller.
func TestToolCallAfterChannelCloseFailsFast(t *testing.T) {
	remote, _ := remoteToolClient(t, &RegistrySurface{Reg: newStubRegistry(&stubTool{name: "t"})})
	mustHello(t, remote)
	if err := remote.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, err := remote.ToolCall(ctx, ToolCallParams{CallID: "c", Tool: "t"})
	if err == nil {
		t.Fatal("ToolCall on a closed channel succeeded, want transport error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("ToolCall took %s to fail on a dead channel, want immediate", elapsed)
	}
	if !errors.Is(err, errConnClosed) && !strings.Contains(err.Error(), "connection") {
		t.Fatalf("err = %v, want a connection-dead error", err)
	}
}

// TestToolCallInFlightFailsWhenChannelDies: a call already waiting for its
// response fails promptly when the channel dies underneath it (R1's
// "对端死亡在途调用即败" for the tool surface).
func TestToolCallInFlightFailsWhenChannelDies(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	blocker := &stubTool{name: "blocker", exec: func(context.Context, json.RawMessage) (string, error) {
		close(started)
		<-release
		return "late", nil
	}}
	remote, _ := remoteToolClient(t, &RegistrySurface{Reg: newStubRegistry(blocker)})
	mustHello(t, remote)

	done := make(chan error, 1)
	go func() {
		_, err := remote.ToolCall(context.Background(), ToolCallParams{CallID: "c", Tool: "blocker"})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("server never started the tool")
	}
	if err := remote.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("in-flight ToolCall succeeded across a dead channel, want error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight ToolCall still pending 2s after channel death — wedged")
	}
}

// TestToolCallToolPanicContained: a panicking tool fails THAT call with an
// explicit error while the channel stays usable. The inline path deliberately
// keeps today's behaviour — the panic reaches the caller — so the divergence
// is pinned on both sides rather than left implicit.
func TestToolCallToolPanicContained(t *testing.T) {
	panicker := &stubTool{name: "panicker", exec: func(context.Context, json.RawMessage) (string, error) {
		panic("kaboom")
	}}
	remote, _ := remoteToolClient(t, &RegistrySurface{Reg: newStubRegistry(panicker)})
	mustHello(t, remote)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := remote.ToolCall(ctx, ToolCallParams{CallID: "c", Tool: "panicker"})
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeInternalError {
		t.Fatalf("remote panic err = %v, want -32603", err)
	}
	if !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("err = %v, want it to name the panic", err)
	}
	if err := remote.Ping(ctx); err != nil {
		t.Fatalf("channel died after contained panic: %v", err)
	}

	// Inline counterpart: the panic propagates exactly as an in-process tool
	// panic does today (no containment added to the switch-off path).
	inline := InlineBaseClient{Surface: &RegistrySurface{Reg: newStubRegistry(panicker)}}
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("inline panic did not propagate to the caller")
			}
		}()
		_, _ = inline.ToolCall(ctx, ToolCallParams{CallID: "c2", Tool: "panicker"})
	}()
}

// TestToolCallInvalidParamsBothPaths: call_id and tool are required by the v1
// contract; both implementations answer -32602, and the server enforces it
// even when a client skips its own check.
func TestToolCallInvalidParamsBothPaths(t *testing.T) {
	inline := InlineBaseClient{Surface: &RegistrySurface{Reg: newStubRegistry(&stubTool{name: "t"})}}
	remote, _ := remoteToolClient(t, &RegistrySurface{Reg: newStubRegistry(&stubTool{name: "t"})})
	mustHello(t, remote)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	bad := []ToolCallParams{
		{CallID: "", Tool: "t"},
		{CallID: "c", Tool: ""},
		{CallID: "", Tool: ""},
	}
	for i, p := range bad {
		if _, err := inline.ToolCall(ctx, p); !isRPCCode(err, CodeInvalidParams) {
			t.Fatalf("case %d inline err = %v, want -32602", i, err)
		}
		if _, err := remote.ToolCall(ctx, p); !isRPCCode(err, CodeInvalidParams) {
			t.Fatalf("case %d remote err = %v, want -32602", i, err)
		}
	}
	// Server-side enforcement, bypassing the client check over the raw conn.
	raw := remote.c
	var res ToolCallResult
	err := raw.call(ctx, MethodToolCall, ToolCallParams{CallID: "", Tool: "t"}, &res)
	if !isRPCCode(err, CodeInvalidParams) {
		t.Fatalf("server without call_id err = %v, want -32602", err)
	}
}

// TestToolProgressPassthroughAndGhostChunks: base.toolProgress belongs to the
// per-call router (ghost ids dropped, no crash); every other notification
// still reaches the manager-supplied callback unchanged (the S1a Options.Notify
// contract, and the base.dying hook S1c builds on).
func TestToolProgressPassthroughAndGhostChunks(t *testing.T) {
	var mu sync.Mutex
	var outer []string
	client, s, _ := startTestServer(t, func(srv *Server) { srv.AttachToolSurface(&RegistrySurface{Reg: newStubRegistry(&stubTool{name: "t"})}) })
	r := newRemoteClient(newConn(client, func(method string, _ json.RawMessage) {
		mu.Lock()
		outer = append(outer, method)
		mu.Unlock()
	}), nil)
	mustHello(t, r)

	rec := &progressRecorder{}
	r.trackProgress("live", rec.sink)
	if err := s.Notify(NotifyToolProgress, ToolProgressParams{CallID: "ghost", Chunk: "stale"}); err != nil {
		t.Fatalf("notify ghost: %v", err)
	}
	if err := s.Notify(NotifyCatalogChanged, CatalogChangedParams{Reason: "plugin hot event"}); err != nil {
		t.Fatalf("notify catalogChanged: %v", err)
	}
	// The ping response is written after both notifications, so its arrival
	// proves the read loop flushed them.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := r.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if got := rec.all(); len(got) != 0 {
		t.Fatalf("ghost progress chunks reached a live sink: %v", got)
	}
	mu.Lock()
	gotOuter := append([]string(nil), outer...)
	mu.Unlock()
	if len(gotOuter) != 1 || gotOuter[0] != NotifyCatalogChanged {
		t.Fatalf("outer notifications = %v, want exactly [%s]", gotOuter, NotifyCatalogChanged)
	}
	r.untrackProgress("live")
}

func mustCall(t *testing.T, ctx context.Context, c BaseClient, p ToolCallParams) ToolCallResult {
	t.Helper()
	res, err := c.ToolCall(ctx, p)
	if err != nil {
		t.Fatalf("toolCall %s: %v", p.Tool, err)
	}
	return res
}

func isRPCCode(err error, code int) bool {
	var rpcErr *RPCError
	return errors.As(err, &rpcErr) && rpcErr.Code == code
}
