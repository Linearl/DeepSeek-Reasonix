package control

// P18-R3 tests: the mid-turn autosave tick is non-blocking. While the
// save-path lock is held by someone else the tick declines quietly (no warn,
// no queueing), and the next tick after release lands the snapshot. The
// existing TestMidTurnAutosavePersistsDuringLongTurn covers the uncontended
// shape.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
)

func TestP18MidTurnAutosaveSkipsWhilePathBusyThenRecovers(t *testing.T) {
	old := midTurnSnapshotInterval.Load()
	midTurnSnapshotInterval.Store(int64(10 * time.Millisecond))
	defer midTurnSnapshotInterval.Store(old)

	dir := t.TempDir()
	sess := agent.NewSession("sys")
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	path := filepath.Join(dir, "session.jsonl")

	release := make(chan struct{})
	c := New(Options{Runner: blockingRunner{session: sess, release: release}, Executor: exec, SessionDir: dir, SessionPath: path, Label: "p18"})
	defer c.autosaveWG.Wait()
	defer close(release)

	hold := agent.HoldSessionSavePathForTest(path)
	released := false
	defer func() {
		if !released {
			hold()
		}
	}()

	c.Send("p18 busy-window probe")

	// While the path is busy the tick must decline: nothing durable lands.
	deadline := time.Now().Add(700 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			t.Fatal("mid-turn autosave wrote while the save-path lock was held elsewhere")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Release exactly once (a second release would race a mid-save goroutine's
	// own unlock of the same path mutex) and let the next tick through.
	hold()
	released = true
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), "p18 busy-window probe") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("mid-turn autosave did not recover after the save-path lock was released")
}
