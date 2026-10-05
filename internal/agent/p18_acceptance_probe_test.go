package agent

// P18 acceptance probe: the incident shape was LoadSession-family callers
// holding the save-path mutex across 25-30s full decodes while the turn's own
// persistence queued behind them (Q2: save-path lock waits of 19-40s). This
// probe replays that shape at reduced scale — a lock-holding loader loop
// (tab switch / history search) against a thread of Saves — and reports the
// save queueing time with the P18-R1 load cache on vs. off. The wall-clock
// numbers are measurement output, not assertions; the functional P18 tests
// pin correctness.

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/provider"
)

func TestP18SaveQueueUnderLoaderLoop(t *testing.T) {
	if dagLoadCacheEnabled() {
		t.Logf("P18 probe: load cache ENABLED (default)")
	} else {
		t.Logf("P18 probe: load cache DISABLED (baseline)")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "p18.jsonl")
	s := p18RoundSession(t, 700) // ≈2100 messages
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSession(path); err != nil { // fill the graph cache
		t.Fatal(err)
	}

	stop := make(chan struct{})
	loaderDone := make(chan struct{})
	go func() {
		defer close(loaderDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := LoadSession(path); err != nil { // holds the save-path lock
				t.Errorf("loader LoadSession: %v", err)
				return
			}
			time.Sleep(30 * time.Millisecond)
		}
	}()
	// Stop first, then wait: the loader only exits after stop closes (a plain
	// deferred <-loaderDone ordered before close would deadlock the test).
	defer func() {
		close(stop)
		<-loaderDone
	}()

	time.Sleep(50 * time.Millisecond)
	var samples []float64
	for i := 0; i < 8; i++ {
		s.Add(provider.Message{Role: provider.RoleUser, Content: fmt.Sprintf("queue probe %d", i)})
		start := time.Now()
		if err := s.Save(path); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
		samples = append(samples, float64(time.Since(start).Milliseconds()))
	}
	worst := samples[0]
	sum := 0.0
	for _, v := range samples {
		if v > worst {
			worst = v
		}
		sum += v
	}
	label := "on"
	if !dagLoadCacheEnabled() {
		label = "off"
	}
	hits, misses := SessionDAGLoadCacheStats()
	t.Logf("P18 save-queue under loader loop (cache=%s): saves_ms=%v worst=%.0f avg=%.0f load_cache_hits=%d misses=%d",
		label, samples, worst, sum/float64(len(samples)), hits, misses)
}
