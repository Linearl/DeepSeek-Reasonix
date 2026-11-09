package control

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1514 验收①的循环场景钉: a turn stuck in the repeating read loop (the
// deepseek-flash field shape) must return to idle within 5s of a stop press —
//任一级。The graceful (L1) cancel already covers the cooperative case: the
// provider stream checks the turn context before every round, executeBatch
// checks it between batches, and the sampling layer returns ctx.Err() ahead of
// every retry classification. This钉 pins that end to end through a real
// Submit + real agent loop, so a regression that lets the loop survive a
// cancel turns red here instead of in the field again.

// loopingToolCallProvider answers every round with the SAME tool call until
// the turn context fires — the never-finishing-on-its-own loop shape.
type loopingToolCallProvider struct {
	entered atomic.Bool
	rounds  atomic.Int64
}

func (p *loopingToolCallProvider) Name() string { return "looping" }

func (p *loopingToolCallProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.entered.Store(true)
	n := p.rounds.Add(1)
	ch := make(chan provider.Chunk, 2)
	ch <- provider.Chunk{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{
		ID:        fmt.Sprintf("call-%d", n),
		Name:      "read_file",
		Arguments: `{"path":"confirm-image.txt"}`,
	}}
	close(ch)
	return ch, nil
}

// loopingReadTool is the loop's read: it always succeeds with the same bytes.
type loopingReadTool struct{ calls atomic.Int64 }

func (t *loopingReadTool) Name() string        { return "read_file" }
func (t *loopingReadTool) Description() string { return "test stand-in read" }
func (t *loopingReadTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)
}
func (t *loopingReadTool) ReadOnly() bool { return true }
func (t *loopingReadTool) Execute(context.Context, json.RawMessage) (string, error) {
	t.calls.Add(1)
	return "same bytes every time", nil
}

func newLoopingController(t *testing.T) (*Controller, *loopingToolCallProvider, *loopingReadTool) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "loop.jsonl")
	writer, err := agent.AcquireSessionWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	prov := &loopingToolCallProvider{}
	rd := &loopingReadTool{}
	reg := tool.NewRegistry()
	reg.Add(rd)
	exec := agent.New(prov, reg, agent.NewSession("sys"), agent.Options{}, event.Discard)
	if err := writer.Bind(exec.Session(), agent.NextSessionWriteGeneration()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(writer.Release)
	sink, _, _ := collectSink()
	c := New(Options{
		Runner:      exec,
		Executor:    exec,
		Sink:        sink,
		SessionDir:  dir,
		SessionPath: path,
	})
	t.Cleanup(func() { c.Close(); c.autosaveWG.Wait() })
	return c, prov, rd
}

func TestCancelStopsToolLoopWithinFiveSeconds(t *testing.T) {
	c, prov, rd := newLoopingController(t)

	c.Submit("confirm the image path and read it")
	deadline := time.Now().Add(controlTestReceiveWindow)
	for {
		if prov.entered.Load() && rd.calls.Load() >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the tool loop never started — provider/tool never ran")
		}
		time.Sleep(time.Millisecond)
	}
	if !c.RuntimeStatus().Running {
		t.Fatal("the looping turn must report running before the stop")
	}

	stopAt := time.Now()
	c.Cancel() // the bare graceful entry —任一级 must reach idle
	idleDeadline := stopAt.Add(5 * time.Second)
	for {
		if !c.RuntimeStatus().Running {
			break
		}
		if time.Now().After(idleDeadline) {
			t.Fatalf("the looping turn stayed running past the 5s bound (took >%v to reach idle)", time.Since(stopAt))
		}
		time.Sleep(5 * time.Millisecond)
	}
	if elapsed := time.Since(stopAt); elapsed > 5*time.Second {
		t.Fatalf("stop→idle took %v, want ≤5s", elapsed)
	}
	// The ladder dies with the turn (finishGuardedTurn's 任务461-P7 reset): the
	// next stop starts fresh and no armed mirror survives into idle.
	if st := c.RuntimeStatus(); st.StopLevel != StopLevelNone {
		t.Fatalf("stop level after the loop stopped = %d, want None", st.StopLevel)
	}
}
