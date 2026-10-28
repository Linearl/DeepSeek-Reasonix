package capability

import (
	"strings"

	"reasonix/internal/textutil"
)

const (
	// DirectoryWarnBytes is the 64KB warning line for the standing L1 capability
	// directory (task 1224). Phase 1 capped the block at this size and truncated
	// whole rows past it; phase 2 (user decision 20261028 23:2x: 全量注入+取消
	// 预算截断机制) removed the cap entirely — capability visibility outranks
	// prompt volume, and a hard cap always re-collides as the population grows.
	// The line survives as a pure warning: DirectoryStats.NearCap fires when the
	// block crosses it (or DirectoryWarnRows), the control package latches one
	// log warn per climb, and the composer gauge shows the over-line state.
	DirectoryWarnBytes = 64 * 1024
	// DirectoryWarnRows is the count-axis warning threshold behind
	// DirectoryStats.NearCap: 500 rows ≈ 61KB at the measured ~122B/row, so
	// many short rows warn even while the byte total is small.
	DirectoryWarnRows = 500
	// DirectorySummaryBytes is the per-row skill summary budget. Tightened from
	// 120B to 100B by user decision (2026-10-17, batch 40 dispatch).
	DirectorySummaryBytes = 100
)

// DirectoryOptions tunes which catalog entries enter the standing directory.
type DirectoryOptions struct {
	// ProviderVisible reports core tools already exposed at the L0 provider
	// surface; listing them again would duplicate the tool schema. Nil keeps
	// every tool row (test and minimal hosts).
	ProviderVisible func(name string) bool
}

// DirectoryStats is the content-free render report for one directory block:
// the mechanical byte counter behind the directory pressure warning.
type DirectoryStats struct {
	Bytes int
	Rows  int
	// NearCap is the warning signal (task 1224): the block crossed
	// DirectoryWarnBytes or the row count crossed DirectoryWarnRows. Nothing is
	// ever dropped at or past the line — the signal only drives the once-per-
	// climb log warn and the gauge's over-line visual state.
	NearCap bool
}

// DirectoryBlock renders the standing L1 directory (task 969): one row per
// skill with a ≤100B summary, tool rows with id/name only, and MCP server
// rows. Full descriptions stay behind use_capability inspect — the standing
// surface must not inline them. Rows are deterministic (catalog order) so an
// unchanged catalog yields identical bytes and the session-context digest gate
// keeps the block cache-stable.
//
// Task 1224 phase 2: every enabled entry renders — the phase-1 64KB hard cap
// (rows dropped past it, capability invisible to the model) is retired. A
// multi-skill user's capabilities must be discoverable, and the block only
// ever carried one-line rows, so unbounded growth stays linear and small
// relative to the conversation it serves.
func DirectoryBlock(catalog Catalog, opts DirectoryOptions) (string, DirectoryStats) {
	var stats DirectoryStats
	var rows []string
	for _, e := range catalog.Entries {
		row, ok := directoryRow(e, opts)
		if !ok {
			continue
		}
		rows = append(rows, row)
		stats.Bytes += 1 + len(row)
		stats.Rows++
	}
	if len(rows) == 0 {
		return "", stats
	}
	stats.NearCap = stats.Bytes >= DirectoryWarnBytes || stats.Rows >= DirectoryWarnRows
	stats.Bytes += len("```\n\n```\n")
	block := "```\n" + strings.Join(rows, "\n") + "\n```"
	return block, stats
}

// directoryRow renders one catalog entry as a directory line. Skills carry a
// ≤100B summary; tools and sources carry id/name only; MCP servers carry a
// status row. Concrete MCP tools stay behind inspect (same policy as
// use_capability list) so one cached directory cannot dominate the block.
func directoryRow(e Entry, opts DirectoryOptions) (string, bool) {
	switch e.Kind {
	case KindSkill:
		// Manual-invocation skills stay invocable by name but must never enter a
		// model-scanned standing catalog (same rule as the skills index).
		if e.Invocation == "manual" {
			return "", false
		}
		summary := strings.TrimSpace(e.ShortDescription)
		if summary == "" {
			summary = clipSummary(e.Description)
		} else {
			summary = clipSummary(summary)
		}
		if summary == "" {
			return "- " + e.ID, true
		}
		return "- " + e.ID + " — " + summary, true
	case KindTool, KindSource:
		if opts.ProviderVisible != nil && e.ToolName != "" && opts.ProviderVisible(e.ToolName) {
			return "", false
		}
		return "- " + e.ID, true
	case KindMCPServer:
		if status := strings.TrimSpace(string(e.Status)); status != "" && e.Status != StatusReady {
			return "- " + e.ID + " — " + status, true
		}
		return "- " + e.ID, true
	default:
		// KindMCPTool and anything newer stay behind inspect.
		return "", false
	}
}

// clipSummary enforces the ≤100B standing-summary budget without splitting a
// grapheme cluster; the ellipsis (3 bytes) counts inside the budget.
func clipSummary(desc string) string {
	desc = strings.TrimSpace(strings.ReplaceAll(desc, "\n", " "))
	if desc == "" {
		return ""
	}
	if len(desc) <= DirectorySummaryBytes {
		return desc
	}
	const ellipsis = "…"
	return textutil.FitGraphemeBytes(desc, DirectorySummaryBytes-len(ellipsis)) + ellipsis
}
