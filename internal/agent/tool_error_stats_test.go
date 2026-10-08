package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestToolErrorStatsClassification pins the task-227 phase-1 error taxonomy:
// host refusals are HARD, execution errors are SOFT, and a plain successful
// call (including a bash non-zero exit, which surfaces through output and not
// errMsg) only counts toward calls.
func TestToolErrorStatsClassification(t *testing.T) {
	a := &Agent{toolStats: newToolErrorStats()}
	// Plain success.
	a.recordToolErrorStats("read_file", toolOutcome{output: "ok"})
	// HARD: host refused (schema validation / capability wrapping / gate).
	a.recordToolErrorStats("use_capability", toolOutcome{blocked: true, output: "blocked: ..."})
	// SOFT: execution ran and errored.
	a.recordToolErrorStats("webfetch", toolOutcome{errMsg: "connection refused"})
	// bash non-zero exit: command failed, tool worked — neither hard nor soft.
	a.recordToolErrorStats("bash", toolOutcome{output: "exit code 1"})

	got := a.ToolErrorStatsSnapshot()
	read := got["read_file"]
	if read.Calls != 1 || read.HardErrors != 0 || read.SoftErrors != 0 {
		t.Fatalf("read_file = %+v", read)
	}
	uc := got["use_capability"]
	if uc.Calls != 1 || uc.HardErrors != 1 {
		t.Fatalf("use_capability = %+v", uc)
	}
	wf := got["webfetch"]
	if wf.Calls != 1 || wf.SoftErrors != 1 {
		t.Fatalf("webfetch = %+v", wf)
	}
	bash := got["bash"]
	if bash.Calls != 1 || bash.HardErrors != 0 || bash.SoftErrors != 0 {
		t.Fatalf("bash non-zero exit must not count as an error: %+v", bash)
	}
}

// TestToolErrorStatsSidecarRoundTrip verifies persistence: counters survive a
// simulated restart, and the sidecar carries ONLY aggregate counters — no
// arguments, output text, or paths (privacy line, task 227).
func TestToolErrorStatsSidecarRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "20260922-demo.jsonl")
	if err := os.WriteFile(sessionPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &Agent{}
	a.sess.path = sessionPath
	a.loadToolErrorStats()
	a.recordToolErrorStats("exa_web_search", toolOutcome{blocked: true, output: "blocked: schema — secret-token-payload"})
	a.recordToolErrorStats("exa_web_search", toolOutcome{blocked: true, output: "blocked: schema — another-secret"})

	sidecar := sessionPath + ".toolstats.json"
	blob, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatalf("sidecar not written: %v", err)
	}
	if strings.Contains(string(blob), "secret-token-payload") || strings.Contains(string(blob), "another-secret") {
		t.Fatal("sidecar leaked argument/output content (privacy line)")
	}

	// Simulated restart: a fresh agent on the same session path continues the
	// same counters.
	b := &Agent{}
	b.sess.path = sessionPath
	b.loadToolErrorStats()
	b.recordToolErrorStats("exa_web_search", toolOutcome{})
	got := b.ToolErrorStatsSnapshot()["exa_web_search"]
	if got.Calls != 3 || got.HardErrors != 2 {
		t.Fatalf("counters did not survive restart: %+v", got)
	}
}

// TestToolErrorStatsCorruptSidecarStartsFresh keeps observation non-fatal.
func TestToolErrorStatsCorruptSidecarStartsFresh(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "demo.jsonl")
	if err := os.WriteFile(sessionPath+".toolstats.json", []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &Agent{}
	a.sess.path = sessionPath
	a.loadToolErrorStats()
	a.recordToolErrorStats("bash", toolOutcome{output: "x"})
	var stored toolErrorStats
	blob, err := os.ReadFile(sessionPath + ".toolstats.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(blob, &stored); err != nil {
		t.Fatalf("sidecar not rewritten after corruption: %v", err)
	}
	if stored.Tools["bash"].Calls != 1 {
		t.Fatalf("fresh counter not used: %+v", stored)
	}
}

// TestToolErrorStatsInMemorySessionSkipsFile: sessions without an on-disk
// path never touch the filesystem.
func TestToolErrorStatsInMemorySessionSkipsFile(t *testing.T) {
	a := &Agent{}
	a.sess.path = ""
	a.loadToolErrorStats()
	a.recordToolErrorStats("read_file", toolOutcome{output: "ok"})
	if a.toolErrorStatsPath() != "" {
		t.Fatal("in-memory session must not produce a sidecar path")
	}
}

// TestToolErrorStatsParallelRecording pins the task 252 crash surface (审查-2,
// 2026-09-22): recordToolErrorStats runs on parallel tool goroutines, so the
// Tools map write was a live `fatal error: concurrent map writes` until
// toolStatsMu landed (2026-09-30). Parallel recording plus a concurrent
// snapshot reader must produce exact totals — a regression to unguarded access
// fatal-crashes the whole test binary instead of failing an assertion.
func TestToolErrorStatsParallelRecording(t *testing.T) {
	// In-memory session: the sidecar write holds the same mutex, so skipping
	// the file keeps this test focused on the map invariant.
	a := &Agent{}
	a.sess.path = ""

	const writers = 16
	const perWriter = 200
	var wg sync.WaitGroup
	// Each writer tallies the outcomes it recorded, so the final assertion
	// compares exact totals without relying on the rotation's distribution.
	totals := make([]struct{ plain, soft, hard int }, writers)
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				switch (g + i) % 3 {
				case 0:
					a.recordToolErrorStats("read_file", toolOutcome{output: "ok"})
					totals[g].plain++
				case 1:
					a.recordToolErrorStats("bash", toolOutcome{errMsg: "spawn failed"})
					totals[g].soft++
				case 2:
					a.recordToolErrorStats("use_capability", toolOutcome{blocked: true, output: "blocked: schema"})
					totals[g].hard++
				}
			}
		}(g)
	}
	// A concurrent reader mirrors what a UI/status poller does mid-turn; the
	// snapshot takes the same mutex and must never observe a torn map.
	stop := make(chan struct{})
	var readerDone sync.WaitGroup
	readerDone.Add(1)
	go func() {
		defer readerDone.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = a.ToolErrorStatsSnapshot()
			}
		}
	}()
	wg.Wait()
	close(stop)
	readerDone.Wait()

	got := a.ToolErrorStatsSnapshot()
	if len(got) != 3 {
		t.Fatalf("tracked %d tools, want 3: %+v", len(got), got)
	}
	var wantPlain, wantSoft, wantHard int
	for _, c := range totals {
		wantPlain += c.plain
		wantSoft += c.soft
		wantHard += c.hard
	}
	if have := got["read_file"]; have.Calls != wantPlain || have.HardErrors != 0 || have.SoftErrors != 0 {
		t.Fatalf("read_file = %+v, want %d plain calls", have, wantPlain)
	}
	if have := got["bash"]; have.Calls != wantSoft || have.SoftErrors != wantSoft || have.HardErrors != 0 {
		t.Fatalf("bash = %+v, want %d soft-error calls", have, wantSoft)
	}
	if have := got["use_capability"]; have.Calls != wantHard || have.HardErrors != wantHard || have.SoftErrors != 0 {
		t.Fatalf("use_capability = %+v, want %d hard-error calls", have, wantHard)
	}
	if wantPlain+wantSoft+wantHard != writers*perWriter {
		t.Fatalf("tally %d+%d+%d != %d recorded calls", wantPlain, wantSoft, wantHard, writers*perWriter)
	}
}
