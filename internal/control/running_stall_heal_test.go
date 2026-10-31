package control

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1288 tests: the fourth 579-family shape — a RUNNING turn whose body
// wedges (provider stream goes silent, executor stuck) never runs
// finishGuardedTurn, so c.running stays true forever. The 303 self-heal rides
// admitGuardedTurn (needs a submitter an unattended session does not have)
// and the 579 bridge reads skip:running and moves on — the 20261031 01:07
// production scene: 11+ minutes of green dot + work counter on a transcript
// with no events, entity on disk so the 1266b projection never engaged.
//
// These tests pin the production shape end to end: a real Submit through a
// real session (ledger stamps included), a provider whose stream channel
// never yields (the wedged body), the heal, and the stale-completion guard
// that keeps the wedged turn's late finish from touching the reopened gate.

// wedgedStreamProvider yields a stream channel that never produces a chunk:
// the provider died mid-turn and the turn body blocks on it forever.
type wedgedStreamProvider struct {
	mu      sync.Mutex
	entered bool
	chans   []chan provider.Chunk
}

func (p *wedgedStreamProvider) Name() string { return "wedged" }

func (p *wedgedStreamProvider) Stream(context.Context, provider.Request) (<-chan provider.Chunk, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.entered = true
	ch := make(chan provider.Chunk)
	p.chans = append(p.chans, ch)
	return ch, nil
}

// release closes every stream channel handed out, letting the wedged body
// return through the normal error path (the "late completion" of production).
func (p *wedgedStreamProvider) release() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, ch := range p.chans {
		close(ch)
	}
	p.chans = nil
}

func newWedgedRunningController(t *testing.T) (*Controller, *wedgedStreamProvider, chan event.Event) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "wedged.jsonl")
	writer, err := agent.AcquireSessionWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	prov := &wedgedStreamProvider{}
	reg := tool.NewRegistry()
	exec := agent.New(prov, reg, agent.NewSession("sys"), agent.Options{}, event.Discard)
	if err := writer.Bind(exec.Session(), agent.NextSessionWriteGeneration()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(writer.Release)
	sink, done, _ := collectSink()
	c := New(Options{
		Runner:      exec,
		Executor:    exec,
		Sink:        sink,
		SessionDir:  dir,
		SessionPath: path,
	})
	t.Cleanup(func() { prov.release(); c.Close(); c.autosaveWG.Wait() })
	return c, prov, done
}

// TestHealStalledRunningReopensWedgedTurn pins the full production sequence:
// submit → provider wedges → running with a quiet ledger → heal reopens the
// gate → the late completion is recognized stale → a fresh turn admits.
func TestHealStalledRunningReopensWedgedTurn(t *testing.T) {
	c, prov, done := newWedgedRunningController(t)

	c.Submit("carry the interrupted work to completion")
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.mu.Lock()
		entered := prov.entered
		c.mu.Unlock()
		if entered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("provider stream never entered — turn did not start")
		}
		time.Sleep(time.Millisecond)
	}
	if !c.RuntimeStatus().Running {
		t.Fatal("wedged turn must report running (the phantom busy state)")
	}

	// The liveness probe reads real ledger stamps: fresh activity is not
	// quiet, so nothing may heal yet.
	if _, ok := c.RunningQuietFor(time.Now()); !ok {
		t.Fatal("a running turn with ledger stamps must report a quiet duration")
	}
	if healed, _ := c.HealStalledRunning(time.Hour); healed {
		t.Fatal("a freshly active turn must not heal (quiet < threshold)")
	}

	// The wedge: no further events. Past the threshold the heal fires.
	time.Sleep(80 * time.Millisecond)
	healed, stalled := c.HealStalledRunning(60 * time.Millisecond)
	if !healed {
		t.Fatal("a running turn quiet past the threshold must heal")
	}
	if stalled < 60*time.Millisecond {
		t.Fatalf("stalled = %v, want ≥ threshold", stalled)
	}
	if st := c.RuntimeStatus(); st.Running {
		t.Fatalf("post-heal status still claims work: %+v", st)
	}
	if _, ok := c.RunningQuietFor(time.Now()); ok {
		t.Fatal("after the heal no turn is running — the probe must say so")
	}

	// The wedged body eventually returns (provider channel closed). Its
	// completion is stale (generation bumped at the heal), so finishGuardedTurn
	// takes the stale-completion branch — no TurnDone is published (that is the
	// 303 contract: a dead turn must not emit over the reopened gate), the
	// gate stays idle, and the event channel stays quiet.
	prov.release()
	waitIdleAdmission(t, c)
	select {
	case e := <-done:
		if e.Kind == event.TurnDone {
			t.Fatal("the stale completion must not publish a TurnDone over the healed gate")
		}
	case <-time.After(300 * time.Millisecond):
	}

	// The gate actually reopens: a fresh submit runs as a new turn.
	bodyRan := make(chan struct{}, 1)
	if got := c.runGuarded(func(context.Context) error {
		bodyRan <- struct{}{}
		return nil
	}); got != turnStarted {
		t.Fatalf("admission after heal = %v, want turnStarted", got)
	}
	select {
	case <-bodyRan:
	case <-time.After(5 * time.Second):
		t.Fatal("fresh body did not run after the heal")
	}
	waitIdleAdmission(t, c)
}

// TestHealStalledRunningRefusesIdleController pins the trivial negative.
func TestHealStalledRunningRefusesIdleController(t *testing.T) {
	c := New(Options{})
	defer c.Close()
	if _, ok := c.RunningQuietFor(time.Now()); ok {
		t.Fatal("an idle controller has no running turn to probe")
	}
	if healed, _ := c.HealStalledRunning(time.Millisecond); healed {
		t.Fatal("an idle controller must not heal")
	}
}
