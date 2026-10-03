package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// 任务461-P7 L3 验收：假工具忽略 ctx cancel（goroutine 类无法杀）→ 执行器在
// force 信号到来时放弃等待（结果记为效果未知）、工具 goroutine 进入 straggler
// 隔离（下轮 drain 有界等待），UI（batch 返回）立即释放；无 force 通道时保持
// 原行为（等待到底，不放弃）。

// stubbornWriteTool is the SEQUENTIAL (mutating) face of the P7 scenario: a
// tool that ignores its context entirely — only release lets Execute return.
// (parallel_cancel_test.go's stubbornTool covers the read-only parallel face.)
type stubbornWriteTool struct {
	name    string
	started chan struct{}
	release chan struct{}
}

func (t stubbornWriteTool) Name() string        { return t.name }
func (t stubbornWriteTool) Description() string { return "" }
func (t stubbornWriteTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (t stubbornWriteTool) ReadOnly() bool { return false }
func (t stubbornWriteTool) Execute(ctx context.Context, _ json.RawMessage) (string, error) {
	close(t.started)
	<-t.release
	return t.name + " done", nil
}

func newStubborn(name string) stubbornWriteTool {
	return stubbornWriteTool{name: name, started: make(chan struct{}), release: make(chan struct{})}
}

// L3: force fires while a sequential tool ignores cancellation → the batch
// returns promptly with the unknown-effect face, and the tool goroutine is
// quarantined as a live straggler until it actually exits.
func TestExecuteBatchForceAbandonsStubbornTool(t *testing.T) {
	stubborn := newStubborn("stubborn_write")
	reg := tool.NewRegistry()
	reg.Add(stubborn)
	a := New(nil, reg, NewSession(""), Options{}, event.Discard)

	// Mirror the admission wiring: turn and force contexts are INDEPENDENT
	// (same empty base), so a plain cancel never fires the force half.
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	forceDone := make(chan struct{})
	ctx := WithStopForce(base, forceDone)

	batchDone := make(chan batchExecution, 1)
	go func() {
		batchDone <- a.executeBatch(ctx, &a.turn, []provider.ToolCall{{Name: "stubborn_write"}})
	}()
	<-stubborn.started

	// L1: plain cancel must NOT abandon the call — the tool keeps its
	// graceful-exit window (the batch stays put).
	cancel()
	select {
	case b := <-batchDone:
		t.Fatalf("a plain cancel must not abandon a stubborn sequential tool, got %+v", b)
	case <-time.After(120 * time.Millisecond):
	}

	// L3: force fires → the executor stops waiting immediately.
	close(forceDone)
	var batch batchExecution
	select {
	case batch = <-batchDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the force signal did not release the executor")
	}
	if len(batch.results) != 1 || !strings.Contains(batch.results[0], "force-stopped") {
		t.Fatalf("abandoned call must carry the unknown-effect face, got %q", batch.results)
	}
	if live := a.stragglers.live.Load(); live != 1 {
		t.Fatalf("the abandoned tool goroutine must be quarantined as a live straggler, got %d", live)
	}

	// The quarantined goroutine eventually drains once the tool exits.
	close(stubborn.release)
	deadline := time.Now().Add(2 * time.Second)
	for a.stragglers.live.Load() > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if live := a.stragglers.live.Load(); live != 0 {
		t.Fatalf("the quarantined straggler must drain after the tool exits, got %d", live)
	}
}

// No force half (WithStopForce absent — older hosts): the pre-P7 wait-forever
// behavior is preserved byte for byte.
func TestExecuteBatchWithoutForceSignalKeepsWaiting(t *testing.T) {
	stubborn := newStubborn("stubborn_write")
	reg := tool.NewRegistry()
	reg.Add(stubborn)
	a := New(nil, reg, NewSession(""), Options{}, event.Discard)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	batchDone := make(chan batchExecution, 1)
	go func() {
		batchDone <- a.executeBatch(ctx, &a.turn, []provider.ToolCall{{Name: "stubborn_write"}})
	}()
	<-stubborn.started
	cancel()
	select {
	case b := <-batchDone:
		t.Fatalf("without the force half a cancelled stubborn tool must still wedge the batch (pre-P7 behavior), got %+v", b)
	case <-time.After(120 * time.Millisecond):
	}
	close(stubborn.release)
	select {
	case b := <-batchDone:
		if len(b.results) != 1 || !strings.Contains(b.results[0], "stubborn_write done") {
			t.Fatalf("unexpected result after release: %v", b.results)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the batch never returned after the tool finished")
	}
}

// A tool that honors its context is untouched: normal results flow, no
// straggler left behind.
func TestExecuteBatchCooperativeToolUnaffectedByForceWiring(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "read_file", readOnly: true})
	a := New(nil, reg, NewSession(""), Options{}, event.Discard)

	forceCtx, forceCancel := context.WithCancel(context.Background())
	defer forceCancel()
	ctx := WithStopForce(context.Background(), forceCtx.Done())
	batch := a.executeBatch(ctx, &a.turn, []provider.ToolCall{{Name: "read_file"}})
	if len(batch.results) != 1 || !strings.Contains(batch.results[0], "read_file done") {
		t.Fatalf("cooperative tool results wrong: %v", batch.results)
	}
	if live := a.stragglers.live.Load(); live != 0 {
		t.Fatalf("no straggler must remain for a finished call, got %d", live)
	}
}
