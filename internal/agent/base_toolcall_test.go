package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/baseproc"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// gatePlainTool is a plain Execute tool counting its executions.
type gatePlainTool struct {
	name  string
	runs  *atomic.Int32
	label string
	emit  []string // chunks streamed through the ctx progress sink before returning
}

func (t *gatePlainTool) Name() string            { return t.name }
func (t *gatePlainTool) Description() string     { return "gate stub " + t.name }
func (t *gatePlainTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *gatePlainTool) ReadOnly() bool          { return false }
func (t *gatePlainTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if t.runs != nil {
		t.runs.Add(1)
	}
	if len(t.emit) > 0 {
		if emit, ok := tool.ProgressFrom(ctx); ok {
			for _, chunk := range t.emit {
				emit(chunk)
			}
		}
	}
	return t.label + ":" + string(args), nil
}

// gateWrappedTool keeps the registry's name but is NOT the registry instance —
// the wrapper shape path-binding/plan-gating produce.
type gateWrappedTool struct{ inner tool.Tool }

func (t *gateWrappedTool) Name() string            { return t.inner.Name() }
func (t *gateWrappedTool) Description() string     { return t.inner.Description() }
func (t *gateWrappedTool) Schema() json.RawMessage { return t.inner.Schema() }
func (t *gateWrappedTool) ReadOnly() bool          { return t.inner.ReadOnly() }
func (t *gateWrappedTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return t.inner.Execute(ctx, args)
}

// gateDetailedTool carries structured execution metadata (bash's shape) and
// must stay on the local path — v1 ToolCallResult cannot carry it.
type gateDetailedTool struct {
	name  string
	runs  *atomic.Int32
	inner *gatePlainTool
}

func (t *gateDetailedTool) Name() string            { return t.name }
func (t *gateDetailedTool) Description() string     { return "detailed stub" }
func (t *gateDetailedTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *gateDetailedTool) ReadOnly() bool          { return false }
func (t *gateDetailedTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return t.inner.Execute(ctx, args)
}
func (t *gateDetailedTool) ExecutionDescriptor(json.RawMessage) *tool.ShellExecution {
	return &tool.ShellExecution{}
}
func (t *gateDetailedTool) ExecuteDetailed(ctx context.Context, args json.RawMessage) (tool.DetailedResult, error) {
	if t.runs != nil {
		t.runs.Add(1)
	}
	out, err := t.inner.Execute(ctx, args)
	return tool.DetailedResult{Output: out, Execution: &tool.ShellExecution{}}, err
}

// gateReadTool implements ReadExecutor — the read-envelope branch must stay
// local (envelope/continuation state lives client-side).
type gateReadTool struct{ name string }

func (t *gateReadTool) Name() string            { return t.name }
func (t *gateReadTool) Description() string     { return "read stub" }
func (t *gateReadTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *gateReadTool) ReadOnly() bool          { return true }
func (t *gateReadTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "", errors.New("must not run through plain Execute in these tests")
}
func (t *gateReadTool) ExecuteRead(context.Context, json.RawMessage) (string, tool.ReadResultEnvelope, error) {
	return "read-local", tool.ReadResultEnvelope{}, nil
}

// newAgentRemoteBase spins a surface-backed serve loop over in-memory pipes
// and returns the dialed/handshaked client from baseproc.Start.
func newAgentRemoteBase(t *testing.T, surface baseproc.ToolSurface) baseproc.BaseClient {
	t.Helper()
	client, _ := newAgentRemoteBaseWithKill(t, surface)
	return client
}

// newAgentRemoteBaseWithKill is the S1c variant: it also returns a func that
// severs the channel the way a crashed subprocess does (pipe EOF) WITHOUT
// releasing the view, so a test can observe the manager degrade a live client
// (design C6) instead of only a closed one.
func newAgentRemoteBaseWithKill(t *testing.T, surface baseproc.ToolSurface) (baseproc.BaseClient, func()) {
	t.Helper()
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	s := baseproc.NewServer("agent-gate-test")
	s.AttachToolSurface(surface)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = s.Serve(ctx, serverIn, serverOut)
		close(done)
	}()
	kill := func() {
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = serverIn.Close()
		_ = serverOut.Close()
	}
	t.Cleanup(func() {
		cancel()
		kill()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Log("agent-gate serve loop did not exit in 5s")
		}
	})
	client := baseproc.Start(context.Background(), baseproc.Options{
		Enabled: true,
		Dial: func(context.Context) (io.ReadWriteCloser, func(), error) {
			rw := &gatePipeRW{r: clientIn, w: clientOut}
			return rw, func() { _ = rw.Close() }, nil
		},
		HandshakeTimeout: 5 * time.Second,
	})
	if client.Mode() != baseproc.ModeRemote {
		t.Fatalf("Start mode = %q, want remote", client.Mode())
	}
	// Registered last, so it runs first (cleanups are LIFO): the S1c
	// supervisor goroutine must be released before the pipes go away or
	// goleak reports it still parked in its backoff wait.
	t.Cleanup(func() { _ = client.Close() })
	return client, kill
}

// gatePipeRW adapts a pipe pair for baseproc.Options.Dial (control has its
// own copy — test helpers stay package-local).
type gatePipeRW struct {
	r io.ReadCloser
	w io.WriteCloser
}

func (p *gatePipeRW) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *gatePipeRW) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *gatePipeRW) Close() error                { return errors.Join(p.r.Close(), p.w.Close()) }

// gateMustGet fetches the registry instance the gate compares identity against.
func gateMustGet(t *testing.T, reg *tool.Registry, name string) tool.Tool {
	t.Helper()
	got, ok := reg.Get(name)
	if !ok {
		t.Fatalf("tool %q missing from registry", name)
	}
	return got
}

func gateCall(id, toolName string, args json.RawMessage) *toolCallPlan {
	return &toolCallPlan{
		call:    provider.ToolCall{ID: id, Name: toolName, Arguments: string(args)},
		runTool: nil, // filled by the caller once the target is chosen
		runArgs: args,
	}
}

// TestBaseToolCallGateMatrix is the S1b对照测试 for the agent consumption
// point: every non-remote state executes in-process (switch off = current
// behaviour), the remote-capable state with the tools capability routes over
// base.toolCall, and every structural mismatch (wrapper, rich executor, no
// call id, dead channel) falls back or fails per the gate contract.
func TestBaseToolCallGateMatrix(t *testing.T) {
	ctx := context.Background()

	t.Run("no base client runs local", func(t *testing.T) {
		var localRuns, serverRuns atomic.Int32
		target := &gatePlainTool{name: "echo", runs: &localRuns, label: "local"}
		reg := tool.NewRegistry()
		reg.Add(target)
		a := &Agent{svc: agentServices{tools: reg}}
		plan := gateCall("c1", "echo", json.RawMessage(`{"k":1}`))
		plan.runTool = gateMustGet(t, reg, "echo")

		if _, handled := a.baseToolCall(ctx, plan); handled {
			t.Fatal("nil base client took the gate")
		}
		res, _, _, err := a.invokeResolvedTool(ctx, plan)
		if err != nil {
			t.Fatalf("local dispatch: %v", err)
		}
		if res != `local:{"k":1}` || localRuns.Load() != 1 || serverRuns.Load() != 0 {
			t.Fatalf("res=%q local=%d server=%d", res, localRuns.Load(), serverRuns.Load())
		}
	})

	t.Run("inline base client runs local", func(t *testing.T) {
		var localRuns atomic.Int32
		target := &gatePlainTool{name: "echo", runs: &localRuns, label: "local"}
		reg := tool.NewRegistry()
		reg.Add(target)
		a := &Agent{svc: agentServices{
			tools: reg,
			base:  baseproc.InlineBaseClient{ServerVersion: "v-test", Surface: &baseproc.RegistrySurface{Reg: reg}},
		}}
		plan := gateCall("c1", "echo", json.RawMessage(`{}`))
		plan.runTool = gateMustGet(t, reg, "echo")

		if _, handled := a.baseToolCall(ctx, plan); handled {
			t.Fatal("inline client took the gate (Mode check failed)")
		}
		res, _, _, err := a.invokeResolvedTool(ctx, plan)
		if err != nil || res != `local:{}` || localRuns.Load() != 1 {
			t.Fatalf("res=%q err=%v local=%d", res, err, localRuns.Load())
		}
	})

	t.Run("remote without capability runs local", func(t *testing.T) {
		var localRuns atomic.Int32
		target := &gatePlainTool{name: "echo", runs: &localRuns, label: "local"}
		reg := tool.NewRegistry()
		reg.Add(target)
		// Server has no attached surface → no CapTools → client answers
		// ErrNotWired locally (no round trip) → gate opens to the local path.
		remote := newAgentRemoteBase(t, nil)
		a := &Agent{svc: agentServices{tools: reg, base: remote}}
		plan := gateCall("c1", "echo", json.RawMessage(`{}`))
		plan.runTool = gateMustGet(t, reg, "echo")

		if _, handled := a.baseToolCall(ctx, plan); handled {
			t.Fatal("remote without capability took the gate")
		}
		res, _, _, err := a.invokeResolvedTool(ctx, plan)
		if err != nil || res != `local:{}` || localRuns.Load() != 1 {
			t.Fatalf("res=%q err=%v local=%d", res, err, localRuns.Load())
		}
	})

	t.Run("remote with capability routes over IPC with progress", func(t *testing.T) {
		var localRuns, serverRuns atomic.Int32
		localTarget := &gatePlainTool{name: "echo", runs: &localRuns, label: "local"}
		localReg := tool.NewRegistry()
		localReg.Add(localTarget)
		serverReg := tool.NewRegistry()
		serverReg.Add(&gatePlainTool{
			name: "echo", runs: &serverRuns, label: "remote",
			emit: []string{"s1", "s2"},
		})
		remote := newAgentRemoteBase(t, &baseproc.RegistrySurface{Reg: serverReg})
		a := &Agent{svc: agentServices{tools: localReg, base: remote}}

		plan := gateCall("call-77", "echo", json.RawMessage(`{"k":2}`))
		plan.runTool = gateMustGet(t, localReg, "echo")

		rec := &gateEventRecorder{}
		callCtx := baseproc.WithToolProgress(ctx, rec, plan.call.ID)
		res, images, execution, err := a.invokeResolvedTool(callCtx, plan)
		if err != nil {
			t.Fatalf("routed dispatch: %v", err)
		}
		if res != `remote:{"k":2}` {
			t.Fatalf("res = %q, want the SERVER's answer (over IPC)", res)
		}
		if images != nil || execution != nil {
			t.Fatalf("images/execution must stay nil on the base path: %v %v", images, execution)
		}
		if localRuns.Load() != 0 || serverRuns.Load() != 1 {
			t.Fatalf("local=%d server=%d, want 0/1 (routing inverted)", localRuns.Load(), serverRuns.Load())
		}
		wantEvents := []string{"s1", "s2"}
		got := rec.outputs()
		if len(got) != len(wantEvents) {
			t.Fatalf("progress events = %v, want %v", got, wantEvents)
		}
		for i, chunk := range wantEvents {
			e := rec.eventAt(i)
			if e.Kind != event.ToolProgress || e.Tool.ID != "call-77" || e.Tool.Output != chunk {
				t.Fatalf("event %d = %+v, want ToolProgress ID=call-77 Output=%q", i, e, chunk)
			}
		}
	})

	t.Run("wrapped target runs local", func(t *testing.T) {
		var innerRuns atomic.Int32
		inner := &gatePlainTool{name: "echo", runs: &innerRuns, label: "local"}
		reg := tool.NewRegistry()
		reg.Add(inner)
		remote := newAgentRemoteBase(t, &baseproc.RegistrySurface{Reg: tool.NewRegistry()})
		a := &Agent{svc: agentServices{tools: reg, base: remote}}
		plan := gateCall("c1", "echo", json.RawMessage(`{}`))
		plan.runTool = &gateWrappedTool{inner: inner} // same name, foreign instance

		if _, handled := a.baseToolCall(ctx, plan); handled {
			t.Fatal("wrapped target took the gate")
		}
		res, _, _, err := a.invokeResolvedTool(ctx, plan)
		if err != nil || res != `local:{}` || innerRuns.Load() != 1 {
			t.Fatalf("res=%q err=%v local=%d", res, err, innerRuns.Load())
		}
	})

	t.Run("rich executor runs local", func(t *testing.T) {
		var detailedRuns, localRuns atomic.Int32
		inner := &gatePlainTool{name: "bash", runs: &localRuns, label: "local"}
		reg := tool.NewRegistry()
		reg.Add(inner)
		remote := newAgentRemoteBase(t, &baseproc.RegistrySurface{Reg: tool.NewRegistry()})
		a := &Agent{svc: agentServices{tools: reg, base: remote}}
		plan := gateCall("c1", "bash", json.RawMessage(`{}`))
		plan.runTool = &gateDetailedTool{name: "bash", runs: &detailedRuns, inner: inner}

		if _, handled := a.baseToolCall(ctx, plan); handled {
			t.Fatal("DetailedExecutor took the gate")
		}
		res, _, execution, err := a.invokeResolvedTool(ctx, plan)
		if err != nil {
			t.Fatalf("local detailed dispatch: %v", err)
		}
		if execution == nil || detailedRuns.Load() != 1 {
			t.Fatalf("execution=%v detailed runs=%d, want the local detailed branch", execution, detailedRuns.Load())
		}
		if res == "" {
			t.Fatal("no result from local detailed branch")
		}
	})

	t.Run("read executor gate refuses", func(t *testing.T) {
		reg := tool.NewRegistry()
		reg.Add(&gateReadTool{name: "read"})
		remote := newAgentRemoteBase(t, &baseproc.RegistrySurface{Reg: tool.NewRegistry()})
		a := &Agent{svc: agentServices{tools: reg, base: remote}}
		plan := gateCall("c1", "read", json.RawMessage(`{}`))
		plan.runTool = &gateReadTool{name: "read"}
		if _, handled := a.baseToolCall(ctx, plan); handled {
			t.Fatal("ReadExecutor took the gate")
		}
	})

	t.Run("missing call id runs local", func(t *testing.T) {
		var localRuns atomic.Int32
		target := &gatePlainTool{name: "echo", runs: &localRuns, label: "local"}
		reg := tool.NewRegistry()
		reg.Add(target)
		remote := newAgentRemoteBase(t, &baseproc.RegistrySurface{Reg: tool.NewRegistry()})
		a := &Agent{svc: agentServices{tools: reg, base: remote}}
		plan := gateCall("", "echo", json.RawMessage(`{}`))
		plan.runTool = gateMustGet(t, reg, "echo")
		if _, handled := a.baseToolCall(ctx, plan); handled {
			t.Fatal("call without id took the gate")
		}
	})

	// S1c split the old "dead channel fails the call without local rerun"
	// case in two: a lifecycle-managed client DEGRADES when its channel dies
	// (design C6 — the gate must then take the pre-S1 local path, because
	// nothing was ever sent), while the double-execution property itself is
	// about a call that already went out. Both halves are asserted below.
	t.Run("dead channel degrades the gate to the local path", func(t *testing.T) {
		var localRuns atomic.Int32
		target := &gatePlainTool{name: "echo", runs: &localRuns, label: "local"}
		reg := tool.NewRegistry()
		reg.Add(target)
		remote, kill := newAgentRemoteBaseWithKill(t, &baseproc.RegistrySurface{Reg: tool.NewRegistry()})
		kill() // pipe EOF: what a subprocess crash looks like from the client

		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) && remote.Mode() != baseproc.ModeInline {
			time.Sleep(5 * time.Millisecond)
		}
		if remote.Mode() != baseproc.ModeInline {
			t.Fatalf("mode = %q 3s after the channel died, want inline (matrix C6)", remote.Mode())
		}
		a := &Agent{svc: agentServices{tools: reg, base: remote}}
		plan := gateCall("c1", "echo", json.RawMessage(`{}`))
		plan.runTool = gateMustGet(t, reg, "echo")

		if _, handled := a.baseToolCall(ctx, plan); handled {
			t.Fatal("degraded gate routed a call at a dead channel")
		}
		if localRuns.Load() != 0 {
			t.Fatalf("local tool ran %d times inside the gate, want 0 (the caller runs it once)", localRuns.Load())
		}
		// Degraded, not released: the view still answers in-process.
		if err := remote.Ping(ctx); err != nil {
			t.Fatalf("ping on a degraded view: %v", err)
		}
	})

	t.Run("in-flight call dies without a local rerun", func(t *testing.T) {
		// The safety property itself: a call that HAS been sent must fail when
		// the channel dies mid-execution — never fall back and execute twice.
		started := make(chan struct{})
		release := make(chan struct{})
		t.Cleanup(func() {
			select {
			case <-release: // already open
			default:
				close(release)
			}
		})
		serverReg := tool.NewRegistry()
		serverReg.Add(&gateBlockingTool{name: "echo", started: started, release: release})
		remote, kill := newAgentRemoteBaseWithKill(t, &baseproc.RegistrySurface{Reg: serverReg})

		var localRuns atomic.Int32
		reg := tool.NewRegistry()
		reg.Add(&gatePlainTool{name: "echo", runs: &localRuns, label: "local"})
		a := &Agent{svc: agentServices{tools: reg, base: remote}}
		plan := gateCall("c1", "echo", json.RawMessage(`{}`))
		plan.runTool = gateMustGet(t, reg, "echo")

		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		type outcome struct {
			out     baseToolOutcome
			handled bool
		}
		result := make(chan outcome, 1)
		go func() {
			out, handled := a.baseToolCall(callCtx, plan)
			result <- outcome{out: out, handled: handled}
		}()

		select {
		case <-started: // the request is on the wire, executing in the subprocess
		case <-callCtx.Done():
			t.Fatal("the remote tool call never started")
		}
		kill()

		var got outcome
		select {
		case got = <-result:
		case <-callCtx.Done():
			t.Fatal("in-flight call did not return when the channel died")
		}
		if !got.handled {
			t.Fatal("in-flight call fell back local: double-execution risk")
		}
		if got.out.err == nil {
			t.Fatal("in-flight call on a dead channel returned no error")
		}
		if localRuns.Load() != 0 {
			t.Fatalf("local tool ran %d times after a sent call, want 0", localRuns.Load())
		}
	})
}

// gateBlockingTool parks inside Execute until release is closed — the shape a
// minute-long remote tool run has while the channel is killed underneath it.
type gateBlockingTool struct {
	name    string
	started chan<- struct{}
	release <-chan struct{}
}

func (t *gateBlockingTool) Name() string            { return t.name }
func (t *gateBlockingTool) Description() string     { return "blocking stub " + t.name }
func (t *gateBlockingTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *gateBlockingTool) ReadOnly() bool          { return false }
func (t *gateBlockingTool) Execute(ctx context.Context, _ json.RawMessage) (string, error) {
	close(t.started)
	select {
	case <-t.release:
		return "blocked:done", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// gateEventRecorder captures emitted events for the progress assertions.
type gateEventRecorder struct {
	mu     sync.Mutex
	events []event.Event
}

func (r *gateEventRecorder) Emit(e event.Event) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
}

func (r *gateEventRecorder) outputs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.events))
	for _, e := range r.events {
		out = append(out, e.Tool.Output)
	}
	return out
}

func (r *gateEventRecorder) eventAt(i int) event.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.events[i]
}
