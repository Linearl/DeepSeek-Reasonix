package main

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/pprof/profile"
)

// perfTestApp: the read APIs only touch config + perfMonitorDir, never App
// state, so an empty host value is enough.
func perfTestApp() *App { return &App{} }

func writePerfSampleLines(t *testing.T, dir, name string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := ""
	for _, line := range lines {
		body += line + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestReadPerfSamplesWindowAndFaultTolerance: window filtering, bad-line
// skipping and chronological order — the series must survive a partially
// written tail (task 338).
func TestReadPerfSamplesWindowAndFaultTolerance(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	inWindow := now.Add(-10 * time.Minute).UTC().Format(time.RFC3339Nano)
	alsoIn := now.Add(-30 * time.Minute).UTC().Format(time.RFC3339Nano)
	stale := now.Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano)
	writePerfSampleLines(t, dir, "perf-sample-a.jsonl",
		`{"ts":"`+inWindow+`","workingSetMb":100}`,
		`{"ts":"`+stale+`","workingSetMb":50}`,
		`{"ts":"`+alsoIn+`","workingSetMb", broken`,
	)
	writePerfSampleLines(t, dir, "perf-sample-b.jsonl",
		`{"ts":"`+alsoIn+`","workingSetMb":120}`,
	)

	samples, err := readPerfSamples(dir, time.Hour, now)
	if err != nil {
		t.Fatalf("readPerfSamples: %v", err)
	}
	// stale dropped by the window, broken line skipped, b-file point merged.
	if len(samples) != 2 {
		t.Fatalf("samples = %d, want 2 (%+v)", len(samples), samples)
	}
	if samples[0].WorkingSetMB != 120 || samples[1].WorkingSetMB != 100 {
		t.Fatalf("order wrong: first=%v second=%v (want oldest first)", samples[0].WorkingSetMB, samples[1].WorkingSetMB)
	}
	// A directory that never existed means "never sampled": no error, nothing
	// created (task 338 empty state + 182 closed contract).
	missing := filepath.Join(t.TempDir(), "absent")
	samples, err = readPerfSamples(missing, time.Hour, now)
	if err != nil || samples != nil {
		t.Fatalf("missing dir = (%v, %v), want (nil, nil)", samples, err)
	}
	if _, statErr := os.Stat(missing); !os.IsNotExist(statErr) {
		t.Fatalf("read must not create the directory, stat err=%v", statErr)
	}
}

// TestPerfTimeSeriesIsPureRead pins the task-338 zero-overhead contract: the
// read API never touches disk in a write direction, and an absent sampler
// directory yields an honest empty view with the switch state attached.
func TestPerfTimeSeriesIsPureRead(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	perfDir := perfMonitorDir()
	// Seed one sample first: the process caches the perf dir across tests, so
	// the zero-overhead proof is "the read neither creates nor removes files",
	// not "the dir happens to be absent here".
	writePerfSampleLines(t, perfDir, "perf-sample-x.jsonl",
		`{"ts":"`+time.Now().Add(-2*time.Hour).UTC().Format(time.RFC3339Nano)+`","workingSetMb":42}`)
	before, err := os.ReadDir(perfDir)
	if err != nil {
		t.Fatal(err)
	}

	view := perfTestApp().PerfTimeSeries(180) // 3h window contains the 2h-old point
	if !view.Available || len(view.Points) != 1 || view.Points[0].WorkingSetMB != 42 {
		t.Fatalf("one-sample view wrong: %+v", view)
	}
	if view.Enabled {
		t.Fatal("default config has the sampler off")
	}
	if view.IntervalSeconds != 5 {
		t.Fatalf("built-in interval = %d, want 5", view.IntervalSeconds)
	}
	after, err := os.ReadDir(perfDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("PerfTimeSeries changed the perf dir (zero-overhead contract): %d -> %d entries", len(before), len(after))
	}

	// A window that contains no points reports unavailable, still without writing.
	view = perfTestApp().PerfTimeSeries(60) // 1h window excludes the 2h-old point
	if view.Available {
		t.Fatalf("1-hour window over a 2h-old sample must be unavailable: %+v", view)
	}
}

// TestSampleHeapBreakdown parses a real profile from this very test process:
// five buckets, bytes that sum to the profile total, percent that sums to
// ~100, and the pprof file kept for export (task 338).
func TestSampleHeapBreakdown(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	heapSink = make([]byte, 1<<20) // keep a real megabyte inuse for the profile

	view, err := perfTestApp().SampleHeapBreakdown()
	if err != nil {
		t.Fatalf("SampleHeapBreakdown: %v", err)
	}
	if view.Path == "" {
		t.Fatal("breakdown must keep the export path")
	}
	if _, err := os.Stat(view.Path); err != nil {
		t.Fatalf("pprof file missing: %v", err)
	}
	if view.TotalBytes <= 0 {
		t.Fatalf("totalBytes = %d, want > 0", view.TotalBytes)
	}
	wantKeys := []string{"transcript", "snapshot", "dag", "frontendCache", "other"}
	if len(view.Categories) != len(wantKeys) {
		t.Fatalf("categories = %d, want %d buckets always present", len(view.Categories), len(wantKeys))
	}
	var percentSum float64
	for i, category := range view.Categories {
		if category.Key != wantKeys[i] {
			t.Fatalf("bucket %d key = %q, want %q (fixed order)", i, category.Key, wantKeys[i])
		}
		percentSum += category.Percent
	}
	if math.Abs(percentSum-100) > 0.5 {
		t.Fatalf("percent sum = %.1f, want ~100", percentSum)
	}
}

// heapSink keeps the sampled allocation alive for TestSampleHeapBreakdown.
var heapSink []byte

// TestHeapClassifyAndAggregate pins the 187-battle bucket mapping with a
// synthetic profile: one leaf frame per bucket, equal bytes, exact percents.
func TestHeapClassifyAndAggregate(t *testing.T) {
	leaf := func(name string) []*profile.Location {
		return []*profile.Location{{Line: []profile.Line{{Function: &profile.Function{Name: name}}}}}
	}
	p := &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "inuse_space", Unit: "bytes"}},
		Sample: []*profile.Sample{
			{Location: leaf("reasonix/internal/transcript.(*Store).appendRows"), Value: []int64{100}},
			{Location: leaf("reasonix/internal/snapshot.materializeSnapshotMessages"), Value: []int64{100}},
			{Location: leaf("reasonix/internal/agent.(*sessionDAGState).replay"), Value: []int64{100}},
			{Location: leaf("reasonix/internal/catalog.(*SessionCatalog).Load"), Value: []int64{100}},
			{Location: leaf("runtime.mallocgc"), Value: []int64{100}},
			// Negative/zero values never contribute to a pie.
			{Location: leaf("runtime.mallocgc"), Value: []int64{0}},
		},
	}
	categories, total := aggregateHeapProfile(p)
	if total != 500 {
		t.Fatalf("total = %d, want 500 (zero/negative skipped)", total)
	}
	wantBytes := map[string]int64{
		"transcript": 100, "snapshot": 100, "dag": 100, "frontendCache": 100, "other": 100,
	}
	for _, category := range categories {
		if category.Bytes != wantBytes[category.Key] {
			t.Errorf("%s bytes = %d, want %d", category.Key, category.Bytes, wantBytes[category.Key])
		}
		if category.Percent != 20 {
			t.Errorf("%s percent = %v, want 20", category.Key, category.Percent)
		}
	}
	// A nil profile still yields the full honest legend.
	categories, total = aggregateHeapProfile(nil)
	if total != 0 || len(categories) != 5 {
		t.Fatalf("nil profile = (total %d, %d buckets), want (0, 5)", total, len(categories))
	}
}

// TestHeapBreakdownPathEmpty: with no sampling yet the export lookup returns
// "" instead of inventing a path (task 338 export entry).
func TestHeapBreakdownPathEmpty(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	// The production wrapper reads the process-cached perf dir (shared across
	// tests); the injected core keeps this proof isolated per test.
	if got := heapBreakdownPathIn(t.TempDir()); got != "" {
		t.Fatalf("empty dir path = %q, want empty", got)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "heap-20260928-120000.pprof"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := heapBreakdownPathIn(dir)
	if got == "" || filepath.Base(got) != "heap-20260928-120000.pprof" {
		t.Fatalf("heapBreakdownPathIn = %q, want the exported file", got)
	}
}
