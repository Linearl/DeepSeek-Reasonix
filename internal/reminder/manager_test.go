package reminder

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/store"
)

type deliveryLog struct {
	mu    sync.Mutex
	seen  []Reminder
	paths []string
}

func (l *deliveryLog) record(path string, r Reminder) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seen = append(l.seen, r)
	l.paths = append(l.paths, path)
}

func (l *deliveryLog) snapshot() []Reminder {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Reminder(nil), l.seen...)
}

func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

// TestSyncDeliversOverdueWithLateAnnotation is the restart catch-up face:
// an entry whose due time passed while the process was down fires immediately
// on the bind scan, marked FiredLate so the injected notice can say so.
func TestSyncDeliversOverdueWithLateAnnotation(t *testing.T) {
	session := testSession(t)
	now := time.Now()
	Set(session, "missed while restarting", now.Add(-time.Minute), now.Add(-2*time.Minute))

	log := &deliveryLog{}
	m := NewManager(log.record)
	defer m.Close()
	m.Sync(session)

	waitFor(t, 2*time.Second, func() bool { return len(log.snapshot()) == 1 })
	got := log.snapshot()[0]
	if !got.FiredLate {
		t.Fatalf("overdue delivery not marked late: %+v", got)
	}
	if got.Message != "missed while restarting" {
		t.Fatalf("message = %q", got.Message)
	}
}

// TestSyncArmsFutureEntry covers the re-arm face: a pending future entry
// survives the process restart (it is read back from disk on Sync) and fires
// on time, unmarked.
func TestSyncArmsFutureEntry(t *testing.T) {
	session := testSession(t)
	now := time.Now()
	r, err := Set(session, "future alarm", now.Add(120*time.Millisecond), now)
	if err != nil {
		t.Fatal(err)
	}

	log := &deliveryLog{}
	m := NewManager(log.record)
	defer m.Close()
	m.Sync(session)

	waitFor(t, 3*time.Second, func() bool { return len(log.snapshot()) == 1 })
	got := log.snapshot()[0]
	if got.ID != r.ID || got.FiredLate {
		t.Fatalf("delivery = %+v want id %s on-time", got, r.ID)
	}
	// Consumed entries leave the store (the controller removes them; here we
	// pin that no further deliveries happen after the single fire).
	time.Sleep(80 * time.Millisecond)
	if n := len(log.snapshot()); n != 1 {
		t.Fatalf("delivered %d times, want 1", n)
	}
}

// TestCancelBeforeFireMeansNoDelivery: cancel removes the durable entry, so
// the armed timer firing into a cancelled id is a no-op at delivery time.
func TestCancelBeforeFireMeansNoDelivery(t *testing.T) {
	session := testSession(t)
	now := time.Now()
	r, _ := Set(session, "will be cancelled", now.Add(80*time.Millisecond), now)

	log := &deliveryLog{}
	m := NewManager(log.record)
	defer m.Close()
	m.Track(session, r)
	m.Forget(r.ID) // the controller cancel path disarms the timer

	// And even if the timer had already fired, the controller's re-read sees
	// the entry gone (Cancel below) and drops the delivery.
	if _, err := Cancel(session, r.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if n := len(log.snapshot()); n != 0 {
		t.Fatalf("cancelled reminder delivered %+v", log.snapshot())
	}
}

func TestCloseStopsTimers(t *testing.T) {
	session := testSession(t)
	now := time.Now()
	r, _ := Set(session, "after close", now.Add(80*time.Millisecond), now)

	log := &deliveryLog{}
	m := NewManager(log.record)
	m.Track(session, r)
	m.Close()
	time.Sleep(250 * time.Millisecond)
	if n := len(log.snapshot()); n != 0 {
		t.Fatalf("delivery after Close: %+v", log.snapshot())
	}
}

func TestSyncCorruptQueueDoesNotPanic(t *testing.T) {
	session := testSession(t)
	dir := store.SessionRemindersDir(session)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	log := &deliveryLog{}
	m := NewManager(log.record)
	defer m.Close()
	m.Sync(session) // must log and return, not panic
	m.Sync("")      // empty path: no-op
	time.Sleep(30 * time.Millisecond)
	if n := len(log.snapshot()); n != 0 {
		t.Fatal("delivery from corrupt queue")
	}
}

func TestReSyncReplacesTimers(t *testing.T) {
	session := testSession(t)
	now := time.Now()
	r1, _ := Set(session, "first", now.Add(80*time.Millisecond), now)
	log := &deliveryLog{}
	m := NewManager(log.record)
	defer m.Close()
	m.Sync(session)
	// Restart simulation within the same process: remove the old entry, add a
	// new one, re-sync. Only the surviving entry may fire.
	Cancel(session, r1.ID)
	r2, _ := Set(session, "second", now.Add(80*time.Millisecond), now)
	m.Sync(session)
	waitFor(t, 3*time.Second, func() bool { return len(log.snapshot()) == 1 })
	if got := log.snapshot()[0]; got.ID != r2.ID {
		t.Fatalf("fired %s, want surviving %s", got.ID, r2.ID)
	}
	if !strings.HasPrefix(r2.ID, "rem_") {
		t.Fatalf("id format: %q", r2.ID)
	}
}
