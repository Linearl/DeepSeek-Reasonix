package jobs

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"reasonix/internal/event"
)

// 任务553：完成观察者契约——完成摘要入队后在 job goroutine 上触发、携带
// (session, job, 终态)；remove 后不再触发。
func TestCompletionObserverFiresWithSessionJobStatus(t *testing.T) {
	m := NewManager(event.Discard)
	t.Cleanup(m.Close)

	type observation struct {
		session, job string
		st           Status
	}
	var mu sync.Mutex
	var got []observation
	remove := m.AddCompletionObserver(func(sessionID, jobID string, st Status) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, observation{sessionID, jobID, st})
	})

	j := m.StartForSession("sess-a", "bash", "wake", func(_ context.Context, _ io.Writer) (string, error) {
		return "result text", nil
	})
	<-j.done
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("completion observer did not fire; got %+v", got)
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	first := got[0]
	mu.Unlock()
	if first.session != "sess-a" || first.job != j.ID || first.st != Done {
		t.Fatalf("observer = %+v, want sess-a/%s/done", first, j.ID)
	}

	// remove 后不再触发。
	remove()
	j2 := m.StartForSession("sess-a", "bash", "wake2", func(_ context.Context, _ io.Writer) (string, error) {
		return "", nil
	})
	<-j2.done
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("observer fired after remove: %+v", got)
	}
}

// peek 不清队列：完成后 true，drain 后 false；stalled 警告不算完成（不唤醒）。
func TestHasCompletedNotesForSessionPeeksWithoutDraining(t *testing.T) {
	m := NewManager(event.Discard, WithStalledWarningAfter(20*time.Millisecond))
	t.Cleanup(m.Close)

	if m.HasCompletedNotesForSession("sess-a") {
		t.Fatal("fresh manager reported pending completions")
	}
	j := m.StartForSession("sess-a", "bash", "peek", func(_ context.Context, _ io.Writer) (string, error) {
		time.Sleep(80 * time.Millisecond) // quiet past the stalled threshold
		return "", nil
	})
	<-j.done
	// 完成摘要与 stalled 警告都已入队；peek 只认终态完成。
	if !m.HasCompletedNotesForSession("sess-a") {
		t.Fatal("terminal completion not visible to the peek")
	}
	m.mu.Lock()
	stalledQueued := false
	for _, item := range m.completed {
		if item.jobID == "" {
			stalledQueued = true
		}
	}
	m.mu.Unlock()
	if !stalledQueued {
		t.Fatal("test precondition lost: the stalled warning never joined the queue")
	}
	if note := m.DrainCompletedNoteForSession("sess-a"); note == "" {
		t.Fatal("drain returned empty despite a pending completion")
	}
	if m.HasCompletedNotesForSession("sess-a") {
		t.Fatal("peek still reports completions after a full drain")
	}
}

// 隔离：其他会话的完成不进入本会话的 peek。
func TestHasCompletedNotesForSessionIsolation(t *testing.T) {
	m := NewManager(event.Discard)
	t.Cleanup(m.Close)
	j := m.StartForSession("sess-a", "bash", "a", func(_ context.Context, _ io.Writer) (string, error) {
		return "", nil
	})
	<-j.done
	if m.HasCompletedNotesForSession("sess-b") {
		t.Fatal("session B saw session A's completion")
	}
	if !m.HasCompletedNotesForSession("sess-a") {
		t.Fatal("session A cannot see its own completion")
	}
}
