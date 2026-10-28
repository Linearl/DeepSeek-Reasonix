package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// tl1228 阶段一验收的量具：resolve 相前后对照（耗时+内存）与图缓存顶的
// 受控 A/B。全部 env 门控——不设变量时 bench 用小夹具照常可跑（CI 冒烟），
// 受控读数测试直接跳过；量产读数在开发机上以大夹具运行（见交付回执）。

// tl1228BenchSessionMB is the fixture size knob (REASONIX_TL1228_SESSION_MB).
const tl1228BenchSessionMB = 4

func tl1228FixtureMB() int {
	if v, err := strconv.Atoi(os.Getenv("REASONIX_TL1228_SESSION_MB")); err == nil && v > 0 {
		return v
	}
	return tl1228BenchSessionMB
}

// tl1228BuildDAGSession writes one schema-2 log of ~mb MiB: chained user/
// assistant messages with ~4KiB content bodies (the applyMessage payload
// shape the tl1223 heap profile attributed the 397MB to).
func tl1228BuildDAGSession(b testing.TB, mb int) string {
	b.Helper()
	path := filepath.Join(b.TempDir(), "bench.jsonl")
	const bodyBytes = 4096
	body := string(make([]byte, bodyBytes)) // zero bytes: compressible, content length is what matters
	base := time.Date(2026, 1, 8, 10, 0, 0, 0, time.UTC)
	entryOverhead := 512
	perEntry := bodyBytes + entryOverhead
	n := mb << 20 / perEntry
	if n < 8 {
		n = 8
	}
	entries := make([]sessionDAGEntry, 0, n+1)
	entries = append(entries, sessionDAGEntry{Type: sessionDAGTypeLog, Generation: 1, At: base})
	parent := ""
	for i := 0; i < n; i++ {
		role := provider.RoleUser
		if i%2 == 1 {
			role = provider.RoleAssistant
		}
		e, err := newSessionDAGMessageEntry(SessionMainHead, parent, "", "t1",
			provider.Message{Role: role, Content: body, ID: fmt.Sprintf("B%d", i)}, base.Add(time.Duration(i)*time.Second))
		if err != nil {
			b.Fatalf("entry %d: %v", i, err)
		}
		entries = append(entries, e)
		parent = fmt.Sprintf("B%d", i)
	}
	if _, err := appendSessionDAGEntries(path, entries, false); err != nil {
		b.Fatalf("append: %v", err)
	}
	return path
}

// BenchmarkTL1228ResolveFullLoad is the BEFORE shape: what
// sessionFileHasConversationContent paid before tl1228 — a full LoadSession
// (whole-log payload decode + transcript digest + graph-cache Put) for one
// boolean. Includes the cache retention its Put causes.
func BenchmarkTL1228ResolveFullLoad(b *testing.B) {
	path := tl1228BuildDAGSession(b, tl1228FixtureMB())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s, err := LoadSession(path)
		if err != nil {
			b.Fatal(err)
		}
		if !s.HasContent() {
			b.Fatal("fixture must carry content")
		}
	}
}

// BenchmarkTL1228ResolveBounded is the AFTER shape: the bounded probe — byte
// scan, no payload materialization, no cache interaction, no lock.
func BenchmarkTL1228ResolveBounded(b *testing.B) {
	path := tl1228BuildDAGSession(b, tl1228FixtureMB())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !SessionHasConversationContent(path) {
			b.Fatal("fixture must carry content")
		}
	}
}

// BenchmarkTL1228HydrateTail pins the 187 tail-replay hydrate the open flow
// uses after resolve (regression watch: this must not regress when the probe
// and cache caps change).
func BenchmarkTL1228HydrateTail(b *testing.B) {
	path := tl1228BuildDAGSession(b, tl1228FixtureMB())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := LoadSessionTail(path); err != nil {
			b.Fatal(err)
		}
	}
}

// TestTL1228MemoryBaselineReading is the controlled A/B for the receipt:
// identical build, identical fixture, three open-flow legs —
//   - leg "fullload_oldcache": LoadSession loop under the pre-1228 cache caps
//     (2GiB/1GiB, pushed through the same setters the config layer uses) —
//     the before-equivalent retained set;
//   - leg "fullload_newcache": same loop under the new 64/64 defaults —
//     isolates the cache-cap change;
//   - leg "probe_newcache": the production after-shape (bounded probe resolve
//     + tail hydrate) under the new defaults.
//
// Each leg GC-settles and reports HeapInuse plus the open wall time. Gated by
// REASONIX_TL1228_MEMBASELINE=1 so CI never pays the big fixture.
func TestTL1228MemoryBaselineReading(t *testing.T) {
	if os.Getenv("REASONIX_TL1228_MEMBASELINE") != "1" {
		t.Skip("set REASONIX_TL1228_MEMBASELINE=1 (and optionally REASONIX_TL1228_SESSION_MB) for the controlled reading")
	}
	mb := tl1228FixtureMB()
	path := tl1228BuildDAGSession(t, mb)
	logPath := store.SessionEventLog(path)
	const opens = 8 // > old capacity 3: evictions churn like the multi-tab reality

	settle := func() runtime.MemStats {
		runtime.GC()
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return ms
	}
	run := func(name string, open func(int)) {
		InvalidateSessionGraph(path)
		start := time.Now()
		for i := 0; i < opens; i++ {
			open(i)
		}
		elapsed := time.Since(start)
		ms := settle()
		bytesNow, _, _, entries, _ := SessionGraphCacheByteStats()
		t.Logf("TL1228 leg=%s fixture_mb=%d opens=%d wall=%s heap_inuse=%.1fMB cache_accounted=%.1fMB cache_entries=%d",
			name, mb, opens, elapsed.Round(time.Millisecond), float64(ms.HeapInuse)/(1<<20), float64(bytesNow)/(1<<20), entries)
	}

	fullLoad := func(i int) {
		s, err := LoadSession(path)
		if err != nil || !s.HasContent() {
			t.Fatalf("full load %d: err=%v", i, err)
		}
	}
	probeFlow := func(i int) {
		if !SessionHasConversationContent(path) {
			t.Fatalf("probe open %d: content expected", i)
		}
		if _, err := LoadSessionTail(path); err != nil {
			t.Fatalf("tail open %d: %v", i, err)
		}
	}

	SetSessionGraphCacheMaxBytes(int64(2) << 30)
	SetSessionGraphCacheEntryMaxBytes(int64(1) << 30)
	run("fullload_oldcache", fullLoad)

	SetSessionGraphCacheMaxBytes(sessionGraphCacheMaxBytesDefault)
	SetSessionGraphCacheEntryMaxBytes(sessionGraphCacheEntryMaxBytesDefault)
	run("fullload_newcache", fullLoad)

	InvalidateSessionGraph(logPath)
	run("probe_newcache", probeFlow)
	_ = logPath
}
