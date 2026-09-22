package agent

import (
	"encoding/json"
	"log"
	"os"
	"strings"

	"reasonix/internal/fileutil"
)

// Per-session tool error statistics (task 227, phase 1: observe only).
//
// The model keeps fumbling the same tools (exa schema mistakes, use_capability
// double-wrapping, heredoc backticks). Before any corrective behavior we need
// numbers: per tool, how many calls, how many were rejected by the host
// (HARD — schema validation, capability wrapping, policy gates) and how many
// failed in execution (SOFT — the tool itself returned an error). A bash
// non-zero exit is a normal test-driven outcome and counts as NEITHER.
//
// Privacy line: the sidecar stores aggregate counters keyed by tool name —
// never call arguments, output, or paths (arguments can carry credentials).

// toolErrorCount is the per-tool aggregate. The file only ever holds counters.
type toolErrorCount struct {
	Calls      int `json:"calls"`
	HardErrors int `json:"hardErrors"`
	SoftErrors int `json:"softErrors"`
}

// toolErrorStats is the whole sidecar document.
type toolErrorStats struct {
	Version int                       `json:"version"`
	Tools   map[string]toolErrorCount `json:"tools"`
}

func newToolErrorStats() *toolErrorStats {
	return &toolErrorStats{Version: 1, Tools: map[string]toolErrorCount{}}
}

// toolErrorStatsPath is the session sidecar carrying the counters. Empty when
// the session has no on-disk path (in-memory sessions stay memory-only).
func (a *Agent) toolErrorStatsPath() string {
	if a.sess.path == "" {
		return ""
	}
	return a.sess.path + ".toolstats.json"
}

// loadToolErrorStats restores counters from a previous run of the same
// session, so phase-1 statistics survive restarts. Missing or corrupt files
// start a fresh counter (observation must never break the session).
func (a *Agent) loadToolErrorStats() {
	a.toolStats = newToolErrorStats()
	path := a.toolErrorStatsPath()
	if path == "" {
		return
	}
	blob, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var stored toolErrorStats
	if err := json.Unmarshal(blob, &stored); err != nil {
		log.Printf("[tool-stats] corrupt sidecar ignored: %v", err)
		return
	}
	if stored.Tools != nil {
		a.toolStats.Tools = stored.Tools
	}
}

// recordToolErrorStats folds one finished tool outcome into the counters and
// persists the sidecar. Classification (task 227 phase 1):
//   - HARD: the host refused the call before/instead of execution
//     (outcome.blocked: schema validation, capability wrapping, policy gates).
//   - SOFT: execution ran and returned an error (outcome.errMsg on a
//     non-blocked outcome).
//   - A bash non-zero exit is neither: the shell executed fine, the command
//     under test failed — that is the test-driven loop working as designed.
func (a *Agent) recordToolErrorStats(toolName string, o toolOutcome) {
	if a.toolStats == nil {
		a.toolStats = newToolErrorStats()
	}
	name := strings.TrimSpace(toolName)
	if name == "" {
		return
	}
	entry := a.toolStats.Tools[name]
	entry.Calls++
	switch {
	case o.blocked:
		entry.HardErrors++
	case o.errMsg != "":
		// bash surfaces command failure through output + exit code, not
		// errMsg; a bash errMsg means the tool itself failed (spawn error),
		// which is a genuine soft error.
		entry.SoftErrors++
	}
	a.toolStats.Tools[name] = entry
	a.persistToolErrorStats()
}

// persistToolErrorStats atomically rewrites the sidecar. Failures log and
// continue: statistics are advisory in phase 1.
func (a *Agent) persistToolErrorStats() {
	path := a.toolErrorStatsPath()
	if path == "" {
		return
	}
	blob, err := json.Marshal(a.toolStats)
	if err != nil {
		return
	}
	if err := fileutil.AtomicWriteFile(path, blob, 0o644); err != nil {
		log.Printf("[tool-stats] persist: %v", err)
	}
}

// ToolErrorStatsSnapshot returns a copy of the current counters for display
// or the CLI aggregator. It contains only tool names and counts.
func (a *Agent) ToolErrorStatsSnapshot() map[string]toolErrorCount {
	out := make(map[string]toolErrorCount, len(a.toolStats.Tools))
	for k, v := range a.toolStats.Tools {
		out[k] = v
	}
	return out
}
