package boot

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"reasonix/internal/baseproc"
	"reasonix/internal/config"
	"reasonix/internal/tool"
)

// sharedBasePipe adapts one read end and one write end to the framed channel
// (test helpers stay package-local — control and agent each carry their own).
type sharedBasePipe struct {
	r io.ReadCloser
	w io.WriteCloser
}

func (p *sharedBasePipe) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *sharedBasePipe) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *sharedBasePipe) Close() error                { return errors.Join(p.r.Close(), p.w.Close()) }

// resetSharedBaseForTest drops the process-wide manager (closing it) before
// and after the test, so no test inherits — or leaks — another one's base.
func resetSharedBaseForTest(t *testing.T) {
	t.Helper()
	drop := func() {
		sharedBaseMu.Lock()
		m := sharedBase
		sharedBase = nil
		sharedBaseMu.Unlock()
		if m != nil {
			_ = m.Close()
		}
	}
	drop()
	t.Cleanup(drop)
}

// TestSharedBaseClientSpawnsOneSubprocessForManyBoots pins the S1b audit
// note this slice exists for: N switch-on builds must converge on ONE resident
// subprocess, and that subprocess must outlive any individual view (the pool
// holds the reference), not be torn down with the last tab.
func TestSharedBaseClientSpawnsOneSubprocessForManyBoots(t *testing.T) {
	resetSharedBaseForTest(t)

	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	server := baseproc.NewServer("boot-shared-test")
	served := make(chan error, 1)
	go func() { served <- server.Serve(context.Background(), serverIn, serverOut) }()
	t.Cleanup(func() {
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = serverIn.Close()
		_ = serverOut.Close()
		select {
		case <-served:
		case <-time.After(5 * time.Second):
			t.Log("shared-base serve loop did not exit in 5s")
		}
	})

	var mu sync.Mutex
	spawns := 0
	opts := baseproc.Options{
		Enabled:       true,
		ServerVersion: "v-boot",
		Dial: func(context.Context) (io.ReadWriteCloser, func(), error) {
			mu.Lock()
			spawns++
			n := spawns
			mu.Unlock()
			if n > 1 {
				// The whole point: a second spawn means the pool failed to
				// converge and tabs are back to N subprocesses each.
				return nil, nil, errors.New("second spawn: builds did not share the manager")
			}
			return &sharedBasePipe{r: clientIn, w: clientOut}, func() {}, nil
		},
		// Keep the supervisor out of the way: no health churn, no respawn.
		HealthInterval:   time.Hour,
		RestartBaseDelay: time.Hour,
		RestartMaxDelay:  time.Hour,
	}

	first := sharedBaseClient(context.Background(), opts)
	second := sharedBaseClient(context.Background(), opts)

	mu.Lock()
	observed := spawns
	mu.Unlock()
	if observed != 1 {
		t.Fatalf("spawn attempts = %d, want exactly 1 (N boots share one subprocess)", observed)
	}
	if first.Mode() != baseproc.ModeRemote || second.Mode() != baseproc.ModeRemote {
		t.Fatalf("modes = %q/%q, want remote/remote", first.Mode(), second.Mode())
	}

	// Releasing every view must NOT take the resident base down.
	if err := first.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	sharedBaseMu.Lock()
	pooled := sharedBase
	sharedBaseMu.Unlock()
	if pooled == nil {
		t.Fatal("pool dropped the manager")
	}
	if pooled.Closed() {
		t.Fatal("closing the last view shut the shared base down — the pool must hold its own reference (resident base)")
	}
	mu.Lock()
	after := spawns
	mu.Unlock()
	if after != 1 {
		t.Fatalf("spawn attempts = %d after closing every view, want 1 (no respawn of a resident base)", after)
	}
}

// TestSharedBaseClientSwitchOffNeverBuildsAManager is the R4 default-path
// half: with the switch off, startBaseClient must not touch the pool at all.
func TestSharedBaseClientSwitchOffNeverBuildsAManager(t *testing.T) {
	resetSharedBaseForTest(t)

	client := startBaseClient(context.Background(), &config.Config{}, tool.NewRegistry())
	if client.Mode() != baseproc.ModeInline {
		t.Fatalf("mode = %q, want inline with the switch off", client.Mode())
	}
	sharedBaseMu.Lock()
	pooled := sharedBase
	sharedBaseMu.Unlock()
	if pooled != nil {
		t.Fatal("switch-off build created a shared manager — R4 says zero side effects")
	}
}
