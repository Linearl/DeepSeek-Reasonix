package agent

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"reasonix/internal/pathidentity"
)

// Task 883 probe overhead benchmark. The probe adds, on the silent (warm)
// path: one time.Now, one time.Since and two atomic adds around
// pathidentity.Canonical. Run with:
//
//	go test ./internal/agent/ -run '^$' -bench BenchmarkCanonicalSessionSavePath -benchtime 2s
//
// The delta between WithProbe and Raw is the per-call probe cost; the task-883
// budget is <1ms (three orders of magnitude above the measured delta).
// benchPath returns one shared path for every benchmark-function invocation:
// the testing framework re-invokes the function per b.N ramp with a fresh
// b.TempDir each time, which would put a cold 10-30ms walk inside an otherwise
// warm loop. One package-level path keeps every measured loop a pure cache-hit
// loop (TTL 30s comfortably outlasts the whole benchmark).
var (
	benchPathOnce  sync.Once
	benchPathValue string
	benchPathErr   error
)

func benchSharedPath(b *testing.B) string {
	b.Helper()
	benchPathOnce.Do(func() {
		path := filepath.Join(b.TempDir(), "sessions", "bench.jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			benchPathErr = err
			return
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			benchPathErr = err
			return
		}
		benchPathValue = path
	})
	if benchPathErr != nil {
		b.Fatal(benchPathErr)
	}
	return benchPathValue
}

func BenchmarkCanonicalSessionSavePathWithProbe(b *testing.B) {
	path := benchSharedPath(b)
	if key := canonicalSessionSavePath(path); key == "" { // warm the TTL cache
		b.Fatal("empty canonical key")
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = canonicalSessionSavePath(path)
	}
}

func BenchmarkCanonicalSessionSavePathRaw(b *testing.B) {
	path := benchSharedPath(b)
	if key := pathidentity.Canonical(path); key == "" { // warm OS-level caches too
		b.Fatal("empty canonical key")
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = pathidentity.Canonical(path)
	}
}
