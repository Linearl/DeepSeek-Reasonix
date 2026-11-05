package control

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1394 tests: the wedge probe (HealStalledRunning / RunningQuietFor) used to
// read ONLY the turn event ledger. The synchronous turn family — the desktop
// idle-turn bridge and bot/ACP via RunInboxTurn, RunTurn, and the protocol
// recovery paths, all through runSynchronousTurn — never Begins the ledger
// (no prepareTurnAdmission on that path), so its events are invisible to
// LastActivityAt and the probe inherited whatever stamp the previous guarded
// turn had left behind. Production scenes:
//
//   - 20261103 17:01 (quiet_for=17m11s, gen=2) and 20261105 09:18/09:25
//     (quiet_for=2h48m/2h55m, glm-5.3-flash session): the sweep's heal fired
//     1-2s AFTER the bridge opened a synchronous turn and canceled it — the
//     "opening attempt 1/3 failed: context canceled" lines carry the
//     HealStalledRunning cancel stack. The UI notice is the "stopped
//     responding" message the user resends against.
//   - 20261103 18:17 (quiet_for=126h): the same inheritance across a
//     compaction-truncated ledger — records empty, LastActivityAt fell back
//     to a turnStarted restored from a five-day-old record.
//
// The fix takes the newer of the two evidence sources (ledger stamp for
// guarded turns, the sink-wide liveness tracker every event refreshes) and
// anchors the synchronous path at admission (liveness.reset beside
// c.running=true, mirroring spawnGuardedTurn). These tests pin both faces:
// a live synchronous turn on a stale ledger must NOT heal, and a genuinely
// wedged synchronous turn must still heal (tl1288's unattended rescue).

// newSyncTurnProbeController builds a controller with a session (ledger) but
// no provider traffic, for direct runSynchronousTurn probing.
func newSyncTurnProbeController(t *testing.T) (*Controller, chan event.Event) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sync.jsonl")
	writer, err := agent.AcquireSessionWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	reg := tool.NewRegistry()
	exec := agent.New(&streamlessProvider{}, reg, agent.NewSession("sys"), agent.Options{}, event.Discard)
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
	t.Cleanup(func() { c.Close(); c.autosaveWG.Wait() })
	return c, done
}

// streamlessProvider never expects to stream: the tests block the turn body
// themselves, so any provider call is a test bug.
type streamlessProvider struct{}

func (p *streamlessProvider) Name() string { return "streamless" }

func (p *streamlessProvider) Stream(context.Context, provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk)
	close(ch)
	return ch, nil
}

// TestSyncTurnOnStaleLedgerMustNotHeal reproduces the 20261103 17:01 /
// 20261105 09:18 shape in miniature: a guarded turn left ledger stamps in the
// past, a synchronous turn is admitted minutes later, and the sweep fires
// while the synchronous turn is alive and emitting events. The probe must
// read the LIVE liveness stamp (and the admission anchor), not the stale
// ledger, so the heal must not fire on a turn that is seconds old.
func TestSyncTurnOnStaleLedgerMustNotHeal(t *testing.T) {
	c, _ := newSyncTurnProbeController(t)

	// A guarded turn runs to completion, leaving ledger + liveness stamps.
	started := make(chan struct{}, 1)
	if got := c.runGuarded(func(context.Context) error {
		started <- struct{}{}
		return nil
	}); got != turnStarted {
		t.Fatalf("guarded turn admission = %v, want turnStarted", got)
	}
	<-started
	waitIdleAdmission(t, c)

	// The long idle: production had minutes-to-hours here. We cannot sleep
	// that long, so we make the comparison threshold-straddling instead:
	// after this sleep the LEDGER stamp is older than the heal threshold
	// below, so a ledger-only probe (the tl1394 bug) would judge the next
	// turn wedged at admission.
	time.Sleep(60 * time.Millisecond)
	const healThreshold = 40 * time.Millisecond

	if _, ok := c.RunningQuietFor(time.Now()); ok {
		t.Fatal("no turn is running between turns — the probe must say so")
	}

	// The synchronous turn: admitted through runSynchronousTurn (the
	// RunInboxTurn/RunTurn lifecycle) and kept alive while it emits sink
	// events — the reasoning/text deltas the wedge probe must respect.
	bodyLive := make(chan struct{})
	bodyDone := make(chan error, 1)
	go func() {
		bodyDone <- c.runSynchronousTurn(context.Background(), nil, func(ctx context.Context) error {
			close(bodyLive)
			for i := 0; i < 6; i++ {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(20 * time.Millisecond):
				}
				c.sink.Emit(event.Event{Kind: event.Text, Text: "tick"})
			}
			return nil
		})
	}()
	<-bodyLive

	// The sweep tick fires while the synchronous turn is running and its
	// events are fresh: a ledger-only probe would read quiet ≥ 60ms (the
	// pre-idle ledger stamp) ≥ threshold and abandon the turn seconds old.
	if healed, stalled := c.HealStalledRunning(healThreshold); healed {
		t.Fatalf("a live synchronous turn on a stale ledger must not heal (stalled=%v)", stalled)
	}
	quiet, ok := c.RunningQuietFor(time.Now())
	if !ok {
		t.Fatal("the running synchronous turn must report a quiet duration")
	}
	if quiet >= healThreshold {
		t.Fatalf("quiet = %v must follow the LIVE liveness stamp, not the stale ledger", quiet)
	}

	// The turn finishes normally; the gate closes without any heal.
	if err := <-bodyDone; err != nil {
		t.Fatalf("synchronous turn failed: %v", err)
	}
	waitIdleAdmission(t, c)
}

// TestWedgedSyncTurnStillHeals pins the tl1288 contract on the synchronous
// path: a synchronous turn whose body produces NOTHING (the wedged bridge
// turn of the 20261031 scene) must still be abandoned once both evidence
// sources — liveness anchored at admission, ledger untouched — agree it has
// been silent past the threshold.
func TestWedgedSyncTurnStillHeals(t *testing.T) {
	c, _ := newSyncTurnProbeController(t)

	blocked := make(chan struct{})
	bodyDone := make(chan error, 1)
	go func() {
		bodyDone <- c.runSynchronousTurn(context.Background(), nil, func(ctx context.Context) error {
			close(blocked)
			<-ctx.Done()
			return ctx.Err()
		})
	}()
	<-blocked
	if !c.RuntimeStatus().Running {
		t.Fatal("the synchronous turn must report running (the phantom busy state)")
	}

	// Fresh at admission: the anchor reset must keep the probe honest even
	// though the ledger has (near-)no stamps for this turn.
	if healed, _ := c.HealStalledRunning(time.Hour); healed {
		t.Fatal("a freshly admitted synchronous turn must not heal")
	}

	// The wedge: no events at all. Past the threshold the heal fires and the
	// stored cancel unwinds the blocked body through the diagnosis path.
	time.Sleep(80 * time.Millisecond)
	healed, stalled := c.HealStalledRunning(60 * time.Millisecond)
	if !healed {
		t.Fatal("a genuinely wedged synchronous turn must heal (tl1288 unattended rescue)")
	}
	if stalled < 60*time.Millisecond {
		t.Fatalf("stalled = %v, want ≥ threshold", stalled)
	}
	select {
	case err := <-bodyDone:
		if err == nil {
			t.Fatal("the healed synchronous turn must unwind with an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the healed synchronous turn body never returned")
	}
	waitIdleAdmission(t, c)
}
