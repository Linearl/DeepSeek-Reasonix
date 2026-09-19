package main

// Task 123 overlay-cache memory probe (2026-09-19 memory investigation).
//
// The question it answers: does the Go-side history time overlay cache retain
// memory while a session keeps appending to its event log? It measures three
// things on a real session:
//
//  1. the heap cost of one full tail read, and the cache footprint it leaves;
//  2. a burst of full reads with the cache invalidated between them — the exact
//     behaviour the pre-fix key (path+size+mtime) produced on a running turn —
//     to show whether those reads accumulate;
//  3. the append-aware path the fix introduced, to show what it retains.
//
// Run: TASK123_PROBE_DIR="<sessions dir>" TASK123_PROBE_SESSIONS="<name>" \
//      go test ./ -run TestTask123OverlayMemoryProbe -v -timeout 900s
// Skips without TASK123_PROBE_DIR so it never touches sessions unasked.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"reasonix/internal/agent"
)

func TestTask123OverlayMemoryProbe(t *testing.T) {
	base := os.Getenv("TASK123_PROBE_DIR")
	if strings.TrimSpace(base) == "" {
		t.Skip("TASK123_PROBE_DIR not set; this probe reads real sessions and only runs on demand")
	}
	for _, name := range strings.Split(os.Getenv("TASK123_PROBE_SESSIONS"), ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(base, name+".jsonl")
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("session %s: %v", path, err)
			}
			task123ProbeOverlayMemory(t, path)
		})
	}
}

func task123HeapInUse() uint64 {
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapInuse
}

func task123MiB(bytes uint64) float64 { return float64(bytes) / (1 << 20) }

// task123OverlayFootprint sums what the package-level cache actually retains:
// one id→int64 map per session entry, plus the string headers.
func task123OverlayFootprint() (entries, ids, bytes int) {
	historyTimeOverlayCache.mu.Lock()
	defer historyTimeOverlayCache.mu.Unlock()
	for _, entry := range historyTimeOverlayCache.entries {
		entries++
		bytes += len(entry.head)
		for id := range entry.times {
			ids++
			bytes += len(id) + 8
		}
	}
	return entries, ids, bytes
}

func task123ProbeOverlayMemory(t *testing.T, sessionPath string) {
	resetHistoryTimeOverlayCache()
	heapBefore := task123HeapInUse()
	times, complete := agent.LoadSessionUserTimesTail(sessionPath, historyTimeOverlayTailBudget)
	heapAfter := task123HeapInUse()
	entries, ids, retained := task123OverlayFootprint()
	t.Logf("single tail read: ids=%d complete=%v heapBefore=%.2f MiB heapAfter=%.2f MiB",
		len(times), complete, task123MiB(heapBefore), task123MiB(heapAfter))
	t.Logf("cache footprint after one read: entries=%d ids=%d retained=%d bytes (%.1f KiB)",
		entries, ids, retained, float64(retained)/1024)

	// Burst with the cache reset in between: this is what an invalidated key did
	// on every read while a turn was appending. If the per-read temporaries were
	// accumulating, HeapInuse would climb by roughly rounds x tail budget.
	resetHistoryTimeOverlayCache()
	start := task123HeapInUse()
	const rounds = 12
	totalIDs := 0
	for i := 0; i < rounds; i++ {
		got, _ := agent.LoadSessionUserTimesTail(sessionPath, historyTimeOverlayTailBudget)
		totalIDs += len(got)
		resetHistoryTimeOverlayCache()
	}
	end := task123HeapInUse()
	t.Logf("burst %d full reads (cache reset each time): idsTotal=%d heapStart=%.2f MiB heapEnd=%.2f MiB growth=%.2f MiB (tail budget=%.1f MiB each)",
		rounds, totalIDs, task123MiB(start), task123MiB(end), task123MiB(end)-task123MiB(start),
		float64(historyTimeOverlayTailBudget)/(1<<20))

	// The append-aware path: one tail read plus repeated window lookups, which is
	// what a running turn now costs (extensions only when the log actually grew).
	resetHistoryTimeOverlayCache()
	for i := 0; i < 6; i++ {
		_ = persistedUserTimesForWindow(sessionPath)
	}
	hits, misses, extends := historyTimeOverlayStats()
	entries, ids, retained = task123OverlayFootprint()
	t.Logf("append-aware path: hits=%d misses=%d extends=%d entries=%d ids=%d retained=%.1f KiB",
		hits, misses, extends, entries, ids, float64(retained)/1024)
}
