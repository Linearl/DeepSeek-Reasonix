package control

// 任务 710 tests: the redundant snapshot-save gate. Redundant savers
// (desktop TurnDone autosave, action-time snapshotTab) coalesce behind a
// fresh durable save; pending rewrites and gap expiry still write.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/store"
)

func osStat(path string) (os.FileInfo, error) { return os.Stat(path) }

// immediateRunner appends the turn content and finishes instantly, so the
// turn-end durability boundary (finishInFlightTurn) lands a real save and
// records the coalesce gate.
type immediateRunner struct{ session *agent.Session }

func (r immediateRunner) Run(_ context.Context, input string) error {
	r.session.Add(provider.Message{Role: provider.RoleUser, Content: input})
	r.session.Add(provider.Message{Role: provider.RoleAssistant, Content: "ack"})
	return nil
}

func TestSnapshotCoalescesRedundantSavesWithinGap(t *testing.T) {
	oldGap := snapshotSaveMinGap.Load()
	snapshotSaveMinGap.Store(int64(600 * time.Millisecond))
	defer snapshotSaveMinGap.Store(oldGap)

	dir := t.TempDir()
	sess := agent.NewSession("sys")
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	path := filepath.Join(dir, "session.jsonl")
	c := New(Options{Runner: immediateRunner{session: sess}, Executor: exec, SessionDir: dir, SessionPath: path, Sink: event.Discard})

	if err := c.RunTurn(context.Background(), "t710 seed"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	// Wait for the turn-end snapshot (and its gate record) to finish.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if !c.snapshotSaveRecentlyDurable(c.SessionPath()) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		break
	}
	if !c.snapshotSaveRecentlyDurable(c.SessionPath()) {
		t.Fatal("turn-end durable save did not arm the coalesce gate")
	}

	before, err := osStat(c.SessionPath())
	if err != nil {
		t.Fatalf("stat transcript: %v", err)
	}
	// Within the gap a redundant Snapshot coalesces: nothing is rewritten.
	if err := c.Snapshot(); err != nil {
		t.Fatalf("gated Snapshot: %v", err)
	}
	after, err := osStat(c.SessionPath())
	if err != nil {
		t.Fatalf("stat transcript after gated Snapshot: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("Snapshot within the gap rewrote the transcript; gate did not coalesce")
	}

	// Past the gap the redundant save goes through again — and because the
	// session grew, it must actually write this time.
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "t710 tail"})
	time.Sleep(700 * time.Millisecond)
	if c.snapshotSaveRecentlyDurable(c.SessionPath()) {
		t.Fatal("gate still armed after the gap expired")
	}
	if err := c.Snapshot(); err != nil {
		t.Fatalf("Snapshot after gap: %v", err)
	}
	final, err := osStat(c.SessionPath())
	if err != nil {
		t.Fatalf("stat transcript after gap Snapshot: %v", err)
	}
	if final.ModTime().Equal(after.ModTime()) {
		t.Fatal("Snapshot after the gap must persist again")
	}
}

func TestSnapshotGateNeverSkipsPendingRewrite(t *testing.T) {
	oldGap := snapshotSaveMinGap.Load()
	snapshotSaveMinGap.Store(int64(time.Hour))
	defer snapshotSaveMinGap.Store(oldGap)

	dir := t.TempDir()
	sess := agent.NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "t710 rewrite probe"})
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	path := filepath.Join(dir, "session.jsonl")
	c := New(Options{Executor: exec, SessionDir: dir, SessionPath: path, Sink: event.Discard})

	// 任务 748: the gate only arms for a path whose file still exists, so the
	// rewrite-valve scenario seeds a real transcript first.
	if err := c.SnapshotDurable(); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
	c.recordDurableSnapshot(c.SessionPath(), time.Now())
	if !c.snapshotSaveRecentlyDurable(c.SessionPath()) {
		t.Fatal("fresh durable record must arm the gate")
	}
	// A pending rewrite (compaction/rewrite landed in memory, not on disk)
	// must never coalesce: the disk transcript is the wrong shape.
	sess.IncrementRewrite()
	if c.snapshotSaveRecentlyDurable(c.SessionPath()) {
		t.Fatal("gate skipped with a pending rewrite")
	}
}

func TestSnapshotGateIsPathScoped(t *testing.T) {
	oldGap := snapshotSaveMinGap.Load()
	snapshotSaveMinGap.Store(int64(time.Hour))
	defer snapshotSaveMinGap.Store(oldGap)

	dir := t.TempDir()
	c := New(Options{SessionDir: dir, SessionPath: filepath.Join(dir, "a.jsonl")})
	c.recordDurableSnapshot(filepath.Join(dir, "other.jsonl"), time.Now())
	if c.snapshotSaveRecentlyDurable(c.SessionPath()) {
		t.Fatal("a record for another path must not arm this path's gate")
	}
}

// 任务 748: the gate's "at most one gap stale" argument collapses when the
// session file vanished externally (the save is also what detects the removal
// and forks recovery) or when turn markers are queued but not yet persisted
// (they are the schema-2 crash contract the skip itself leans on).
func TestSnapshotGateNeverSkipsRemovedFileOrPendingMarkers(t *testing.T) {
	oldGap := snapshotSaveMinGap.Load()
	snapshotSaveMinGap.Store(int64(time.Hour))
	defer snapshotSaveMinGap.Store(oldGap)

	dir := t.TempDir()
	sess := agent.NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "t748 seed"})
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	path := filepath.Join(dir, "session.jsonl")
	c := New(Options{Executor: exec, SessionDir: dir, SessionPath: path, Sink: event.Discard})
	if err := c.Snapshot(); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
	c.recordDurableSnapshot(path, time.Now())
	if !c.snapshotSaveRecentlyDurable(path) {
		t.Fatal("fresh durable record must arm the gate")
	}

	// Pending turn markers: the gate must not skip the save that would carry
	// them into the log.
	if !exec.Session().QueueTurnBegin("t748-marker", true) {
		t.Fatal("session must be schema 2 after its first save")
	}
	if !exec.Session().HasPendingTurnMarkers() {
		t.Fatal("queued marker must report pending")
	}
	if c.snapshotSaveRecentlyDurable(path) {
		t.Fatal("gate skipped with pending turn markers")
	}

	// External removal: the gate must fall through so the save detects the
	// removal and forks the stable recovery branch. Persist first so the
	// queued marker from the previous step does not pin the valve.
	if err := c.SnapshotDurable(); err != nil {
		t.Fatalf("persist markers: %v", err)
	}
	c.recordDurableSnapshot(path, time.Now())
	if !c.snapshotSaveRecentlyDurable(path) {
		t.Fatal("gate must re-arm after the marker flush")
	}
	for _, artifact := range append([]string{path}, store.SessionSidecarFiles(path)...) {
		if artifact != "" {
			_ = os.Remove(artifact)
		}
	}
	if c.snapshotSaveRecentlyDurable(path) {
		t.Fatal("gate skipped with the session file removed externally")
	}
}

func TestMidTurnEffectiveIntervalBacksOffOnCost(t *testing.T) {
	base := 30 * time.Second
	cases := []struct {
		lastSaveMs int64
		want       time.Duration
	}{
		{0, base},                       // no measurement yet: base
		{700, base},                     // cheap save: base
		{750, base},                     // exactly the floor: still one base interval per unit
		{1500, 2 * base},                // 2x the floor: 2x
		{2250, 3 * base},                // 3x
		{7500, 10 * base},               // factor saturates at 10x (= the 5m cap for a 30s base)
		{750000, midTurnSaveBackoffCap}, // far past the cap: capped
	}
	for _, tc := range cases {
		if got := midTurnEffectiveInterval(base, tc.lastSaveMs); got != tc.want {
			t.Fatalf("midTurnEffectiveInterval(base, %dms) = %v, want %v", tc.lastSaveMs, got, tc.want)
		}
	}
}
