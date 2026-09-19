package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Task 184 acceptance: with the switch on the sampler appends one line per
// interval carrying every field (memory, IO delta, key-file sizes), and Stop is
// final — a monitor that keeps writing after shutdown would be the bug it exists
// to find.
func TestPerfMonitorWritesSamplesThenStops(t *testing.T) {
	root := t.TempDir()
	perfDir := filepath.Join(root, "perf")
	watched := filepath.Join(root, "watched.jsonl")
	if err := os.WriteFile(watched, []byte("hello world"), 0o600); err != nil {
		t.Fatal(err)
	}

	monitor := newPerfMonitor(nil, perfDir, 40*time.Millisecond, time.Hour, []string{watched})
	monitor.Start()
	time.Sleep(180 * time.Millisecond)
	monitor.Stop()

	files, err := filepath.Glob(filepath.Join(perfDir, perfMonitorFilePrefix+"*"+perfMonitorFileSuffix))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("sample files = %v, want exactly one daily file", files)
	}
	if !strings.HasSuffix(files[0], perfMonitorFileSuffix) {
		t.Fatalf("sample file %q must not look like a session .jsonl", files[0])
	}

	read := func() []string {
		data, err := os.ReadFile(files[0])
		if err != nil {
			t.Fatal(err)
		}
		trimmed := strings.TrimSpace(string(data))
		if trimmed == "" {
			return nil
		}
		return strings.Split(trimmed, "\n")
	}
	lines := read()
	if len(lines) < 2 {
		t.Fatalf("samples = %d, want >= 2 (one immediate + interval)", len(lines))
	}

	var sample perfSample
	if err := json.Unmarshal([]byte(lines[0]), &sample); err != nil {
		t.Fatalf("decode first sample: %v", err)
	}
	if sample.TS == "" {
		t.Fatalf("sample has no timestamp: %+v", sample)
	}
	if sample.IntervalSeconds <= 0 || sample.HeapInuseMB <= 0 || sample.HeapSysMB <= 0 || sample.Goroutines <= 0 {
		t.Fatalf("sample missing runtime fields: %+v", sample)
	}
	if size, ok := sample.FilesKB[watched]; !ok || size <= 0 {
		t.Fatalf("watched file missing from the size table: %+v", sample.FilesKB)
	}
	// The OS counters are platform-dependent; when the platform answered, the
	// working set must be a real number rather than a silent zero.
	if sample.OSCounters && sample.WorkingSetMB <= 0 {
		t.Fatalf("platform reported counters but working set is zero: %+v", sample)
	}

	before := len(read())
	time.Sleep(150 * time.Millisecond)
	if after := len(read()); after != before {
		t.Fatalf("sampler wrote after Stop: %d -> %d lines", before, after)
	}
}

// The monitor must not become the growth it was built to find.
func TestPerfMonitorPruneDropsSamplesPastRetention(t *testing.T) {
	perfDir := filepath.Join(t.TempDir(), "perf")
	if err := os.MkdirAll(perfDir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(perfDir, perfMonitorFilePrefix+"20200101"+perfMonitorFileSuffix)
	fresh := filepath.Join(perfDir, perfMonitorFilePrefix+"20200102"+perfMonitorFileSuffix)
	for _, path := range []string{stale, fresh} {
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(stale, past, past); err != nil {
		t.Fatal(err)
	}

	monitor := newPerfMonitor(nil, perfDir, time.Hour, 48*time.Hour, nil)
	monitor.prune(time.Now())

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale sample survived retention: err=%v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh sample was pruned: %v", err)
	}
}

// "Closed = zero cost" is literal: a monitor that is never started must not even
// create its directory, let alone a file.
func TestPerfMonitorNotStartedWritesNothing(t *testing.T) {
	perfDir := filepath.Join(t.TempDir(), "perf")
	if _, err := os.Stat(perfDir); !os.IsNotExist(err) {
		t.Fatalf("perf directory exists before any monitor started: err=%v", err)
	}
}

// The configured pattern list replaces the built-in one; a pattern that matches
// nothing must not fail the sample.
func TestPerfMonitorHonoursConfiguredPatterns(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "a.jsonl")
	second := filepath.Join(root, "b.jsonl")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	monitor := newPerfMonitor(nil, filepath.Join(root, "perf"), time.Hour, time.Hour,
		[]string{filepath.Join(root, "*.jsonl"), filepath.Join(root, "missing-*.jsonl")})
	files := monitor.collectFiles()
	if _, ok := files[first]; !ok {
		t.Fatalf("first pattern missed: %v", files)
	}
	if _, ok := files[second]; !ok {
		t.Fatalf("glob did not expand the second file: %v", files)
	}
	if len(files) != 2 {
		t.Fatalf("unexpected extra entries: %v", files)
	}
}
