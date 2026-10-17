package capability

import (
	"fmt"
	"strings"

	"reasonix/internal/textutil"
)

const (
	// DirectoryMaxBytes caps the standing L1 capability directory rendered into
	// session context (task 969). The renderer counts bytes and truncates at a
	// row boundary past the cap so the block cannot silently grow with the
	// skill/tool population; callers surface DirectoryStats.Truncated as the
	// over-limit warning.
	DirectoryMaxBytes = 20 * 1024
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
// the mechanical byte counter behind the 20KB ratchet.
type DirectoryStats struct {
	Bytes     int
	Rows      int
	Dropped   int
	Truncated bool
}

// DirectoryBlock renders the standing L1 directory (task 969): one row per
// skill with a ≤100B summary, tool rows with id/name only, and MCP server
// rows. Full descriptions stay behind use_capability inspect — the standing
// surface must not inline them. Rows are deterministic (catalog order) so an
// unchanged catalog yields identical bytes and the session-context digest gate
// keeps the block cache-stable.
func DirectoryBlock(catalog Catalog, opts DirectoryOptions) (string, DirectoryStats) {
	var stats DirectoryStats
	var rows []string
	appendRow := func(row string) bool {
		// +1 for the joining newline; the fence trailer is added after the cap
		// check so truncation always lands on a whole row.
		if stats.Bytes+1+len(row) > DirectoryMaxBytes {
			stats.Dropped++
			return false
		}
		rows = append(rows, row)
		stats.Bytes += 1 + len(row)
		stats.Rows++
		return true
	}

	truncating := false
	for _, e := range catalog.Entries {
		row, ok := directoryRow(e, opts)
		if !ok {
			continue
		}
		if !appendRow(row) {
			truncating = true
			// Keep scanning to count what the cap drops, but stop rendering.
			continue
		}
	}
	if len(rows) == 0 {
		return "", stats
	}
	stats.Bytes += len("```\n\n```\n")
	block := "```\n" + strings.Join(rows, "\n") + "\n```"
	if truncating {
		stats.Truncated = true
		block += fmt.Sprintf("\n… (directory truncated: %d rows over the %dKB cap — use use_capability inspect for the rest)", stats.Dropped, DirectoryMaxBytes/1024)
	}
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
