package jobs

import (
	"context"
	"io"
	"testing"
	"time"

	"reasonix/internal/event"
)

// TestAfterUnfinishedWithNoJobsRunsInline pins the zero-wait branch: with
// nothing unfinished at call time, fn runs before the call returns.
func TestAfterUnfinishedWithNoJobsRunsInline(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()

	ran := false
	m.AfterUnfinished(func() { ran = true })
	if !ran {
		t.Fatal("AfterUnfinished with no jobs did not run fn synchronously")
	}
}

// TestAfterUnfinishedWaitsForSnapshotJobs (task 890) pins the handoff
// semantics: fn waits for the jobs unfinished AT CALL TIME — a job started
// after the call must NOT extend the wait.
func TestAfterUnfinishedWaitsForSnapshotJobs(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()

	releaseFirst := make(chan struct{})
	firstDone := make(chan struct{})
	m.StartForSession("after-unfin", "bash", "first", func(ctx context.Context, _ io.Writer) (string, error) {
		defer close(firstDone)
		select {
		case <-releaseFirst:
			return "ok", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})

	fnRan := make(chan struct{}, 1)
	m.AfterUnfinished(func() { fnRan <- struct{}{} })

	// A job starting after the snapshot must not hold fn hostage.
	releaseSecond := make(chan struct{})
	m.StartForSession("after-unfin", "bash", "second", func(ctx context.Context, _ io.Writer) (string, error) {
		select {
		case <-releaseSecond:
			return "ok", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	defer close(releaseSecond)

	select {
	case <-fnRan:
		t.Fatal("AfterUnfinished ran while the snapshot job was still running")
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseFirst)
	<-firstDone
	waitFor(t, func() bool {
		select {
		case <-fnRan:
			return true
		default:
			return false
		}
	})
}
