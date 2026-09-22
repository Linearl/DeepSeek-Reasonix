package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
